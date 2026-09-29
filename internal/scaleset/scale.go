// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"errors"
	"log/slog"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/types"
)

// HandleDesiredRunnerCount scales the scale set to assignedJobs, as scaleTo
// does. The listener calls it each time GitHub answers its poll, with a
// message or without.
//
// Failures are logged, never returned: an error stops the listener, and
// GitHub asks again with its next message anyway.
func (s *scaleSet) HandleDesiredRunnerCount(ctx context.Context, assignedJobs int) (int, error) {
	s.metrics.SetLastPoll(s.spec.Name, s.now())

	return s.scaleTo(ctx, assignedJobs), nil
}

// scaleTo creates or removes runners until the scale set has as many as
// assignedJobs calls for within the minimum in force and its maximum, or none
// while it is paused, and returns how many it has. Only runners not running a
// job are removed. It stops creating or removing once the scale set no longer
// serves.
func (s *scaleSet) scaleTo(ctx context.Context, assignedJobs int) int {
	s.decisionLock.Lock()
	defer s.decisionLock.Unlock()

	return s.scaleToLocked(ctx, assignedJobs)
}

// scaleToLocked is scaleTo with s.decisionLock held.
func (s *scaleSet) scaleToLocked(ctx context.Context, assignedJobs int) int {
	s.mu.Lock()
	s.assigned, s.assignedKnown = assignedJobs, true
	s.mu.Unlock()
	s.metrics.SetJobsAssigned(s.spec.Name, assignedJobs)

	target := min(s.spec.MaxRunners, s.updateMinRunners()+assignedJobs)
	if s.isPaused() {
		target = 0
	}
	current := s.runnerCount()

	s.desired.Store(int64(target))
	s.metrics.SetDesiredRunners(s.spec.Name, target)

	switch {
	case target < current:
		if removed := s.removeIdle(ctx, current-target); removed > 0 {
			s.logger.Info("scaled down",
				slog.Int("removed", removed),
				slog.Int("target", target),
				slog.Int("assigned_jobs", assignedJobs))
		}

		return s.runnerCount()
	case target == current:
		return current
	}

	s.logger.Info("scaling up",
		slog.Int("current", current),
		slog.Int("target", target),
		slog.Int("assigned_jobs", assignedJobs))
	for range target - current {
		// Paused, or the session let go, while runners were being created.
		// The listener hands on its messages with a context never
		// cancelled, so it is serving that keeps a decision from outliving
		// the session.
		if s.isPaused() || !s.serving.Load() {
			break
		}

		r, err := s.createRunner(ctx)
		if err != nil {
			// GitHub keeps the job queued.
			if errors.Is(err, errdefs.ErrNoCapacity) {
				s.logger.Warn("cannot scale up further", slog.Any("error", err))
				s.metrics.CountScaleUpFailed(s.spec.Name, types.CallNoCapacity)
			} else {
				s.logger.Error("cannot create a runner", slog.Any("error", err))
				s.metrics.CountScaleUpFailed(s.spec.Name, types.CallError)
			}

			return s.runnerCount()
		}

		s.metrics.CountRunnerCreated(s.spec.Name, r.Provider)
	}

	return s.runnerCount()
}

// desiredCount returns the target of the last scaling decision, and whether
// there has been one.
func (s *scaleSet) desiredCount() (int, bool) {
	n := s.desired.Load()

	return int(n), n >= 0
}

// retry repeats the last scaling decision with the last job count GitHub sent,
// since GitHub's messages come only every ~50s on a quiet scale set. It does
// nothing before the first. The count is read under decisionLock, so that a
// retry never undoes a newer decision with an older count.
func (s *scaleSet) retry(ctx context.Context) {
	if s.retryWaiting != nil {
		s.retryWaiting()
	}

	s.decisionLock.Lock()
	defer s.decisionLock.Unlock()

	s.mu.Lock()
	assignedJobs, known := s.assigned, s.assignedKnown
	s.mu.Unlock()

	if !known {
		return
	}

	s.scaleToLocked(ctx, assignedJobs)
}
