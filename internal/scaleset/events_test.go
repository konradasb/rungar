// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"slices"
	"testing"
	"time"

	ghscaleset "github.com/actions/scaleset"

	"github.com/konradasb/rungar/internal/events"
	"github.com/konradasb/rungar/internal/types"
)

// TestARunnerCreatedAndRemovedAreEvents checks a runner's life is recorded:
// created, on which provider, and removed, with why.
func TestARunnerCreatedAndRemovedAreEvents(t *testing.T) {
	s, _, _ := newScaling(t, 10, 0, 10)
	ctx := context.Background()

	if _, err := s.HandleDesiredRunnerCount(ctx, 1); err != nil {
		t.Fatal(err)
	}
	name := s.sortedRunners()[0].Name

	if err := s.HandleJobCompleted(ctx, &ghscaleset.JobCompleted{RunnerName: name, Result: "succeeded"}); err != nil {
		t.Fatal(err)
	}

	recorded := recordedBy(t, s.events)
	want := []happened{
		{action: events.ActionCreated, name: name},
		{action: events.ActionRemoved, name: name, reason: string(types.RemovalJobCompleted)},
	}
	if got := happenings(recorded); !slices.Equal(got, want) {
		t.Fatalf("events = %+v, want %+v", got, want)
	}

	for _, e := range recorded {
		if e.Kind != events.KindRunner || e.ScaleSet != testScaleSet || e.Provider != "a" {
			t.Errorf("event = %+v, want one of runner %s of %s, on a", e, name, testScaleSet)
		}
	}
	if want := "Runner created on a: 2 vCPU, 4 GiB"; recorded[0].Message != want {
		t.Errorf("message = %q, want %q", recorded[0].Message, want)
	}
	if want := "Runner removed from a: its job completed"; recorded[1].Message != want {
		t.Errorf("message = %q, want %q", recorded[1].Message, want)
	}
}

// TestAdoptionIsAnEvent checks a runner adopted at start is recorded, and so
// is one left behind finished, which is removed as its job completed.
func TestAdoptionIsAnEvent(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 16, memoryGiB: 64})
	fleet.put("a", "alive", types.MachineRunning, time.Now())
	fleet.put("a", "stopped", types.MachineStopped, time.Now())

	// GitHub has dropped the finished runner's registration.
	s := newTestScaleSet(fleet, statusWith(registered(map[string]answer{"alive": {Registered: true, Online: true}})))
	if err := s.adopt(context.Background()); err != nil {
		t.Fatal(err)
	}

	got := happenings(recordedBy(t, s.events))
	for _, want := range []happened{
		{action: events.ActionAdopted, name: "alive"},
		{action: events.ActionRemoved, name: "stopped", reason: string(types.RemovalJobCompleted)},
	} {
		if !slices.Contains(got, want) {
			t.Errorf("events = %+v, want %+v among them", got, want)
		}
	}
	if len(got) != 2 {
		t.Errorf("events = %+v, want one per machine", got)
	}
}

// TestReconcileRecordsAdoptionsAndLosses checks reconciliation records a
// runner it adopts, and one whose provider has been unreachable for too long.
func TestReconcileRecordsAdoptionsAndLosses(t *testing.T) {
	fleet := newFakeScaleSetProviders(
		providerRoom{name: "a", vcpus: 16, memoryGiB: 64},
		providerRoom{name: "b", vcpus: 16, memoryGiB: 64},
	)
	fleet.put("b", "unreachable", types.MachineRunning, time.Now())

	s := newTestScaleSet(fleet)
	if err := s.adopt(context.Background()); err != nil {
		t.Fatal(err)
	}
	adopted := len(recordedBy(t, s.events))

	fleet.put("a", "stray", types.MachineRunning, time.Now())
	fleet.unreachable = map[string]bool{"b": true}
	s.unreachable["b"] = time.Now().Add(-2 * unreachableGrace)

	if err := s.reconcileFleet(context.Background()); err != nil {
		t.Fatal(err)
	}

	got := happenings(recordedBy(t, s.events)[adopted:])
	for _, want := range []happened{
		{action: events.ActionAdopted, name: "stray"},
		{action: events.ActionLost, name: "unreachable", reason: string(types.LossUnreachable)},
	} {
		if !slices.Contains(got, want) {
			t.Errorf("events = %+v, want %+v among them", got, want)
		}
	}
	if len(got) != 2 {
		t.Errorf("events = %+v, want two", got)
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

	recorded := recordedBy(t, f.m.events)
	want := []happened{{action: events.ActionRemoved, name: "gone-vm-1", reason: string(types.RemovalRequested)}}
	if got := happenings(recorded); !slices.Equal(got, want) {
		t.Fatalf("events = %+v, want %+v", got, want)
	}
	if e := recorded[0]; e.ScaleSet != "gone-vm" || e.Provider != "compute1" {
		t.Errorf("event = %+v, want it of gone-vm, on compute1", e)
	}
}
