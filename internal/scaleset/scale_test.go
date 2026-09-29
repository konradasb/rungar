// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	ghscaleset "github.com/actions/scaleset"

	"github.com/konradasb/rungar/internal/types"
)

// newScaling returns a scale set of the given bounds on a fleet with room for
// fits runners, and the metrics it reports to.
func newScaling(t *testing.T, fits, minRunners, maxRunners int, opts ...func(*fakeGitHub)) (*scaleSet, *fakeFleet, *recorder) {
	t.Helper()

	f := newFleet(fakeProvider{name: "a", vcpus: 2 * fits, memoryGiB: int64(4 * fits)})
	gh := &fakeGitHub{}
	for _, opt := range opts {
		opt(gh)
	}

	spec := testScaleSetSpec(f)
	spec.MinRunners, spec.MaxRunners = minRunners, maxRunners

	rec := &recorder{}
	s := newScaleSetOver(f, gh, spec, rec, discardLogger())

	return s, f, rec
}

// markAllBusy has every runner start a job.
func markAllBusy(s *scaleSet) {
	for _, r := range s.list() {
		s.jobStarted(r.Name, "job")
	}
}

func TestScaleUpToAssignedJobs(t *testing.T) {
	s, f, rec := newScaling(t, 10, 0, 10)

	got, err := s.HandleDesiredRunnerCount(context.Background(), 3)
	if err != nil {
		t.Fatalf("HandleDesiredRunnerCount() = %v", err)
	}

	if got != 3 || len(f.created) != 3 {
		t.Errorf("returned %d having made %d runners, want 3", got, len(f.created))
	}
	if len(rec.desired) != 1 || rec.desired[0] != 3 {
		t.Errorf("desired recorded as %v, want [3]", rec.desired)
	}
	if len(rec.created) != 3 || rec.created[0] != "a" {
		t.Errorf("recorded creations %v, want 3 on a", rec.created)
	}
}

func TestScaleUpAddsTheMinimum(t *testing.T) {
	s, f, _ := newScaling(t, 10, 2, 10)

	// Two jobs assigned, plus two kept in reserve.
	if _, err := s.HandleDesiredRunnerCount(context.Background(), 2); err != nil {
		t.Fatal(err)
	}

	if len(f.created) != 4 {
		t.Errorf("made %d runners, want 4 (2 jobs + 2 reserve)", len(f.created))
	}
}

func TestScaleUpStopsAtTheCeiling(t *testing.T) {
	s, f, rec := newScaling(t, 10, 0, 3)

	got, err := s.HandleDesiredRunnerCount(context.Background(), 50)
	if err != nil {
		t.Fatal(err)
	}

	if got != 3 || len(f.created) != 3 {
		t.Errorf("made %d runners (returned %d), want 3: max_runners is the ceiling", len(f.created), got)
	}
	if len(rec.desired) != 1 || rec.desired[0] != 3 {
		t.Errorf("desired recorded as %v, want [3] after the ceiling is applied", rec.desired)
	}
}

// TestScaleDownRemovesWhatIsNoLongerWanted checks runners made for jobs that
// went away, cancelled or taken by other runners, are removed.
func TestScaleDownRemovesWhatIsNoLongerWanted(t *testing.T) {
	s, f, rec := newScaling(t, 10, 0, 10)
	ctx := context.Background()

	if _, err := s.HandleDesiredRunnerCount(ctx, 5); err != nil {
		t.Fatal(err)
	}

	got, err := s.HandleDesiredRunnerCount(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}

	if got != 1 || len(f.deleted) != 4 {
		t.Errorf("returned %d having deleted %v, want 1 left", got, f.deleted)
	}
	down := types.RemovalScaledDown
	if want := []types.RemovalReason{down, down, down, down}; !slices.Equal(rec.removed, want) {
		t.Errorf("recorded removals %v, want %v", rec.removed, want)
	}
}

// TestScaleDownSparesBusyRunners is the invariant that keeps jobs safe: a
// runner running a job is never taken away, whatever the count says.
func TestScaleDownSparesBusyRunners(t *testing.T) {
	s, f, _ := newScaling(t, 10, 0, 10)
	ctx := context.Background()

	if _, err := s.HandleDesiredRunnerCount(ctx, 3); err != nil {
		t.Fatal(err)
	}
	markAllBusy(s)

	if got, _ := s.HandleDesiredRunnerCount(ctx, 0); got != 3 || len(f.deleted) != 0 {
		t.Errorf("returned %d having deleted %v; busy runners are only removed when their job ends",
			got, f.deleted)
	}
}

