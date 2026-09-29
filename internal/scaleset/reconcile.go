// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/konradasb/rungar/internal/fleet"
	"github.com/konradasb/rungar/internal/types"
)

// unreachableGrace is how long a provider may not answer before its runners
// are forgotten and replaced elsewhere: long enough to ride out a restart of
// the backend.
const unreachableGrace = 5 * time.Minute

// removalGrace is how long reconciliation leaves a removed runner's machine
// alone: long enough for the deletion to finish and for older listings to go
// stale. A machine still there after it is adopted, and removed again.
const removalGrace = time.Minute

// reconcileFleet brings what the scale set knows in step with the fleet,
// which changes without GitHub saying.
//
// Unknown machines are adopted. Runners whose machines are gone are
// forgotten, and those whose machines have stopped are removed. The rest are
// checked with GitHub and removed as shouldRemove decides, replacing at most
// one that could still take a job per pass. Runners on a provider that cannot
// be listed are left alone until it has been unreachable for
// unreachableGrace, then forgotten.
func (s *scaleSet) reconcileFleet(ctx context.Context) error {
	machines, err := s.fleet.List(ctx, s.runnerSelector())

	var partial *fleet.UnreachableError
	if err != nil {
		if !errors.As(err, &partial) {
			return err
		}

		s.logger.Debug("some providers cannot be looked at", slog.Any("error", err))
	}

	now := time.Now()
	writtenOff := s.noteUnreachable(partial, now)
	s.forgetRemovals(now)

	onFleet := s.adoptUnknown(machines)
	replaced := false

	for _, runner := range s.list() {
		if partial != nil && partial.Unreachable(runner.Provider) {
			if writtenOff[runner.Provider] {
				s.logger.Warn("forgetting runner on a provider that has not answered for too long",
					slog.String("runner", runner.Name), slog.String("provider", runner.Provider))
				s.lose(ctx, runner, types.LossUnreachable)
			}

			continue
		}

		machine, onIt := onFleet[runner.Name]

		switch {
		case !onIt:
			s.logger.Info("forgetting runner whose VM is gone",
				slog.String("runner", runner.Name), slog.String("provider", runner.Provider))
			s.lose(ctx, runner, types.LossGone)

		case !machine.State.Alive():
			s.logger.Info("removing runner whose VM has stopped",
				slog.String("runner", runner.Name),
				slog.String("provider", runner.Provider),
				slog.String("state", string(machine.State)))

			if err := s.remove(ctx, runner.Name, types.RemovalStopped, true); err != nil {
				s.logger.Warn("cannot remove stopped runner",
					slog.String("runner", runner.Name), slog.Any("error", err))
			}

		default:
			if s.reconcileRunner(ctx, runner, now, replaced) {
				replaced = true
			}
		}
	}

	return nil
}

// adoptUnknown adopts the listed machines the scale set does not know, leaving
// those being made or recently removed, and returns every listed machine by
// name.
func (s *scaleSet) adoptUnknown(machines []types.Machine) map[string]types.Machine {
	onFleet := make(map[string]types.Machine, len(machines))

	for _, machine := range machines {
		onFleet[machine.Name] = machine

		s.mu.Lock()
		_, known := s.runners[machine.Name]
		_, removing := s.removed[machine.Name]
		adopt := !known && !removing && !s.creating[machine.Name]
		if adopt {
			s.runners[machine.Name] = adoptedRunner(s.spec.Name, machine)
		}
		s.mu.Unlock()

		if adopt {
			s.logger.Info("adopting unknown runner found on the fleet",
				slog.String("runner", machine.Name), slog.String("provider", machine.Provider))
			s.events.Record(adoptedEvent(s.spec.Name, machine.Name, machine.Provider, adoptedFound))
		}
	}

	return onFleet
}

// lose forgets a runner without touching its machine. One that was not
// running a job has its registration removed, which it would otherwise leave
// behind.
func (s *scaleSet) lose(ctx context.Context, runner types.Runner, reason types.LossReason) {
	s.forget(runner.Name)
	s.events.Record(lostEvent(s.spec.Name, runner.Name, runner.Provider, reason))

	if runner.State != types.RunnerBusy {
		s.tryDeregister(ctx, runner.Name)
	}
}

// reconcileRunner checks a runner whose machine is alive with GitHub and
// removes it if shouldRemove says so. A replacement is skipped when one has
// been made this pass already; it reports whether it made one.
func (s *scaleSet) reconcileRunner(ctx context.Context, runner types.Runner, now time.Time, replaced bool) bool {
	status, err := s.syncFromGitHub(ctx, runner.Name)
	if err != nil {
		s.logger.Debug("cannot ask GitHub about the runner",
			slog.String("runner", runner.Name), slog.Any("error", err))

		return false
	}

	rm, ok := shouldRemove(s.spec, runner, status, s.offlineFor(runner.Name, now), now)
	if !ok || rm.replacement && replaced {
		return false
	}

	// Replacing a runner that could still take a job is routine.
	level := slog.LevelWarn
	if rm.replacement {
		level = slog.LevelInfo
	}

	s.logger.Log(ctx, level, "removing runner",
		slog.String("runner", runner.Name),
		slog.String("provider", runner.Provider),
		slog.String("reason", string(rm.reason)))

	if err := s.remove(ctx, runner.Name, rm.reason, rm.force); err != nil {
		s.logger.Warn("cannot remove runner",
			slog.String("runner", runner.Name), slog.Any("error", err))
	}

	return rm.replacement
}

// syncFromGitHub asks GitHub about a runner and brings its state in line: busy
// if it is running a job, which GitHub may say before the job's message
// arrives, and idle if it was starting and has connected. It records when a
// runner is first found disconnected. Every question asked of GitHub about a
// runner goes through here, so the daemon's view never trails GitHub's.
func (s *scaleSet) syncFromGitHub(ctx context.Context, name string) (types.GitHubStatus, error) {
	status, err := s.gitHubStatuses.GitHubStatus(ctx, name)
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	runner, ok := s.runners[name]
	if !ok {
		return status, nil
	}

	switch status {
	case types.GitHubBusy:
		runner.State = types.RunnerBusy
		delete(s.offline, name)
	case types.GitHubIdle:
		if runner.State == types.RunnerStarting {
			runner.State = types.RunnerIdle
		}
		delete(s.offline, name)
	default:
		if _, ok := s.offline[name]; !ok {
			s.offline[name] = time.Now()
		}
	}

	return status, nil
}

// offlineFor returns how long GitHub has had a runner disconnected, or 0 if it
// is connected.
func (s *scaleSet) offlineFor(name string, now time.Time) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()

	since, ok := s.offline[name]
	if !ok {
		return 0
	}

	return now.Sub(since)
}

// noteUnreachable records since when each provider could not be listed, and
// returns those unreachable for longer than unreachableGrace.
func (s *scaleSet) noteUnreachable(partial *fleet.UnreachableError, now time.Time) map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	for name := range s.unreachable {
		if partial == nil || !partial.Unreachable(name) {
			delete(s.unreachable, name)
		}
	}

	writtenOff := map[string]bool{}
	if partial == nil {
		return writtenOff
	}

	for name := range partial.Providers {
		since, ok := s.unreachable[name]
		if !ok {
			s.unreachable[name] = now
			continue
		}
		if now.Sub(since) > unreachableGrace {
			writtenOff[name] = true
		}
	}

	return writtenOff
}

// forgetRemovals forgets the removals older than removalGrace.
func (s *scaleSet) forgetRemovals(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for name, at := range s.removed {
		if now.Sub(at) > removalGrace {
			delete(s.removed, name)
		}
	}
}
