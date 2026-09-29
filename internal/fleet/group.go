// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package fleet

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/types"
)

// Group is one scale set's providers, in the scale set's order.
type Group struct {
	fleet     *Manager
	scaleSet  string
	priority  int
	placement types.Placement
	members   []*member
}

// Group returns the scale set's providers.
func (m *Manager) Group(set types.ScaleSetSpec) *Group {
	g := &Group{fleet: m, scaleSet: set.Name, priority: set.Priority, placement: set.Placement}

	for _, name := range set.ProviderNames() {
		if mem, ok := m.byName[name]; ok {
			g.members = append(g.members, mem)
		}
	}

	return g
}

// CanPlace returns an errdefs.ErrNoCapacity error saying why if Place would
// try no provider. It calls no provider, so that a runner is not registered
// with GitHub for nothing.
func (g *Group) CanPlace() error {
	_, err := g.candidates(nil)
	return err
}

// Place makes a runner's machine on the first provider that takes it, in the
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
// runner. If a failed provider's leftover machine cannot be removed, it stops
// and returns that provider with the error, since the machine has the
// runner's name.
func (g *Group) Place(ctx context.Context, runners map[string]int,
	spec func(provider string) types.MachineSpec,
) (provider string, release func(), err error) {
	order, err := g.candidates(runners)
	if err != nil {
		return "", nil, err
	}

	var refusals []string

	for _, mem := range order {
		release, ok := mem.claim(func() int { return g.fleet.runnerCounts()[mem.name] })
		if !ok {
			// Another scale set took the last place since the order was made.
			refusals = append(refusals, mem.name+": at its limit")
			continue
		}

		machine := spec(mem.name)

		err := mem.provider.Create(ctx, machine)
		if err == nil {
			mem.recordSuccess(g.scaleSet)
			g.releaseHolds()
			return mem.name, release, nil
		}

		release()
		refusals = append(refusals, fmt.Sprintf("%s: %v", mem.name, err))

		if cleanupErr := cleanup(ctx, mem, machine.Name); cleanupErr != nil {
			return mem.name, nil, fmt.Errorf("provider %q: %w; and the machine it may have left "+
				"could not be removed: %w", mem.name, err, cleanupErr)
		}

		// A cancelled caller says nothing of the provider; a runner out of
		// time to start does.
		if !errors.Is(ctx.Err(), context.Canceled) {
			g.handleFailure(mem, machine.Name, err)
		}
		if ctx.Err() != nil {
			return "", nil, fmt.Errorf("provider %q: %w", mem.name, err)
		}
	}

	g.holdFullProviders()

	return "", nil, errdefs.NoCapacity("no provider took the runner: %s", strings.Join(refusals, "; "))
}

// candidates returns the providers Place tries, in order, or an
// errdefs.ErrNoCapacity error saying why there are none. runners counts the
// scale set's runners on each provider, for spread; nil counts none.
func (g *Group) candidates(runners map[string]int) ([]*member, error) {
	counts := g.fleet.runnerCounts()

	var (
		out     []*member
		skipped []string
	)
	for _, mem := range g.members {
		if why := mem.skipReason(g.scaleSet, g.priority, counts[mem.name]); why != "" {
			skipped = append(skipped, mem.name+": "+why)
			continue
		}
		out = append(out, mem)
	}

	if len(out) == 0 {
		g.holdFullProviders()

		if len(g.members) == 0 {
			return nil, errdefs.NoCapacity("the scale set has no providers")
		}

		return nil, errdefs.NoCapacity("no provider can be tried: %s", strings.Join(skipped, "; "))
	}

	order(out, runners, g.placement)

	return out, nil
}

// releaseHolds lifts the holds of the scale set's priority or lower on its
// providers, now it has placed a runner.
func (g *Group) releaseHolds() {
	for _, mem := range g.members {
		mem.releaseHold(g.priority)
	}
}

// handleFailure records that a provider refused a runner, so that the scale
// set skips it for a while, and logs and records it.
func (g *Group) handleFailure(mem *member, runner string, err error) {
	wait, failures, full := mem.recordFailure(g.scaleSet, err)

	logger := g.fleet.logger.With(slog.String("scale_set", g.scaleSet), slog.String("provider", mem.name),
		slog.String("runner", runner), slog.Any("error", err), slog.Duration("skipped_for", wait),
		slog.Int("failures", failures))

	if full {
		logger.Info("provider is full; trying the next")
		g.fleet.events.Record(fullEvent(g.scaleSet, mem.name, runner, wait, err))

		return
	}

	logger.Warn("provider failed to make a runner; trying the next")
	g.fleet.events.Record(failingEvent(g.scaleSet, mem.name, runner, wait, failures, err))
}

// holdFullProviders keeps lower priorities off the providers the scale set is
// skipping as full, so that room freed there goes to it.
func (g *Group) holdFullProviders() {
	for _, mem := range g.members {
		if mem.isFull(g.scaleSet) {
			mem.claimHold(g.priority, g.scaleSet)
		}
	}
}

// HoldBackReason says which providers the scale set holds back from for a
// higher priority, or "" if none.
func (g *Group) HoldBackReason() string {
	var held []string

	for _, mem := range g.members {
		if to, ok := mem.heldFor(g.priority); ok {
			held = append(held, fmt.Sprintf("%s for scale set %q", mem.name, to))
		}
	}

	if len(held) == 0 {
		return ""
	}

	return "holding back from " + strings.Join(held, ", ") + ", which is waiting for room and takes priority"
}

// List returns the machines carrying the selector's labels on the group's
// providers; see Manager.List.
func (g *Group) List(ctx context.Context, selector map[string]string) ([]types.Machine, error) {
	names := make([]string, 0, len(g.members))
	for _, mem := range g.members {
		names = append(names, mem.name)
	}

	return g.fleet.List(ctx, names, selector)
}

// Delete removes a machine from a provider; see Manager.Delete.
func (g *Group) Delete(ctx context.Context, provider, machine string) error {
	return g.fleet.Delete(ctx, provider, machine)
}

// cleanup removes the machine a failed create may have left, even if the
// caller has given up.
func cleanup(ctx context.Context, mem *member, name string) error {
	return mem.provider.Delete(context.WithoutCancel(ctx), name)
}
