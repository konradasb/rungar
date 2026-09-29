// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package fleet

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/types"
)

// ScaleSetProviders is one scale set's providers, in the scale set's order.
type ScaleSetProviders struct {
	manager   *Manager
	scaleSet  string
	priority  int
	placement types.Placement
	members   []*member
}

// ScaleSetProviders returns the scale set's providers.
func (m *Manager) ScaleSetProviders(set types.ScaleSetSpec) *ScaleSetProviders {
	p := &ScaleSetProviders{manager: m, scaleSet: set.Name, priority: set.Priority, placement: set.Placement}

	for _, name := range set.ProviderNames() {
		if found, ok := m.byName[name]; ok {
			p.members = append(p.members, found)
		}
	}

	return p
}

// CanPlace returns an errdefs.ErrNoCapacity error saying why if Place would
// try no provider. It calls no provider, so that a runner is not registered
// with GitHub for nothing.
//
// Finding no provider to try, it renews the scale set's holds on the providers
// it skips as full, as Place does: a scale set that keeps asking while it
// waits for room keeps lower priorities off them.
func (p *ScaleSetProviders) CanPlace() error {
	_, err := p.candidates(nil)
	return err
}

// Place creates a runner's machine on the first provider that takes it, in the
// order the scale set's placement puts them, and returns that provider. spec
// returns the machine for a provider; runners counts the scale set's runners
// on each provider. The runner counts against the provider's max_runners
// until release is called.
//
// A provider that refuses the runner is skipped by the scale set for a while,
// longer each time in a row, and the next provider is tried. One that says it
// is full also keeps lower priorities off it, so that the room the scale set
// is waiting for goes to it (see provider.Provider.Create).
//
// Place returns an errdefs.ErrNoCapacity error if no provider takes the
// runner. If a refusing provider's leftover machine cannot be deleted, it
// records the refusal, stops, and returns that provider with the error, since
// the machine has the runner's name.
func (p *ScaleSetProviders) Place(ctx context.Context, runners map[string]int,
	spec func(provider string) types.MachineSpec,
) (provider string, release func(), err error) {
	candidates, err := p.candidates(runners)
	if err != nil {
		return "", nil, err
	}

	var refusals []string

	for _, candidate := range candidates {
		releaseClaim, ok := candidate.claim(func() int { return p.manager.runnerCounts()[candidate.name] })
		if !ok {
			// Another scale set took the last place since the order was made.
			refusals = append(refusals, candidate.name+": at its limit")
			continue
		}

		machine := spec(candidate.name)

		createErr := candidate.provider.Create(ctx, machine)
		if createErr == nil {
			candidate.recordSuccess(p.scaleSet)
			p.releaseHolds()
			return candidate.name, releaseClaim, nil
		}

		releaseClaim()
		refusals = append(refusals, fmt.Sprintf("%s: %v", candidate.name, createErr))

		deleteErr := candidate.deleteLeftover(ctx, machine.Name)

		// A cancelled caller says nothing of the provider; a runner out of
		// time to start does.
		if !errors.Is(ctx.Err(), context.Canceled) {
			p.recordRefusal(candidate, machine.Name, createErr)
		}
		if deleteErr != nil {
			return candidate.name, nil, fmt.Errorf("provider %q: %w; and the machine it may have left "+
				"could not be deleted: %w", candidate.name, createErr, deleteErr)
		}
		if ctx.Err() != nil {
			return "", nil, fmt.Errorf("provider %q: %w", candidate.name, createErr)
		}
	}

	p.holdFullProviders()

	return "", nil, errdefs.NoCapacity("no provider took the runner: %s", strings.Join(refusals, "; "))
}

