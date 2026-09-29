// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/konradasb/rungar/internal/errdefs"
)

// TestPauseLastsUntilRestart checks pause asks the daemon, and says the change
// lasts until it restarts.
func TestPauseLastsUntilRestart(t *testing.T) {
	h := newHarness(t)

	if err := h.run("scale-sets", "pause", "rungar-vm"); err != nil {
		t.Fatal(err)
	}

	if len(h.daemon.scaleSetChanges) != 1 || h.daemon.scaleSetChanges[0] != "pause rungar-vm" {
		t.Errorf("calls = %v, want rungar-vm paused", h.daemon.scaleSetChanges)
	}
	if got, want := h.out.String(), "Scale set rungar-vm is paused until the daemon restarts.\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

// TestPauseOfAnUnconfiguredScaleSetIsNotFound checks a leftover cannot be
// paused, and the error names the scale sets that can.
func TestPauseOfAnUnconfiguredScaleSetIsNotFound(t *testing.T) {
	h := newHarness(t)

	err := h.run("scale-sets", "pause", "gone-vm")
	if !errors.Is(err, errdefs.ErrNotFound) {
		t.Fatalf("pause of a leftover = %v, want not found", err)
	}
	if !strings.Contains(err.Error(), "it has rungar-vm") {
		t.Errorf("error = %v, want the configured scale sets named", err)
	}
}

func TestPauseAPausedScaleSetDoesNothing(t *testing.T) {
	h := newHarness(t)
	h.daemon.status.GetScaleSets()[0].Paused = true

	if err := h.run("scale-sets", "pause", "rungar-vm"); err != nil {
		t.Fatal(err)
	}

	if len(h.daemon.scaleSetChanges) != 0 {
		t.Errorf("calls = %v, want none", h.daemon.scaleSetChanges)
	}
	h.says(t, "already paused")
}

func TestPauseWaitsForTheRunners(t *testing.T) {
	h := newHarness(t)
	h.daemon.emptyAfter = 2

	if err := h.run("scale-sets", "pause", "rungar-vm", "--wait"); err != nil {
		t.Fatal(err)
	}

	h.says(t, "Waiting for 2 runners of rungar-vm to finish", "rungar-vm is paused, and no runner of it is left")
}

// TestResumeAgainstTheConfigurationLastsUntilRestart checks resuming a scale
// set the configuration has paused asks the daemon, and says the change lasts
// until the daemon restarts.
func TestResumeAgainstTheConfigurationLastsUntilRestart(t *testing.T) {
	h := newHarness(t)
	set := h.daemon.status.GetScaleSets()[0]
	set.Paused, set.ConfiguredPaused = true, true

	if err := h.run("scale-sets", "resume", "rungar-vm"); err != nil {
		t.Fatal(err)
	}

	if len(h.daemon.scaleSetChanges) != 1 || h.daemon.scaleSetChanges[0] != "resume rungar-vm" {
		t.Errorf("calls = %v, want rungar-vm resumed", h.daemon.scaleSetChanges)
	}
	h.says(t, "Scale set rungar-vm is resumed until the daemon restarts.")
}

// TestResumeBackToTheConfiguration checks a change back to the configuration is
// not called temporary.
func TestResumeBackToTheConfiguration(t *testing.T) {
	h := newHarness(t)
	h.daemon.status.GetScaleSets()[0].Paused = true

	if err := h.run("scale-sets", "resume", "rungar-vm"); err != nil {
		t.Fatal(err)
	}

	h.says(t, "Scale set rungar-vm is resumed, as the configuration has it.")
}
