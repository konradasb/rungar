// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/konradasb/rungar/internal/errdefs"
	rungarv1 "github.com/konradasb/rungar/proto/rungar/v1"
)

// TestScaleSetListIncludesLeftovers checks the listing has the configured
// scale sets and those found only by their runners.
func TestScaleSetListIncludesLeftovers(t *testing.T) {
	h := newHarness(t)

	if err := h.run("scale-sets", "ls"); err != nil {
		t.Fatal(err)
	}

	h.says(t, "rungar-vm", "LISTENING", "gone-vm", "LEFTOVER", "Runners on compute2 are not listed: connection refused")
}

// TestScaleSetListAsJSONHasEachScaleSet checks --json lists every scale set,
// leftovers too, by the API's field names.
func TestScaleSetListAsJSONHasEachScaleSet(t *testing.T) {
	h := newHarness(t)

	if err := h.run("scale-sets", "ls", "--json"); err != nil {
		t.Fatal(err)
	}

	var got struct {
		ScaleSets []struct {
			Name       string `json:"name"`
			Configured bool   `json:"configured"`
			RunsOn     string `json:"runs_on"`
		} `json:"scale_sets"`
	}
	if err := json.Unmarshal(h.out.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, h.out)
	}

	if len(got.ScaleSets) != 2 || !got.ScaleSets[0].Configured || got.ScaleSets[0].RunsOn != "runs-on: rungar-vm" {
		t.Errorf("scale sets = %+v, want rungar-vm configured with its runs-on, and gone-vm", got.ScaleSets)
	}
}

// TestInspectScaleSetShowsConfigurationGitHubAndRunners checks inspect shows
// what the configuration, the daemon and GitHub have of a scale set, its
// runners, and the runs-on line for it.
func TestInspectScaleSetShowsConfigurationGitHubAndRunners(t *testing.T) {
	h := newHarness(t)

	if err := h.run("scale-sets", "inspect", "rungar-vm"); err != nil {
		t.Fatal(err)
	}

	h.says(t, "rungar-vm", "compute1, compute2", "2 vCPU, 4 GiB", "3 assigned, 2 running", "rungar-vm, linux",
		"rungar-vm-1", "Workflows: runs-on: rungar-vm")
}

func TestInspectALeftoverScaleSetSaysNoRunsOn(t *testing.T) {
	h := newHarness(t)

	if err := h.run("scale-sets", "inspect", "gone-vm"); err != nil {
		t.Fatal(err)
	}

	h.says(t, "LEFTOVER", "no scale set of this name")
	if strings.Contains(h.out.String(), "runs-on") {
		t.Errorf("a leftover scale set is offered to workflows:\n%s", h.out)
	}
}

func TestInspectScaleSetAsJSONHasTheRunners(t *testing.T) {
	h := newHarness(t)

	if err := h.run("scale-sets", "inspect", "rungar-vm", "--json"); err != nil {
		t.Fatal(err)
	}

	var got struct {
		ScaleSet struct{ Name string } `json:"scale_set"`
		Runners  struct {
			Runners []struct{ Name string } `json:"runners"`
		} `json:"runners"`
	}
	if err := json.Unmarshal(h.out.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, h.out)
	}
	if got.ScaleSet.Name != "rungar-vm" || len(got.Runners.Runners) != 2 {
		t.Errorf("inspect --json = %+v, want the scale set and its runners", got)
	}
}

// TestRemoveScaleSetWaitsForBusyRunners checks --wait asks again until the
// runners running jobs have finished, writing each runner removed.
func TestRemoveScaleSetWaitsForBusyRunners(t *testing.T) {
	h := newHarness(t)
	h.daemon.removeScaleSetResponses = []*rungarv1.RemoveScaleSetResponse{
		{Removed: []*rungarv1.Runner{{Name: "gone-vm-1", Provider: "compute1"}}, BusyLeft: 1},
		{ScaleSetId: 9},
	}

	if err := h.run("scale-sets", "rm", "gone-vm", "--wait"); err != nil {
		t.Fatal(err)
	}

	h.says(t, "Removed runner gone-vm-1 from compute1", "Waiting for 1 runner to finish its job",
		`Removed scale set "gone-vm" (9) from GitHub`)
}

// TestRemoveScaleSetRefusesAConfiguredOne checks the daemon's refusal to
// remove a configured scale set is returned.
func TestRemoveScaleSetRefusesAConfiguredOne(t *testing.T) {
	h := newHarness(t)

	err := h.run("scale-sets", "rm", "rungar-vm")
	if err == nil || errorMessage(err) != `scale set "rungar-vm" is configured` {
		t.Errorf("removeScaleSet() of a configured scale set = %v, want the daemon's refusal", err)
	}
}

