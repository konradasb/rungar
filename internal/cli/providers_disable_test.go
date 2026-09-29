// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"testing"
)

// TestDisableLastsUntilRestart checks disable asks the daemon, and says the
// change lasts until it restarts.
func TestDisableLastsUntilRestart(t *testing.T) {
	h := newHarness(t)

	if err := h.run("providers", "disable", "compute1"); err != nil {
		t.Fatal(err)
	}

	if len(h.daemon.providerChanges) != 1 || h.daemon.providerChanges[0] != "disable compute1" {
		t.Errorf("calls = %v, want compute1 disabled", h.daemon.providerChanges)
	}
	if got, want := h.out.String(), "Provider compute1 is disabled until the daemon restarts.\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestEnableAnEnabledProviderDoesNothing(t *testing.T) {
	h := newHarness(t)

	if err := h.run("providers", "enable", "compute1"); err != nil {
		t.Fatal(err)
	}

	if len(h.daemon.providerChanges) != 0 {
		t.Errorf("calls = %v, want none", h.daemon.providerChanges)
	}
	h.says(t, "already enabled")
}

// TestEnableBackToTheConfiguration checks a change back to the configuration is
// not called temporary.
func TestEnableBackToTheConfiguration(t *testing.T) {
	h := newHarness(t)
	h.daemon.status.GetProviders()[0].Disabled = true

	if err := h.run("providers", "enable", "compute1"); err != nil {
		t.Fatal(err)
	}

	h.says(t, "Provider compute1 is enabled, as the configuration has it.")
}

func TestDrainWaitsForTheRunners(t *testing.T) {
	h := newHarness(t)
	h.daemon.emptyAfter = 2

	if err := h.run("providers", "drain", "compute1"); err != nil {
		t.Fatal(err)
	}

	h.says(t, "Waiting for 2 runners on compute1 to finish", "compute1 is drained")
}
