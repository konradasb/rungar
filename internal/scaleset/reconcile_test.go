// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/konradasb/rungar/internal/types"
)

// statusWith returns an option setting the status checker.
func statusWith(fn statusFunc) func(*fakeGitHub) {
	return func(o *fakeGitHub) { o.status = fn }
}

// says is a status checker with a fixed answer.
func says(registered, online bool) statusFunc {
	return func(context.Context, string) (answer, error) {
		return answer{Registered: registered, Online: online}, nil
	}
}

func TestAdoptTakesOverRunningVMs(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 16, memoryGiB: 64})
	created := time.Now().Add(-time.Hour)
	fleet.put("a", "rungar-vm-1", types.MachineRunning, created)
	fleet.put("a", "rungar-vm-2", types.MachineStarting, created)

	s := newTestScaleSet(fleet)

	if err := s.adopt(context.Background()); err != nil {
		t.Fatalf("adopt() = %v", err)
	}

	if s.count() != 2 {
		t.Fatalf("adopted %d runners, want 2", s.count())
	}

	for _, r := range s.list() {
		if r.Provider != "a" {
			t.Errorf("runner %s has provider %q; without one it could never be destroyed", r.Name, r.Provider)
		}
		if r.State != types.RunnerIdle {
			t.Errorf("runner %s adopted as %q, want %q", r.Name, r.State, types.RunnerIdle)
		}
		if !r.CreatedAt.Equal(created) {
			t.Errorf("runner %s adopted with CreatedAt %v, want the VM's %v", r.Name, r.CreatedAt, created)
		}
	}
}

// TestAdoptRemovesFinishedRunners checks that a VM left behind by a previous
// daemon, whose job is over, is cleaned up rather than counted.
func TestAdoptRemovesFinishedRunners(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 16, memoryGiB: 64})
	fleet.put("a", "alive", types.MachineRunning, time.Now())
	fleet.put("a", "stopped", types.MachineStopped, time.Now())
	fleet.put("a", "failed", types.MachineStopped, time.Now())

	s := newTestScaleSet(fleet)

	if err := s.adopt(context.Background()); err != nil {
		t.Fatalf("adopt() = %v", err)
	}

	if s.count() != 1 {
		t.Errorf("adopted %d runners, want only the running one", s.count())
	}
	if fleet.providerOf("stopped") != "" || fleet.providerOf("failed") != "" {
		t.Error("a finished runner's VM was left on the fleet")
	}
	if fleet.providerOf("alive") == "" {
		t.Error("a running runner's VM was destroyed")
	}
}

// TestAdoptReplacesWhatWasKnown checks that adoption is a fresh picture, not
// a merge: the fleet is the record.
func TestAdoptReplacesWhatWasKnown(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 16, memoryGiB: 64})
	fleet.put("a", "on-the-fleet", types.MachineRunning, time.Now())

	s := newTestScaleSet(fleet)
	s.runners["stale"] = &types.Runner{Name: "stale", Provider: "a"}

	if err := s.adopt(context.Background()); err != nil {
		t.Fatal(err)
	}

	if s.count() != 1 {
		t.Fatalf("count() = %d, want 1", s.count())
	}
	if s.list()[0].Name != "on-the-fleet" {
		t.Errorf("kept %q, want only what the fleet has", s.list()[0].Name)
	}
}

func TestAdoptFailsWhenTheFleetCannotBeRead(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 16, memoryGiB: 64})
	fleet.listErr = errors.New("provider unreachable")

	s := newTestScaleSet(fleet)

	if err := s.adopt(context.Background()); err == nil {
		t.Fatal("adopt() = nil; an incomplete picture would double the fleet's runners")
	}
}

func TestReconcileForgetsAVanishedVM(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 16, memoryGiB: 64})
	s := newTestScaleSet(fleet)
	ctx := context.Background()

	r, err := s.createRunner(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Somebody removes the VM without telling Rungar.
	if err := fleet.Delete(ctx, r.Provider, r.Name); err != nil {
		t.Fatal(err)
	}

	if err := s.reconcileFleet(ctx); err != nil {
		t.Fatalf("reconcileFleet() = %v", err)
	}

	if s.count() != 0 {
		t.Errorf("count() = %d, want 0: the VM is gone", s.count())
	}
}