// candidates returns the providers Place tries, in order, or an
// errdefs.ErrNoCapacity error saying why there are none. runners counts the
// scale set's runners on each provider, for spread; nil counts none.
//
// Finding none, it renews the scale set's holds on the providers it skips as
// full (see holdFullProviders), for both Place and CanPlace.
func (p *ScaleSetProviders) candidates(runners map[string]int) ([]*member, error) {
	counts := p.manager.runnerCounts()

	var (
		out     []*member
		skipped []string
	)
	for _, candidate := range p.members {
		if why := candidate.skipReason(p.scaleSet, p.priority, counts[candidate.name]); why != "" {
			skipped = append(skipped, candidate.name+": "+why)
			continue
		}
		out = append(out, candidate)
	}

	if len(out) == 0 {
		p.holdFullProviders()

		if len(p.members) == 0 {
			return nil, errdefs.NoCapacity("the scale set has no providers")
		}

		return nil, errdefs.NoCapacity("no provider can be tried: %s", strings.Join(skipped, "; "))
	}

	sortForPlacement(out, runners, p.placement)

	return out, nil
}

// sortForPlacement sorts members, given in the scale set's order, into the
// order Place tries them. Pack puts the highest weight first; spread puts
// first the fewest of the scale set's runners per unit of weight. Ties keep
// the scale set's order.
func sortForPlacement(members []*member, runners map[string]int, placement types.Placement) {
	slices.SortStableFunc(members, func(a, b *member) int {
		if placement == types.PlacementPack {
			return cmp.Compare(b.weight, a.weight)
		}

		return cmp.Compare(float64(runners[a.name])/a.weight, float64(runners[b.name])/b.weight)
	})
}

// releaseHolds lifts the holds of the scale set's priority or lower on its
// providers, now it has placed a runner.
func (p *ScaleSetProviders) releaseHolds() {
	for _, each := range p.members {
		each.releaseHold(p.priority)
	}
}

// recordRefusal records that a provider refused a runner, so that the scale
// set skips it for a while, and logs it and records its event.
func (p *ScaleSetProviders) recordRefusal(refuser *member, runner string, err error) {
	wait, refusals, full := refuser.recordRefusal(p.scaleSet, err)

	// The keys are as the troubleshooting guide shows them.
	logger := p.manager.logger.With(slog.String("scale_set", p.scaleSet), slog.String("provider", refuser.name),
		slog.String("runner", runner), slog.Any("error", err), slog.Duration("skipped_for", wait),
		slog.Int("refusals", refusals))

	if full {
		logger.Info("provider is full; trying the next")
		p.manager.events.Record(fullEvent(p.scaleSet, refuser.name, runner, wait, err))

		return
	}

	logger.Warn("provider failed to create a runner; trying the next")
	p.manager.events.Record(refusalEvent(p.scaleSet, refuser.name, runner, wait, refusals, err))
}

// holdFullProviders keeps lower priorities off the providers the scale set is
// skipping as full, so that room freed there goes to it. Each call renews the
// holds' hold-down.
func (p *ScaleSetProviders) holdFullProviders() {
	for _, each := range p.members {
		if each.isFull(p.scaleSet) {
			each.placeHold(p.priority, p.scaleSet)
		}
	}
}

// HoldingBackReason says which providers the scale set holds back from for a
// higher priority, or "" if none.
func (p *ScaleSetProviders) HoldingBackReason() string {
	var held []string

	for _, each := range p.members {
		if to, ok := each.heldFor(p.priority); ok {
			held = append(held, fmt.Sprintf("%s for scale set %q", each.name, to))
		}
	}

	if len(held) == 0 {
		return ""
	}

	return "holding back from " + strings.Join(held, ", ") + ", which is waiting for room and takes priority"
}

// List returns the machines carrying the selector's labels on the scale set's
// providers; see Manager.List.
func (p *ScaleSetProviders) List(ctx context.Context, selector map[string]string) ([]types.Machine, error) {
	names := make([]string, 0, len(p.members))
	for _, each := range p.members {
		names = append(names, each.name)
	}

	return p.manager.List(ctx, names, selector)
}

// Delete deletes a machine on a provider; see Manager.Delete. It is here,
// passed through to the Manager, so that a scale set needs nothing of the
// fleet but its ScaleSetProviders: the provider need not be one of them.
func (p *ScaleSetProviders) Delete(ctx context.Context, provider, machine string) error {
	return p.manager.Delete(ctx, provider, machine)
}
