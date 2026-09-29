// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/konradasb/rungar/internal/types"
)

// TestAdoptTakesItsStateFromGitHub checks an adopted runner is what GitHub
// says it is, rather than idle whatever it is doing.
func TestAdoptTakesItsStateFromGitHub(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 16, memoryGiB: 64})
	old, young := time.Now().Add(-time.Hour), time.Now()
	fleet.put("a", "busy", types.MachineRunning, old)
	fleet.put("a", "idle", types.MachineRunning, old)
	fleet.put("a", "booting", types.MachineRunning, young)
	fleet.put("a", "silent", types.MachineRunning, old)

	github := map[string]answer{
		"busy":    {Registered: true, Online: true, Busy: true},
		"idle":    {Registered: true, Online: true},
		"booting": {Registered: true},
		"silent":  {Registered: true},
	}
	s := newTestScaleSet(fleet, statusWith(func(_ context.Context, name string) (answer, error) {
		return github[name], nil
	}))

	if err := s.adopt(context.Background()); err != nil {
		t.Fatalf("adopt() = %v", err)
	}

	want := map[string]types.RunnerState{
		"busy":    types.RunnerBusy,
		"idle":    types.RunnerIdle,
		"booting": types.RunnerStarting,
		// Offline, and too old to be booting: idle, and timed from now as
		// any idle runner that goes offline is.
		"silent": types.RunnerIdle,
	}
	for _, r := range s.sortedRunners() {
		t.Run(r.Name, func(t *testing.T) {
			if r.State != want[r.Name] || !r.Adopted {
				t.Errorf("state = %s, adopted = %v; want %s, adopted", r.State, r.Adopted, want[r.Name])
			}
		})
	}
}

// TestAdoptWarnsOfAnotherInstallation checks runners of the scale set under
// another installation are not adopted, and are warned of.
func TestAdoptWarnsOfAnotherInstallation(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 16, memoryGiB: 64})
	fleet.put("a", "mine", types.MachineRunning, time.Now())
	fleet.machines["a"] = append(fleet.machines["a"], types.Machine{
		Name:   "theirs",
		Labels: types.RunnerLabels("gh-elsewhere", testScaleSet, "theirs", ""),
		State:  types.MachineRunning,
	})

	var logs bytes.Buffer
	s := newScaleSetOver(fleet, &fakeGitHub{}, testScaleSetSpec(fleet), &fakeMetrics{},
		slog.New(slog.NewTextHandler(&logs, nil)))
	if err := s.adopt(context.Background()); err != nil {
		t.Fatal(err)
	}

	if runners := s.sortedRunners(); len(runners) != 1 || runners[0].Name != "mine" {
		t.Errorf("adopted %+v, want only this installation's runner", runners)
	}
	if !strings.Contains(logs.String(), "another installation") ||
		!strings.Contains(logs.String(), "their_installation=gh-elsewhere") {
		t.Errorf("no warning of the other installation's runner; logged:\n%s", logs.String())
	}
	if fleet.providerOf("theirs") == "" {
		t.Error("another installation's runner was removed")
	}
}

func TestAdoptedRunnerIsIdleWithTheMachinesDetails(t *testing.T) {
	created := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)

	r := adoptedRunner(testScaleSet, types.Machine{
		Name:      "x",
		Provider:  "a",
		State:     types.MachineRunning,
		Size:      "2 vCPU, 4 GiB",
		CreatedAt: created,
	})

	want := types.Runner{
		Name:      "x",
		ScaleSet:  testScaleSet,
		Provider:  "a",
		State:     types.RunnerIdle,
		Size:      "2 vCPU, 4 GiB",
		CreatedAt: created,
		Adopted:   true,
	}
	if *r != want {
		t.Errorf("adoptedRunner() = %+v, want %+v", *r, want)
	}
}