func TestReconcileRemovesAStoppedVM(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 16, memoryGiB: 64})
	s := newTestScaleSet(fleet)
	ctx := context.Background()

	r, err := s.createRunner(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// The guest exited but the VM was not removed.
	fleet.mu.Lock()
	for i := range fleet.machines["a"] {
		fleet.machines["a"][i].State = types.MachineStopped
	}
	fleet.mu.Unlock()

	if err := s.reconcileFleet(ctx); err != nil {
		t.Fatalf("reconcileFleet() = %v", err)
	}

	if s.count() != 0 {
		t.Errorf("count() = %d, want 0", s.count())
	}
	if fleet.providerOf(r.Name) != "" {
		t.Error("the stopped VM was left on the provider, holding its capacity")
	}
	if got := removals(t, s); !slices.Equal(got, []types.RemovalReason{types.RemovalStopped}) {
		t.Errorf("counted removals %v, want [stopped]", got)
	}
}

// TestReconcileAdoptsAnUnknownVM checks that a VM carrying this scale set's
// labels but unknown to the manager is taken over rather than ignored, since
// it holds capacity either way.
func TestReconcileAdoptsAnUnknownVM(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 16, memoryGiB: 64})
	fleet.put("a", "stray", types.MachineRunning, time.Now())

	s := newTestScaleSet(fleet)

	if err := s.reconcileFleet(context.Background()); err != nil {
		t.Fatalf("reconcileFleet() = %v", err)
	}

	if s.count() != 1 {
		t.Fatalf("count() = %d, want the stray adopted", s.count())
	}
	if got := s.list()[0]; got.Name != "stray" || got.Provider != "a" {
		t.Errorf("adopted %+v, want the stray on provider a", got)
	}
}

func TestReconcileReapsAnUnregisteredRunner(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 16, memoryGiB: 64})
	s := newTestScaleSet(fleet, statusWith(says(false, false)))
	ctx := context.Background()

	r, err := s.createRunner(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Older than the start timeout, and GitHub has never heard of it.
	s.mu.Lock()
	s.runners[r.Name].CreatedAt = time.Now().Add(-2 * time.Minute)
	s.mu.Unlock()

	if err := s.reconcileFleet(ctx); err != nil {
		t.Fatalf("reconcileFleet() = %v", err)
	}

	if s.count() != 0 {
		t.Errorf("count() = %d, want 0: a VM that never registered is of no use", s.count())
	}
	if fleet.providerOf(r.Name) != "" {
		t.Error("the unregistered VM was left on the provider")
	}
	if got := removals(t, s); !slices.Equal(got, []types.RemovalReason{types.RemovalUnregistered}) {
		t.Errorf("counted removals %v, want [unregistered]", got)
	}
}

// TestReconcileMarksAnOnlineRunnerIdle checks a runner is idle once GitHub
// says it is connected, and is kept however long it waits for a job.
func TestReconcileMarksAnOnlineRunnerIdle(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 16, memoryGiB: 64})
	s := newTestScaleSet(fleet, statusWith(says(true, true)))
	ctx := context.Background()

	r, err := s.createRunner(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.reconcileFleet(ctx); err != nil {
		t.Fatalf("reconcileFleet() = %v", err)
	}
	if got := s.list()[0].State; got != types.RunnerIdle {
		t.Errorf("state = %q, want %q as soon as GitHub says it is online", got, types.RunnerIdle)
	}

	// Long after the start timeout, still waiting for a job.
	s.mu.Lock()
	s.runners[r.Name].CreatedAt = time.Now().Add(-time.Hour)
	s.mu.Unlock()

	if err := s.reconcileFleet(ctx); err != nil {
		t.Fatal(err)
	}
	if s.count() != 1 {
		t.Errorf("count() = %d, want the idle runner kept", s.count())
	}
}

