// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package grpcapi

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/konradasb/rungar/internal/types"
	rungarv1 "github.com/konradasb/rungar/proto/rungar/v1"
)

func TestListRunners(t *testing.T) {
	f := newFixture(t, "rungar-vm/rungar-vm-1", "gone-vm/gone-vm-1")

	resp, err := f.client.ListRunners(context.Background(), &rungarv1.ListRunnersRequest{})
	if err != nil {
		t.Fatal(err)
	}

	runners := resp.GetRunners()
	if len(runners) != 2 {
		t.Fatalf("got %d runners, want 2", len(runners))
	}

	// Sorted by scale set, then name: gone-vm's before rungar-vm's.
	if runners[0].GetName() != "gone-vm-1" {
		t.Errorf("first runner = %q, want gone-vm-1", runners[0].GetName())
	}

	r := runners[1]
	switch {
	case r.GetName() != "rungar-vm-1" || r.GetScaleSet() != "rungar-vm" || r.GetProvider() != "compute1":
		t.Errorf("second runner = %s of %s on %s, want rungar-vm-1 of rungar-vm on compute1",
			r.GetName(), r.GetScaleSet(), r.GetProvider())
	case r.GetMachineState() != "running":
		t.Errorf("machine = %q, want running", r.GetMachineState())
	case r.GetGithubStatus() != rungarv1.RunnerGitHubStatus_RUNNER_GIT_HUB_STATUS_IDLE:
		t.Errorf("github = %v, want idle", r.GetGithubStatus())
	case r.GetCreateTime() == nil:
		t.Error("create time is missing")
	}

	if len(resp.GetUnreachableProviders()) != 0 || resp.GetGithubError() != "" {
		t.Errorf("unreachable = %v, github error = %q; want neither", resp.GetUnreachableProviders(), resp.GetGithubError())
	}
}

func TestListRunnersFilters(t *testing.T) {
	f := newFixture(t, "rungar-vm/rungar-vm-1", "gone-vm/gone-vm-1")
	ctx := context.Background()

	resp, err := f.client.ListRunners(ctx, &rungarv1.ListRunnersRequest{ScaleSet: "gone-vm"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.GetRunners()) != 1 || resp.GetRunners()[0].GetName() != "gone-vm-1" {
		t.Errorf("gone-vm's runners = %v, want gone-vm-1 alone", resp.GetRunners())
	}

	resp, err = f.client.ListRunners(ctx, &rungarv1.ListRunnersRequest{Provider: "compute1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.GetRunners()) != 2 {
		t.Errorf("compute1's runners = %d, want 2", len(resp.GetRunners()))
	}

	_, err = f.client.ListRunners(ctx, &rungarv1.ListRunnersRequest{Provider: "nowhere"})
	wantCode(t, err, codes.NotFound)
}

// TestListRunnersSaysWhatIsMissing checks a listing says which providers
// could not be listed, and that GitHub could not be asked, rather than
// failing.
func TestListRunnersSaysWhatIsMissing(t *testing.T) {
	f := newFixture(t, "rungar-vm/rungar-vm-1")
	f.compute.listErr = errors.New("connection refused")
	f.github.listErr = errors.New("rate limited")

	resp, err := f.client.ListRunners(context.Background(), &rungarv1.ListRunnersRequest{})
	if err != nil {
		t.Fatal(err)
	}

	if got := resp.GetUnreachableProviders()["compute1"]; got != "connection refused" {
		t.Errorf("unreachable[compute1] = %q, want the provider's error", got)
	}
	if len(resp.GetRunners()) != 0 {
		t.Errorf("got %d runners from a provider that cannot be listed", len(resp.GetRunners()))
	}

	f.compute.listErr = nil

	resp, err = f.client.ListRunners(context.Background(), &rungarv1.ListRunnersRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetGithubError() != "rate limited" {
		t.Errorf("github error = %q, want GitHub's", resp.GetGithubError())
	}
}

func TestDeleteRunner(t *testing.T) {
	f := newFixture(t, "gone-vm/gone-vm-1")

	r, err := f.client.DeleteRunner(context.Background(), &rungarv1.DeleteRunnerRequest{Name: "gone-vm-1"})
	if err != nil {
		t.Fatal(err)
	}
	if r.GetName() != "gone-vm-1" || r.GetProvider() != "compute1" {
		t.Errorf("deleted %s on %s, want gone-vm-1 on compute1", r.GetName(), r.GetProvider())
	}
	if len(f.compute.machines) != 0 {
		t.Errorf("machines left = %d, want none", len(f.compute.machines))
	}
}

func TestRemoveRunnerRefusesBusyOrUnknown(t *testing.T) {
	f := newFixture(t, "gone-vm/gone-vm-1")
	f.github.setBusy("gone-vm-1")
	ctx := context.Background()

	_, err := f.client.DeleteRunner(ctx, &rungarv1.DeleteRunnerRequest{Name: "gone-vm-1"})
	wantCode(t, err, codes.FailedPrecondition)

	if len(f.compute.machines) != 1 {
		t.Error("the machine of a runner running a job was removed")
	}

	_, err = f.client.DeleteRunner(ctx, &rungarv1.DeleteRunnerRequest{Name: "nowhere"})
	wantCode(t, err, codes.NotFound)
}

func TestRunnerToProto(t *testing.T) {
	created := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	out := runnerToProto(types.Runner{
		Name: "rungar-vm-1", ScaleSet: "rungar-vm", Provider: "compute1", State: types.RunnerBusy, JobID: "42",
		MachineState: types.MachineRunning, GitHubStatus: types.GitHubBusy, CreatedAt: created,
	})

	switch {
	case out.GetName() != "rungar-vm-1" || out.GetScaleSet() != "rungar-vm" || out.GetProvider() != "compute1":
		t.Errorf("runner = %v", out)
	case out.GetState() != rungarv1.RunnerState_RUNNER_STATE_BUSY || out.GetJobId() != "42":
		t.Errorf("state %v, job %q; want busy, 42", out.GetState(), out.GetJobId())
	case out.GetMachineState() != "running" || out.GetGithubStatus() != rungarv1.RunnerGitHubStatus_RUNNER_GIT_HUB_STATUS_BUSY:
		t.Errorf("machine %q, github %v; want running, busy", out.GetMachineState(), out.GetGithubStatus())
	case !out.GetCreateTime().AsTime().Equal(created):
		t.Errorf("created = %v, want %v", out.GetCreateTime().AsTime(), created)
	}

	if got := runnersToProto(nil); got == nil || len(got) != 0 {
		t.Errorf("runnersToProto(nil) = %v, want an empty list", got)
	}
}
