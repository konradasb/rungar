// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	ghscaleset "github.com/actions/scaleset"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/fleet"
	"github.com/konradasb/rungar/internal/github"
	"github.com/konradasb/rungar/internal/provider"
	"github.com/konradasb/rungar/internal/types"
)

// backend is a provider held in memory: the machines on it.
type backend struct {
	mu sync.Mutex

	machines []types.Machine

	// refuse is what Create answers with, if anything.
	refuse error

	// deleted records what Delete was asked to remove.
	deleted []string
}

var _ provider.Provider = (*backend)(nil)

func (b *backend) List(_ context.Context, selector map[string]string) ([]types.Machine, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	var out []types.Machine
	for _, m := range b.machines {
		if types.Matches(m.Labels, selector) {
			out = append(out, m)
		}
	}

	return out, nil
}

// Create makes the machine, unless the test has it refuse.
func (b *backend) Create(_ context.Context, spec types.MachineSpec) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.refuse != nil {
		return b.refuse
	}
	b.machines = append(b.machines, types.Machine{
		Name: spec.Name, Labels: spec.Labels, State: types.MachineRunning, CreatedAt: time.Now(),
	})

	return nil
}

func (b *backend) Close() error { return nil }

func (b *backend) Delete(_ context.Context, name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.deleted = append(b.deleted, name)
	b.machines = slices.DeleteFunc(b.machines, func(m types.Machine) bool { return m.Name == name })

	return nil
}

// fixture is a Manager that has not been run, without a message session:
// scale set rungar-vm on compute1, a fleet holding the runners given, and
// GitHub holding rungar-vm and gone-vm.
type fixture struct {
	m       *Manager
	github  *fakeGitHub
	compute *backend

	// onGitHub is what GitHub says of each runner.
	onGitHub map[string]answer
}

// newFixture makes a Manager over runners written scaleSet/name.
func newFixture(t *testing.T, runners ...string) *fixture {
	t.Helper()

	compute := &backend{}

	f := &fixture{compute: compute, onGitHub: map[string]answer{}}
	f.github = &fakeGitHub{
		status: registered(f.onGitHub),
		sets: map[string]*ghscaleset.RunnerScaleSet{
			"rungar-vm": {ID: 7, Name: "rungar-vm", RunnerGroupName: "Default",
				Statistics: &ghscaleset.RunnerScaleSetStatistic{TotalAssignedJobs: 3, TotalRunningJobs: 2}},
			"gone-vm": {ID: 9, Name: "gone-vm", RunnerGroupName: "Default"},
		},
	}

	for _, r := range runners {
		scaleSet, name, _ := strings.Cut(r, "/")
		compute.machines = append(compute.machines, types.Machine{
			Name:      name,
			Labels:    types.RunnerLabels(testInstallation, scaleSet, name, ""),
			State:     types.MachineRunning,
			CreatedAt: time.Now().Add(-time.Minute),
		})
		f.onGitHub[name] = answer{Registered: true, Online: true}
	}

	fl := fleet.New([]fleet.Provider{{Name: "compute1", Type: "test", Weight: 1, Backend: compute}}, fleet.Options{})

	spec := types.ScaleSetSpec{
		Name: "rungar-vm", MaxRunners: 4, StartTimeout: time.Minute, RunnerGroup: types.DefaultRunnerGroup,
		Providers:   []types.ProviderRef{{Name: "compute1"}},
		RunnerSpecs: map[string]types.RunnerSpec{"compute1": runnerSize{VCPUs: 2, MemoryBytes: 4 * gib}},
	}

	f.m = &Manager{
		installation:   testInstallation,
		specs:          []types.ScaleSetSpec{spec},
		interval:       time.Minute,
		fleet:          fl,
		github:         f.github,
		gitHubStatuses: f.github,
		metrics:        &recorder{},
		events:         &fakeEvents{},
		logger:         discardLogger(),
	}
	f.m.sets = []*scaleSet{newScaleSetOver(fl.Group(spec), f.github, spec, &recorder{}, discardLogger())}

	return f
}

