// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/konradasb/rungar/internal/errdefs"
	rungarv1 "github.com/konradasb/rungar/proto/rungar/v1"
)

// TestRunnerListShowsStateMachineGitHubAndJob checks each runner's columns.
func TestRunnerListShowsStateMachineGitHubAndJob(t *testing.T) {
	h := newHarness(t)

	if err := h.run("runners", "ls"); err != nil {
		t.Fatal(err)
	}

	h.says(t, "RUNNER", "STATE", "JOB", "rungar-vm-1", "Busy", "Running", "octo/tests: build", "gone-vm-1", "Offline")
}

// TestJobCellNamesRepositoryAndJob checks a job is named by its repository
// and name, falling back to its ID.
func TestJobCellNamesRepositoryAndJob(t *testing.T) {
	tests := []struct {
		name string
		job  *rungarv1.RunnerJob
		want string
	}{
		{"none", nil, "-"},
		{"repository and name", &rungarv1.RunnerJob{Id: "1", Repository: "octo/tests", DisplayName: "build"}, "octo/tests: build"},
		{"no repository", &rungarv1.RunnerJob{Id: "1", DisplayName: "build"}, "build"},
		{"only an ID", &rungarv1.RunnerJob{Id: "1", Repository: "octo/tests"}, "octo/tests: 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := jobCell(tt.job); got != tt.want {
				t.Errorf("jobCell() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestRemoveRunnersReportsEachFailureAndCountsThem checks every runner not
// removed is reported, on standard error, and counted in the error returned,
// while those removed are reported on standard output.
func TestRemoveRunnersReportsEachFailureAndCountsThem(t *testing.T) {
	h := newHarness(t)
	h.daemon.removeRunnerErr = map[string]error{
		"rungar-vm-1": errdefs.Busy(`runner "rungar-vm-1" is running a job`),
		"nowhere":     errdefs.NotFound(`no runner "nowhere" on the fleet`),
	}

	var stdout, stderr bytes.Buffer
	cmd := NewCommand()
	cmd.SetArgs([]string{"runners", "rm", "rungar-vm-1", "gone-vm-1", "nowhere", "--socket", h.socket})
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	err := cmd.ExecuteContext(t.Context())
	if err == nil || !strings.Contains(err.Error(), "2 runners not removed") {
		t.Errorf("runners rm = %v, want 2 not removed", err)
	}

	if want := "gone-vm-1: removed from compute1\n"; stdout.String() != want {
		t.Errorf("standard output = %q, want %q", stdout.String(), want)
	}
	for _, want := range []string{
		`rungar-vm-1: runner "rungar-vm-1" is running a job`,
		`nowhere: no runner "nowhere" on the fleet`,
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("standard error does not say %q:\n%s", want, stderr.String())
		}
	}
	if strings.Contains(stderr.String(), "rpc error") {
		t.Errorf("the output carries the transport's words:\n%s", stderr.String())
	}
}

// TestRemoveRunnerWithNoMachineSaysSo checks a registration-only removal names
// no provider.
func TestRemoveRunnerWithNoMachineSaysSo(t *testing.T) {
	h := newHarness(t)

	if err := h.run("runners", "rm", "machineless"); err != nil {
		t.Fatal(err)
	}

	h.says(t, "machineless: registration removed from GitHub; it had no machine")
}

// TestWriteRunnersSaysNoneRatherThanNothing checks an empty listing says so.
func TestWriteRunnersSaysNoneRatherThanNothing(t *testing.T) {
	var out bytes.Buffer
	writeRunners(&out, palette{}, nil, time.Now(), 0)

	got := out.String()
	if !strings.Contains(got, "none") {
		t.Errorf("writeRunners(empty) = %q, want it to say there are none", got)
	}
	if strings.Contains(got, "RUNNER") {
		t.Errorf("writeRunners(empty) printed a header with no rows: %q", got)
	}
}
