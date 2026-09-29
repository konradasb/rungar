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

// unreachableGrace is how long a provider may be unreachable before its
// runners are forgotten and replaced elsewhere: long enough to ride out a
// restart of the backend.
const unreachableGrace = 5 * time.Minute

// leftGrace is how long reconciliation holds off adopting the machine of a
// runner that has left: long enough for listings older than its deletion to
// go stale. A machine still listed after that is adopted, and removed again.
const leftGrace = time.Minute

// reconcileFleet brings what the scale set knows in step with the fleet,
// which changes without GitHub saying.
//
// Unknown machines are adopted. Runners whose machines have ended, stopped or
// gone, are let go of and settled with the others leaving; see leavingRunner.
// The rest are checked with GitHub and removed as decideRemoval decides,
// replacing at most one that could still take a job per pass. Runners on a
// provider that cannot be listed are left alone until it has been unreachable
// for unreachableGrace, then forgotten. Orphaned registrations are removed
// once every provider has been listed or written off.
func (s *scaleSet) reconcileFleet(ctx context.Context) error {
	// A runner created while the providers are listed may be missing from
	// the listing without being gone.
	s.mu.Lock()
	knownBefore := make(map[string]bool, len(s.runners))
	for name := range s.runners {
		knownBefore[name] = true
	}
	s.mu.Unlock()

	machines, err := s.providers.List(ctx, s.runnerSelector())

	var partial *fleet.UnreachableError
	if err != nil {
		if !errors.As(err, &partial) {
			return err
		}

		s.logger.Debug("some providers cannot be listed", slog.Any("error", err))
	}

	now := s.now()
	writtenOff := s.noteUnreachable(partial, now)
	s.forgetLeft(now)

	onFleet := s.adoptUnknown(machines)
	replaced := false

	for _, runner := range s.sortedRunners() {
		if partial != nil && partial.Includes(runner.Provider) {
			if writtenOff[runner.Provider] {
				s.logger.Warn("forgetting runner on a provider unreachable for too long",
					slog.String("runner", runner.Name), slog.String("provider", runner.Provider))
				s.lose(ctx, runner, types.LossUnreachable)
			}

			continue
		}

		machine, onIt := onFleet[runner.Name]

		switch {
		case !onIt && !knownBefore[runner.Name]:
			// Created since the listing; the next pass checks it.
		case !onIt:
			s.letGoEnded(runner, "gone", now)
		case !machine.State.Alive():
			s.letGoEnded(runner, string(machine.State), now)
		case s.reconcileRunner(ctx, runner, now, replaced):
			replaced = true
		}
	}

	s.settleLeaving(ctx, now)

	if partial == nil || len(writtenOff) == len(partial.Providers) {
		s.removeOrphans(ctx, now)
	}

	return nil
}

// adoptUnknown adopts the listed machines the scale set does not track, and
// returns every listed machine by name.
func (s *scaleSet) adoptUnknown(machines []types.Machine) map[string]types.Machine {
	onFleet := make(map[string]types.Machine, len(machines))

	for _, machine := range machines {
		onFleet[machine.Name] = machine

		s.mu.Lock()
		adopt := !s.tracks(machine.Name)
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

// lose forgets a runner without touching its machine, and records it lost for
// reason. One not running a job has its registration removed, which it would
// otherwise leave behind.
func (s *scaleSet) lose(ctx context.Context, runner types.Runner, reason types.LossReason) {
	s.forget(runner.Name)
	s.recordLoss(runner, reason)
	if runner.State != types.RunnerBusy {
		s.tryRemoveRegistration(ctx, runner.Name)
	}
}

// reconcileRunner checks a runner whose machine is alive with GitHub and
// removes it if decideRemoval says so. A runner due for replacement is left
// when the pass has already removed one for that; it reports whether it
// removed one for replacement.
func (s *scaleSet) reconcileRunner(ctx context.Context, runner types.Runner, now time.Time, replaced bool) bool {
	status, err := s.syncFromGitHub(ctx, runner.Name)
	if err != nil {
		s.logger.Debug("cannot ask GitHub about the runner",
			slog.String("runner", runner.Name), slog.Any("error", err))

		return false
	}

	decision, ok := decideRemoval(s.spec, runner, status, s.offlineFor(runner.Name, now), now)
	if !ok || decision.replacement && replaced {
		return false
	}

	// Replacing a runner that could still take a job is routine.
	level := slog.LevelWarn
	if decision.replacement {
		level = slog.LevelInfo
	}

	s.logger.Log(ctx, level, "removing runner",
		slog.String("runner", runner.Name),
		slog.String("provider", runner.Provider),
		slog.String("reason", string(decision.reason)))

	if err := s.remove(ctx, runner.Name, decision.reason, decision.force); err != nil {
		s.logger.Warn("cannot remove runner",
			slog.String("runner", runner.Name), slog.Any("error", err))
	}

	return decision.replacement
}

// syncFromGitHub asks GitHub about a runner and brings its state in line: busy
// if it is running a job, which GitHub may say before the job's message
// arrives, and idle if it was starting and has connected. It records when a
// runner is first found disconnected, and when one first connects. Every
// question asked of GitHub about a runner goes through here, so the daemon's
// view never trails GitHub's.
func (s *scaleSet) syncFromGitHub(ctx context.Context, name string) (types.GitHubStatus, error) {
	status, err := s.gitHubStatuses.GitHubStatus(ctx, name)
	if err != nil {
		return "", err
	}

	s.mu.Lock()

	runner, ok := s.runners[name]
	if !ok {
		s.mu.Unlock()
		return status, nil
	}

	wasStarting := runner.State == types.RunnerStarting

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
			s.offline[name] = s.now()
		}
	}

	connected := wasStarting && runner.State != types.RunnerStarting
	if connected {
		runner.ConnectedAt = s.now()
	}
	snapshot := *runner

	s.mu.Unlock()

	if connected {
		s.recordConnected(snapshot)
	}

	return status, nil
}

// syncStartingRunners syncs the runners still starting with GitHub, to see
// them connect sooner than reconciliation would. GitHub is listed no more
// often than its cache allows, however often this is called.
func (s *scaleSet) syncStartingRunners(ctx context.Context) {
	for _, runner := range s.sortedRunners() {
		if runner.State != types.RunnerStarting {
			continue
		}

		if _, err := s.syncFromGitHub(ctx, runner.Name); err != nil {
			s.logger.Debug("cannot ask GitHub about the starting runner",
				slog.String("runner", runner.Name), slog.Any("error", err))

			return
		}
	}
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
		if partial == nil || !partial.Includes(name) {
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

// forgetLeft forgets the runners that left longer than leftGrace ago.
func (s *scaleSet) forgetLeft(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for name, at := range s.left {
		if now.Sub(at) > leftGrace {
			delete(s.left, name)
		}
	}
}
