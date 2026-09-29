// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package fleet

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/konradasb/rungar/internal/errdefs"
)

// TestSnapshotsAskNothing checks Snapshots is what placement has learnt of each
// provider, without calling any: reachability from its last listing, its
// runners from the counter and those being created.
func TestSnapshotsAskNothing(t *testing.T) {
	a, b := &fake{}, &fake{listErr: errors.New("connection refused")}
	m, _ := testFleet(t, Config{},
		ProviderConfig{Name: "a", Type: "dicer", Weight: 2, MaxRunners: 5, Backend: a},
		ProviderConfig{Name: "b", Type: "dicer", Weight: 1, Backend: b})
	m.SetRunnerCounter(func() map[string]int { return map[string]int{"a": 3} })

	before := m.Snapshots([]string{"a", "b", "c"})
	if !before[0].Reachable || !before[1].Reachable {
		t.Errorf("Snapshots() = %+v, want providers never asked taken as reachable", before)
	}
	if before[2].Name != "c" || before[2].Error != "no such provider" {
		t.Errorf("Snapshots() of a provider not in the fleet = %+v", before[2])
	}

	_, _ = m.List(context.Background(), []string{"a", "b"}, nil)
	release, _ := m.byName["a"].claim(func() int { return 3 })
	defer release()

	created, listed := a.requests()
	s := m.Snapshots([]string{"a", "b"})
	if again, listedAgain := a.requests(); len(again) != len(created) || listedAgain != listed {
		t.Error("Snapshots() called a provider")
	}

	if got := s[0]; got.Name != "a" || got.Type != "dicer" || got.Weight != 2 || !got.Reachable ||
		got.MaxRunners != 5 || got.RunnerCount != 4 || got.Disabled {
		t.Errorf("a = %+v, want 3 runners and 1 being created of 5", got)
	}
	if got := s[1]; got.Reachable || got.Error != "connection refused" {
		t.Errorf("b = %+v, want it unreachable, saying why", got)
	}

	b.mu.Lock()
	b.listErr = nil
	b.mu.Unlock()
	_, _ = m.List(context.Background(), []string{"b"}, nil)
	if got := m.Snapshots([]string{"b"})[0]; !got.Reachable || got.Error != "" {
		t.Errorf("b = %+v, want it reachable again once it lists", got)
	}
}

// TestADisabledProviderIsLeftOutOfPlacement checks a provider can be taken
// out of placement while the fleet is in use, and put back.
func TestADisabledProviderIsLeftOutOfPlacement(t *testing.T) {
	m, _ := testFleet(t, Config{}, ProviderConfig{Name: "a", Backend: &fake{}})

	if err := m.SetDisabled("a", true); err != nil {
		t.Fatal(err)
	}
	if state := m.Snapshots([]string{"a"})[0]; !state.Disabled {
		t.Errorf("a = %+v, want it disabled and out of placement", state)
	}

	if err := m.SetDisabled("a", false); err != nil {
		t.Fatal(err)
	}
	if state := m.Snapshots([]string{"a"})[0]; state.Disabled {
		t.Errorf("a = %+v, want it enabled again", state)
	}

	if err := m.SetDisabled("nowhere", true); err == nil {
		t.Error("SetDisabled() of a provider not in the fleet = nil, want an error")
	}
}

// TestNamesKeepsTheConfigurationsOrder checks Names lists the providers in
// the configuration's order, and that CheckName refuses a name not among them.
func TestNamesKeepsTheConfigurationsOrder(t *testing.T) {
	m, _ := testFleet(t, Config{}, ProviderConfig{Name: "b", Backend: &fake{}}, ProviderConfig{Name: "a", Backend: &fake{}})

	if names := m.Names(); strings.Join(names, ",") != "b,a" {
		t.Errorf("Names() = %v; want b,a", names)
	}
	if err := m.CheckName("a"); err != nil {
		t.Errorf("CheckName(a) = %v, want nil", err)
	}
	if err := m.CheckName("c"); !errors.Is(err, errdefs.ErrNotFound) || !strings.Contains(err.Error(), "b, a") {
		t.Errorf("CheckName(c) = %v, want an ErrNotFound naming the providers there are", err)
	}
}

// TestNewFillsDefaultConfig checks settings left unset take the defaults.
func TestNewFillsDefaultConfig(t *testing.T) {
	a := New([]ProviderConfig{{Name: "a"}}, Config{}).byName["a"]

	if a.backoffFirst != defaultBackoffFirst || a.backoffMax != defaultBackoffMax ||
		a.holdDown != defaultHoldDown {
		t.Errorf("New() with no settings = %v/%v %v, want the defaults",
			a.backoffFirst, a.backoffMax, a.holdDown)
	}
}
