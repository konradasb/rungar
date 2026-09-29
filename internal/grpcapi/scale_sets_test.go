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

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/types"
	rungarv1 "github.com/konradasb/rungar/proto/rungar/v1"
)

// TestListScaleSetsKeepsTheOrderAndWhatIsMissing checks the scale sets are
// listed in the order given, with the providers that could not be listed.
func TestListScaleSetsKeepsTheOrderAndWhatIsMissing(t *testing.T) {
	f := newFixture(t)
	f.scaleSets.scaleSets = types.ScaleSetList{
		Items: []types.ScaleSet{
			{
				Spec:   types.ScaleSetSpec{Name: "rungar-vm", MaxRunners: 4},
				Status: types.ScaleSetStatus{Configured: true, FleetRunners: 1, GitHub: &types.GitHubScaleSet{Found: true, ID: 7}},
			},
			{Spec: types.ScaleSetSpec{Name: "gone-vm"}, Status: types.ScaleSetStatus{FleetRunners: 1}},
		},
		Unreachable: map[string]error{"cloud": errors.New("connection refused")},
	}

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
	case configured.GetGithub().GetId() != 7 || configured.GetFleetRunners() != 1:
		t.Errorf("github ID %d, %d runners; want 7 and 1", configured.GetGithub().GetId(), configured.GetFleetRunners())
	case leftover.GetName() != "gone-vm" || leftover.GetConfigured():
		t.Errorf("second = %s (configured %v), want gone-vm, not configured", leftover.GetName(),
			leftover.GetConfigured())
	}

	if got := resp.GetUnreachableProviders()["cloud"]; got != "connection refused" {
		t.Errorf("unreachable[cloud] = %q, want the provider's error", got)
	}
}

func TestGetScaleSetFindsByNameInTheRunnerGroup(t *testing.T) {
	f := newFixture(t)
	f.scaleSets.scaleSets.Items = []types.ScaleSet{{
		Spec:   types.ScaleSetSpec{Name: "rungar-vm", Labels: []string{"linux"}},
		Status: types.ScaleSetStatus{Configured: true, Phase: types.ScaleSetStarting},
	}}
	ctx := context.Background()

	set, err := f.client.GetScaleSet(ctx, &rungarv1.GetScaleSetRequest{Name: "rungar-vm", RunnerGroup: "builds"})
	if err != nil {
		t.Fatal(err)
	}

	switch {
	case f.scaleSets.askedRunnerGroup() != "builds":
		t.Errorf("asked for runner group %q, want builds", f.scaleSets.askedRunnerGroup())
	case !set.GetConfigured() || set.GetPhase() != rungarv1.ScaleSetPhase_SCALE_SET_PHASE_STARTING:
		t.Errorf("configured %v, phase %v; want configured and starting", set.GetConfigured(), set.GetPhase())
	case !slices.Equal(set.GetLabels(), []string{"rungar-vm", "linux"}):
		t.Errorf("labels = %v, want [rungar-vm linux]", set.GetLabels())
	case set.GetRunsOn() != "runs-on: rungar-vm":
		t.Errorf("runs on = %q, want the line that targets it", set.GetRunsOn())
	}

	_, err = f.client.GetScaleSet(ctx, &rungarv1.GetScaleSetRequest{Name: "nowhere"})
	wantCode(t, err, codes.NotFound)
}

