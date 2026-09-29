// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"slices"
	"testing"
	"time"

	ghscaleset "github.com/actions/scaleset"

	"github.com/konradasb/rungar/internal/types"
)

// happened is an event as a test checks it: what happened, to which runner,
// and why.
type happened struct {
	action types.EventAction
	name   string
	reason string
}

// happenings returns what events say happened, in order.
func happenings(events []types.Event) []happened {
	out := make([]happened, 0, len(events))
	for _, e := range events {
		out = append(out, happened{action: e.Action, name: e.Name, reason: e.Attributes["reason"]})
	}

	return out
}

// TestARunnerMadeAndRemovedAreEvents checks a runner's life is recorded: made,
// on which provider, and removed, with why.
func TestARunnerMadeAndRemovedAreEvents(t *testing.T) {
	s, _, _ := newScaling(t, 10, 0, 10)
	ctx := context.Background()

	if _, err := s.HandleDesiredRunnerCount(ctx, 1); err != nil {
		t.Fatal(err)
	}
	name := s.list()[0].Name

	if err := s.HandleJobCompleted(ctx, &ghscaleset.JobCompleted{RunnerName: name, Result: "succeeded"}); err != nil {
		t.Fatal(err)
	}

	events := recordedBy(t, s.events)
	want := []happened{
		{action: types.ActionCreated, name: name},
		{action: types.ActionRemoved, name: name, reason: string(types.RemovalJobCompleted)},
	}
	if got := happenings(events); !slices.Equal(got, want) {
		t.Fatalf("events = %+v, want %+v", got, want)
	}

	for _, e := range events {
		if e.Kind != types.KindRunner || e.ScaleSet != testScaleSet || e.Provider != "a" {
			t.Errorf("event = %+v, want one of runner %s of %s, on a", e, name, testScaleSet)
		}
	}
	if want := "Runner created on a: 2 vCPU, 4 GiB"; events[0].Message != want {
		t.Errorf("message = %q, want %q", events[0].Message, want)
	}
	if want := "Runner removed from a: its job completed"; events[1].Message != want {
		t.Errorf("message = %q, want %q", events[1].Message, want)
	}
}

// TestAdoptionIsAnEvent checks a runner adopted at start is recorded, and so
// is one left behind finished, which is removed.
func TestAdoptionIsAnEvent(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 16, memoryGiB: 64})
	fleet.put("a", "alive", types.MachineRunning, time.Now())
	fleet.put("a", "stopped", types.MachineStopped, time.Now())

	s := newTestScaleSet(fleet)
	if err := s.adopt(context.Background()); err != nil {
		t.Fatal(err)
	}

	got := happenings(recordedBy(t, s.events))
	for _, want := range []happened{
		{action: types.ActionAdopted, name: "alive"},
		{action: types.ActionRemoved, name: "stopped", reason: string(types.RemovalStopped)},
	} {
		if !slices.Contains(got, want) {
			t.Errorf("events = %+v, want %+v among them", got, want)
		}
	}
	if len(got) != 2 {
		t.Errorf("events = %+v, want one per machine", got)
	}
}

// TestReconcileRecordsStrangersAndLosses checks reconciliation records a
// runner it adopts, one whose VM has gone, and one whose provider has not
// answered for too long.
func TestReconcileRecordsStrangersAndLosses(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 16, memoryGiB: 64}, fakeProvider{name: "b", vcpus: 16, memoryGiB: 64})
	fleet.put("a", "vanishing", types.MachineRunning, time.Now())
	fleet.put("b", "unanswered", types.MachineRunning, time.Now())

	s := newTestScaleSet(fleet)
	if err := s.adopt(context.Background()); err != nil {
		t.Fatal(err)
	}
	adopted := len(recordedBy(t, s.events))

	fleet.mu.Lock()
	fleet.machines["a"] = nil
	fleet.mu.Unlock()
	fleet.put("a", "stray", types.MachineRunning, time.Now())
	fleet.unreachable = map[string]bool{"b": true}
	s.unreachable["b"] = time.Now().Add(-2 * unreachableGrace)

	if err := s.reconcileFleet(context.Background()); err != nil {
		t.Fatal(err)
	}

	got := happenings(recordedBy(t, s.events)[adopted:])
	for _, want := range []happened{
		{action: types.ActionAdopted, name: "stray"},
		{action: types.ActionLost, name: "vanishing", reason: string(types.LossGone)},
		{action: types.ActionLost, name: "unanswered", reason: string(types.LossUnreachable)},
	} {
		if !slices.Contains(got, want) {
			t.Errorf("events = %+v, want %+v among them", got, want)
		}
	}
	if len(got) != 3 {
		t.Errorf("events = %+v, want three", got)
	}
}

// TestRemovingAnUnconfiguredRunnerIsAnEvent checks a runner removed on
// request, of a scale set no longer configured, is recorded under its scale
// set.
func TestRemovingAnUnconfiguredRunnerIsAnEvent(t *testing.T) {
	f := newFixture(t, "gone-vm/gone-vm-1")

	if _, err := f.m.RemoveRunner(context.Background(), "gone-vm-1"); err != nil {
		t.Fatal(err)
	}

	events := recordedBy(t, f.m.events)
	want := []happened{{action: types.ActionRemoved, name: "gone-vm-1", reason: string(types.RemovalRequested)}}
	if got := happenings(events); !slices.Equal(got, want) {
		t.Fatalf("events = %+v, want %+v", got, want)
	}
	if e := events[0]; e.ScaleSet != "gone-vm" || e.Provider != "compute1" {
		t.Errorf("event = %+v, want it of gone-vm, on compute1", e)
	}
}
