// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"log/slog"
	"time"

	ghscaleset "github.com/actions/scaleset"

	"github.com/konradasb/rungar/internal/types"
)

// HandleJobStarted records that a job has started on a runner, and how long
// the job waited for it.
func (s *scaleSet) HandleJobStarted(_ context.Context, msg *ghscaleset.JobStarted) error {
	job := jobOf(msg, s.now())
	wait, known := waitOf(msg)

	attrs := []any{
		slog.String("runner", msg.RunnerName),
		slog.String("job", job.DisplayName),
		slog.String("job_id", job.ID),
		slog.Int64("workflow_run_id", job.WorkflowRunID),
		slog.String("repository", job.Repository),
		slog.String("workflow", job.WorkflowRef),
	}
	if known {
		attrs = append(attrs, slog.Duration("wait", wait.total), slog.Duration("runner_wait", wait.forRunner))
		s.metrics.ObserveJobWait(s.spec.Name, wait.total, wait.forRunner)
	}
	s.logger.Info("job started", attrs...)

	if runner, ok := s.markBusy(msg.RunnerName, job); ok {
		s.events.Record(jobStartedEvent(runner, wait, known))
	}
	s.metrics.CountJobStarted(s.spec.Name)

	return nil
}

// HandleJobCompleted removes the runner that ran the job, unless the scale set
// no longer serves. A failure is logged rather than returned, which would stop
// the listener: the machine ends with its job, and reconciliation removes the
// runner.
func (s *scaleSet) HandleJobCompleted(ctx context.Context, job *ghscaleset.JobCompleted) error {
	s.logger.Info("job completed",
		slog.String("runner", job.RunnerName),
		slog.String("job", job.JobDisplayName),
		slog.String("job_id", job.JobID),
		slog.Int64("workflow_run_id", job.WorkflowRunID),
		slog.String("result", job.Result))

	s.metrics.CountJobCompleted(s.spec.Name, job.Result)

	// A job cancelled before a runner took it completes with none. Once the
	// session is let go, the runner is left to whichever daemon holds it
	// next.
	if job.RunnerName == "" || !s.serving.Load() {
		return nil
	}

	if err := s.remove(ctx, job.RunnerName, types.RemovalJobCompleted, false); err != nil {
		s.logger.Warn("cannot remove the runner; reconciliation will",
			slog.String("runner", job.RunnerName), slog.Any("error", err))
	}

	return nil
}

// jobOf returns the job a job started message describes, which came at now.
func jobOf(msg *ghscaleset.JobStarted, now time.Time) types.Job {
	job := types.Job{
		ID:            msg.JobID,
		Repository:    msg.RepositoryName,
		WorkflowRef:   msg.JobWorkflowRef,
		DisplayName:   msg.JobDisplayName,
		WorkflowRunID: msg.WorkflowRunID,
		StartedAt:     msg.RunnerAssignTime,
	}
	if msg.OwnerName != "" && msg.RepositoryName != "" {
		job.Repository = msg.OwnerName + "/" + msg.RepositoryName
	}
	// GitHub may leave it out; the message came just now.
	if job.StartedAt.IsZero() {
		job.StartedAt = now
	}

	return job
}

// jobWait is how long a job waited: in all, since GitHub queued it, and for
// a runner, since GitHub assigned it to the scale set.
type jobWait struct {
	total, forRunner time.Duration
}

// waitOf returns how long the job a job started message describes waited,
// and whether GitHub gave the times to tell. Every time is GitHub's, so no
// clock is compared with another.
func waitOf(msg *ghscaleset.JobStarted) (jobWait, bool) {
	queued, assigned, taken := msg.QueueTime, msg.ScaleSetAssignTime, msg.RunnerAssignTime
	if queued.IsZero() || assigned.IsZero() || taken.IsZero() {
		return jobWait{}, false
	}

	return jobWait{total: max(taken.Sub(queued), 0), forRunner: max(taken.Sub(assigned), 0)}, true
}

// markBusy marks a runner busy with a job, and returns it, or false if the
// scale set does not know it. A runner still starting has connected, if only
// just.
func (s *scaleSet) markBusy(name string, job types.Job) (types.Runner, bool) {
	s.mu.Lock()

	runner, ok := s.runners[name]
	if !ok {
		s.mu.Unlock()
		s.logger.Warn("job started on a runner Rungar does not know", slog.String("runner", name))

		return types.Runner{}, false
	}

	connected := runner.State == types.RunnerStarting
	if connected {
		runner.ConnectedAt = s.now()
	}
	runner.State = types.RunnerBusy
	runner.Job = job
	snapshot := *runner

	s.mu.Unlock()

	if connected {
		s.recordConnected(snapshot)
	}

	return snapshot, true
}

// recordConnected records a runner seen connecting to GitHub for the first
// time, with its boot duration. A runner whose boot duration is not known,
// one adopted rather than created by this daemon, is not recorded.
func (s *scaleSet) recordConnected(r types.Runner) {
	bootDuration, known := r.BootDuration()
	if !known {
		return
	}

	s.logger.Info("runner connected",
		slog.String("runner", r.Name),
		slog.String("provider", r.Provider),
		slog.Duration("boot_duration", bootDuration))
	s.metrics.ObserveRunnerBootDuration(s.spec.Name, r.Provider, bootDuration)
	s.events.Record(connectedEvent(r, bootDuration))
}