// TestAdoptCarriesOnWithoutAProvider checks that one provider being down does
// not keep a scale set from starting: what can be seen is adopted.
func TestAdoptCarriesOnWithoutAProvider(t *testing.T) {
	fleet := newFakeScaleSetProviders(
		providerRoom{name: "a", vcpus: 8, memoryGiB: 16},
		providerRoom{name: "b", vcpus: 8, memoryGiB: 16},
	)
	fleet.put("a", "rungar-vm-on-a", types.MachineRunning, time.Now())
	fleet.put("b", "rungar-vm-on-b", types.MachineRunning, time.Now())
	fleet.unreachable = map[string]bool{"b": true}

	s := newTestScaleSet(fleet)
	if err := s.adopt(context.Background()); err != nil {
		t.Fatalf("adopt() = %v; a provider being down must not keep the scale set from starting", err)
	}

	if got := s.runnerCount(); got != 1 {
		t.Errorf("adopted %d runners, want the one that can be seen", got)
	}

	// Once the provider is reachable, what is on it is adopted.
	fleet.unreachable = nil
	if err := s.reconcileFleet(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := s.runnerCount(); got != 2 {
		t.Errorf("%d runners after the provider came back, want both", got)
	}
}

func TestAdoptTakesOverRunningMachines(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 16, memoryGiB: 64})
	created := time.Now().Add(-time.Hour)
	fleet.put("a", "rungar-vm-1", types.MachineRunning, created)
	fleet.put("a", "rungar-vm-2", types.MachineStarting, created)

	s := newTestScaleSet(fleet)

	if err := s.adopt(context.Background()); err != nil {
		t.Fatalf("adopt() = %v", err)
	}

	if s.runnerCount() != 2 {
		t.Fatalf("adopted %d runners, want 2", s.runnerCount())
	}

	for _, r := range s.sortedRunners() {
		if r.Provider != "a" {
			t.Errorf("runner %s has provider %q; without one it could never be deleted", r.Name, r.Provider)
		}
		if r.State != types.RunnerIdle {
			t.Errorf("runner %s adopted as %q, want %q", r.Name, r.State, types.RunnerIdle)
		}
		if !r.CreatedAt.Equal(created) {
			t.Errorf("runner %s adopted with CreatedAt %v, want the machine's %v", r.Name, r.CreatedAt, created)
		}
	}
}

// TestAdoptRemovesRunnersWhoseMachinesEnded checks that a runner left behind
// by a previous daemon, whose machine has ended, is removed, its machine
// deleted, rather than counted.
func TestAdoptRemovesRunnersWhoseMachinesEnded(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 16, memoryGiB: 64})
	fleet.put("a", "alive", types.MachineRunning, time.Now())
	fleet.put("a", "stopped", types.MachineStopped, time.Now())
	fleet.put("a", "crashed", types.MachineStopped, time.Now())

	s := newTestScaleSet(fleet)

	if err := s.adopt(context.Background()); err != nil {
		t.Fatalf("adopt() = %v", err)
	}

	if s.runnerCount() != 1 {
		t.Errorf("adopted %d runners, want only the running one", s.runnerCount())
	}
	if fleet.providerOf("stopped") != "" || fleet.providerOf("crashed") != "" {
		t.Error("a finished runner's machine was left on the fleet")
	}
	if fleet.providerOf("alive") == "" {
		t.Error("a running runner's machine was deleted")
	}
}

// TestAdoptReplacesWhatWasKnown checks that adoption is a fresh picture, not
// a merge: the fleet is the record.
func TestAdoptReplacesWhatWasKnown(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 16, memoryGiB: 64})
	fleet.put("a", "on-the-fleet", types.MachineRunning, time.Now())

	s := newTestScaleSet(fleet)
	s.runners["stale"] = &types.Runner{Name: "stale", Provider: "a"}

	if err := s.adopt(context.Background()); err != nil {
		t.Fatal(err)
	}

	if s.runnerCount() != 1 {
		t.Fatalf("runnerCount() = %d, want 1", s.runnerCount())
	}
	if s.sortedRunners()[0].Name != "on-the-fleet" {
		t.Errorf("kept %q, want only what the fleet has", s.sortedRunners()[0].Name)
	}
}

func TestAdoptFailsWhenTheFleetCannotBeRead(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 16, memoryGiB: 64})
	fleet.listErr = errors.New("provider unreachable")

	s := newTestScaleSet(fleet)

	if err := s.adopt(context.Background()); err == nil {
		t.Fatal("adopt() = nil; an incomplete picture would double the fleet's runners")
	}
}

// TestAdoptLeavesAloneARunnerThatHasJustLeft checks that a runner that has
// just left, whose machine a listing older than its deletion still shows, is
// not adopted again when a session starts.
func TestAdoptLeavesAloneARunnerThatHasJustLeft(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 16, memoryGiB: 64})
	fleet.put("a", "alive", types.MachineRunning, time.Now())
	fleet.put("a", "left", types.MachineRunning, time.Now())

	s := newTestScaleSet(fleet)
	s.left["left"] = time.Now()

	if err := s.adopt(context.Background()); err != nil {
		t.Fatalf("adopt() = %v", err)
	}

	var names []string
	for _, r := range s.sortedRunners() {
		names = append(names, r.Name)
	}
	if want := []string{"alive"}; !slices.Equal(names, want) {
		t.Errorf("adopted %v, want %v", names, want)
	}
}
