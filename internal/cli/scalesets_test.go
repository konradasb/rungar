// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/konradasb/rungar/internal/errdefs"
	rungarv1 "github.com/konradasb/rungar/proto/rungar/v1"
)

func TestListScaleSets(t *testing.T) {
	h := newHarness(t)

	if err := h.run("scale-sets", "ls"); err != nil {
		t.Fatal(err)
	}

	h.says(t, "rungar-vm", "LISTENING", "gone-vm", "LEFTOVER", "Runners on compute2 are not listed: connection refused")
}

func TestListScaleSetsAsJSON(t *testing.T) {
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

func TestInspectScaleSet(t *testing.T) {
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

func TestRemoveScaleSet(t *testing.T) {
	h := newHarness(t)
	h.daemon.deleteScaleSetResponses = []*rungarv1.DeleteScaleSetResponse{
		{Removed: []*rungarv1.Runner{{Name: "gone-vm-1", Provider: "compute1"}}, BusyLeft: 1},
		{ScaleSetId: 9},
	}

	if err := h.run("scale-sets", "rm", "gone-vm", "--wait"); err != nil {
		t.Fatal(err)
	}

	h.says(t, "Removed runner gone-vm-1 from compute1", "Waiting for 1 runner to finish its job",
		`Removed scale set "gone-vm" (9) from GitHub`)

	if err := h.run("scale-sets", "rm", "rungar-vm"); err == nil {
		t.Error("removeScaleSet() of a configured scale set = nil, want the daemon's refusal")
	}
}

// TestRemoveScaleSetRefusesWithoutWait checks that a scale set with runners
// running jobs is left on GitHub, and said so, unless asked to wait.
func TestRemoveScaleSetRefusesWithoutWait(t *testing.T) {
	h := newHarness(t)
	h.daemon.deleteScaleSetResponses = []*rungarv1.DeleteScaleSetResponse{{BusyLeft: 2}}

	err := h.run("scale-sets", "rm", "gone-vm")
	if !errors.Is(err, errdefs.ErrBusy) || !strings.Contains(err.Error(), "--wait") {
		t.Errorf("removeScaleSet() = %v, want an ErrBusy suggesting --wait", err)
	}
}

func TestRunnerSizes(t *testing.T) {
	set := &rungarv1.ScaleSet{RunnerSizes: []*rungarv1.RunnerSize{
		{Provider: "compute1", Description: "4 vCPU, 8 GiB"},
		{Provider: "compute2", Description: "4 vCPU, 8 GiB"},
		{Provider: "gcp", Description: "e2-custom-4-8192"},
		{Provider: "other"},
	}}
	if got := runnerSizes(set); got != "4 vCPU, 8 GiB; e2-custom-4-8192" {
		t.Errorf("runnerSizes() = %q, want each distinct description once", got)
	}
	if got := runnerSizes(&rungarv1.ScaleSet{}); got != "-" {
		t.Errorf("runnerSizes() = %q, want - when none says", got)
	}
}