// adopt has rungar-vm adopt its runners, as it does once running.
func (f *fixture) adopt(t *testing.T) *scaleSet {
	t.Helper()

	s := f.m.sets[0]
	if err := s.adopt(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.setPhase(types.ScaleSetListening)

	return s
}

// TestProvidersSeeWhatPlacementSees is what the socket is for: a provider a
// scale set is skipping for refusing its runners is reported so, where a
// report built from outside the daemon would see nothing wrong with it.
func TestProvidersSeeWhatPlacementSees(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.compute.refuse = errors.New("the hypervisor crashed")
	_, _, _ = f.m.sets[0].fleet.Place(ctx, nil, func(string) types.MachineSpec {
		return types.MachineSpec{Name: "rungar-vm-1"}
	})

	providers, err := f.m.Providers(ctx, "")
	if err != nil {
		t.Fatal(err)
	}

	if len(providers[0].ScaleSets) != 1 {
		t.Fatalf("compute1's scale sets = %+v, want rungar-vm", providers[0].ScaleSets)
	}
	if s := providers[0].ScaleSets[0]; s.BackoffFor <= 0 || s.Failures != 1 || s.Failure != "the hypervisor crashed" {
		t.Errorf("compute1 for rungar-vm = %+v, want it skipped, and why, after failing once", s)
	}
}

func TestConfiguredSaysWhatEachScaleSetIsDoing(t *testing.T) {
	f := newFixture(t, "rungar-vm/rungar-vm-1")

	if got := f.m.Configured()[0].Status; got.Phase != types.ScaleSetStarting || got.Runners != nil {
		t.Errorf("before adopting = %+v, want it starting, its runners not yet known", got)
	}

	f.adopt(t)

	if got := f.m.Configured()[0].Status; got.Phase != types.ScaleSetListening || len(got.Runners) != 1 {
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

func TestScaleSetNotAnywhere(t *testing.T) {
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
	if got := f.m.Configured()[0].Status.Runners[0].State; got != types.RunnerIdle {
		t.Errorf("scale set's runner = %q, want %q", got, types.RunnerIdle)
	}
}

// TestDeleteRunnerGoesThroughItsScaleSet checks a runner of a running scale
// set is removed through it, so it forgets the runner at once rather than at
// the next reconcile.
func TestDeleteRunnerGoesThroughItsScaleSet(t *testing.T) {
	f := newFixture(t, "rungar-vm/rungar-vm-1")
	s := f.adopt(t)

	r, err := f.m.RemoveRunner(context.Background(), "rungar-vm-1")
	if err != nil {
		t.Fatalf("DeleteRunner() = %v", err)
	}
	if r.Provider != "compute1" {
		t.Errorf("removed runner = %+v, want it on compute1", r)
	}
	if s.count() != 0 {
		t.Error("the scale set still has the runner")
	}
	if !slices.Equal(f.compute.deleted, []string{"rungar-vm-1"}) {
		t.Errorf("deleted = %v, want rungar-vm-1", f.compute.deleted)
	}
}

// TestDeleteRunnerRefusesABusyOne checks both ways a runner is known to be
// running a job: the daemon was told so, or GitHub refuses to let it go.
func TestDeleteRunnerRefusesABusyOne(t *testing.T) {
	t.Run("the daemon knows", func(t *testing.T) {
		f := newFixture(t, "rungar-vm/rungar-vm-1")
		f.adopt(t).jobStarted("rungar-vm-1", "job-1")

		if _, err := f.m.RemoveRunner(context.Background(), "rungar-vm-1"); !errors.Is(err, errdefs.ErrBusy) {
			t.Errorf("DeleteRunner() = %v, want an ErrBusy", err)
		}
		if len(f.compute.deleted) != 0 || len(f.github.removed) != 0 {
			t.Error("a busy runner was removed")
		}
	})

	t.Run("GitHub knows", func(t *testing.T) {
		f := newFixture(t, "gone-vm/gone-vm-1")
		f.github.setBusy("gone-vm-1", true)

		if _, err := f.m.RemoveRunner(context.Background(), "gone-vm-1"); !errors.Is(err, errdefs.ErrBusy) {
			t.Errorf("DeleteRunner() = %v, want an ErrBusy", err)
		}
		if len(f.compute.deleted) != 0 {
			t.Error("a busy runner's machine was removed")
		}
	})
}

func TestDeleteRunnerNotOnTheFleet(t *testing.T) {
	f := newFixture(t)

	if _, err := f.m.RemoveRunner(context.Background(), "nowhere"); !errors.Is(err, errdefs.ErrNotFound) {
		t.Errorf("DeleteRunner() = %v, want an ErrNotFound", err)
	}
}

func TestDeleteScaleSetRefusesAConfiguredOne(t *testing.T) {
	f := newFixture(t, "rungar-vm/rungar-vm-1")

	_, err := f.m.RemoveScaleSet(context.Background(), "rungar-vm", "")
	if !errors.Is(err, errdefs.ErrInvalidArgument) || !strings.Contains(err.Error(), "configured") {
		t.Errorf("DeleteScaleSet() = %v, want it refused as configured", err)
	}
	if len(f.compute.deleted) != 0 || len(f.github.deletedSetIDs) != 0 {
		t.Error("a configured scale set's runners or scale set were removed")
	}
}

func TestRemoveScaleSetRemovesItsRunnersThenGitHubs(t *testing.T) {
	f := newFixture(t, "gone-vm/gone-vm-1", "gone-vm/gone-vm-2", "rungar-vm/rungar-vm-1")

	d, err := f.m.RemoveScaleSet(context.Background(), "gone-vm", "")
	if err != nil {
		t.Fatalf("DeleteScaleSet() = %v", err)
	}

	if len(d.Removed) != 2 || d.BusyLeft != 0 || d.ScaleSetID != 9 {
		t.Errorf("deletion = %+v, want two runners and then scale set 9", d)
	}
	if slices.Contains(f.compute.deleted, "rungar-vm-1") {
		t.Error("another scale set's runner was removed")
	}
}

// TestDeleteScaleSetThatIsNowhere checks removing a scale set that has no
// runners and that GitHub does not have is not found, as inspecting it is,
// and that removing one a second time is too.
func TestDeleteScaleSetThatIsNowhere(t *testing.T) {
	f := newFixture(t, "gone-vm/gone-vm-1")
	ctx := context.Background()

	if _, err := f.m.RemoveScaleSet(ctx, "nowhere", ""); !errors.Is(err, errdefs.ErrNotFound) {
		t.Errorf("DeleteScaleSet(nowhere) = %v, want an ErrNotFound", err)
	}

	if _, err := f.m.RemoveScaleSet(ctx, "gone-vm", ""); err != nil {
		t.Fatalf("DeleteScaleSet(gone-vm) = %v", err)
	}
	if _, err := f.m.RemoveScaleSet(ctx, "gone-vm", ""); !errors.Is(err, errdefs.ErrNotFound) {
		t.Errorf("DeleteScaleSet(gone-vm) again = %v, want an ErrNotFound", err)
	}
}

// TestDeleteScaleSetLeavesBusyRunners checks the scale set is kept on GitHub
// while a runner of it is running a job, and removed by a later call once it
// has finished.
func TestDeleteScaleSetLeavesBusyRunners(t *testing.T) {
	f := newFixture(t, "gone-vm/gone-vm-1")
	f.github.setBusy("gone-vm-1", true)

	d, err := f.m.RemoveScaleSet(context.Background(), "gone-vm", "")
	if err != nil {
		t.Fatalf("DeleteScaleSet() = %v", err)
	}
	if d.BusyLeft != 1 || d.ScaleSetID != 0 || len(f.github.deletedSetIDs) != 0 {
		t.Fatalf("deletion = %+v, deleted = %v; want the busy runner and the scale set left",
			d, f.github.deletedSetIDs)
	}

	f.github.setBusy("gone-vm-1", false)

	d, err = f.m.RemoveScaleSet(context.Background(), "gone-vm", "")
	if err != nil {
		t.Fatalf("DeleteScaleSet() again = %v", err)
	}
	if len(d.Removed) != 1 || d.ScaleSetID != 9 || !slices.Equal(f.github.deletedSetIDs, []int{9}) {
		t.Errorf("deletion = %+v, deleted = %v; want the runner and then scale set 9 removed",
			d, f.github.deletedSetIDs)
	}
}

// TestSetProviderDisabledOverridesConfiguration checks a provider disabled while the daemon runs is
// said to be so against the configuration, and enabling it again is back to
// what the configuration says.
func TestSetProviderDisabledOverridesConfiguration(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	p, err := f.m.SetProviderDisabled(ctx, "compute1", true)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Snapshot.Disabled || p.ConfiguredDisabled {
		t.Errorf("compute1 = %+v, want it disabled against the configuration", p)
	}

	if p, err = f.m.SetProviderDisabled(ctx, "compute1", false); err != nil || p.Snapshot.Disabled {
		t.Errorf("compute1 = %+v, %v; want it enabled again", p, err)
	}

	if _, err := f.m.SetProviderDisabled(ctx, "nowhere", true); !errors.Is(err, errdefs.ErrNotFound) {
		t.Errorf("SetProviderDisabled() of a provider not configured = %v, want an ErrNotFound", err)
	}
}

func TestProvidersSaysWhatIsOnIt(t *testing.T) {
	f := newFixture(t, "rungar-vm/rungar-vm-1")

	providers, err := f.m.Providers(context.Background(), "compute1")
	if err != nil {
		t.Fatal(err)
	}

	p := providers[0]
	if p.Snapshot.RunnerCount != 1 {
		t.Errorf("compute1 = %+v, want 1 runner on it", p)
	}
	if len(p.ScaleSets) != 1 || p.ScaleSets[0].Name != "rungar-vm" || p.ScaleSets[0].BackoffFor != 0 {
		t.Errorf("compute1's scale sets = %+v, want rungar-vm, tried", p.ScaleSets)
	}
}

// TestReconcileAdoptsANewRunnerAtOnce checks a pass asked for is made at once: a runner that
// appeared on the fleet since the last is adopted.
func TestReconcileAdoptsANewRunnerAtOnce(t *testing.T) {
	f := newFixture(t, "rungar-vm/rungar-vm-1")
	f.adopt(t)

	f.compute.mu.Lock()
	f.compute.machines = append(f.compute.machines, types.Machine{
		Name:   "rungar-vm-2",
		Labels: types.RunnerLabels(testInstallation, "rungar-vm", "rungar-vm-2", ""),
		State:  types.MachineRunning, CreatedAt: time.Now(),
	})
	f.compute.mu.Unlock()
	f.onGitHub["rungar-vm-2"] = answer{Registered: true, Online: true}

	sets, err := f.m.Reconcile(context.Background(), nil)
	if err != nil {
		t.Fatalf("Reconcile() = %v", err)
	}
	if len(sets) != 1 || len(sets[0].Status.Runners) != 2 {
		t.Errorf("sets = %+v; want rungar-vm with both its runners", sets)
	}
}

func TestReconcileRefusesUnknownAndStartingScaleSets(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if _, err := f.m.Reconcile(ctx, []string{"nowhere"}); !errors.Is(err, errdefs.ErrNotFound) {
		t.Errorf("Reconcile() of a scale set not configured = %v, want an ErrNotFound", err)
	}
	if _, err := f.m.Reconcile(ctx, nil); !errors.Is(err, errdefs.ErrUnavailable) {
		t.Errorf("Reconcile() of a scale set still starting = %v, want an ErrUnavailable", err)
	}
}

// TestProviderLimitHoldsAcrossScaleSets checks that New has the fleet count
// every scale set's runners against a provider's max_runners: two scale sets
// sharing a provider that allows 3 get 3 between them, whatever each wants.
func TestProviderLimitHoldsAcrossScaleSets(t *testing.T) {
	compute := &backend{}
	fl := fleet.New([]fleet.Provider{{Name: "compute1", Type: "test", Weight: 1, MaxRunners: 3, Backend: compute}},
		fleet.Options{})

	var specs []types.ScaleSetSpec
	for _, name := range []string{"small", "large"} {
		specs = append(specs, types.ScaleSetSpec{
			Name: name, MaxRunners: 10, StartTimeout: time.Minute, RunnerGroup: types.DefaultRunnerGroup,
			Providers:   []types.ProviderRef{{Name: "compute1"}},
			RunnerSpecs: map[string]types.RunnerSpec{"compute1": runnerSize{VCPUs: 2, MemoryBytes: 4 * gib}},
		})
	}

	m, err := New(Config{
		ScaleSets:         specs,
		Installation:      testInstallation,
		ReconcileInterval: time.Minute,
		Fleet:             fl,
		GitHub:            &fakeGitHub{},
		NewScaleSetClient: func() (*ghscaleset.Client, error) {
			return ghscaleset.NewClientWithPersonalAccessToken(ghscaleset.NewClientWithPersonalAccessTokenConfig{
				GitHubConfigURL: "https://github.com/my-org", PersonalAccessToken: "not-a-token",
			})
		},
		GitHubRunners: noRunners{},
		Logger:        discardLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	for _, s := range m.sets {
		s.github = &fakeGitHub{}
		s.id.Store(1)
	}
	for _, s := range m.sets {
		if _, err := s.HandleDesiredRunnerCount(ctx, 5); err != nil {
			t.Fatalf("%s: HandleDesiredRunnerCount() = %v", s.spec.Name, err)
		}
	}

	total := m.sets[0].count() + m.sets[1].count()
	if total != 3 || len(compute.machines) != 3 {
		t.Errorf("%d runners between the scale sets, %d machines on compute1; want 3 of each, its limit",
			total, len(compute.machines))
	}
}

// noRunners is GitHub's REST API with no runners registered.
type noRunners struct{}

func (noRunners) ListRunners(context.Context) ([]github.Runner, error) { return nil, nil }