// TestReconcileReapsARunnerThatNeverConnects is the case this is for: a VM
// that boots into something that is never a runner. It would otherwise count
// towards the scale set, standing in for a runner that could take a job.
func TestReconcileReapsARunnerThatNeverConnects(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 16, memoryGiB: 64})
	s := newTestScaleSet(fleet, statusWith(says(true, false)))
	ctx := context.Background()

	r, err := s.createRunner(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Still within the start timeout: it may yet connect.
	if err := s.reconcileFleet(ctx); err != nil {
		t.Fatal(err)
	}
	if s.count() != 1 || s.list()[0].State != types.RunnerStarting {
		t.Fatal("a runner still booting was reaped, or taken for idle")
	}

	s.mu.Lock()
	s.runners[r.Name].CreatedAt = time.Now().Add(-2 * time.Minute)
	s.mu.Unlock()

	if err := s.reconcileFleet(ctx); err != nil {
		t.Fatal(err)
	}
	if s.count() != 0 || fleet.providerOf(r.Name) != "" {
		t.Error("a runner still offline after the start timeout was kept")
	}
	if got := removals(t, s); !slices.Equal(got, []types.RemovalReason{types.RemovalNeverConnected}) {
		t.Errorf("counted removals %v, want [never_connected]", got)
	}
}

// TestReconcileReapsARunnerThatStaysDisconnected checks an idle runner that
// loses its connection: kept through a blip, removed once it has been gone
// for the start timeout.
func TestReconcileReapsARunnerThatStaysDisconnected(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 16, memoryGiB: 64})

	online := true
	s := newTestScaleSet(fleet, statusWith(func(context.Context, string) (answer, error) {
		return answer{Registered: true, Online: online}, nil
	}))
	ctx := context.Background()

	r, err := s.createRunner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.reconcileFleet(ctx); err != nil {
		t.Fatal(err)
	}

	online = false
	if err := s.reconcileFleet(ctx); err != nil {
		t.Fatal(err)
	}
	if s.count() != 1 {
		t.Fatal("an idle runner was reaped the moment it went offline")
	}

	s.mu.Lock()
	s.offline[r.Name] = time.Now().Add(-2 * time.Minute)
	s.mu.Unlock()

	if err := s.reconcileFleet(ctx); err != nil {
		t.Fatal(err)
	}
	if s.count() != 0 {
		t.Error("a runner offline for longer than the start timeout was kept")
	}
	if got := removals(t, s); !slices.Equal(got, []types.RemovalReason{types.RemovalDisconnected}) {
		t.Errorf("counted removals %v, want [disconnected]", got)
	}
}

// TestReconcileGivesANewRunnerTimeToBeListed checks a runner too new for
// GitHub's cached listing is not taken for one GitHub has dropped.
func TestReconcileGivesANewRunnerTimeToBeListed(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 16, memoryGiB: 64})
	s := newTestScaleSet(fleet, statusWith(says(false, false)))

	if _, err := s.createRunner(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.reconcileFleet(context.Background()); err != nil {
		t.Fatal(err)
	}

	if s.count() != 1 {
		t.Error("a runner just made was reaped for not being listed yet")
	}
}

// TestReconcileLeavesARunnerAloneWhenGitHubIsSilent checks that GitHub failing
// to answer is not read as "this runner does not exist".
func TestReconcileLeavesARunnerAloneWhenGitHubIsSilent(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 16, memoryGiB: 64})
	s := newTestScaleSet(fleet, statusWith(
		func(context.Context, string) (answer, error) { return answer{}, errors.New("timeout") }))
	ctx := context.Background()

	r, err := s.createRunner(ctx)
	if err != nil {
		t.Fatal(err)
	}

	s.mu.Lock()
	s.runners[r.Name].CreatedAt = time.Now().Add(-2 * time.Minute)
	s.mu.Unlock()

	if err := s.reconcileFleet(ctx); err != nil {
		t.Fatalf("reconcileFleet() = %v", err)
	}

	if s.count() != 1 {
		t.Errorf("count() = %d; GitHub not answering says nothing about the runner", s.count())
	}
}