func TestRemoveScaleSetReportsWhatItRemoved(t *testing.T) {
	f := newFixture(t)
	f.scaleSets.removal = types.ScaleSetRemoval{
		Removed:    []types.Runner{{Name: "gone-vm-1"}},
		BusyLeft:   1,
		ScaleSetID: 9,
	}

	resp, err := f.client.RemoveScaleSet(context.Background(),
		&rungarv1.RemoveScaleSetRequest{Name: "gone-vm", RunnerGroup: "builds"})
	if err != nil {
		t.Fatal(err)
	}

	switch {
	case f.scaleSets.askedRunnerGroup() != "builds":
		t.Errorf("asked for runner group %q, want builds", f.scaleSets.askedRunnerGroup())
	case len(resp.GetRemoved()) != 1 || resp.GetRemoved()[0].GetName() != "gone-vm-1":
		t.Errorf("removed = %v, want gone-vm-1", resp.GetRemoved())
	case resp.GetBusyLeft() != 1 || resp.GetScaleSetId() != 9:
		t.Errorf("busy = %d, scale set ID = %d; want 1 and 9", resp.GetBusyLeft(), resp.GetScaleSetId())
	}

	f.scaleSets.setErr(errdefs.InvalidArgument("scale set %q is configured", "rungar-vm"))
	_, err = f.client.RemoveScaleSet(context.Background(), &rungarv1.RemoveScaleSetRequest{Name: "rungar-vm"})
	wantCode(t, err, codes.InvalidArgument)
}

