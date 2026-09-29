// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package grpcapi

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/scaleset"
	"github.com/konradasb/rungar/internal/types"
	rungarv1 "github.com/konradasb/rungar/proto/rungar/v1"
)

func TestListRunnersSendsEveryRunnerAndWhatIsMissing(t *testing.T) {
	f := newFixture(t)
	f.scaleSets.runners = types.RunnerList{
		Items: []types.Runner{
			{Name: "gone-vm-1", ScaleSet: "gone-vm", Provider: "compute1"},
			{
				Name: "rungar-vm-1", ScaleSet: "rungar-vm", Provider: "compute1", State: types.RunnerIdle,
				MachineState: types.MachineRunning, GitHubStatus: types.GitHubIdle, CreatedAt: time.Now(),
			},
		},
		Unreachable: map[string]error{"cloud": errors.New("connection refused")},
		GitHubErr:   errors.New("rate limited"),
	}

	resp, err := f.client.ListRunners(context.Background(),
		&rungarv1.ListRunnersRequest{ScaleSet: "rungar-vm", Provider: "compute1"})
	if err != nil {
		t.Fatal(err)
	}

	if got := f.scaleSets.askedRunnerFilter(); got != (scaleset.RunnerFilter{ScaleSet: "rungar-vm", Provider: "compute1"}) {
		t.Errorf("filter = %+v, want the request's scale set and provider", got)
	}

	runners := resp.GetRunners()
	if len(runners) != 2 || runners[0].GetName() != "gone-vm-1" || runners[1].GetName() != "rungar-vm-1" {
		t.Fatalf("runners = %v, want gone-vm-1 then rungar-vm-1, in the order given", runners)
	}

	r := runners[1]
	switch {
	case r.GetScaleSet() != "rungar-vm" || r.GetProvider() != "compute1":
		t.Errorf("runner of %s on %s, want rungar-vm on compute1", r.GetScaleSet(), r.GetProvider())
	case r.GetMachineState() != rungarv1.MachineState_MACHINE_STATE_RUNNING:
		t.Errorf("machine = %v, want running", r.GetMachineState())
	case r.GetGithubStatus() != rungarv1.RunnerGitHubStatus_RUNNER_GITHUB_STATUS_IDLE:
		t.Errorf("github = %v, want idle", r.GetGithubStatus())
	case r.GetCreateTime() == nil:
		t.Error("create time is missing")
	}

	if got := resp.GetUnreachableProviders()["cloud"]; got != "connection refused" {
		t.Errorf("unreachable[cloud] = %q, want the provider's error", got)
	}
	if resp.GetGithubError() != "rate limited" {
		t.Errorf("github error = %q, want GitHub's", resp.GetGithubError())
	}
}

func TestListRunnersPassesErrorsOn(t *testing.T) {
	f := newFixture(t)
	f.scaleSets.setErr(errdefs.NotFound("no provider %q", "nowhere"))

	_, err := f.client.ListRunners(context.Background(), &rungarv1.ListRunnersRequest{Provider: "nowhere"})
	wantCode(t, err, codes.NotFound)
}

func TestRemoveRunnerReturnsTheRunnerRemoved(t *testing.T) {
	f := newFixture(t)
	f.scaleSets.runners.Items = []types.Runner{{Name: "gone-vm-1", ScaleSet: "gone-vm", Provider: "compute1"}}

	r, err := f.client.RemoveRunner(context.Background(), &rungarv1.RemoveRunnerRequest{Name: "gone-vm-1"})
	if err != nil {
		t.Fatal(err)
	}
	if r.GetName() != "gone-vm-1" || r.GetProvider() != "compute1" {
		t.Errorf("removed %s on %s, want gone-vm-1 on compute1", r.GetName(), r.GetProvider())
	}

	_, err = f.client.RemoveRunner(context.Background(), &rungarv1.RemoveRunnerRequest{Name: "nowhere"})
	wantCode(t, err, codes.NotFound)
}

