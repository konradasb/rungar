// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/konradasb/rungar/internal/types"
)

func TestScaleUpToAssignedJobs(t *testing.T) {
	s, f, rec := newScaling(t, 10, 0, 10)

	got, err := s.HandleDesiredRunnerCount(context.Background(), 3)
	if err != nil {
		t.Fatalf("HandleDesiredRunnerCount() = %v", err)
	}

	if got != 3 || len(f.created) != 3 {
		t.Errorf("returned %d having created %d runners, want 3", got, len(f.created))
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
		t.Errorf("created %d runners, want 4 (2 jobs + 2 reserve)", len(f.created))
	}
}

func TestScaleUpStopsAtTheCeiling(t *testing.T) {
	s, f, rec := newScaling(t, 10, 0, 3)

	got, err := s.HandleDesiredRunnerCount(context.Background(), 50)
	if err != nil {
		t.Fatal(err)
	}

	if got != 3 || len(f.created) != 3 {
		t.Errorf("created %d runners (returned %d), want 3: max_runners is the ceiling", len(f.created), got)
	}
	if len(rec.desired) != 1 || rec.desired[0] != 3 {
		t.Errorf("desired recorded as %v, want [3] after the ceiling is applied", rec.desired)
	}
}

// TestScaleDownRemovesWhatIsNoLongerWanted checks runners created for jobs that
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
		t.Errorf("created %d runners in total, want 3: the second call needed one more", len(f.created))
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
	if len(rec.scaleUpFailures) != 1 || rec.scaleUpFailures[0] != types.CallNoCapacity {
		t.Errorf("recorded failures %v, want [no_capacity]", rec.scaleUpFailures)
	}
}

// TestScaleUpErrorDoesNotStopListener checks that a runner that cannot be
// created does not stop the listener: an error returned here would restart the
// whole daemon over one refusal, and the next message asks again anyway.
func TestScaleUpErrorDoesNotStopListener(t *testing.T) {
	s, _, rec := newScaling(t, 10, 0, 10, func(g *fakeGitHub) {
		g.jit = func(context.Context, string) (string, error) {
			return "", errors.New("github refused the registration")
		}
	})

	if _, err := s.HandleDesiredRunnerCount(context.Background(), 2); err != nil {
		t.Fatalf("HandleDesiredRunnerCount() = %v, want nil: the listener would stop", err)
	}

	if len(rec.scaleUpFailures) != 1 || rec.scaleUpFailures[0] != types.CallError {
		t.Errorf("recorded failures %v, want [error]", rec.scaleUpFailures)
	}
}

func TestNoJobsAndNoMinimumCreateNoRunners(t *testing.T) {
	s, f, rec := newScaling(t, 10, 0, 10)

	got, err := s.HandleDesiredRunnerCount(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}

	if got != 0 || len(f.created) != 0 {
		t.Errorf("created %d runners, want none for an idle scale set", len(f.created))
	}
	if len(rec.created) != 0 {
		t.Error("a creation was recorded although nothing was created")
	}
}

