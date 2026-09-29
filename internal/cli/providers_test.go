// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	rungarv1 "github.com/konradasb/rungar/proto/rungar/v1"
)

func TestListProviders(t *testing.T) {
	h := newHarness(t)

	if err := h.run("providers", "ls"); err != nil {
		t.Fatal(err)
	}

	h.says(t, "RUNNERS", "compute1", "OK", "2/8", "compute2", "UNREACHABLE", "cloud",
		"compute3", "compute2 cannot be reached: connection refused",
		"rungar-gpu found compute3 failing (2 in a row); tries it again in 30s: connection reset",
		"rungar-big found cloud full; tries it again in 9s")
}

func TestInspectProvider(t *testing.T) {
	h := newHarness(t)

	if err := h.run("providers", "inspect", "compute1"); err != nil {
		t.Fatal(err)
	}

	h.says(t, "10.10.0.101:7443", "OK", "2/8", "rungar-vm", "Runners (2)")
}

// TestInspectProviderSaysWhyItIsNotTried checks inspect says which scale set
// skips the provider, and why.
func TestInspectProviderSaysWhyItIsNotTried(t *testing.T) {
	h := newHarness(t)

	if err := h.run("providers", "inspect", "compute3"); err != nil {
		t.Fatal(err)
	}

	h.says(t, "rungar-gpu failing (2 in a row), tried again in 30s: connection reset")
}

// TestProviderStatus checks each thing that keeps runners off a provider is
// said, the one most in the way first.
func TestProviderStatus(t *testing.T) {
	tests := []struct {
		name string
		p    *rungarv1.Provider
		says string
	}{
		{"fine", &rungarv1.Provider{Reachable: true}, "OK"},
		{"down, its error said beneath", &rungarv1.Provider{Error: "refused"}, "UNREACHABLE"},
		{"disabled", &rungarv1.Provider{Reachable: true, Disabled: true, ConfiguredDisabled: true}, "DISABLED"},
		{
			"disabled while running",
			&rungarv1.Provider{Reachable: true, Disabled: true},
			"DISABLED until the daemon restarts",
		},
		{"draining", &rungarv1.Provider{Reachable: true, Disabled: true, ConfiguredDisabled: true, RunnerCount: 2},
			"DRAINING 2 runners left"},
		{
			"enabled while running",
			&rungarv1.Provider{Reachable: true, ConfiguredDisabled: true},
			"OK enabled until the daemon restarts",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := providerStatus(palette{}, tt.p); got != tt.says {
				t.Errorf("providerStatus() = %q, want %q", got, tt.says)
			}
		})
	}
}

func TestRunnersOf(t *testing.T) {
	if got := runnersOf(&rungarv1.Provider{RunnerCount: 6, MaxRunners: 8}); got != "6/8" {
		t.Errorf("runnersOf() = %q, want 6/8", got)
	}
	if got := runnersOf(&rungarv1.Provider{RunnerCount: 6}); got != "6" {
		t.Errorf("runnersOf() = %q, want 6 for a provider with no limit", got)
	}
}

func TestPlacementNote(t *testing.T) {
	tests := []struct {
		pl   *rungarv1.ProviderScaleSet
		want string
	}{
		{&rungarv1.ProviderScaleSet{ScaleSet: "a"}, ""},
		{
			&rungarv1.ProviderScaleSet{BackoffFor: durationpb.New(8400 * time.Millisecond), Full: true},
			"full, tried again in 8s",
		},
		{
			&rungarv1.ProviderScaleSet{BackoffFor: durationpb.New(time.Minute), Failures: 3, Failure: "no such kernel"},
			"failing (3 in a row), tried again in 1m0s: no such kernel",
		},
		{&rungarv1.ProviderScaleSet{Failures: 3, Failure: "no such kernel"}, ""},
	}

	for _, tt := range tests {
		if got := placementNote(tt.pl); got != tt.want {
			t.Errorf("placementNote(%v) = %q, want %q", tt.pl, got, tt.want)
		}
	}
}
