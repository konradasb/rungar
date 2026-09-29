// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"log/slog"
	"maps"
	"slices"
	"time"

	"github.com/konradasb/rungar/internal/types"
)

// A runner leaves the scale set in one of two ways: the scale set removes it,
// for a reason of its own, or its machine ends, which a provider shows by
// listing the machine as stopped or by no longer listing it. Either way the
// runner stops counting at once, so that it is replaced, and is kept as
// leaving until its machine is deleted and what became of it is recorded.
//
// What became of a runner whose machine ended is GitHub's to say, not the
// provider's. GitHub drops an ephemeral runner's registration when its job
// completes, so a runner GitHub no longer has finished, and is recorded as
// removed for that; one GitHub still has after the start timeout ended without
// its job completing, and is recorded as lost. A runner's story so reads the
// same on every provider, however the provider ends its machines.

// outcome is what became of a runner leaving the scale set: removed for a
// reason, or lost for one. The zero outcome is not yet known.
type outcome struct {
	removal types.RemovalReason
	loss    types.LossReason
}

// known reports whether the outcome has been decided.
func (o outcome) known() bool {
	return o.removal != "" || o.loss != ""
}

// leavingRunner is a runner the scale set has let go of, whose machine is not
// yet deleted or whose outcome is not yet recorded. It is no longer one of the
// scale set's runners, and its machine is not adopted again.
type leavingRunner struct {
	runner  types.Runner
	outcome outcome

	// endedAt is when the runner's machine was found ended, or zero for a
	// runner the scale set removed.
	endedAt time.Time

	// deleted reports that the runner's machine has been deleted.
	deleted bool

	// settling is set while settle works on the runner, so that one caller
	// settles it at a time.
	settling bool
}

// endedOutcome returns what became of a runner whose machine ended endedFor
// ago, given GitHub's status for it: removed as its job completed if GitHub no
// longer has it, and lost if GitHub still has it after timeout. Until then it
// is not yet known: GitHub may list a runner whose job has just completed for
// a while, and one that crashed while running a job as busy.
func endedOutcome(status types.GitHubStatus, endedFor, timeout time.Duration) outcome {
	switch {
	case status == types.GitHubNotRegistered:
		return outcome{removal: types.RemovalJobCompleted}
	case endedFor > timeout:
		return outcome{loss: types.LossEnded}
	default:
		return outcome{}
	}
}

// letGo moves a runner from the scale set's runners to those leaving, with o
// as its outcome, and reports whether it was there to move: of two callers
// letting the same runner go, one does.
func (s *scaleSet) letGo(name string, o outcome, endedAt time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	runner, ok := s.runners[name]
	if !ok {
		return false
	}

	delete(s.runners, name)
	delete(s.offline, name)
	s.leaving[name] = &leavingRunner{runner: *runner, outcome: o, endedAt: endedAt}

	return true
}

// letGoEnded lets go of a runner whose machine has ended, in state, for settle
// to find out what became of it.
func (s *scaleSet) letGoEnded(runner types.Runner, state string, now time.Time) {
	if !s.letGo(runner.Name, outcome{}, now) {
		return
	}

	s.logger.Info("runner's machine has ended",
		slog.String("runner", runner.Name),
		slog.String("provider", runner.Provider),
		slog.String("state", state))
}

// settleLeaving settles every runner leaving the scale set. A machine that
// cannot be deleted is logged, and tried again on the next call.
func (s *scaleSet) settleLeaving(ctx context.Context, now time.Time) {
	s.mu.Lock()
	names := slices.Sorted(maps.Keys(s.leaving))
	s.mu.Unlock()

	for _, name := range names {
		if err := s.settle(ctx, name, now); err != nil {
			s.logger.Warn("cannot delete the machine of a runner that has left; trying again",
				slog.String("runner", name), slog.Any("error", err))
		}
	}
}

// settle takes a leaving runner as far out as it can: it deletes the runner's
// machine and, for one whose machine ended, asks GitHub what became of it.
// Once both are done it records the outcome and drops the runner. It returns
// the error deleting the machine; a runner not leaving, or being settled by
// another caller, is left alone.
func (s *scaleSet) settle(ctx context.Context, name string, now time.Time) error {
	s.mu.Lock()
	l, ok := s.leaving[name]
	if !ok || l.settling {
		s.mu.Unlock()
		return nil
	}
	l.settling = true
	runner, o, endedAt, deleted := l.runner, l.outcome, l.endedAt, l.deleted
	s.mu.Unlock()

	var err error
	if !deleted {
		err = s.deleteMachine(ctx, runner.Provider, name)
		deleted = err == nil
	}

	var status types.GitHubStatus
	if !o.known() {
		status, o = s.endedOutcomeOf(ctx, name, now.Sub(endedAt))
	}

	s.mu.Lock()
	l.settling = false
	l.deleted = deleted
	// An outcome set while GitHub was asked, by the job's completion, wins.
	if !l.outcome.known() {
		l.outcome = o
	}
	o = l.outcome
	done := deleted && o.known()
	if done {
		delete(s.leaving, name)
		s.left[name] = now
	}
	s.mu.Unlock()

	if done {
		s.recordOutcome(ctx, runner, o, status)
	}

	return err
}

// endedOutcomeOf asks GitHub what became of a runner whose machine ended
// endedFor ago, and returns its status with the outcome. Without an answer the
// outcome is not yet known.
func (s *scaleSet) endedOutcomeOf(ctx context.Context, name string, endedFor time.Duration,
) (types.GitHubStatus, outcome) {
	status, err := s.syncFromGitHub(ctx, name)
	if err != nil {
		s.logger.Debug("cannot ask GitHub what became of a runner whose machine ended",
			slog.String("runner", name), slog.Any("error", err))

		return "", outcome{}
	}

	return status, endedOutcome(status, endedFor, s.spec.StartTimeout)
}

// recordOutcome records the outcome of a runner that has left, whose GitHub
// status was last status, or empty if not asked. A lost runner not running a
// job has its registration removed, which it would otherwise leave behind;
// GitHub refuses that for one running a job.
func (s *scaleSet) recordOutcome(ctx context.Context, runner types.Runner, o outcome, status types.GitHubStatus) {
	if o.loss != "" {
		s.recordLoss(runner, o.loss)
		if status != types.GitHubBusy {
			s.tryRemoveRegistration(ctx, runner.Name)
		}

		return
	}

	s.metrics.CountRunnerRemoved(s.spec.Name, runner.Provider, o.removal)
	s.logger.Info("runner removed", slog.String("runner", runner.Name), slog.String("provider", runner.Provider),
		slog.String("reason", string(o.removal)))
	s.events.Record(removedEvent(s.spec.Name, runner.Name, runner.Provider, o.removal))
}

// recordLoss records a runner lost for reason.
func (s *scaleSet) recordLoss(runner types.Runner, reason types.LossReason) {
	s.metrics.CountRunnerLost(s.spec.Name, runner.Provider, reason)
	s.logger.Warn("runner lost", slog.String("runner", runner.Name), slog.String("provider", runner.Provider),
		slog.String("reason", string(reason)))
	s.events.Record(lostEvent(s.spec.Name, runner.Name, runner.Provider, reason))
}