// TestReconcileLeavesRunnersAloneWhenGitHubIsNotAsked checks the conservative
// default: with no way to ask GitHub, a runner is not written off on the
// timeout alone.
func TestReconcileLeavesRunnersAloneWhenGitHubIsNotAsked(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 16, memoryGiB: 64})
	s := newTestScaleSet(fleet)
	ctx := context.Background()

	r, err := s.createRunner(ctx)
	if err != nil {
		t.Fatal(err)
	}

	s.mu.Lock()
	s.runners[r.Name].CreatedAt = time.Now().Add(-2 * time.Minute)
	s.mu.Unlock()

	if err := s.reconcileFleet(ctx); err != nil {
		t.Fatal(err)
	}

	if s.count() != 1 {
		t.Errorf("count() = %d, want the runner kept when there is no checker", s.count())
	}
}

// TestReconcileDoesNotTouchABusyRunner checks that a runner running somebody's
// job is never reaped for being old.
func TestReconcileDoesNotTouchABusyRunner(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 16, memoryGiB: 64})
	s := newTestScaleSet(fleet, statusWith(says(false, false)))
	ctx := context.Background()

	r, err := s.createRunner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s.jobStarted(r.Name, "job-1")

	s.mu.Lock()
	s.runners[r.Name].CreatedAt = time.Now().Add(-time.Hour)
	s.mu.Unlock()

	if err := s.reconcileFleet(ctx); err != nil {
		t.Fatal(err)
	}

	if s.count() != 1 {
		t.Error("a busy runner was reaped, failing somebody's job")
	}
}

func TestReconcileReportsAnUnreadableFleet(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 16, memoryGiB: 64})
	s := newTestScaleSet(fleet)

	r, err := s.createRunner(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	fleet.listErr = errors.New("provider unreachable")

	if err := s.reconcileFleet(context.Background()); err == nil {
		t.Fatal("reconcileFleet() = nil, want the error")
	}
	if s.count() != 1 {
		t.Errorf("count() = %d; a partition is not a reason to forget %q", s.count(), r.Name)
	}
}

// TestSyncFromGitHub checks a runner's state follows GitHub's answer: a
// connected starting runner is idle, one running a job is busy, and one not
// connected is left to reconcile.
func TestSyncFromGitHub(t *testing.T) {
	s := newTestScaleSet(newFleet(), statusWith(registered(map[string]answer{
		"starting": {Registered: true, Online: true},
		"idle":     {Registered: true, Online: true, Busy: true},
		"busy":     {Registered: true, Online: true},
		"offline":  {Registered: true},
	})))
	for name, state := range map[string]types.RunnerState{
		"starting": types.RunnerStarting,
		"idle":     types.RunnerIdle,
		"busy":     types.RunnerBusy,
		"offline":  types.RunnerStarting,
	} {
		s.runners[name] = &types.Runner{Name: name, State: state}
	}

	for _, name := range []string{"starting", "idle", "busy", "offline", "absent"} {
		if _, err := s.syncFromGitHub(context.Background(), name); err != nil {
			t.Fatal(err)
		}
	}

	want := map[string]types.RunnerState{
		"starting": types.RunnerIdle,
		"idle":     types.RunnerBusy,
		"busy":     types.RunnerBusy,
		"offline":  types.RunnerStarting,
	}
	for _, r := range s.list() {
		if r.State != want[r.Name] {
			t.Errorf("%s = %q, want %q", r.Name, r.State, want[r.Name])
		}
	}
}

// TestReconcileLeavesARunnerBeingRemovedAlone checks a reconcile pass during a
// runner's removal does not adopt its machine, still being deleted, and delete
// it again.
func TestReconcileLeavesARunnerBeingRemovedAlone(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 8, memoryGiB: 16})
	fleet.deleteDelay = 200 * time.Millisecond

	s := newTestScaleSet(fleet, statusWith(func(context.Context, string) (answer, error) {
		return answer{}, nil
	}))
	ctx := context.Background()

	r, err := s.createRunner(ctx)
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- s.removeRunner(ctx, r.Name, "job_completed") }()

	// The machine is being deleted: listed still, but no longer the scale
	// set's.
	time.Sleep(50 * time.Millisecond)
	if err := s.reconcileFleet(ctx); err != nil {
		t.Fatal(err)
	}

	if err := <-done; err != nil {
		t.Fatalf("removeRunner() = %v", err)
	}
	if got := len(fleet.deleted); got != 1 {
		t.Errorf("the machine was deleted %d times, want once", got)
	}
	if got := s.count(); got != 0 {
		t.Errorf("count() = %d, want 0: the runner being removed was adopted again", got)
	}
}