func TestRunnerToProtoCarriesEveryField(t *testing.T) {
	created := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	out := runnerToProto(types.Runner{
		Name: "rungar-vm-1", ScaleSet: "rungar-vm", Provider: "compute1", State: types.RunnerBusy,
		MachineState: types.MachineRunning, GitHubStatus: types.GitHubBusy, CreatedAt: created,
		Job: types.Job{
			ID: "42", Repository: "octo/tests", WorkflowRef: "octo/tests/.github/workflows/ci.yaml@refs/heads/main",
			DisplayName: "build", WorkflowRunID: 7, StartedAt: created,
		},
	})
	job := out.GetJob()

	switch {
	case out.GetName() != "rungar-vm-1" || out.GetScaleSet() != "rungar-vm" || out.GetProvider() != "compute1":
		t.Errorf("runner = %v", out)
	case out.GetState() != rungarv1.RunnerState_RUNNER_STATE_BUSY || job.GetId() != "42":
		t.Errorf("state %v, job %q; want busy, 42", out.GetState(), job.GetId())
	case job.GetRepository() != "octo/tests" || job.GetDisplayName() != "build" || job.GetWorkflowRunId() != 7 ||
		job.GetWorkflowRef() != "octo/tests/.github/workflows/ci.yaml@refs/heads/main":
		t.Errorf("job = %v", job)
	case !job.GetStartTime().AsTime().Equal(created):
		t.Errorf("job started = %v, want %v", job.GetStartTime().AsTime(), created)
	case out.GetMachineState() != rungarv1.MachineState_MACHINE_STATE_RUNNING || out.GetGithubStatus() != rungarv1.RunnerGitHubStatus_RUNNER_GITHUB_STATUS_BUSY:
		t.Errorf("machine %v, github %v; want running, busy", out.GetMachineState(), out.GetGithubStatus())
	case !out.GetCreateTime().AsTime().Equal(created):
		t.Errorf("created = %v, want %v", out.GetCreateTime().AsTime(), created)
	}

	if idle := runnerToProto(types.Runner{Name: "rungar-vm-2", State: types.RunnerIdle}); idle.GetJob() != nil {
		t.Errorf("an idle runner's job = %v, want none", idle.GetJob())
	}

	if got := runnersToProto(nil); got == nil || len(got) != 0 {
		t.Errorf("runnersToProto(nil) = %v, want an empty list", got)
	}
}

// TestRunnerStateToProto checks every state has its own value, and anything
// unknown is unspecified rather than taken for one.
func TestRunnerStateToProto(t *testing.T) {
	tests := []struct {
		in   types.RunnerState
		want rungarv1.RunnerState
	}{
		{types.RunnerStarting, rungarv1.RunnerState_RUNNER_STATE_STARTING},
		{types.RunnerIdle, rungarv1.RunnerState_RUNNER_STATE_IDLE},
		{types.RunnerBusy, rungarv1.RunnerState_RUNNER_STATE_BUSY},
		{"", rungarv1.RunnerState_RUNNER_STATE_UNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run(string(tt.in), func(t *testing.T) {
			if got := runnerStateToProto(tt.in); got != tt.want {
				t.Errorf("runnerStateToProto() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestMachineStateToProto checks every state has its own value, and anything
// unknown is unspecified rather than taken for one.
func TestMachineStateToProto(t *testing.T) {
	tests := []struct {
		in   types.MachineState
		want rungarv1.MachineState
	}{
		{types.MachineStarting, rungarv1.MachineState_MACHINE_STATE_STARTING},
		{types.MachineRunning, rungarv1.MachineState_MACHINE_STATE_RUNNING},
		{types.MachineStopped, rungarv1.MachineState_MACHINE_STATE_STOPPED},
		{"", rungarv1.MachineState_MACHINE_STATE_UNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run(string(tt.in), func(t *testing.T) {
			if got := machineStateToProto(tt.in); got != tt.want {
				t.Errorf("machineStateToProto() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestGitHubStatusToProto checks every status has its own value, and anything
// unknown is unspecified rather than taken for one.
func TestGitHubStatusToProto(t *testing.T) {
	tests := []struct {
		in   types.GitHubStatus
		want rungarv1.RunnerGitHubStatus
	}{
		{types.GitHubBusy, rungarv1.RunnerGitHubStatus_RUNNER_GITHUB_STATUS_BUSY},
		{types.GitHubIdle, rungarv1.RunnerGitHubStatus_RUNNER_GITHUB_STATUS_IDLE},
		{types.GitHubOffline, rungarv1.RunnerGitHubStatus_RUNNER_GITHUB_STATUS_OFFLINE},
		{types.GitHubNotRegistered, rungarv1.RunnerGitHubStatus_RUNNER_GITHUB_STATUS_NOT_REGISTERED},
		{"", rungarv1.RunnerGitHubStatus_RUNNER_GITHUB_STATUS_UNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run(string(tt.in), func(t *testing.T) {
			if got := gitHubStatusToProto(tt.in); got != tt.want {
				t.Errorf("gitHubStatusToProto() = %v, want %v", got, tt.want)
			}
		})
	}
}
