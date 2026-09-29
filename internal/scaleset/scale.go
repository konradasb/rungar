// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"errors"
	"log/slog"

	ghscaleset "github.com/actions/scaleset"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/types"
)

// HandleJobStarted records that a job has started on a runner.
func (s *scaleSet) HandleJobStarted(_ context.Context, job *ghscaleset.JobStarted) error {
	s.logger.Info("job started",
		slog.String("runner", job.RunnerName),
		slog.String("job", job.JobDisplayName),
		slog.String("repository", job.RepositoryName))

	s.jobStarted(job.RunnerName, job.JobID)
	s.metrics.CountJobStarted(s.spec.Name)

	return nil
}

// HandleJobCompleted removes the runner that ran the job. A failure is logged
// rather than returned, which would stop the listener: the machine removes
// itself, or reconciliation removes it.
func (s *scaleSet) HandleJobCompleted(ctx context.Context, job *ghscaleset.JobCompleted) error {
	s.logger.Info("job completed",
		slog.String("runner", job.RunnerName),
		slog.String("job", job.JobDisplayName),
		slog.String("result", job.Result))

	s.metrics.CountJobCompleted(s.spec.Name, job.Result)

	// A job cancelled before a runner took it completes with none.
	if job.RunnerName == "" {
		return nil
	}

	if err := s.removeRunner(ctx, job.RunnerName, types.RemovalJobCompleted); err != nil {
		s.logger.Warn("cannot remove the runner; reconciliation will",
			slog.String("runner", job.RunnerName), slog.Any("error", err))
	}

	return nil
}

// HandleDesiredRunnerCount makes or removes runners until the scale set has as
// many as count, its assigned jobs, calls for within its minimum and maximum,
// or none while it is paused, and returns how many it has. Only runners not
// running a job are removed.
//
// Failures are logged, never returned: an error stops the listener, and
// GitHub asks again with its next message anyway.
func (s *scaleSet) HandleDesiredRunnerCount(ctx context.Context, count int) (int, error) {
	s.decide.Lock()
	defer s.decide.Unlock()

	s.assigned, s.assignedKnown = count, true

	target := min(s.spec.MaxRunners, s.spec.MinRunners+count)
	if s.isPaused() {
		target = 0
	}
	current := s.count()

	s.desired.Store(int64(target))
	s.metrics.SetDesiredRunners(s.spec.Name, target)

	switch {
	case target < current:
		if removed := s.removeIdle(ctx, current-target); removed > 0 {
			s.logger.Info("scaled down",
				slog.Int("removed", removed),
				slog.Int("target", target),
				slog.Int("assigned_jobs", count))
		}

		return s.count(), nil
	case target == current:
		return current, nil
	}

	s.logger.Info("scaling up",
		slog.Int("current", current),
		slog.Int("target", target),
		slog.Int("assigned_jobs", count))

	for range target - current {
		// Paused while runners were being made.
		if s.isPaused() {
			break
		}

		r, err := s.createRunner(ctx)
		if err != nil {
			// GitHub keeps the job queued.
			if errors.Is(err, errdefs.ErrNoCapacity) {
				s.logger.Warn("cannot scale up further", slog.String("reason", err.Error()))
				s.metrics.CountScaleUpFailed(s.spec.Name, "no_capacity")
			} else {
				s.logger.Error("cannot create a runner", slog.Any("error", err))
				s.metrics.CountScaleUpFailed(s.spec.Name, "error")
			}

			return s.count(), nil
		}

		s.metrics.CountRunnerCreated(s.spec.Name, r.Provider)
	}

	return s.count(), nil
}

// desiredCount returns the target of the last scaling decision, and whether
// there has been one.
func (s *scaleSet) desiredCount() (int, bool) {
	n := s.desired.Load()

	return int(n), n >= 0
}

// retry repeats the last scaling decision with the last job count GitHub sent,
// since GitHub's messages come only every ~50s on a quiet scale set. It does
// nothing before the first.
func (s *scaleSet) retry(ctx context.Context) {
	s.decide.Lock()
	count, known := s.assigned, s.assignedKnown
	s.decide.Unlock()

	if !known {
		return
	}

	_, _ = s.HandleDesiredRunnerCount(ctx, count)
}

// jobStarted marks a runner busy with a job.
func (s *scaleSet) jobStarted(name, jobID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	runner, ok := s.runners[name]
	if !ok {
		s.logger.Warn("job started on a runner Rungar does not know", slog.String("runner", name))
		return
	}

	runner.State = types.RunnerBusy
	runner.JobID = jobID
}