func TestReconcileReturnsTheScaleSetsAfterwards(t *testing.T) {
	f := newFixture(t)
	f.scaleSets.configured = []types.ScaleSet{{
		Spec: types.ScaleSetSpec{Name: "rungar-vm"}, Status: types.ScaleSetStatus{Configured: true},
	}}

	resp, err := f.client.Reconcile(context.Background(), &rungarv1.ReconcileRequest{ScaleSets: []string{"rungar-vm"}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.scaleSets.askedReconciled(), []string{"rungar-vm"}) {
		t.Errorf("reconciled %v, want [rungar-vm]", f.scaleSets.askedReconciled())
	}
	if len(resp.GetScaleSets()) != 1 || resp.GetScaleSets()[0].GetName() != "rungar-vm" {
		t.Errorf("scale sets = %v, want rungar-vm", resp.GetScaleSets())
	}

	f.scaleSets.setErr(errdefs.Unavailable("scale set %q is starting", "rungar-vm"))
	_, err = f.client.Reconcile(context.Background(), &rungarv1.ReconcileRequest{})
	wantCode(t, err, codes.Unavailable)
}

func TestPauseAndResumeScaleSet(t *testing.T) {
	f := newFixture(t)
	f.scaleSets.configured = []types.ScaleSet{{
		Spec: types.ScaleSetSpec{Name: "rungar-vm"}, Status: types.ScaleSetStatus{Configured: true},
	}}
	ctx := context.Background()

	set, err := f.client.PauseScaleSet(ctx, &rungarv1.PauseScaleSetRequest{Name: "rungar-vm"})
	if err != nil {
		t.Fatal(err)
	}
	if !set.GetPaused() {
		t.Error("after pausing: not paused")
	}

	set, err = f.client.ResumeScaleSet(ctx, &rungarv1.ResumeScaleSetRequest{Name: "rungar-vm"})
	if err != nil {
		t.Fatal(err)
	}
	if set.GetPaused() {
		t.Error("after resuming: still paused")
	}

	_, err = f.client.PauseScaleSet(ctx, &rungarv1.PauseScaleSetRequest{Name: "nowhere"})
	wantCode(t, err, codes.NotFound)
}

// TestScaleSetToProtoMarksTheWindowInForce checks a scale set in a window of
// its schedule reports the window's min_runners as in force, beside the
// configuration's.
func TestScaleSetToProtoMarksTheWindowInForce(t *testing.T) {
	zone, err := types.ParseTimeZone("Europe/Vilnius")
	if err != nil {
		t.Fatal(err)
	}
	weekdays, err := types.ParseWeekdays("mon-fri")
	if err != nil {
		t.Fatal(err)
	}

	set := types.ScaleSet{
		Spec: types.ScaleSetSpec{
			Name:       "rungar-vm",
			MinRunners: 0,
			MaxRunners: 8,
			Schedule: types.Schedule{
				TimeZone: zone,
				Windows: []types.ScheduleWindow{
					{Days: weekdays, From: 8 * 60, To: 19 * 60, MinRunners: 4},
					{Days: weekdays, From: 19 * 60, To: 22 * 60, MinRunners: 1},
				},
			},
		},
		Status: types.ScaleSetStatus{Configured: true, MinRunners: 4, Window: 0, InWindow: true},
	}

	out := scaleSetToProto(set)

	if out.GetMinRunners() != 4 || out.GetConfiguredMinRunners() != 0 {
		t.Errorf("min_runners %d, configured %d; want 4 in force, 0 configured",
			out.GetMinRunners(), out.GetConfiguredMinRunners())
	}

	schedule := out.GetSchedule()
	if schedule.GetTimeZone() != "Europe/Vilnius" || len(schedule.GetWindows()) != 2 {
		t.Fatalf("schedule = %v, want Europe/Vilnius with 2 windows", schedule)
	}

	first, second := schedule.GetWindows()[0], schedule.GetWindows()[1]
	if first.GetDays() != "mon-fri" || first.GetFrom() != "08:00" || first.GetTo() != "19:00" ||
		first.GetMinRunners() != 4 || !first.GetInForce() {
		t.Errorf("first window = %v, want mon-fri 08:00-19:00, 4, in force", first)
	}
	if second.GetInForce() {
		t.Errorf("second window = %v, want it not in force", second)
	}
}

// TestScaleSetToProtoWithoutSchedule checks a scale set without a schedule
// has none in its message.
func TestScaleSetToProtoWithoutSchedule(t *testing.T) {
	set := types.ScaleSet{
		Spec:   types.ScaleSetSpec{Name: "rungar-vm", MinRunners: 2, MaxRunners: 4},
		Status: types.ScaleSetStatus{Configured: true, MinRunners: 2},
	}

	out := scaleSetToProto(set)

	if out.GetSchedule() != nil {
		t.Errorf("schedule = %v, want none", out.GetSchedule())
	}
	if out.GetMinRunners() != 2 || out.GetConfiguredMinRunners() != 2 {
		t.Errorf("min_runners %d, configured %d; want 2 and 2", out.GetMinRunners(), out.GetConfiguredMinRunners())
	}
}

func TestScaleSetToProtoCarriesTheConfiguration(t *testing.T) {
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
				"compute1": runnerSize{VCPUs: 2, Memory: 4 * gib},
				"cloud":    runnerSize{},
			},
		},
		Status: types.ScaleSetStatus{
			Configured:   true,
			Phase:        types.ScaleSetListening,
			MinRunners:   1,
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

// TestScaleSetToProtoLeavesALeftoverUnconfigured checks a scale set found only
// by its runners carries no configuration, and no counts the daemon does not
// have.
func TestScaleSetToProtoLeavesALeftoverUnconfigured(t *testing.T) {
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

func TestGitHubScaleSetToProtoCarriesOnlyWhatWasFound(t *testing.T) {
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

// TestScaleSetPhaseToProto checks every phase has its own value, and anything
// unknown is unspecified rather than taken for one.
func TestScaleSetPhaseToProto(t *testing.T) {
	tests := []struct {
		in   types.ScaleSetPhase
		want rungarv1.ScaleSetPhase
	}{
		{types.ScaleSetStarting, rungarv1.ScaleSetPhase_SCALE_SET_PHASE_STARTING},
		{types.ScaleSetWaitingForSession, rungarv1.ScaleSetPhase_SCALE_SET_PHASE_WAITING_FOR_SESSION},
		{types.ScaleSetListening, rungarv1.ScaleSetPhase_SCALE_SET_PHASE_LISTENING},
		{types.ScaleSetGitHubUnreachable, rungarv1.ScaleSetPhase_SCALE_SET_PHASE_GITHUB_UNREACHABLE},
		{types.ScaleSetWaitingForLead, rungarv1.ScaleSetPhase_SCALE_SET_PHASE_WAITING_FOR_LEAD},
		{"", rungarv1.ScaleSetPhase_SCALE_SET_PHASE_UNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run(string(tt.in), func(t *testing.T) {
			if got := scaleSetPhaseToProto(tt.in); got != tt.want {
				t.Errorf("scaleSetPhaseToProto() = %v, want %v", got, tt.want)
			}
		})
	}
}
