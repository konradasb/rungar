// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/types"
)

// TestConfiguredScaleSetsSayWhatEachIsDoing checks a configured scale set is
// starting, its runners unknown, until it adopts them, and listening after.
func TestConfiguredScaleSetsSayWhatEachIsDoing(t *testing.T) {
	f := newFixture(t, "rungar-vm/rungar-vm-1")

	if got := f.m.ConfiguredScaleSets()[0].Status; got.Phase != types.ScaleSetStarting || got.Runners != nil {
		t.Errorf("before adopting = %+v, want it starting, its runners not yet known", got)
	}

	f.adopt(t)

	if got := f.m.ConfiguredScaleSets()[0].Status; got.Phase != types.ScaleSetListening || len(got.Runners) != 1 {
		t.Errorf("rungar-vm = %+v, want it listening with its one runner adopted", got)
	}

	runners, err := f.m.Runners(context.Background(), RunnerFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(runners.Items) != 1 || runners.Items[0].State == "" {
		t.Errorf("runners = %+v, want the runner with what the daemon knows of it", runners.Items)
	}
}

func TestScaleSetsListsLeftovers(t *testing.T) {
	f := newFixture(t, "rungar-vm/rungar-vm-1", "gone-vm/gone-vm-1", "gone-vm/gone-vm-2")

	list, err := f.m.ScaleSets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Unreachable) != 0 {
		t.Errorf("unreachable = %v, want none", list.Unreachable)
	}
	if len(list.Items) != 2 {
		t.Fatalf("scale sets = %+v, want rungar-vm and the leftover gone-vm", list.Items)
	}

	gone := list.Items[1]
	if gone.Spec.Name != "gone-vm" || gone.Status.Configured || gone.Status.FleetRunners != 2 {
		t.Errorf("leftover = %+v, want gone-vm, not configured, with its 2 runners", gone)
	}
	if gh := gone.Status.GitHub; gh == nil || !gh.Found || gh.ID != 9 {
		t.Errorf("leftover on GitHub = %+v, want scale set 9", gh)
	}
}

// TestAScaleSetNeitherConfiguredNorOnGitHubIsNotFound checks the lookup of a
// scale set nothing has.
func TestAScaleSetNeitherConfiguredNorOnGitHubIsNotFound(t *testing.T) {
	f := newFixture(t)

	if _, err := f.m.ScaleSet(context.Background(), "nowhere", ""); !errors.Is(err, errdefs.ErrNotFound) {
		t.Errorf("ScaleSet() = %v, want an ErrNotFound", err)
	}
}

func TestRunnersFiltersByProvider(t *testing.T) {
	f := newFixture(t, "rungar-vm/rungar-vm-1")
	ctx := context.Background()

	if _, err := f.m.Runners(ctx, RunnerFilter{Provider: "nowhere"}); !errors.Is(err, errdefs.ErrNotFound) {
		t.Errorf("Runners() on a provider not configured = %v, want an ErrNotFound", err)
	}

	runners, err := f.m.Runners(ctx, RunnerFilter{Provider: "compute1"})
	if err != nil || len(runners.Items) != 1 {
		t.Errorf("Runners() on compute1 = %+v, %v; want rungar-vm-1", runners, err)
	}
}

func TestRunnersSayWhatGitHubSays(t *testing.T) {
	f := newFixture(t, "rungar-vm/rungar-vm-1", "rungar-vm/rungar-vm-2")
	f.onGitHub["rungar-vm-1"] = answer{Registered: true, Online: true, Busy: true}
	delete(f.onGitHub, "rungar-vm-2")

	runners, err := f.m.Runners(context.Background(), RunnerFilter{})
	if err != nil {
		t.Fatal(err)
	}

	got := []types.GitHubStatus{runners.Items[0].GitHubStatus, runners.Items[1].GitHubStatus}
	if want := []types.GitHubStatus{types.GitHubBusy, types.GitHubNotRegistered}; !slices.Equal(got, want) {
		t.Errorf("registrations = %v, want %v", got, want)
	}
}

// TestRunnersAgreeWithGitHub checks a starting runner GitHub says is
// connected is listed idle, and is idle in its scale set, as soon as GitHub is
// asked rather than at the next reconcile.
func TestRunnersAgreeWithGitHub(t *testing.T) {
	f := newFixture(t, "rungar-vm/rungar-vm-1")
	s := f.adopt(t)

	s.mu.Lock()
	s.runners["rungar-vm-1"].State = types.RunnerStarting
	s.mu.Unlock()

	runners, err := f.m.Runners(context.Background(), RunnerFilter{})
	if err != nil {
		t.Fatal(err)
	}

	if got := runners.Items[0]; got.GitHubStatus != types.GitHubIdle || got.State != types.RunnerIdle {
		t.Errorf("runner = %+v, want it idle, as GitHub says", got)
	}
	if got := f.m.ConfiguredScaleSets()[0].Status.Runners[0].State; got != types.RunnerIdle {
		t.Errorf("scale set's runner = %q, want %q", got, types.RunnerIdle)
	}
}