// TestRemoveScaleSetRefusesWithoutWait checks that a scale set with runners
// running jobs is left on GitHub, and said so, unless asked to wait.
func TestRemoveScaleSetRefusesWithoutWait(t *testing.T) {
	h := newHarness(t)
	h.daemon.removeScaleSetResponses = []*rungarv1.RemoveScaleSetResponse{{BusyLeft: 2}}

	err := h.run("scale-sets", "rm", "gone-vm")
	if !errors.Is(err, errdefs.ErrBusy) || !strings.Contains(err.Error(), "--wait") {
		t.Errorf("removeScaleSet() = %v, want an ErrBusy suggesting --wait", err)
	}
}

// TestRunnerSizesCellListsEachSizeOnce checks each distinct size is listed
// once, and "-" when no provider describes one.
func TestRunnerSizesCellListsEachSizeOnce(t *testing.T) {
	set := &rungarv1.ScaleSet{RunnerSizes: []*rungarv1.RunnerSize{
		{Provider: "compute1", Description: "4 vCPU, 8 GiB"},
		{Provider: "compute2", Description: "4 vCPU, 8 GiB"},
		{Provider: "gcp", Description: "e2-custom-4-8192"},
		{Provider: "other"},
	}}
	if got := runnerSizesCell(set); got != "4 vCPU, 8 GiB; e2-custom-4-8192" {
		t.Errorf("runnerSizesCell() = %q, want each distinct description once", got)
	}
	if got := runnerSizesCell(&rungarv1.ScaleSet{}); got != "-" {
		t.Errorf("runnerSizesCell() = %q, want - when none says", got)
	}
}

// TestInspectScaleSetShowsItsSchedule checks each window is shown in order,
// with the one in force marked, and what holds outside them.
func TestInspectScaleSetShowsItsSchedule(t *testing.T) {
	set := &rungarv1.ScaleSet{
		Name: "rungar-vm", Configured: true, Phase: rungarv1.ScaleSetPhase_SCALE_SET_PHASE_LISTENING,
		MinRunners: 4, ConfiguredMinRunners: 0, MaxRunners: 8,
		Schedule: &rungarv1.Schedule{
			TimeZone: "Europe/Vilnius",
			Windows: []*rungarv1.ScheduleWindow{
				{Days: "mon-fri", From: "08:00", To: "19:00", MinRunners: 4, InForce: true},
				{Days: "sun", From: "22:00", To: "02:00", MinRunners: 1},
			},
		},
	}

	var out bytes.Buffer
	writeScaleSet(&out, palette{}, set)

	for _, want := range []string{
		"at least 4, at most 8",
		"Schedule",
		"Europe/Vilnius",
		"mon-fri 08:00-19:00, at least 4 (in force)",
		"sun 22:00-02:00, at least 1\n",
		"otherwise, at least 0\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the output does not say %q:\n%s", want, out.String())
		}
	}
}

func TestInspectScaleSetMarksOutsideTheScheduleInForce(t *testing.T) {
	set := &rungarv1.ScaleSet{
		Name: "rungar-vm", Configured: true, MinRunners: 1, ConfiguredMinRunners: 1, MaxRunners: 8,
		Schedule: &rungarv1.Schedule{
			TimeZone: "UTC",
			Windows:  []*rungarv1.ScheduleWindow{{Days: "mon-fri", From: "08:00", To: "19:00", MinRunners: 4}},
		},
	}

	var out bytes.Buffer
	writeScaleSet(&out, palette{}, set)

	if !strings.Contains(out.String(), "otherwise, at least 1 (in force)") {
		t.Errorf("the output does not mark outside the schedule in force:\n%s", out.String())
	}
}

// TestScaleSetStatusSaysWhatItWaitsFor checks each phase short of listening has
// its own status and note.
func TestScaleSetStatusSaysWhatItWaitsFor(t *testing.T) {
	tests := []struct {
		phase        rungarv1.ScaleSetPhase
		status       string
		noteContains string
	}{
		{rungarv1.ScaleSetPhase_SCALE_SET_PHASE_STARTING, scaleSetStarting, "looked up on GitHub"},
		{rungarv1.ScaleSetPhase_SCALE_SET_PHASE_WAITING_FOR_SESSION, scaleSetWaiting, "another message session"},
		{rungarv1.ScaleSetPhase_SCALE_SET_PHASE_WAITING_FOR_LEAD, scaleSetWaiting, "lead scale set"},
		{rungarv1.ScaleSetPhase_SCALE_SET_PHASE_GITHUB_UNREACHABLE, statusUnreachable, "GitHub does not answer"},
		{rungarv1.ScaleSetPhase_SCALE_SET_PHASE_LISTENING, scaleSetListening, ""},
	}

	for _, tt := range tests {
		t.Run(tt.phase.String(), func(t *testing.T) {
			set := &rungarv1.ScaleSet{Configured: true, Phase: tt.phase}

			if got := scaleSetStatusCell(set); got != tt.status {
				t.Errorf("scaleSetStatusCell() = %q, want %q", got, tt.status)
			}
			note := scaleSetStatusNote(set)
			if tt.noteContains == "" && note != "" || !strings.Contains(note, tt.noteContains) {
				t.Errorf("scaleSetStatusNote() = %q, want it to say %q", note, tt.noteContains)
			}
		})
	}
}
