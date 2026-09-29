// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package fleet

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/konradasb/rungar/internal/types"
)

// TestProviders checks each provider comes with the machines listed on it, and
// how it stands for each scale set on it: how long the scale set skips it, and
// why. A provider that could not be listed is still there, unreachable.
func TestProviders(t *testing.T) {
	a := &fake{machines: []types.Machine{{Name: "r1"}, {Name: "r2"}, {Name: "r3"}}}
	b := &fake{machines: []types.Machine{{Name: "r4"}}}
	c := &fake{listErr: errors.New("connection refused")}

	m, _ := testFleet(t, Options{},
		Provider{Name: "a", Endpoint: "10.0.0.1:7443", MaxRunners: 5, Backend: a},
		Provider{Name: "b", Disabled: true, Backend: b},
		Provider{Name: "c", Backend: c})
	m.byName["b"].recordFailure("s1", errFull)
	m.byName["b"].recordFailure("s2", errFailing)
	m.byName["b"].recordFailure("s2", errFailing)

	sets := []types.ScaleSetSpec{
		scaleSet("s1", 0, types.PlacementPack, "a", "b"),
		scaleSet("s2", 0, types.PlacementPack, "b"),
		scaleSet("s3", 0, types.PlacementPack, "c", "a"),
	}

	names := []string{"a", "b", "c"}
	machines, err := m.List(context.Background(), names, nil)

	var unreachable *UnreachableError
	if !errors.As(err, &unreachable) || !unreachable.Unreachable("c") {
		t.Fatalf("List() = %v, want c named unreachable", err)
	}

	providers := m.Providers(names, sets, machines)
	if len(machines) != 4 || len(providers) != 3 {
		t.Fatalf("Providers() = %d providers from %d machines, want 3 from 4", len(providers), len(machines))
	}

	pa := providers[0]
	if pa.Endpoint != "10.0.0.1:7443" || pa.Snapshot.RunnerCount != 3 || pa.Snapshot.MaxRunners != 5 ||
		!pa.Snapshot.Reachable {
		t.Errorf("a = %+v, want its endpoint and 3 of 5 runners", pa)
	}
	var setNames []string
	for _, set := range pa.ScaleSets {
		setNames = append(setNames, set.Name)
		if set.BackoffFor != 0 || set.Failures != 0 || set.Failure != "" {
			t.Errorf("a for %s = %+v, want it tried", set.Name, set)
		}
	}
	if got := strings.Join(setNames, ","); got != "s1,s3" {
		t.Errorf("a's scale sets = %s, want s1,s3", got)
	}

	pb := providers[1]
	if !pb.Snapshot.Disabled || !pb.ConfiguredDisabled || pb.Snapshot.RunnerCount != 1 {
		t.Errorf("b = %+v, want it disabled, with one runner", pb)
	}
	if len(pb.ScaleSets) != 2 {
		t.Fatalf("b's scale sets = %+v, want s1 and s2", pb.ScaleSets)
	}
	if s1 := pb.ScaleSets[0]; s1.Name != "s1" || s1.BackoffFor != 10*time.Second || !s1.Full ||
		s1.Failures != 1 || s1.Failure != "not enough memory" {
		t.Errorf("b for s1 = %+v, want it skipped as full for 10s", s1)
	}
	if s2 := pb.ScaleSets[1]; s2.Name != "s2" || s2.BackoffFor != 20*time.Second || s2.Full ||
		s2.Failures != 2 || s2.Failure != "connection reset" {
		t.Errorf("b for s2 = %+v, want it skipped for 20s after failing twice, saying why", s2)
	}

	if pc := providers[2]; pc.Snapshot.Name != "c" || pc.Snapshot.Reachable || pc.Snapshot.Err != "connection refused" {
		t.Errorf("c = %+v, want it there, unreachable, saying why", pc)
	}
}