// TestReconcileAdoptsWhatARemovalLeft checks the other side: a machine whose
// deletion failed is not left alone for good, but adopted, and removed again,
// once the removal is old enough.
func TestReconcileAdoptsWhatARemovalLeft(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 8, memoryGiB: 16})
	fleet.put("a", "rungar-vm-left", types.MachineRunning, time.Now())

	s := newTestScaleSet(fleet)
	s.removed = map[string]time.Time{"rungar-vm-left": time.Now().Add(-2 * removalGrace)}

	if err := s.reconcileFleet(context.Background()); err != nil {
		t.Fatal(err)
	}

	if got := s.count(); got != 1 {
		t.Errorf("count() = %d, want the machine a failed removal left adopted", got)
	}
}

// TestReconcileLeavesARunnerBeingMadeAlone checks a reconcile pass does not
// adopt a machine listed while it is still being made, nor remove it for GitHub
// not having its runner yet.
func TestReconcileLeavesARunnerBeingMadeAlone(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 8, memoryGiB: 16})
	fleet.createDelay, fleet.listedEarly = 200*time.Millisecond, true

	s := newTestScaleSet(fleet, statusWith(func(context.Context, string) (answer, error) {
		return answer{}, nil
	}))
	ctx := context.Background()

	done := make(chan error, 1)
	go func() {
		_, err := s.createRunner(ctx)
		done <- err
	}()

	time.Sleep(50 * time.Millisecond)
	if err := s.reconcileFleet(ctx); err != nil {
		t.Fatal(err)
	}

	if err := <-done; err != nil {
		t.Fatalf("createRunner() = %v", err)
	}
	if len(fleet.deleted) != 0 {
		t.Errorf("deleted %v: a runner being made was taken for a stranger and removed", fleet.deleted)
	}
	if runners := s.list(); len(runners) != 1 || runners[0].Adopted || runners[0].State != types.RunnerStarting {
		t.Errorf("runners = %+v, want the one made, starting and not adopted", runners)
	}
}

// TestReconcileReplacesOutdatedRunnersOneAtATime checks that runners made from
// an older spec are replaced, one per pass, so that the reserve is never
// emptied at once.
func TestReconcileReplacesOutdatedRunnersOneAtATime(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 16, memoryGiB: 64})
	fleet.put("a", "rungar-vm-1", types.MachineRunning, time.Now())
	fleet.put("a", "rungar-vm-2", types.MachineRunning, time.Now())

	s := newTestScaleSet(fleet, statusWith(says(true, true)))
	s.spec.RunnerRevisions = map[string]string{"a": "current"}
	ctx := context.Background()

	if err := s.adopt(ctx); err != nil {
		t.Fatal(err)
	}

	outdated := types.RemovalOutdated
	for pass, want := range [][]types.RemovalReason{{outdated}, {outdated, outdated}, {outdated, outdated}} {
		if err := s.reconcileFleet(ctx); err != nil {
			t.Fatal(err)
		}
		if got := removals(t, s); !slices.Equal(got, want) {
			t.Fatalf("after pass %d, counted removals %v, want %v", pass+1, got, want)
		}
	}
}

