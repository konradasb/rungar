// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package grpcapi

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/konradasb/rungar/internal/types"
	rungarv1 "github.com/konradasb/rungar/proto/rungar/v1"
)

// TestListScaleSets checks the configured scale set comes first, with what
// GitHub has of it, and then those found only by their runners.
func TestListScaleSets(t *testing.T) {
	f := newFixture(t, "rungar-vm/rungar-vm-1", "gone-vm/gone-vm-1")

	resp, err := f.client.ListScaleSets(context.Background(), &rungarv1.ListScaleSetsRequest{})
	if err != nil {
		t.Fatal(err)
	}

	sets := resp.GetScaleSets()
	if len(sets) != 2 {
		t.Fatalf("got %d scale sets, want 2", len(sets))
	}

	configured, leftover := sets[0], sets[1]

	switch {
	case configured.GetName() != "rungar-vm" || !configured.GetConfigured():
		t.Errorf("first = %s (configured %v), want rungar-vm, configured", configured.GetName(),
			configured.GetConfigured())
	case configured.GetGithub().GetId() != 7 || configured.GetGithub().GetStatistics().GetAssignedJobs() != 3:
		t.Errorf("github = %v, want ID 7 with 3 assigned jobs", configured.GetGithub())
	case configured.GetFleetRunners() != 1:
		t.Errorf("fleet runners = %d, want 1", configured.GetFleetRunners())
	}

	switch {
	case leftover.GetName() != "gone-vm" || leftover.GetConfigured():
		t.Errorf("second = %s (configured %v), want gone-vm, not configured", leftover.GetName(),
			leftover.GetConfigured())
	case leftover.GetGithub().GetId() != 9 || leftover.GetFleetRunners() != 1:
		t.Errorf("gone-vm: github ID %d, %d runners; want 9 and 1", leftover.GetGithub().GetId(),
			leftover.GetFleetRunners())
	case leftover.GetMaxRunners() != 0 || leftover.GetRunsOn() != "":
		t.Error("a scale set not configured has configuration")
	}
}

func TestGetScaleSet(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	set, err := f.client.GetScaleSet(ctx, &rungarv1.GetScaleSetRequest{Name: "rungar-vm"})
	if err != nil {
		t.Fatal(err)
	}

	switch {
	case !set.GetConfigured() || set.GetPhase() != rungarv1.ScaleSetPhase_SCALE_SET_PHASE_STARTING:
		t.Errorf("configured %v, phase %v; want configured and starting", set.GetConfigured(), set.GetPhase())
	case !slices.Equal(set.GetLabels(), []string{"rungar-vm", "linux"}):
		t.Errorf("labels = %v, want [rungar-vm linux]", set.GetLabels())
	case set.GetRunsOn() != "runs-on: rungar-vm":
		t.Errorf("runs on = %q, want the line that targets it", set.GetRunsOn())
	case !set.GetGithub().GetFound():
		t.Error("GitHub's scale set is not found")
	}

	// GitHub has it, though nothing else does.
	if _, err := f.client.GetScaleSet(ctx, &rungarv1.GetScaleSetRequest{Name: "gone-vm"}); err != nil {
		t.Errorf("GetScaleSet(gone-vm) = %v", err)
	}

	_, err = f.client.GetScaleSet(ctx, &rungarv1.GetScaleSetRequest{Name: "nowhere"})
	wantCode(t, err, codes.NotFound)
}

func TestDeleteScaleSet(t *testing.T) {
	f := newFixture(t, "gone-vm/gone-vm-1")

	resp, err := f.client.DeleteScaleSet(context.Background(), &rungarv1.DeleteScaleSetRequest{Name: "gone-vm"})
	if err != nil {
		t.Fatal(err)
	}

	switch {
	case len(resp.GetRemoved()) != 1 || resp.GetRemoved()[0].GetName() != "gone-vm-1":
		t.Errorf("removed = %v, want gone-vm-1", resp.GetRemoved())
	case resp.GetBusyLeft() != 0 || resp.GetScaleSetId() != 9:
		t.Errorf("busy = %d, scale set ID = %d; want 0 and 9", resp.GetBusyLeft(), resp.GetScaleSetId())
	case !slices.Equal(f.github.deleted, []int{9}):
		t.Errorf("GitHub deleted %v, want [9]", f.github.deleted)
	}
}

// TestDeleteScaleSetLeavesBusyRunners checks a runner running a job is left,
// and the scale set on GitHub with it.
func TestDeleteScaleSetLeavesBusyRunners(t *testing.T) {
	f := newFixture(t, "gone-vm/gone-vm-1")
	f.github.setBusy("gone-vm-1")

	resp, err := f.client.DeleteScaleSet(context.Background(), &rungarv1.DeleteScaleSetRequest{Name: "gone-vm"})
	if err != nil {
		t.Fatal(err)
	}

	if resp.GetBusyLeft() != 1 || resp.GetScaleSetId() != 0 || len(f.github.deleted) != 0 {
		t.Errorf("busy = %d, scale set ID = %d, GitHub deleted %v; want 1, 0 and nothing",
			resp.GetBusyLeft(), resp.GetScaleSetId(), f.github.deleted)
	}
}

func TestDeleteScaleSetRefusesAConfiguredOne(t *testing.T) {
	f := newFixture(t)

	_, err := f.client.DeleteScaleSet(context.Background(), &rungarv1.DeleteScaleSetRequest{Name: "rungar-vm"})
	wantCode(t, err, codes.InvalidArgument)
}