// TestScaleDownKeepsTheMinimum checks that the idle runners a scale set keeps
// in reserve are not given back.
func TestScaleDownKeepsTheMinimum(t *testing.T) {
	s, _, _ := newScaling(t, 10, 2, 10)
	ctx := context.Background()

	if _, err := s.HandleDesiredRunnerCount(ctx, 3); err != nil {
		t.Fatal(err)
	}

	if got, _ := s.HandleDesiredRunnerCount(ctx, 0); got != 2 {
		t.Errorf("returned %d, want the 2 kept in reserve", got)
	}
}

func TestScaleUpIsRelativeToWhatExists(t *testing.T) {
	s, f, _ := newScaling(t, 10, 0, 10)
	ctx := context.Background()

	if _, err := s.HandleDesiredRunnerCount(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HandleDesiredRunnerCount(ctx, 3); err != nil {
		t.Fatal(err)
	}

	if len(f.created) != 3 {
		t.Errorf("made %d runners in total, want 3: the second call needed one more", len(f.created))
	}
}

// TestAFullFleetIsNotAFailure checks the decision that keeps Rungar up: there
// being no room is a wait, not an error, because stopping the listener would
// change nothing except that Rungar would be down.
func TestAFullFleetIsNotAFailure(t *testing.T) {
	s, _, rec := newScaling(t, 1, 0, 10)

	got, err := s.HandleDesiredRunnerCount(context.Background(), 5)
	if err != nil {
		t.Fatalf("HandleDesiredRunnerCount() = %v, want nil for a full fleet", err)
	}

	if got != 1 {
		t.Errorf("returned %d, want the 1 runner there was room for", got)
	}
	if len(rec.scaleUpFailures) != 1 || rec.scaleUpFailures[0] != "no_capacity" {
		t.Errorf("recorded failures %v, want [no_capacity]", rec.scaleUpFailures)
	}
}

// TestScaleUpErrorDoesNotStopListener checks that a runner that cannot be made
// does not stop the listener: an error returned here would restart the whole
// daemon over one refusal, and the next message asks again anyway.
func TestScaleUpErrorDoesNotStopListener(t *testing.T) {
	s, _, rec := newScaling(t, 10, 0, 10, func(g *fakeGitHub) {
		g.jit = func(context.Context, string) (string, error) {
			return "", errors.New("github refused the registration")
		}
	})

	if _, err := s.HandleDesiredRunnerCount(context.Background(), 2); err != nil {
		t.Fatalf("HandleDesiredRunnerCount() = %v, want nil: the listener would stop", err)
	}

	if len(rec.scaleUpFailures) != 1 || rec.scaleUpFailures[0] != "error" {
		t.Errorf("recorded failures %v, want [error]", rec.scaleUpFailures)
	}
}

func TestJobStartedMarksTheRunnerBusy(t *testing.T) {
	s, _, rec := newScaling(t, 10, 0, 10)
	ctx := context.Background()

	if _, err := s.HandleDesiredRunnerCount(ctx, 1); err != nil {
		t.Fatal(err)
	}
	name := s.list()[0].Name

	err := s.HandleJobStarted(ctx, &ghscaleset.JobStarted{
		RunnerName: name,
		JobMessageBase: ghscaleset.JobMessageBase{
			JobID:          "job-1",
			JobDisplayName: "build",
			RepositoryName: "tests",
		},
	})
	if err != nil {
		t.Fatalf("HandleJobStarted() = %v", err)
	}

	if r := s.list()[0]; r.State != "busy" || r.JobID != "job-1" {
		t.Errorf("runner = %+v, want busy with job-1", r)
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
	name := s.list()[0].Name

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
// remove one VM does not stop the listener: the VM removes itself, and
// reconciliation removes it if it does not.
func TestJobCompletedSurvivesAFailedRemoval(t *testing.T) {
	s, f, _ := newScaling(t, 10, 0, 10)
	ctx := context.Background()

	if _, err := s.HandleDesiredRunnerCount(ctx, 1); err != nil {
		t.Fatal(err)
	}
	f.deleteErr = errors.New("host unreachable")

	err := s.HandleJobCompleted(ctx, &ghscaleset.JobCompleted{RunnerName: s.list()[0].Name, Result: "failed"})
	if err != nil {
		t.Fatalf("HandleJobCompleted() = %v, want nil: the listener would stop", err)
	}
}

// TestCancelledJobsAreStillCompletions checks that a cancelled job frees its
// runner: GitHub reports the reassignment as a completion with a result of
// "canceled", and the VM must not be left behind.
func TestCancelledJobsAreStillCompletions(t *testing.T) {
	s, f, rec := newScaling(t, 10, 0, 10)
	ctx := context.Background()

	if _, err := s.HandleDesiredRunnerCount(ctx, 1); err != nil {
		t.Fatal(err)
	}

	if err := s.HandleJobCompleted(ctx, &ghscaleset.JobCompleted{
		RunnerName: s.list()[0].Name,
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

func TestZeroDemandCreatesNothing(t *testing.T) {
	s, f, rec := newScaling(t, 10, 0, 10)

	got, err := s.HandleDesiredRunnerCount(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}

	if got != 0 || len(f.created) != 0 {
		t.Errorf("made %d runners, want none for an idle scale set", len(f.created))
	}
	if len(rec.created) != 0 {
		t.Error("a creation was recorded although nothing was created")
	}
}

// TestJobCompletedWithNoRunner checks a job cancelled before any runner took
// it: there is nothing to remove, and nothing to count as removed.
func TestJobCompletedWithNoRunner(t *testing.T) {
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

// TestRetryMakesUpWhatWasShort checks the timer's side of scaling: a runner
// that could not be made for want of room is made once there is room, without
// waiting for GitHub's next message.
func TestRetryMakesUpWhatWasShort(t *testing.T) {
	s, f, _ := newScaling(t, 0, 0, 10)
	ctx := context.Background()

	// Nothing known yet: nothing to retry.
	s.retry(ctx)
	if len(f.created) != 0 {
		t.Fatal("retry made runners before GitHub gave a count")
	}

	if _, err := s.HandleDesiredRunnerCount(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if s.count() != 0 {
		t.Fatal("runners were made on a full fleet")
	}

	// Room frees up; the timer, not a message, notices.
	f.setRoom("a", 16, 64)

	s.retry(ctx)

	if got := s.count(); got != 2 {
		t.Errorf("%d runners after the retry, want the 2 GitHub asked for", got)
	}
}

// TestScalingDecisionsDoNotOverlap checks that GitHub's messages and the
// timer, deciding at once, do not both make up the same shortfall.
func TestScalingDecisionsDoNotOverlap(t *testing.T) {
	s, _, _ := newScaling(t, 50, 0, 50)
	ctx := context.Background()

	if _, err := s.HandleDesiredRunnerCount(ctx, 5); err != nil {
		t.Fatal(err)
	}
	markAllBusy(s)

	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() { _, _ = s.HandleDesiredRunnerCount(ctx, 8) })
		wg.Go(func() { s.retry(ctx) })
	}
	wg.Wait()

	if got := s.count(); got != 8 {
		t.Errorf("%d runners, want the 8 asked for: two decisions made up the same shortfall", got)
	}
}

// TestDesiredCountUnknownUntilFirstDecision checks the target the last decision was made for is there to
// be read, and is not before GitHub has given a count.
func TestDesiredCountUnknownUntilFirstDecision(t *testing.T) {
	s, _, _ := newScaling(t, 10, 1, 3)

	if _, known := s.desiredCount(); known {
		t.Error("desiredCount() is known before GitHub has given a count")
	}

	if _, err := s.HandleDesiredRunnerCount(context.Background(), 5); err != nil {
		t.Fatal(err)
	}

	if n, known := s.desiredCount(); !known || n != 3 {
		t.Errorf("desiredCount() = %d, %v; want 3, bounded by max_runners", n, known)
	}
}

func TestJobStartedMarksBusyAndIgnoresUnknown(t *testing.T) {
	s := &scaleSet{
		spec:   types.ScaleSetSpec{Name: "rungar-vm"},
		logger: discardLogger(),
		runners: map[string]*types.Runner{
			"r1": {Name: "r1", Provider: "a", State: types.RunnerStarting},
		},
	}

	if got := s.count(); got != 1 {
		t.Fatalf("count() = %d, want 1", got)
	}

	s.jobStarted("r1", "job-1")
	runner := s.list()[0]
	if runner.State != types.RunnerBusy || runner.JobID != "job-1" {
		t.Errorf("runner = %+v, want busy with job-1", runner)
	}

	// A runner GitHub mentions that Rungar has never heard of is ignored
	// rather than invented.
	s.jobStarted("nobody", "job-2")
	if got := s.count(); got != 1 {
		t.Errorf("count() = %d, want 1: an unknown runner should not be added", got)
	}

	s.forget("r1")
	if got := s.count(); got != 0 {
		t.Errorf("count() = %d, want 0", got)
	}
}
