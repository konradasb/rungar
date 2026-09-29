// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"errors"
	"testing"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/types"
)

// TestProvidersSeeWhatPlacementSees is what the socket is for: a provider a
// scale set is skipping for refusing its runners is reported so, where a
// report built from outside the daemon would see nothing wrong with it.
func TestProvidersSeeWhatPlacementSees(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.compute.refuse = errors.New("the hypervisor crashed")
	_, _, _ = f.m.sets[0].providers.Place(ctx, nil, func(string) types.MachineSpec {
		return types.MachineSpec{Name: "rungar-vm-1"}
	})

	providers, err := f.m.Providers(ctx, "")
	if err != nil {
		t.Fatal(err)
	}

	if len(providers[0].ScaleSets) != 1 {
		t.Fatalf("compute1's scale sets = %+v, want rungar-vm", providers[0].ScaleSets)
	}
	if s := providers[0].ScaleSets[0]; s.BackoffFor <= 0 || s.Refusals != 1 || s.Refusal != "the hypervisor crashed" {
		t.Errorf("compute1 for rungar-vm = %+v, want it skipped, and why, after failing once", s)
	}
}

// TestSetProviderDisabledOverridesConfiguration checks a provider disabled
// while the daemon runs is said to be so against the configuration, and
// enabling it again is back to what the configuration says.
func TestSetProviderDisabledOverridesConfiguration(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	p, err := f.m.SetProviderDisabled(ctx, "compute1", true)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Snapshot.Disabled || p.ConfiguredDisabled {
		t.Errorf("compute1 = %+v, want it disabled against the configuration", p)
	}

	if p, err = f.m.SetProviderDisabled(ctx, "compute1", false); err != nil || p.Snapshot.Disabled {
		t.Errorf("compute1 = %+v, %v; want it enabled again", p, err)
	}

	if _, err := f.m.SetProviderDisabled(ctx, "nowhere", true); !errors.Is(err, errdefs.ErrNotFound) {
		t.Errorf("SetProviderDisabled() of a provider not configured = %v, want an ErrNotFound", err)
	}
}

func TestProvidersSaysWhatIsOnIt(t *testing.T) {
	f := newFixture(t, "rungar-vm/rungar-vm-1")

	providers, err := f.m.Providers(context.Background(), "compute1")
	if err != nil {
		t.Fatal(err)
	}

	p := providers[0]
	if p.Snapshot.RunnerCount != 1 {
		t.Errorf("compute1 = %+v, want 1 runner on it", p)
	}
	if len(p.ScaleSets) != 1 || p.ScaleSets[0].Name != "rungar-vm" || p.ScaleSets[0].BackoffFor != 0 {
		t.Errorf("compute1's scale sets = %+v, want rungar-vm, tried", p.ScaleSets)
	}
}