// TestReconcileRemovesAStuckRunner checks a runner GitHub has had disconnected
// for longer than the start timeout is removed, even while running a job.
func TestReconcileRemovesAStuckRunner(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 16, memoryGiB: 64})
	s := newTestScaleSet(fleet, statusWith(says(true, false)))
	ctx := context.Background()

	r, err := s.createRunner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s.jobStarted(r.Name, "job-1")

	if err := s.reconcileFleet(ctx); err != nil {
		t.Fatal(err)
	}
	if s.count() != 1 {
		t.Fatal("a busy runner was removed the moment it went offline")
	}

	s.mu.Lock()
	s.offline[r.Name] = time.Now().Add(-10 * time.Minute)
	s.mu.Unlock()

	if err := s.reconcileFleet(ctx); err != nil {
		t.Fatal(err)
	}
	if s.count() != 0 || fleet.providerOf(r.Name) != "" {
		t.Error("a busy runner offline for longer than the start timeout was kept")
	}
	if got := removals(t, s); !slices.Equal(got, []types.RemovalReason{types.RemovalStuck}) {
		t.Errorf("counted removals %v, want [%s]", got, types.RemovalStuck)
	}
}

// TestReconcileCarriesOnWithoutAProvider checks that one provider being down does not
// stop the rest of the fleet being reconciled, and does not have its runners
// taken for gone.
func TestReconcileCarriesOnWithoutAProvider(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 8, memoryGiB: 16}, fakeProvider{name: "b", vcpus: 8, memoryGiB: 16})
	fleet.put("a", "rungar-vm-vanishing", types.MachineRunning, time.Now())
	fleet.put("b", "rungar-vm-on-b", types.MachineRunning, time.Now())

	s := newTestScaleSet(fleet)
	if err := s.adopt(context.Background()); err != nil {
		t.Fatal(err)
	}

	fleet.mu.Lock()
	fleet.machines["a"] = nil
	fleet.mu.Unlock()
	fleet.unreachable = map[string]bool{"b": true}

	if err := s.reconcileFleet(context.Background()); err != nil {
		t.Fatalf("reconcileFleet() = %v", err)
	}

	names := []string{}
	for _, r := range s.list() {
		names = append(names, r.Name)
	}
	if !slices.Equal(names, []string{"rungar-vm-on-b"}) {
		t.Errorf("runners = %v, want the vanished one forgotten and the unreachable provider's kept", names)
	}
}

// TestRunnersOnALongLostProviderAreForgotten checks the runners of a provider
// that stays down are forgotten, so that they are replaced elsewhere.
func TestRunnersOnALongLostProviderAreForgotten(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 8, memoryGiB: 16}, fakeProvider{name: "b", vcpus: 8, memoryGiB: 16})
	fleet.put("b", "rungar-vm-on-b", types.MachineRunning, time.Now())

	d := &deregistrations{}
	s := newTestScaleSet(fleet, deregisteringWith(d))
	if err := s.adopt(context.Background()); err != nil {
		t.Fatal(err)
	}

	fleet.unreachable = map[string]bool{"b": true}

	// Just gone: kept.
	if err := s.reconcileFleet(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.count() != 1 {
		t.Fatal("a runner was forgotten the moment its provider stopped answering")
	}

	// Gone for longer than the grace: forgotten, so it can be replaced.
	s.mu.Lock()
	s.unreachable["b"] = time.Now().Add(-unreachableGrace - time.Minute)
	s.mu.Unlock()

	if err := s.reconcileFleet(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.count() != 0 {
		t.Error("a runner on a provider that has not answered for longer than the grace is still counted")
	}
	if got := d.got(); !slices.Equal(got, []string{"rungar-vm-on-b"}) {
		t.Errorf("deregistered %v, want the forgotten runner", got)
	}
}

// TestReconcileMarksBusyWhatGitHubSaysIs checks a runner GitHub reports running
// a job is marked busy, with or without the job's message.
func TestReconcileMarksBusyWhatGitHubSaysIs(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 16, memoryGiB: 64})
	fleet.put("a", "rungar-vm-1", types.MachineRunning, time.Now().Add(-time.Hour))

	busy := false
	s := newTestScaleSet(fleet, statusWith(func(context.Context, string) (answer, error) {
		return answer{Registered: true, Online: true, Busy: busy}, nil
	}))
	if err := s.adopt(context.Background()); err != nil {
		t.Fatal(err)
	}

	busy = true
	if err := s.reconcileFleet(context.Background()); err != nil {
		t.Fatal(err)
	}

	if got := s.list()[0].State; got != types.RunnerBusy {
		t.Errorf("state = %s, want busy", got)
	}
}
