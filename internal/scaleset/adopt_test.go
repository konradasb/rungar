// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/konradasb/rungar/internal/types"
)

// TestAdoptTakesItsStateFromGitHub checks an adopted runner is what GitHub
// says it is, rather than idle whatever it is doing.
func TestAdoptTakesItsStateFromGitHub(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 16, memoryGiB: 64})
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
	for _, r := range s.list() {
		if r.State != want[r.Name] || !r.Adopted {
			t.Errorf("%s: state = %s, adopted = %v; want %s, adopted", r.Name, r.State, r.Adopted, want[r.Name])
		}
	}
}

// TestAdoptWarnsOfAnotherInstallation checks runners of the scale set under
// another installation are not adopted, and are warned of.
func TestAdoptWarnsOfAnotherInstallation(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 16, memoryGiB: 64})
	fleet.put("a", "mine", types.MachineRunning, time.Now())
	fleet.machines["a"] = append(fleet.machines["a"], types.Machine{
		Name:   "theirs",
		Labels: types.RunnerLabels("gh-elsewhere", testScaleSet, "theirs", ""),
		State:  types.MachineRunning,
	})

	var logs bytes.Buffer
	s := newScaleSetOver(fleet, &fakeGitHub{}, testScaleSetSpec(fleet), &recorder{},
		slog.New(slog.NewTextHandler(&logs, nil)))
	if err := s.adopt(context.Background()); err != nil {
		t.Fatal(err)
	}

	if runners := s.list(); len(runners) != 1 || runners[0].Name != "mine" {
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

func TestAdoptedRunner(t *testing.T) {
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

// TestAdoptCarriesOnWithoutAProvider checks that one provider being down does not
// keep a scale set from starting: what can be seen is adopted.
func TestAdoptCarriesOnWithoutAProvider(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 8, memoryGiB: 16}, fakeProvider{name: "b", vcpus: 8, memoryGiB: 16})
	fleet.put("a", "rungar-vm-on-a", types.MachineRunning, time.Now())
	fleet.put("b", "rungar-vm-on-b", types.MachineRunning, time.Now())
	fleet.unreachable = map[string]bool{"b": true}

	s := newTestScaleSet(fleet)
	if err := s.adopt(context.Background()); err != nil {
		t.Fatalf("adopt() = %v; a provider being down must not keep the scale set from starting", err)
	}

	if got := s.count(); got != 1 {
		t.Errorf("adopted %d runners, want the one that can be seen", got)
	}

	// Once the provider answers, what is on it is adopted.
	fleet.unreachable = nil
	if err := s.reconcileFleet(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := s.count(); got != 2 {
		t.Errorf("%d runners after the provider came back, want both", got)
	}
}