// TestRetryCreatesTheRunnersThatHadNoRoom checks the timer's side of
// scaling: a runner that could not be created for want of room is created
// once there is room, without waiting for GitHub's next message.
func TestRetryCreatesTheRunnersThatHadNoRoom(t *testing.T) {
	s, f, _ := newScaling(t, 0, 0, 10)
	ctx := context.Background()

	// Nothing known yet: nothing to retry.
	s.retry(ctx)
	if len(f.created) != 0 {
		t.Fatal("retry created runners before GitHub gave a count")
	}

	if _, err := s.HandleDesiredRunnerCount(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if s.runnerCount() != 0 {
		t.Fatal("runners were created on a full fleet")
	}

	// Room frees up; the timer, not a message, notices.
	f.setRoom("a", 16, 64)

	s.retry(ctx)

	if got := s.runnerCount(); got != 2 {
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

	if got := s.runnerCount(); got != 8 {
		t.Errorf("%d runners, want the 8 asked for: two decisions created runners for the same shortfall", got)
	}
}

// TestDesiredCountUnknownUntilFirstDecision checks the target of the last
// decision is there to be read, and is not before GitHub has given a count.
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

// TestPollsAreRecordedButRetriesAreNot checks the time of GitHub's last answer
// moves only when GitHub answers, not when the scale set retries its last
// decision.
func TestPollsAreRecordedButRetriesAreNot(t *testing.T) {
	s, _, rec := newScaling(t, 10, 0, 10)
	ctx := context.Background()

	if _, err := s.HandleDesiredRunnerCount(ctx, 2); err != nil {
		t.Fatal(err)
	}
	s.retry(ctx)

	if rec.polls != 1 {
		t.Errorf("recorded %d polls, want 1", rec.polls)
	}
	if want := []int{2, 2}; !slices.Equal(rec.assigned, want) {
		t.Errorf("assigned jobs = %v, want %v", rec.assigned, want)
	}
}

// TestRetryDoesNotUndoANewerDecision checks that a retry waiting on a scaling
// decision under way repeats that decision, not the one before it: the job
// count is read once the retry has its turn.
func TestRetryDoesNotUndoANewerDecision(t *testing.T) {
	s, _, _ := newScaling(t, 10, 0, 10)
	ctx := context.Background()

	if _, err := s.HandleDesiredRunnerCount(ctx, 2); err != nil {
		t.Fatal(err)
	}

	// A decision for 5 is under way while the retry comes.
	waiting := make(chan struct{})
	s.retryWaiting = func() { close(waiting) }
	s.decisionLock.Lock()
	retried := make(chan struct{})
	go func() {
		s.retry(ctx)
		close(retried)
	}()

	// The retry has read the count by now, were it read before its turn.
	<-waiting
	s.scaleToLocked(ctx, 5)
	s.decisionLock.Unlock()
	<-retried

	if got := s.runnerCount(); got != 5 {
		t.Errorf("%d runners, want 5: the retry undid the newer decision with the older count", got)
	}
}

// TestScalingStopsOnceNotServing checks a scale set that has let its session
// go acts on the fleet no more, though the listener hands GitHub's messages
// on with a context never cancelled: GitHub's count creates and removes
// nothing, and a decision under way stops at the runner it is on.
func TestScalingStopsOnceNotServing(t *testing.T) {
	// The listener's, once the daemon is stopping.
	stopped, cancel := context.WithCancel(context.Background())
	cancel()
	ctx := context.WithoutCancel(stopped)

	t.Run("creates nothing", func(t *testing.T) {
		s, f, _ := newScaling(t, 10, 0, 10)
		s.serving.Store(false)

		if _, err := s.HandleDesiredRunnerCount(ctx, 5); err != nil {
			t.Fatal(err)
		}

		if len(f.created) != 0 {
			t.Errorf("created %d runners without the session, want none", len(f.created))
		}
	})

	t.Run("removes nothing", func(t *testing.T) {
		s, f, _ := newScaling(t, 10, 0, 10)
		if _, err := s.HandleDesiredRunnerCount(ctx, 3); err != nil {
			t.Fatal(err)
		}
		s.serving.Store(false)

		if _, err := s.HandleDesiredRunnerCount(ctx, 0); err != nil {
			t.Fatal(err)
		}

		if len(f.deleted) != 0 {
			t.Errorf("deleted %v without the session, want nothing", f.deleted)
		}
	})

	t.Run("stops creating mid-way", func(t *testing.T) {
		s, f, _ := newScaling(t, 10, 0, 10)
		created := newGate()
		f.createGate = created

		done := make(chan struct{})
		go func() {
			defer close(done)
			_, _ = s.HandleDesiredRunnerCount(ctx, 5)
		}()

		<-created.reached
		s.serving.Store(false)
		close(created.released)
		<-done

		if got := len(f.created); got != 1 {
			t.Errorf("created %d runners, want only the 1 under way as the session was let go", got)
		}
	})

	t.Run("stops removing mid-way", func(t *testing.T) {
		s, f, _ := newScaling(t, 10, 0, 10)
		if _, err := s.HandleDesiredRunnerCount(ctx, 5); err != nil {
			t.Fatal(err)
		}
		deleting := newGate()
		f.mu.Lock()
		f.deleteGate = deleting
		f.mu.Unlock()

		done := make(chan struct{})
		go func() {
			defer close(done)
			_, _ = s.HandleDesiredRunnerCount(ctx, 0)
		}()

		<-deleting.reached
		s.serving.Store(false)
		close(deleting.released)
		<-done

		f.mu.Lock()
		got := len(f.deleted)
		f.mu.Unlock()
		if got != 1 {
			t.Errorf("deleted %d machines, want only the 1 under way as the session was let go", got)
		}
	})
}
