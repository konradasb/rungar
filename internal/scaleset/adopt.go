// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"

	"github.com/konradasb/rungar/internal/fleet"
	"github.com/konradasb/rungar/internal/types"
)

// adoption is how a runner came to be adopted, as its event says.
type adoption string

const (
	adoptedAtStart adoption = "left there by an earlier daemon"
	adoptedFound   adoption = "found on the fleet, unknown to the daemon"
)

// adopt takes over the scale set's runners already on the fleet: those alive
// are adopted with their state from GitHub, and those whose machines have
// ended are let go of and settled, as reconcileFleet does. Runners already
// leaving are left to leave, and those that have just left are not adopted
// again from a listing older than their deletion. Runners on providers that
// cannot be listed are adopted later, by reconcileFleet.
func (s *scaleSet) adopt(ctx context.Context) error {
	// Listed without the installation, so that another installation's
	// runners of the same scale set are seen, and warned of.
	machines, err := s.providers.List(ctx, map[string]string{
		types.LabelManaged:  "true",
		types.LabelScaleSet: s.spec.Name,
	})
	if err != nil {
		var unreachable *fleet.UnreachableError
		if !errors.As(err, &unreachable) {
			return fmt.Errorf("list machines: %w", err)
		}

		s.logger.Warn("adopting what can be seen; the rest once their providers are reachable", slog.Any("error", err))
	}

	now := s.now()

	s.mu.Lock()
	alreadyLeaving := maps.Clone(s.leaving)
	alreadyLeft := maps.Clone(s.left)
	s.mu.Unlock()

	adopted := map[string]*types.Runner{}
	ended := map[string]*leavingRunner{}
	otherInstallations := map[string]int{}

	for _, machine := range machines {
		if other := machine.Labels[types.LabelInstallation]; other != s.installation {
			otherInstallations[other]++
			continue
		}
		if _, ok := alreadyLeaving[machine.Name]; ok {
			continue
		}
		if _, ok := alreadyLeft[machine.Name]; ok {
			continue
		}

		runner := adoptedRunner(s.spec.Name, machine)
		if !machine.State.Alive() {
			s.logger.Info("runner's machine has ended",
				slog.String("runner", machine.Name),
				slog.String("provider", machine.Provider),
				slog.String("state", string(machine.State)))
			ended[machine.Name] = &leavingRunner{runner: *runner, endedAt: now}

			continue
		}

		s.setStateFromGitHub(ctx, runner)
		adopted[machine.Name] = runner
		s.events.Record(adoptedEvent(s.spec.Name, machine.Name, machine.Provider, adoptedAtStart))
	}

	s.mu.Lock()
	s.runners = adopted
	for name, l := range ended {
		if _, ok := s.leaving[name]; !ok {
			s.leaving[name] = l
		}
	}
	s.mu.Unlock()
	s.serving.Store(true)

	if len(adopted) > 0 {
		s.logger.Info("adopted runners already on the fleet", slog.Int("runners", len(adopted)))
	}

	s.warnOtherInstallations(otherInstallations)
	s.settleLeaving(ctx, now)

	return nil
}

// setStateFromGitHub sets an adopted runner's state from GitHub: busy if it is
// running a job, idle if it is connected, and starting if it is neither and
// within its start timeout. Without an answer it stays idle, which is safe,
// since removing a runner asks GitHub first.
func (s *scaleSet) setStateFromGitHub(ctx context.Context, runner *types.Runner) {
	status, err := s.syncFromGitHub(ctx, runner.Name)
	if err != nil {
		s.logger.Debug("cannot ask GitHub about an adopted runner; taking it for idle",
			slog.String("runner", runner.Name), slog.Any("error", err))

		return
	}

	switch {
	case status == types.GitHubBusy:
		runner.State = types.RunnerBusy
	case status == types.GitHubIdle:
		runner.State = types.RunnerIdle
	case !runner.CreatedAt.IsZero() && s.now().Sub(runner.CreatedAt) <= s.spec.StartTimeout:
		runner.State = types.RunnerStarting
	}
}

// warnOtherInstallations warns of the scale set's runners that belong to
// another installation: another Rungar's, or this one's before github.url or
// installation changed.
func (s *scaleSet) warnOtherInstallations(counts map[string]int) {
	for installation, count := range counts {
		s.logger.Warn("runners of this scale set belong to another installation, and are left alone; "+
			"if github.url or installation was changed, they were left behind and need removing by hand, "+
			"otherwise another Rungar serves them",
			slog.Int("runners", count),
			slog.String("their_installation", installation),
			slog.String("installation", s.installation))
	}
}

// adoptedRunner returns a runner found on the fleet, idle until GitHub says
// otherwise.
func adoptedRunner(scaleSet string, machine types.Machine) *types.Runner {
	return &types.Runner{
		Name:      machine.Name,
		ScaleSet:  scaleSet,
		Provider:  machine.Provider,
		State:     types.RunnerIdle,
		Size:      machine.Size,
		CreatedAt: machine.CreatedAt,
		Adopted:   true,
		Revision:  machine.Labels[types.LabelRevision],
	}
}
