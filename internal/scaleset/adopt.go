// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/konradasb/rungar/internal/fleet"
	"github.com/konradasb/rungar/internal/types"
)

// How a runner came to be adopted, as its event says.
const (
	adoptedAtStart = "left there by an earlier daemon"
	adoptedFound   = "found on the fleet, unknown to the daemon"
)

// adopt takes over the scale set's runners already on the fleet: those alive
// are adopted with their state from GitHub, and those stopped are removed.
// Runners on providers that cannot be listed are adopted later, by
// reconcileFleet.
func (s *scaleSet) adopt(ctx context.Context) error {
	// Listed without the installation, so that another installation's
	// runners of the same scale set are seen, and warned of.
	machines, err := s.fleet.List(ctx, map[string]string{
		types.LabelManaged:  "true",
		types.LabelScaleSet: s.spec.Name,
	})
	if err != nil {
		var unreachable *fleet.UnreachableError
		if !errors.As(err, &unreachable) {
			return fmt.Errorf("list runners: %w", err)
		}

		s.logger.Warn("adopting what can be seen; the rest once its providers answer", slog.Any("error", err))
	}

	adopted := map[string]*types.Runner{}
	strangers := map[string]int{}

	for _, machine := range machines {
		if other := machine.Labels[types.LabelInstallation]; other != s.installation {
			strangers[other]++
			continue
		}

		if !machine.State.Alive() {
			s.removeFinished(ctx, machine)
			continue
		}

		runner := adoptedRunner(s.spec.Name, machine)
		s.adoptState(ctx, runner)
		adopted[machine.Name] = runner
		s.events.Record(adoptedEvent(s.spec.Name, machine.Name, machine.Provider, adoptedAtStart))
	}

	s.mu.Lock()
	s.runners = adopted
	s.mu.Unlock()
	s.adopted.Store(true)

	if len(adopted) > 0 {
		s.logger.Info("adopted runners already on the fleet", slog.Int("count", len(adopted)))
	}

	s.warnStrangers(strangers)

	return nil
}

// removeFinished removes the machine of a runner found stopped at start, and
// its registration.
func (s *scaleSet) removeFinished(ctx context.Context, machine types.Machine) {
	s.logger.Info("removing finished runner left behind",
		slog.String("runner", machine.Name),
		slog.String("provider", machine.Provider),
		slog.String("state", string(machine.State)))

	s.tryDeregister(ctx, machine.Name)

	if err := s.deleteMachine(ctx, machine.Provider, machine.Name); err != nil {
		s.logger.Warn("cannot remove finished runner",
			slog.String("runner", machine.Name),
			slog.String("provider", machine.Provider),
			slog.Any("error", err))

		return
	}

	s.events.Record(removedEvent(s.spec.Name, machine.Name, machine.Provider, types.RemovalStopped))
}

// adoptState sets an adopted runner's state from GitHub: busy if it is running
// a job, idle if it is connected, and starting if it is neither and within its
// start timeout. Without an answer it stays idle, which is safe, since removing
// a runner asks GitHub first.
func (s *scaleSet) adoptState(ctx context.Context, runner *types.Runner) {
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
	case !runner.CreatedAt.IsZero() && time.Since(runner.CreatedAt) <= s.spec.StartTimeout:
		runner.State = types.RunnerStarting
	}
}

// warnStrangers warns of the scale set's runners that belong to another
// installation: another Rungar's, or this one's before github.url or
// installation changed.
func (s *scaleSet) warnStrangers(strangers map[string]int) {
	for installation, count := range strangers {
		s.logger.Warn("runners of this scale set belong to another installation, and are left alone; "+
			"if github.url or installation was changed, they were left behind and need removing by hand, "+
			"otherwise another Rungar serves them",
			slog.Int("count", count),
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
