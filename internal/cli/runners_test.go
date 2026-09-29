// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/konradasb/rungar/internal/errdefs"
)

func TestListRunners(t *testing.T) {
	h := newHarness(t)

	if err := h.run("runners", "ls"); err != nil {
		t.Fatal(err)
	}

	h.says(t, "RUNNER", "STATE", "rungar-vm-1", "Busy", "Running", "gone-vm-1", "Offline")
}

func TestRemoveRunnersReportsEachFailureAndCountsThem(t *testing.T) {
	h := newHarness(t)
	h.daemon.deleteRunnerErr = map[string]error{
		"rungar-vm-1": errdefs.Busy(`runner "rungar-vm-1" is running a job`),
		"nowhere":     errdefs.NotFound(`no runner "nowhere" on the fleet`),
	}

	err := h.run("runners", "rm", "rungar-vm-1", "gone-vm-1", "nowhere")
	if err == nil || !strings.Contains(err.Error(), "2 runners not removed") {
		t.Errorf("removeRunners() = %v, want 2 not removed", err)
	}

	h.says(t, `rungar-vm-1: runner "rungar-vm-1" is running a job`, "gone-vm-1: removed from compute1",
		`nowhere: no runner "nowhere" on the fleet`)
	if strings.Contains(h.out.String(), "rpc error") {
		t.Errorf("the output carries the transport's words:\n%s", h.out)
	}
}

// TestWriteRunnersSaysNoneRatherThanNothing checks an empty listing says so.
func TestWriteRunnersSaysNoneRatherThanNothing(t *testing.T) {
	var out bytes.Buffer
	writeRunners(&out, palette{}, nil, 0)

	got := out.String()
	if !strings.Contains(got, "none") {
		t.Errorf("writeRunners(empty) = %q, want it to say there are none", got)
	}
	if strings.Contains(got, "RUNNER") {
		t.Errorf("writeRunners(empty) printed a header with no rows: %q", got)
	}
}
