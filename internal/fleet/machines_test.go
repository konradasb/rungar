// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package fleet

import (
	"context"
	"errors"
	"testing"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/types"
)

// TestListNamesTheUnreachableProviders checks List returns what the
// reachable providers have, matching the selector and marked with their
// provider, and names the others unreachable.
func TestListNamesTheUnreachableProviders(t *testing.T) {
	a, b := &fake{}, &fake{listErr: errors.New("connection refused")}
	a.machines = []types.Machine{
		{Name: "r1", Labels: types.RunnerLabels("i", "set", "r1", "")},
		{Name: "x", Labels: map[string]string{"app": "web"}},
	}
	m, _ := testFleet(t, Config{}, ProviderConfig{Name: "a", Backend: a}, ProviderConfig{Name: "b", Backend: b})
	ctx := context.Background()

	machines, err := m.List(ctx, []string{"a", "b"}, types.ScaleSetSelector("i", "set"))

	var unreachable *UnreachableError
	if !errors.As(err, &unreachable) || !unreachable.Includes("b") || unreachable.Includes("a") {
		t.Fatalf("List() = %v, want b named unreachable", err)
	}
	if len(machines) != 1 || machines[0].Name != "r1" || machines[0].Provider != "a" {
		t.Errorf("List() = %+v, want r1 on a, all the same", machines)
	}
}

// TestListRefusesAProviderNotInTheFleet checks a name not in the fleet is
// reported as not found, not as an unreachable provider, and nothing is
// listed.
func TestListRefusesAProviderNotInTheFleet(t *testing.T) {
	a := &fake{machines: []types.Machine{{Name: "r1", Labels: types.RunnerLabels("i", "set", "r1", "")}}}
	m, _ := testFleet(t, Config{}, ProviderConfig{Name: "a", Backend: a})

	machines, err := m.List(context.Background(), []string{"a", "nowhere"}, types.ScaleSetSelector("i", "set"))

	var unreachable *UnreachableError
	if errors.As(err, &unreachable) || !errors.Is(err, errdefs.ErrNotFound) {
		t.Errorf("List() = %v, want an errdefs.ErrNotFound error", err)
	}
	if len(machines) != 0 {
		t.Errorf("List() = %+v, want nothing listed", machines)
	}
	if s := m.Snapshots([]string{"a"})[0]; !s.Reachable {
		t.Errorf("a = %+v, want it untouched", s)
	}
}

// TestDeleteAsksOnlyTheMachinesProvider checks Delete asks the machine's
// provider alone, and refuses a provider not in the fleet.
func TestDeleteAsksOnlyTheMachinesProvider(t *testing.T) {
	a, b := &fake{}, &fake{}
	m, _ := testFleet(t, Config{}, ProviderConfig{Name: "a", Backend: a}, ProviderConfig{Name: "b", Backend: b})
	ctx := context.Background()

	if err := m.Delete(ctx, "a", "r1"); err != nil || len(a.deleted) != 1 || len(b.deleted) != 0 {
		t.Errorf("Delete() = %v; a deleted %v, b deleted %v", err, a.deleted, b.deleted)
	}
	if err := m.Delete(ctx, "nowhere", "r1"); err == nil {
		t.Error("Delete() on a provider not in the fleet = nil, want an error")
	}
}

// TestCloseClosesEveryProvider checks Close closes every provider.
func TestCloseClosesEveryProvider(t *testing.T) {
	a, b := &fake{}, &fake{}
	m, _ := testFleet(t, Config{}, ProviderConfig{Name: "a", Backend: a}, ProviderConfig{Name: "b", Backend: b})

	if err := m.Close(); err != nil || !a.closed || !b.closed {
		t.Errorf("Close() = %v; closed a %v, b %v", err, a.closed, b.closed)
	}
}
