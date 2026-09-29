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
	"github.com/actions/scaleset/listener"
	"github.com/google/uuid"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/types"
)

func TestPausedScaleSetMakesNoRunners(t *testing.T) {
	s, f, rec := newScaling(t, 10, 2, 10)
	s.setPaused(true)

	got, err := s.HandleDesiredRunnerCount(context.Background(), 3)
	if err != nil {
		t.Fatal(err)
	}

	if got != 0 || len(f.created) != 0 {
		t.Errorf("returned %d having made %d runners, want none: it is paused, min_runners too", got, len(f.created))
	}
	if !slices.Equal(rec.desired, []int{0}) {
		t.Errorf("desired recorded as %v, want [0]", rec.desired)
	}
}

// TestPauseRemovesIdleAndSparesBusy checks pausing takes away what is not
// running a job at once, and never a runner that is.
func TestPauseRemovesIdleAndSparesBusy(t *testing.T) {
	s, f, _ := newScaling(t, 10, 0, 10)
	ctx := context.Background()

	if _, err := s.HandleDesiredRunnerCount(ctx, 3); err != nil {
		t.Fatal(err)
	}
	busy := s.list()[0].Name
	s.jobStarted(busy, "job")

	s.setPaused(true)

	select {
	case <-s.wake:
	default:
		t.Fatal("pausing did not wake the scale set to act at once")
	}
	s.retry(ctx)

	left := s.list()
	if len(left) != 1 || left[0].Name != busy {
		t.Errorf("left %v, want only the busy runner %s", left, busy)
	}
	if len(f.deleted) != 2 || slices.ContainsFunc(f.deleted, func(d deletedMachine) bool { return d.name == busy }) {
		t.Errorf("deleted %v, want the 2 idle runners", f.deleted)
	}
}

func TestResumeMakesTheMinimum(t *testing.T) {
	s, f, _ := newScaling(t, 10, 2, 10)
	ctx := context.Background()
	s.setPaused(true)

	if _, err := s.HandleDesiredRunnerCount(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if len(f.created) != 0 {
		t.Fatalf("made %d runners while paused, want none", len(f.created))
	}

	s.setPaused(false)
	s.retry(ctx)

	if len(f.created) != 2 {
		t.Errorf("made %d runners once resumed, want the 2 of min_runners", len(f.created))
	}
}

func TestPauseRecordsOnlyChanges(t *testing.T) {
	s, _, rec := newScaling(t, 10, 0, 10)

	s.setPaused(true)
	s.setPaused(true)
	s.setPaused(false)

	if want := []bool{true, false}; !slices.Equal(rec.paused, want) {
		t.Errorf("paused recorded as %v, want %v", rec.paused, want)
	}

	var actions []types.EventAction
	for _, e := range recordedBy(t, s.events) {
		if e.Kind != types.KindScaleSet || e.Name != s.spec.Name || e.ScaleSet != s.spec.Name {
			t.Errorf("event %+v is not of scale set %s", e, s.spec.Name)
		}
		actions = append(actions, e.Action)
	}
	if want := []types.EventAction{types.ActionPaused, types.ActionResumed}; !slices.Equal(actions, want) {
		t.Errorf("events %v, want %v", actions, want)
	}
}

func TestConfiguredPausedStartsPaused(t *testing.T) {
	f := newFleet(fakeProvider{name: "a", vcpus: 20, memoryGiB: 40})
	spec := testScaleSetSpec(f)
	spec.Paused = true

	s := newScaleSetOver(f, &fakeGitHub{}, spec, &recorder{}, discardLogger())

	if !s.status().Status.Paused {
		t.Error("a scale set the configuration has paused is not paused")
	}
}

func TestSetScaleSetPaused(t *testing.T) {
	f := newFixture(t)

	set, err := f.m.SetScaleSetPaused("rungar-vm", true)
	if err != nil {
		t.Fatal(err)
	}
	if !set.Status.Paused || set.Spec.Paused {
		t.Errorf("paused = %v, configured paused = %v; want paused, not by the configuration",
			set.Status.Paused, set.Spec.Paused)
	}

	if _, err := f.m.SetScaleSetPaused("gone-vm", true); !errors.Is(err, errdefs.ErrNotFound) {
		t.Errorf("SetScaleSetPaused() of a scale set not configured = %v, want not found", err)
	}
}

// TestPausedCapacityIsReportedToGitHub checks the listener tells GitHub a
// paused scale set has no room, so that no job is assigned to it, and its
// max_runners again once resumed.
func TestPausedCapacityIsReportedToGitHub(t *testing.T) {
	s, _, _ := newScaling(t, 10, 0, 4)
	session := &capacitySession{polls: make(chan int)}

	l, err := listener.New(session, listener.Config{ScaleSetID: 1, MaxRunners: s.spec.MaxRunners})
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()

	s.setListener(l)
	s.setPaused(true)

	go func() { _ = l.Run(ctx, s) }()

	if got := session.next(t); got != 0 {
		t.Errorf("capacity while paused = %d, want 0", got)
	}

	s.setPaused(false)
	session.next(t) // the poll already under way when resumed

	if got := session.next(t); got != 4 {
		t.Errorf("capacity once resumed = %d, want max_runners, 4", got)
	}
}

// capacitySession is a message session that sends the capacity of each poll
// on polls, and has no message.
type capacitySession struct {
	polls chan int
}

func (c *capacitySession) GetMessage(ctx context.Context, _, capacity int) (*ghscaleset.RunnerScaleSetMessage, error) {
	select {
	case c.polls <- capacity:
		return nil, nil //nolint:nilnil // no message, as a long poll that times out answers

	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *capacitySession) DeleteMessage(context.Context, int) error { return nil }

func (c *capacitySession) AcquireJobs(context.Context, []int64) ([]int64, error) { return nil, nil }

func (c *capacitySession) Session() ghscaleset.RunnerScaleSetSession {
	return ghscaleset.RunnerScaleSetSession{
		SessionID:  uuid.New(),
		Statistics: &ghscaleset.RunnerScaleSetStatistic{},
	}
}

// next returns the capacity of the next poll.
func (c *capacitySession) next(t *testing.T) int {
	t.Helper()

	select {
	case capacity := <-c.polls:
		return capacity
	case <-time.After(5 * time.Second):
		t.Fatal("the listener did not poll")
		return 0
	}
}
