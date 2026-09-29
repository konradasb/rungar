// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	ghscaleset "github.com/actions/scaleset"

	"github.com/konradasb/rungar/internal/events"
	"github.com/konradasb/rungar/internal/types"
)

// jobStartedAt returns a job started message for a runner, with GitHub's
// times: queued, assigned to the scale set a minute later, and to the runner
// 30 seconds after that.
func jobStartedAt(runner string, queued time.Time) *ghscaleset.JobStarted {
	return &ghscaleset.JobStarted{
		RunnerName: runner,
		JobMessageBase: ghscaleset.JobMessageBase{
			JobID:              "job-1",
			JobDisplayName:     "build",
			OwnerName:          "octo",
			RepositoryName:     "tests",
			WorkflowRunID:      7,
			QueueTime:          queued,
			ScaleSetAssignTime: queued.Add(time.Minute),
			RunnerAssignTime:   queued.Add(time.Minute + 30*time.Second),
		},
	}
}

// TestJobWaitIsMeasuredByGitHubsTimes checks a job's wait is told from the
// times GitHub gives, and not at all when one is missing.
func TestJobWaitIsMeasuredByGitHubsTimes(t *testing.T) {
	queued := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		change func(m *ghscaleset.JobMessageBase)
		want   jobWait
		ok     bool
	}{
		{
			name:   "every time given",
			change: func(*ghscaleset.JobMessageBase) {},
			want:   jobWait{total: 90 * time.Second, forRunner: 30 * time.Second},
			ok:     true,
		},
		{
			name:   "no queue time",
			change: func(m *ghscaleset.JobMessageBase) { m.QueueTime = time.Time{} },
		},
		{
			name:   "no scale set assignment",
			change: func(m *ghscaleset.JobMessageBase) { m.ScaleSetAssignTime = time.Time{} },
		},
		{
			name:   "no runner assignment",
			change: func(m *ghscaleset.JobMessageBase) { m.RunnerAssignTime = time.Time{} },
		},
		{
			name: "times out of order",
			change: func(m *ghscaleset.JobMessageBase) {
				m.RunnerAssignTime = m.ScaleSetAssignTime.Add(-time.Second)
			},
			want: jobWait{total: 59 * time.Second, forRunner: 0},
			ok:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := jobStartedAt("r1", queued)
			tt.change(&m.JobMessageBase)

			got, ok := waitOf(m)
			if ok != tt.ok || got != tt.want {
				t.Errorf("waitOf() = %+v, %v, want %+v, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

// TestARunnersTimelineIsRecorded checks a runner created, taking a job while
// still starting, and removed, is recorded in order, with how long each step
// took, and that the job's wait and the runner's create and boot durations
// are measured.
func TestARunnersTimelineIsRecorded(t *testing.T) {
	s, _, rec := newScaling(t, 10, 0, 10)
	ctx := context.Background()

	if _, err := s.HandleDesiredRunnerCount(ctx, 1); err != nil {
		t.Fatal(err)
	}
	name := s.sortedRunners()[0].Name

	if err := s.HandleJobStarted(ctx, jobStartedAt(name, time.Now())); err != nil {
		t.Fatal(err)
	}
	if err := s.HandleJobCompleted(ctx, &ghscaleset.JobCompleted{RunnerName: name, Result: "succeeded"}); err != nil {
		t.Fatal(err)
	}

	recorded := recordedBy(t, s.events)
	want := []happened{
		{action: events.ActionCreated, name: name},
		{action: events.ActionConnected, name: name},
		{action: events.ActionJobStarted, name: name},
		{action: events.ActionRemoved, name: name, reason: string(types.RemovalJobCompleted)},
	}
	if got := happenings(recorded); !slices.Equal(got, want) {
		t.Fatalf("events = %+v, want %+v", got, want)
	}

	if recorded[0].Attributes["create_duration"] == "" {
		t.Errorf("created event attributes = %v, want its create duration", recorded[0].Attributes)
	}
	if recorded[1].Attributes["boot_duration"] == "" {
		t.Errorf("connected event attributes = %v, want its boot duration", recorded[1].Attributes)
	}

	started := recorded[2]
	for key, want := range map[string]string{
		"job_id": "job-1", "repository": "octo/tests", "workflow_run_id": "7",
		"wait": "1m30s", "runner_wait": "30s",
	} {
		if got := started.Attributes[key]; got != want {
			t.Errorf("job started %s = %q, want %q", key, got, want)
		}
	}
	wantMessage := "Job started: build of octo/tests, after waiting 1m30s, 30s of it for a runner"
	if started.Message != wantMessage {
		t.Errorf("message = %q, want %q", started.Message, wantMessage)
	}

	if want := []jobWait{{total: 90 * time.Second, forRunner: 30 * time.Second}}; !slices.Equal(rec.jobWaits, want) {
		t.Errorf("job waits = %v, want %v", rec.jobWaits, want)
	}
	if len(rec.createDurations) != 1 || len(rec.bootDurations) != 1 {
		t.Errorf("recorded %d create and %d boot durations, want one of each",
			len(rec.createDurations), len(rec.bootDurations))
	}
}

// TestAJobWithoutGitHubsTimesIsNotMeasured checks a job whose message lacks
// GitHub's times is recorded without a wait rather than with a made-up one.
func TestAJobWithoutGitHubsTimesIsNotMeasured(t *testing.T) {
	s, _, rec := newScaling(t, 10, 0, 10)
	ctx := context.Background()

	if _, err := s.HandleDesiredRunnerCount(ctx, 1); err != nil {
		t.Fatal(err)
	}
	name := s.sortedRunners()[0].Name

	err := s.HandleJobStarted(ctx, &ghscaleset.JobStarted{
		RunnerName:     name,
		JobMessageBase: ghscaleset.JobMessageBase{JobID: "job-1", JobDisplayName: "build"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(rec.jobWaits) != 0 {
		t.Errorf("job waits = %v, want none", rec.jobWaits)
	}

	started := recordedBy(t, s.events)[2]
	if _, ok := started.Attributes["wait"]; ok || started.Message != "Job started: build" {
		t.Errorf("job started event = %+v, want one without a wait", started)
	}
}

// TestARunnerSeenConnectingOnGitHubIsRecordedOnce checks a starting runner that
// GitHub lists as connected is marked so, and its boot duration measured,
// once.
func TestARunnerSeenConnectingOnGitHubIsRecordedOnce(t *testing.T) {
	online := map[string]answer{}
	s, _, rec := newScaling(t, 10, 0, 10, func(g *fakeGitHub) { g.status = registered(online) })
	ctx := context.Background()

	if _, err := s.HandleDesiredRunnerCount(ctx, 1); err != nil {
		t.Fatal(err)
	}
	name := s.sortedRunners()[0].Name

	online[name] = answer{Registered: true}
	s.syncStartingRunners(ctx)

	if r := s.sortedRunners()[0]; r.State != types.RunnerStarting || !r.ConnectedAt.IsZero() {
		t.Fatalf("runner = %+v, want still starting while GitHub has it offline", r)
	}

	online[name] = answer{Registered: true, Online: true}
	s.syncStartingRunners(ctx)
	s.syncStartingRunners(ctx)

	if r := s.sortedRunners()[0]; r.State != types.RunnerIdle || r.ConnectedAt.IsZero() {
		t.Errorf("runner = %+v, want idle and connected", r)
	}
	if len(rec.bootDurations) != 1 {
		t.Errorf("recorded %d boot durations, want 1", len(rec.bootDurations))
	}

	var connected int
	for _, e := range recordedBy(t, s.events) {
		if e.Action == events.ActionConnected {
			connected++
		}
	}
	if connected != 1 {
		t.Errorf("recorded %d connected events, want 1", connected)
	}
}

func TestJobStartedMarksTheRunnerBusy(t *testing.T) {
	s, _, rec := newScaling(t, 10, 0, 10)
	ctx := context.Background()

	if _, err := s.HandleDesiredRunnerCount(ctx, 1); err != nil {
		t.Fatal(err)
	}
	name := s.sortedRunners()[0].Name
	assigned := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

	err := s.HandleJobStarted(ctx, &ghscaleset.JobStarted{
		RunnerName: name,
		JobMessageBase: ghscaleset.JobMessageBase{
			JobID:            "job-1",
			JobDisplayName:   "build",
			OwnerName:        "octo",
			RepositoryName:   "tests",
			JobWorkflowRef:   "octo/tests/.github/workflows/ci.yaml@refs/heads/main",
			WorkflowRunID:    7,
			RunnerAssignTime: assigned,
		},
	})
	if err != nil {
		t.Fatalf("HandleJobStarted() = %v", err)
	}

	want := types.Job{
		ID:            "job-1",
		Repository:    "octo/tests",
		WorkflowRef:   "octo/tests/.github/workflows/ci.yaml@refs/heads/main",
		DisplayName:   "build",
		WorkflowRunID: 7,
		StartedAt:     assigned,
	}
	if r := s.sortedRunners()[0]; r.State != types.RunnerBusy || r.Job != want {
		t.Errorf("runner = %+v, want busy with %+v", r, want)
	}
	if rec.jobsStarted != 1 {
		t.Errorf("recorded %d jobs started, want 1", rec.jobsStarted)
	}
}

func TestJobCompletedRemovesTheRunner(t *testing.T) {
	s, f, rec := newScaling(t, 10, 0, 10)
	ctx := context.Background()

	if _, err := s.HandleDesiredRunnerCount(ctx, 1); err != nil {
		t.Fatal(err)
	}
	name := s.sortedRunners()[0].Name

	err := s.HandleJobCompleted(ctx, &ghscaleset.JobCompleted{
		RunnerName:     name,
		Result:         "succeeded",
		JobMessageBase: ghscaleset.JobMessageBase{JobID: "job-1", JobDisplayName: "build"},
	})
	if err != nil {
		t.Fatalf("HandleJobCompleted() = %v", err)
	}

	if len(f.deleted) != 1 || f.deleted[0].name != name {
		t.Errorf("deleted %v, want %s: a runner takes one job", f.deleted, name)
	}
	if want := []types.RemovalReason{types.RemovalJobCompleted}; !slices.Equal(rec.removed, want) {
		t.Errorf("recorded removals %v, want %v", rec.removed, want)
	}
	if len(rec.jobResults) != 1 || rec.jobResults[0] != "succeeded" {
		t.Errorf("recorded results %v, want [succeeded]", rec.jobResults)
	}
}

// TestJobCompletedSurvivesAFailedRemoval checks that a provider failing to
// delete one machine does not stop the listener: reconciliation removes the
// runner later.
func TestJobCompletedSurvivesAFailedRemoval(t *testing.T) {
	s, f, _ := newScaling(t, 10, 0, 10)
	ctx := context.Background()

	if _, err := s.HandleDesiredRunnerCount(ctx, 1); err != nil {
		t.Fatal(err)
	}
	f.deleteErr = errors.New("host unreachable")

	err := s.HandleJobCompleted(ctx, &ghscaleset.JobCompleted{RunnerName: s.sortedRunners()[0].Name, Result: "failed"})
	if err != nil {
		t.Fatalf("HandleJobCompleted() = %v, want nil: the listener would stop", err)
	}
}

// TestCancelledJobsAreStillCompletions checks that a cancelled job frees its
// runner: GitHub reports the reassignment as a completion with a result of
// "canceled", and the machine must not be left behind.
func TestCancelledJobsAreStillCompletions(t *testing.T) {
	s, f, rec := newScaling(t, 10, 0, 10)
	ctx := context.Background()

	if _, err := s.HandleDesiredRunnerCount(ctx, 1); err != nil {
		t.Fatal(err)
	}

	if err := s.HandleJobCompleted(ctx, &ghscaleset.JobCompleted{
		RunnerName: s.sortedRunners()[0].Name,
		Result:     "canceled",
	}); err != nil {
		t.Fatal(err)
	}

	if len(f.deleted) != 1 {
		t.Error("a cancelled job left its runner alive")
	}
	if len(rec.jobResults) != 1 || rec.jobResults[0] != "canceled" {
		t.Errorf("recorded results %v, want [canceled]", rec.jobResults)
	}
}

// TestAJobCancelledBeforeARunnerTookItRemovesNothing checks a job cancelled
// before any runner took it: there is nothing to remove, and nothing to count
// as removed.
func TestAJobCancelledBeforeARunnerTookItRemovesNothing(t *testing.T) {
	s, f, rec := newScaling(t, 10, 0, 10)

	if err := s.HandleJobCompleted(context.Background(), &ghscaleset.JobCompleted{Result: "canceled"}); err != nil {
		t.Fatal(err)
	}
	if len(f.deleted) != 0 || len(rec.removed) != 0 {
		t.Errorf("deleted %v, counted %v; a job with no runner has none to remove", f.deleted, rec.removed)
	}
	if len(rec.jobResults) != 1 {
		t.Errorf("job results %v, want the cancellation counted", rec.jobResults)
	}
}

func TestMarkBusyMarksAKnownRunnerBusy(t *testing.T) {
	s := newTestScaleSet(newFakeScaleSetProviders())
	s.runners["r1"] = &types.Runner{Name: "r1", Provider: "a", State: types.RunnerStarting}

	if _, ok := s.markBusy("r1", types.Job{ID: "job-1"}); !ok {
		t.Fatal("markBusy() = false, want the runner")
	}
	if runner := s.sortedRunners()[0]; runner.State != types.RunnerBusy || runner.Job.ID != "job-1" {
		t.Errorf("runner = %+v, want busy with job-1", runner)
	}
}

// TestMarkBusyIgnoresAnUnknownRunner checks a runner GitHub mentions that
// Rungar has never heard of is ignored rather than invented.
func TestMarkBusyIgnoresAnUnknownRunner(t *testing.T) {
	s := newTestScaleSet(newFakeScaleSetProviders())

	if _, ok := s.markBusy("nobody", types.Job{ID: "job-2"}); ok {
		t.Error("markBusy() = true for a runner the scale set does not have")
	}
	if got := s.runnerCount(); got != 0 {
		t.Errorf("runnerCount() = %d, want 0: an unknown runner should not be added", got)
	}
}

// TestJobCompletedAfterTheSessionIsLetGoLeavesTheRunner checks a job's
// completion handed on once the scale set has let its session go removes
// nothing: the runner is left to whichever daemon holds the session next.
func TestJobCompletedAfterTheSessionIsLetGoLeavesTheRunner(t *testing.T) {
	s, f, _ := newScaling(t, 10, 0, 10)
	ctx := context.Background()

	if _, err := s.HandleDesiredRunnerCount(ctx, 1); err != nil {
		t.Fatal(err)
	}
	name := s.sortedRunners()[0].Name
	s.serving.Store(false)

	if err := s.HandleJobCompleted(ctx, &ghscaleset.JobCompleted{RunnerName: name, Result: "succeeded"}); err != nil {
		t.Fatal(err)
	}

	if len(f.deleted) != 0 {
		t.Errorf("deleted %v without the session, want nothing", f.deleted)
	}
}
