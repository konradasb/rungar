// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	rungarv1 "github.com/konradasb/rungar/proto/rungar/v1"
)

// TestProviderListSaysWhyProvidersAreSkipped checks the listing says which
// provider cannot be reached, and which scale set skips which provider, and
// why.
func TestProviderListSaysWhyProvidersAreSkipped(t *testing.T) {
	h := newHarness(t)

	if err := h.run("providers", "ls"); err != nil {
		t.Fatal(err)
	}

	h.says(t, "RUNNERS", "compute1", "OK", "2/8", "compute2", "UNREACHABLE", "cloud",
		"compute3", "compute2 cannot be reached: connection refused",
		"rungar-gpu found compute3 failing (2 in a row); tries it again in 30s: connection reset",
		"rungar-big found cloud full; tries it again in 9s")
}

// TestInspectProviderShowsEndpointLimitAndRunners checks inspect shows where a
// provider is, its runners against its limit, its scale sets and runners.
func TestInspectProviderShowsEndpointLimitAndRunners(t *testing.T) {
	h := newHarness(t)

	if err := h.run("providers", "inspect", "compute1"); err != nil {
		t.Fatal(err)
	}

	h.says(t, "dicer:10.10.0.101:7443", "OK", "2/8", "rungar-vm", "Runners (2)")
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

// TestProviderStatusCellSaysWhatKeepsRunnersOff checks each thing that keeps
// runners off a provider is said, the one most in the way first.
func TestProviderStatusCellSaysWhatKeepsRunnersOff(t *testing.T) {
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
			if got := providerStatusCell(palette{}, tt.p); got != tt.says {
				t.Errorf("providerStatusCell() = %q, want %q", got, tt.says)
			}
		})
	}
}

// TestProviderRunnersCellShowsLimit checks a provider's runners are shown
// against its limit, if it has one.
func TestProviderRunnersCellShowsLimit(t *testing.T) {
	if got := providerRunnersCell(&rungarv1.Provider{RunnerCount: 6, MaxRunners: 8}); got != "6/8" {
		t.Errorf("providerRunnersCell() = %q, want 6/8", got)
	}
	if got := providerRunnersCell(&rungarv1.Provider{RunnerCount: 6}); got != "6" {
		t.Errorf("providerRunnersCell() = %q, want 6 for a provider with no limit", got)
	}
}

// TestSkipNoteSaysWhyAScaleSetSkipsTheProvider checks the note says
// whether the provider was found full or failing, and when it is tried again,
// and is empty when the scale set does not skip it.
func TestSkipNoteSaysWhyAScaleSetSkipsTheProvider(t *testing.T) {
	tests := []struct {
		name string
		set  *rungarv1.ProviderScaleSet
		want string
	}{
		{"not skipped", &rungarv1.ProviderScaleSet{ScaleSet: "a"}, ""},
		{
			"full",
			&rungarv1.ProviderScaleSet{BackoffFor: durationpb.New(8400 * time.Millisecond), Full: true},
			"full, tried again in 8s",
		},
		{
			"failing",
			&rungarv1.ProviderScaleSet{BackoffFor: durationpb.New(time.Minute), Refusals: 3, Refusal: "no such kernel"},
			"failing (3 in a row), tried again in 1m0s: no such kernel",
		},
		{"failed but no longer skipped", &rungarv1.ProviderScaleSet{Refusals: 3, Refusal: "no such kernel"}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := skipNote(tt.set); got != tt.want {
				t.Errorf("skipNote(%v) = %q, want %q", tt.set, got, tt.want)
			}
		})
	}
}