// TestReconcileRefusesUnknownOrStarting checks Reconcile names a scale set not configured, and
// says one still starting is unavailable, rather than reconciling it.
func TestReconcileRefusesUnknownOrStarting(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	_, err := f.client.Reconcile(ctx, &rungarv1.ReconcileRequest{ScaleSets: []string{"nowhere"}})
	wantCode(t, err, codes.NotFound)

	_, err = f.client.Reconcile(ctx, &rungarv1.ReconcileRequest{})
	wantCode(t, err, codes.Unavailable)
}

func TestScaleSetToProtoConfigured(t *testing.T) {
	set := types.ScaleSet{
		Spec: types.ScaleSetSpec{
			Name:        "rungar-vm",
			RunnerGroup: "builds",
			MinRunners:  1,
			MaxRunners:  4,
			Priority:    2,
			Placement:   types.PlacementPack,
			Providers:   []types.ProviderRef{{Name: "compute1"}, {Name: "cloud"}},
			RunnerSpecs: map[string]types.RunnerSpec{
				"compute1": runnerSize{VCPUs: 2, MemoryBytes: 4 * gib},
				"cloud":    runnerSize{},
			},
		},
		Status: types.ScaleSetStatus{
			Configured:   true,
			Phase:        types.ScaleSetListening,
			Desired:      3,
			DesiredKnown: true,
			FleetRunners: 3,
			Runners: []types.Runner{
				{State: types.RunnerStarting}, {State: types.RunnerIdle}, {State: types.RunnerBusy},
				{State: types.RunnerBusy},
			},
		},
	}

	out := scaleSetToProto(set)

	switch {
	case out.GetPhase() != rungarv1.ScaleSetPhase_SCALE_SET_PHASE_LISTENING:
		t.Errorf("phase = %v, want listening", out.GetPhase())
	case out.GetPlacement() != "pack" || out.GetRunnerGroup() != "builds" || out.GetPriority() != 2:
		t.Errorf("placement %q, group %q, priority %d; want pack, builds, 2",
			out.GetPlacement(), out.GetRunnerGroup(), out.GetPriority())
	case out.GetMinRunners() != 1 || out.GetMaxRunners() != 4 || out.GetDesiredRunners() != 3:
		t.Errorf("min %d, max %d, desired %d; want 1, 4, 3",
			out.GetMinRunners(), out.GetMaxRunners(), out.GetDesiredRunners())
	case out.GetRunnerCounts().GetStarting() != 1 || out.GetRunnerCounts().GetIdle() != 1 || out.GetRunnerCounts().GetBusy() != 2:
		t.Errorf("runner counts = %v, want 1 starting, 1 idle, 2 busy", out.GetRunnerCounts())
	case out.GetGithub() != nil:
		t.Errorf("github = %v, want nil when GitHub was not asked", out.GetGithub())
	}

	sizes := out.GetRunnerSizes()
	if len(sizes) != 2 || sizes[0].GetProvider() != "compute1" || sizes[1].GetProvider() != "cloud" {
		t.Fatalf("runner sizes = %v, want one per provider, in order", sizes)
	}
	if sizes[0].GetDescription() != "2 vCPU, 4 GiB" || sizes[1].GetDescription() != "" {
		t.Errorf("runner sizes = %v, want compute1's size and none for cloud's", sizes)
	}
}

// TestScaleSetToProtoLeftover checks a scale set found only by its runners
// carries no configuration, and no counts the daemon does not have.
func TestScaleSetToProtoLeftover(t *testing.T) {
	out := scaleSetToProto(types.ScaleSet{
		Spec:   types.ScaleSetSpec{Name: "gone-vm", MaxRunners: 4},
		Status: types.ScaleSetStatus{FleetRunners: 2},
	})

	switch {
	case out.GetConfigured() || out.GetMaxRunners() != 0 || out.GetLabels() != nil:
		t.Errorf("a leftover has configuration: %v", out)
	case out.GetRunnerCounts() != nil || out.GetDesiredRunners() != 0:
		t.Errorf("runners %v, desired %d; want neither", out.GetRunnerCounts(), out.GetDesiredRunners())
	case out.GetFleetRunners() != 2:
		t.Errorf("fleet runners = %d, want 2", out.GetFleetRunners())
	}
}

func TestGitHubScaleSetToProto(t *testing.T) {
	if gitHubScaleSetToProto(nil) != nil {
		t.Error("not asked is not nil")
	}

	if out := gitHubScaleSetToProto(&types.GitHubScaleSet{Err: errors.New("rate limited")}); out.GetError() !=
		"rate limited" || out.GetFound() {
		t.Errorf("an error = %v, want the error alone", out)
	}

	if out := gitHubScaleSetToProto(&types.GitHubScaleSet{ID: 7}); out.GetFound() || out.GetId() != 0 {
		t.Errorf("not found = %v, want empty", out)
	}

	created := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	out := gitHubScaleSetToProto(&types.GitHubScaleSet{
		Found: true, ID: 7, RunnerGroup: "Default", Labels: []string{"rungar-vm"}, CreatedAt: created,
		Statistics: &types.ScaleSetStatistics{AssignedJobs: 3, RunningJobs: 2, RegisteredRunners: 4, BusyRunners: 2,
			IdleRunners: 1},
	})

	switch {
	case !out.GetFound() || out.GetId() != 7 || out.GetRunnerGroup() != "Default":
		t.Errorf("found = %v", out)
	case !out.GetCreateTime().AsTime().Equal(created):
		t.Errorf("created = %v, want %v", out.GetCreateTime().AsTime(), created)
	case out.GetStatistics().GetRegisteredRunners() != 4 || out.GetStatistics().GetIdleRunners() != 1:
		t.Errorf("statistics = %v", out.GetStatistics())
	}
}
