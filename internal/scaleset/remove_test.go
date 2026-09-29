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

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/types"
)

func TestRemovingAnIdleRunnerRemovesItsRegistration(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 8, memoryGiB: 16})
	d := &registrationRemovals{}
	s := newTestScaleSet(fleet, removingRegistrationsWith(d))

	r, err := s.createRunner(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if err := s.remove(context.Background(), r.Name, types.RemovalJobCompleted, false); err != nil {
		t.Fatal(err)
	}

	if got := d.got(); !slices.Equal(got, []string{r.Name}) {
		t.Errorf("removed registrations %v, want %s: it never ran a job, so its registration is left behind", got, r.Name)
	}
}

// TestAFinishedJobLeavesTheRegistrationAlone checks that a runner that ran its
// job does not have its registration removed: it removed that itself, and
// asking GitHub again would be a wasted call on every job.
func TestAFinishedJobLeavesTheRegistrationAlone(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 8, memoryGiB: 16})
	d := &registrationRemovals{}
	s := newTestScaleSet(fleet, removingRegistrationsWith(d))

	r, err := s.createRunner(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	s.markBusy(r.Name, types.Job{ID: "job-1"})

	if err := s.remove(context.Background(), r.Name, types.RemovalJobCompleted, false); err != nil {
		t.Fatal(err)
	}

	if got := d.got(); len(got) != 0 {
		t.Errorf("removed registrations %v, want nothing after a job", got)
	}
}

// TestAVanishedIdleRunnerRemovesItsRegistration checks the case of a machine
// that died without ever taking a job: once GitHub has had the start timeout to
// say otherwise, the runner is lost, and its registration removed.
func TestAVanishedIdleRunnerRemovesItsRegistration(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 8, memoryGiB: 16})
	d := &registrationRemovals{}
	s := newTestScaleSet(fleet, removingRegistrationsWith(d), statusWith(says(true, false)))
	ctx := context.Background()

	r, err := s.createRunner(ctx)
	if err != nil {
		t.Fatal(err)
	}

	fleet.mu.Lock()
	fleet.machines["a"] = nil
	fleet.mu.Unlock()

	if err := s.reconcileFleet(ctx); err != nil {
		t.Fatal(err)
	}
	if got := d.got(); len(got) != 0 {
		t.Fatalf("removed registrations %v before GitHub had the start timeout to say what became of it", got)
	}

	ageEnded(t, s, r.Name, s.spec.StartTimeout)
	if err := s.reconcileFleet(ctx); err != nil {
		t.Fatal(err)
	}

	if got := d.got(); !slices.Equal(got, []string{r.Name}) {
		t.Errorf("removed registrations %v, want %s", got, r.Name)
	}
}

// TestRegistrationIsRemovedBeforeTheMachineIsDeleted checks the order that
// makes removing an idle runner safe: once GitHub has no registration for it,
// no job can be given to it while its machine is deleted.
func TestRegistrationIsRemovedBeforeTheMachineIsDeleted(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 8, memoryGiB: 16})

	var machinePresentAtRemoval bool
	s := newTestScaleSet(fleet, func(o *fakeGitHub) {
		o.removeRunner = func(_ context.Context, name string) error {
			machinePresentAtRemoval = fleet.providerOf(name) != ""
			return nil
		}
	})

	r, err := s.createRunner(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.remove(context.Background(), r.Name, types.RemovalJobCompleted, false); err != nil {
		t.Fatal(err)
	}

	if !machinePresentAtRemoval {
		t.Error("the machine was deleted before the registration was removed: GitHub could have given it a job in between")
	}
	if fleet.providerOf(r.Name) != "" {
		t.Error("the machine was not deleted")
	}
}

// TestARunnerGitHubSaysIsBusyIsLeft checks a runner GitHub has given a job is
// not removed before the job's message arrives.
func TestARunnerGitHubSaysIsBusyIsLeft(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 8, memoryGiB: 16})
	s := newTestScaleSet(fleet, func(o *fakeGitHub) {
		o.removeRunner = func(context.Context, string) error { return errdefs.Busy("running a job") }
	})

	r, err := s.createRunner(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	err = s.remove(context.Background(), r.Name, types.RemovalJobCompleted, false)
	if !errors.Is(err, errdefs.ErrBusy) {
		t.Errorf("remove() = %v, want an ErrBusy", err)
	}
	if fleet.providerOf(r.Name) == "" {
		t.Fatal("the machine of a runner running a job was deleted")
	}
	if got := s.sortedRunners()[0].State; got != types.RunnerBusy {
		t.Errorf("state = %s, want busy: GitHub said so", got)
	}
}

// TestARunnerIsLeftWhenGitHubCannotBeAsked checks that without GitHub's word
// that no job can reach a runner, its machine is not deleted.
func TestARunnerIsLeftWhenGitHubCannotBeAsked(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 8, memoryGiB: 16})
	s := newTestScaleSet(fleet, func(o *fakeGitHub) {
		o.removeRunner = func(context.Context, string) error { return errors.New("GitHub timed out") }
	})

	r, err := s.createRunner(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if err := s.remove(context.Background(), r.Name, types.RemovalJobCompleted, false); err == nil {
		t.Error("remove() = nil, want GitHub's failure")
	}
	if fleet.providerOf(r.Name) == "" || s.runnerCount() != 1 {
		t.Error("the runner was removed without GitHub saying no job could reach it")
	}
}

// TestAStoppedMachineIsDeletedWhateverGitHubSays checks that a machine with no
// runner left in it is deleted even when its registration cannot be removed.
func TestAStoppedMachineIsDeletedWhateverGitHubSays(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 8, memoryGiB: 16})
	s := newTestScaleSet(fleet, func(o *fakeGitHub) {
		o.removeRunner = func(context.Context, string) error { return errors.New("GitHub timed out") }
	})

	r, err := s.createRunner(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	fleet.mu.Lock()
	for i := range fleet.machines["a"] {
		fleet.machines["a"][i].State = types.MachineStopped
	}
	fleet.mu.Unlock()

	if err := s.reconcileFleet(context.Background()); err != nil {
		t.Fatal(err)
	}

	if fleet.providerOf(r.Name) != "" || s.runnerCount() != 0 {
		t.Error("a stopped machine was kept because GitHub could not be asked about its registration")
	}
}

// TestRemoveIdleKeepsTheFreshest checks the order runners are given back in:
// those created from an older spec first, then the oldest.
func TestRemoveIdleKeepsTheFreshest(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 16, memoryGiB: 32})
	fleet.put("a", "rungar-vm-outdated", types.MachineRunning, time.Now())

	s := newTestScaleSet(fleet)
	s.spec.RunnerRevisions = map[string]string{"a": "current"}
	ctx := context.Background()

	if err := s.adopt(ctx); err != nil {
		t.Fatal(err)
	}
	older, err := s.createRunner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	newer, err := s.createRunner(ctx)
	if err != nil {
		t.Fatal(err)
	}

	s.mu.Lock()
	s.runners[older.Name].CreatedAt = time.Now().Add(-time.Hour)
	s.mu.Unlock()

	for _, gone := range []string{"rungar-vm-outdated", older.Name} {
		if got := s.removeIdle(ctx, 1); got != 1 {
			t.Fatalf("removeIdle() = %d, want 1", got)
		}
		if fleet.providerOf(gone) != "" {
			t.Fatalf("%s was kept over a fresher runner", gone)
		}
	}
	if fleet.providerOf(newer.Name) == "" {
		t.Error("the newest runner was given back")
	}
}

// TestRemoveIdleSkipsWhatGitHubSaysIsBusy checks that a runner GitHub has just
// given a job survives being counted as surplus.
func TestRemoveIdleSkipsWhatGitHubSaysIsBusy(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 16, memoryGiB: 32})
	s := newTestScaleSet(fleet, func(o *fakeGitHub) {
		o.removeRunner = func(context.Context, string) error { return errdefs.Busy("running a job") }
	})

	if _, err := s.createRunner(context.Background()); err != nil {
		t.Fatal(err)
	}

	if got := s.removeIdle(context.Background(), 1); got != 0 || s.runnerCount() != 1 {
		t.Errorf("removeIdle() = %d with %d left; a runner running a job was removed", got, s.runnerCount())
	}
}

// TestRemovingOnRequestRefusesABusyRunner checks a runner removed on request
// has its registration removed first, and that one running a job is refused.
func TestRemovingOnRequestRefusesABusyRunner(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 8, memoryGiB: 16})

	d := &registrationRemovals{}
	s := newTestScaleSet(fleet, removingRegistrationsWith(d))
	ctx := context.Background()

	if err := s.adopt(ctx); err != nil {
		t.Fatalf("adopt() = %v", err)
	}

	idle, err := s.createRunner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	busy, err := s.createRunner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s.markBusy(busy.Name, types.Job{ID: "job-1"})

	if err := s.removeUnlessBusy(ctx, fleet.machine(t, busy.Name)); !errors.Is(err, errdefs.ErrBusy) {
		t.Errorf("removeUnlessBusy() of a busy runner = %v, want an ErrBusy", err)
	}

	if err := s.removeUnlessBusy(ctx, fleet.machine(t, idle.Name)); err != nil {
		t.Fatalf("removeUnlessBusy() = %v", err)
	}
	if got := d.got(); !slices.Equal(got, []string{idle.Name}) {
		t.Errorf("removed registrations %v, want %s alone", got, idle.Name)
	}
	if s.runnerCount() != 1 {
		t.Errorf("runnerCount() = %d, want the busy runner left", s.runnerCount())
	}
	if got := removals(t, s); !slices.Equal(got, []types.RemovalReason{types.RemovalRequested}) {
		t.Errorf("counted removals %v, want [%s]", got, types.RemovalRequested)
	}
}

func TestRemoveDeletesTheRunnersMachine(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 8, memoryGiB: 16})
	s := newTestScaleSet(fleet)
	ctx := context.Background()

	r, err := s.createRunner(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.remove(ctx, r.Name, types.RemovalJobCompleted, false); err != nil {
		t.Fatalf("remove() = %v", err)
	}

	if s.runnerCount() != 0 {
		t.Errorf("runnerCount() = %d, want 0", s.runnerCount())
	}
	if fleet.count() != 0 {
		t.Errorf("the fleet still holds %d machines", fleet.count())
	}
	if len(fleet.deleted) != 1 || fleet.deleted[0].name != r.Name {
		t.Errorf("deleted = %v, want just %q", fleet.deleted, r.Name)
	}
}

// TestRemovingAnUnknownRunnerIsNotAnError checks that a completion GitHub
// reports twice, or for a runner this daemon never had, is not an error.
func TestRemovingAnUnknownRunnerIsNotAnError(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 8, memoryGiB: 16})
	s := newTestScaleSet(fleet)

	if err := s.remove(context.Background(), "never-existed", types.RemovalJobCompleted, false); err != nil {
		t.Errorf("remove() = %v, want nil for a runner that is already gone", err)
	}
	if len(fleet.deleted) != 0 {
		t.Error("a delete was sent for a runner the scale set does not know")
	}
}

// TestARunnerWhoseMachineCannotBeDeletedIsStillForgotten checks that a runner
// whose machine could not be deleted is still forgotten: it is not this scale
// set's any more, and holding it would block the scale set's ceiling forever.
func TestARunnerWhoseMachineCannotBeDeletedIsStillForgotten(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 8, memoryGiB: 16})
	s := newTestScaleSet(fleet)
	ctx := context.Background()

	r, err := s.createRunner(ctx)
	if err != nil {
		t.Fatal(err)
	}

	fleet.deleteErr = errors.New("provider unreachable")

	if err := s.remove(ctx, r.Name, types.RemovalJobCompleted, false); err == nil {
		t.Fatal("remove() = nil, want the provider's error")
	}
	if s.runnerCount() != 0 {
		t.Errorf("runnerCount() = %d, want 0: the runner is no longer this scale set's", s.runnerCount())
	}
}

// TestRemoveRunnerGoesThroughItsScaleSet checks a runner of a running scale
// set is removed through it, so it forgets the runner at once rather than at
// the next reconcile.
func TestRemoveRunnerGoesThroughItsScaleSet(t *testing.T) {
	f := newFixture(t, "rungar-vm/rungar-vm-1")
	s := f.adopt(t)

	r, err := f.m.RemoveRunner(context.Background(), "rungar-vm-1")
	if err != nil {
		t.Fatalf("RemoveRunner() = %v", err)
	}
	if r.Provider != "compute1" {
		t.Errorf("removed runner = %+v, want it on compute1", r)
	}
	if s.runnerCount() != 0 {
		t.Error("the scale set still has the runner")
	}
	if !slices.Equal(f.compute.deleted, []string{"rungar-vm-1"}) {
		t.Errorf("deleted = %v, want rungar-vm-1", f.compute.deleted)
	}
}

// TestRemoveRunnerRefusesOnStandby checks a runner of a scale set whose
// session another daemon holds is left to that daemon.
func TestRemoveRunnerRefusesOnStandby(t *testing.T) {
	f := newFixture(t, "rungar-vm/rungar-vm-1")
	f.m.sets[0].setPhase(types.ScaleSetWaitingForSession)

	_, err := f.m.RemoveRunner(context.Background(), "rungar-vm-1")
	if !errors.Is(err, errdefs.ErrUnavailable) || !strings.Contains(err.Error(), "standby") {
		t.Errorf("RemoveRunner() = %v, want an ErrUnavailable saying the scale set is on standby", err)
	}
	if len(f.compute.deleted) != 0 || len(f.github.removed) != 0 {
		t.Error("the standby removed the runner")
	}
}

// TestRemoveRunnerRefusesABusyOne checks both ways a runner is known to be
// running a job: the daemon was told so, or GitHub refuses to let it go.
func TestRemoveRunnerRefusesABusyOne(t *testing.T) {
	t.Run("the daemon knows", func(t *testing.T) {
		f := newFixture(t, "rungar-vm/rungar-vm-1")
		f.adopt(t).markBusy("rungar-vm-1", types.Job{ID: "job-1"})

		if _, err := f.m.RemoveRunner(context.Background(), "rungar-vm-1"); !errors.Is(err, errdefs.ErrBusy) {
			t.Errorf("RemoveRunner() = %v, want an ErrBusy", err)
		}
		if len(f.compute.deleted) != 0 || len(f.github.removed) != 0 {
			t.Error("a busy runner was removed")
		}
	})

	t.Run("GitHub knows", func(t *testing.T) {
		f := newFixture(t, "gone-vm/gone-vm-1")
		f.github.setBusy("gone-vm-1", true)

		if _, err := f.m.RemoveRunner(context.Background(), "gone-vm-1"); !errors.Is(err, errdefs.ErrBusy) {
			t.Errorf("RemoveRunner() = %v, want an ErrBusy", err)
		}
		if len(f.compute.deleted) != 0 {
			t.Error("a busy runner's machine was deleted")
		}
	})
}

// TestRemoveRunnerRefusesOneBeingCreated checks a runner whose machine is
// listed before its create returns is left to the create, not removed from
// under it.
func TestRemoveRunnerRefusesOneBeingCreated(t *testing.T) {
	f := newFixture(t)
	s := f.adopt(t)
	ctx := context.Background()

	creating := newGate()
	f.compute.mu.Lock()
	f.compute.createGate = creating
	f.compute.mu.Unlock()

	created := make(chan error, 1)
	go func() {
		_, err := s.createRunner(ctx)
		created <- err
	}()
	<-creating.reached

	f.compute.mu.Lock()
	name := f.compute.machines[0].Name
	f.compute.mu.Unlock()

	if _, err := f.m.RemoveRunner(ctx, name); !errors.Is(err, errdefs.ErrUnavailable) {
		t.Errorf("RemoveRunner() of a runner being created = %v, want an ErrUnavailable", err)
	}

	close(creating.released)
	if err := <-created; err != nil {
		t.Fatalf("createRunner() = %v", err)
	}

	if len(f.compute.deleted) != 0 || len(removedRegistrations(f.github)) != 0 {
		t.Error("the runner was removed while being created")
	}
	if s.runnerCount() != 1 {
		t.Errorf("runnerCount() = %d, want the runner created", s.runnerCount())
	}
}

// TestRemoveRunnerAdoptsAnUnknownRunnerFirst checks a runner its scale set has
// yet to adopt is removed by the scale set, which counts the removal once and
// does not adopt the machine again from a listing older than its deletion.
func TestRemoveRunnerAdoptsAnUnknownRunnerFirst(t *testing.T) {
	f := newFixture(t, "rungar-vm/rungar-vm-1")
	s := f.adopt(t)

	f.compute.mu.Lock()
	f.compute.machines = append(f.compute.machines, types.Machine{
		Name:   "rungar-vm-2",
		Labels: types.RunnerLabels(testInstallation, "rungar-vm", "rungar-vm-2", ""),
		State:  types.MachineRunning, CreatedAt: time.Now(),
	})
	f.compute.mu.Unlock()
	f.onGitHub["rungar-vm-2"] = answer{Registered: true, Online: true}

	if _, err := f.m.RemoveRunner(context.Background(), "rungar-vm-2"); err != nil {
		t.Fatalf("RemoveRunner() = %v", err)
	}

	if !slices.Equal(f.compute.deleted, []string{"rungar-vm-2"}) {
		t.Errorf("deleted = %v, want rungar-vm-2", f.compute.deleted)
	}
	if got := removals(t, s); !slices.Equal(got, []types.RemovalReason{types.RemovalRequested}) {
		t.Errorf("the scale set counted removals %v, want [%s]", got, types.RemovalRequested)
	}
	if got := managerRemovals(t, f.m); len(got) != 0 {
		t.Errorf("the Manager counted removals %v, want the scale set to count it", got)
	}

	s.mu.Lock()
	tracked := s.tracks("rungar-vm-2")
	s.mu.Unlock()
	if !tracked {
		t.Error("the scale set does not know the runner has left, and would adopt it again")
	}
	if s.runnerCount() != 1 {
		t.Errorf("runnerCount() = %d, want rungar-vm-1 alone", s.runnerCount())
	}
}

// TestRemoveRunnerThatHasLeftRecordsNothingAgain checks the machine of a runner
// that has just left, still listed, is deleted with nothing recorded again.
func TestRemoveRunnerThatHasLeftRecordsNothingAgain(t *testing.T) {
	f := newFixture(t, "rungar-vm/rungar-vm-1")
	s := f.adopt(t)

	s.mu.Lock()
	delete(s.runners, "rungar-vm-1")
	s.left["rungar-vm-1"] = time.Now()
	s.mu.Unlock()

	if _, err := f.m.RemoveRunner(context.Background(), "rungar-vm-1"); err != nil {
		t.Fatalf("RemoveRunner() = %v", err)
	}

	if !slices.Equal(f.compute.deleted, []string{"rungar-vm-1"}) {
		t.Errorf("deleted = %v, want rungar-vm-1", f.compute.deleted)
	}
	if got := append(removals(t, s), managerRemovals(t, f.m)...); len(got) != 0 {
		t.Errorf("counted removals %v, want none", got)
	}
	if got := recordedBy(t, f.m.events); len(got) != 0 {
		t.Errorf("recorded %v, want nothing", got)
	}
}

func TestRemoveRunnerNotOnTheFleetIsNotFound(t *testing.T) {
	f := newFixture(t)

	if _, err := f.m.RemoveRunner(context.Background(), "nowhere"); !errors.Is(err, errdefs.ErrNotFound) {
		t.Errorf("RemoveRunner() = %v, want an ErrNotFound", err)
	}
}

func TestRemoveScaleSetRefusesAConfiguredOne(t *testing.T) {
	f := newFixture(t, "rungar-vm/rungar-vm-1")

	_, err := f.m.RemoveScaleSet(context.Background(), "rungar-vm", "")
	if !errors.Is(err, errdefs.ErrInvalidArgument) || !strings.Contains(err.Error(), "configured") {
		t.Errorf("RemoveScaleSet() = %v, want it refused as configured", err)
	}
	if len(f.compute.deleted) != 0 || len(f.github.deletedSetIDs) != 0 {
		t.Error("a configured scale set's runners or scale set were removed")
	}
}

func TestRemoveScaleSetRemovesItsRunnersThenGitHubs(t *testing.T) {
	f := newFixture(t, "gone-vm/gone-vm-1", "gone-vm/gone-vm-2", "rungar-vm/rungar-vm-1")

	got, err := f.m.RemoveScaleSet(context.Background(), "gone-vm", "")
	if err != nil {
		t.Fatalf("RemoveScaleSet() = %v", err)
	}

	if len(got.Removed) != 2 || got.BusyLeft != 0 || got.ScaleSetID != 9 {
		t.Errorf("removal = %+v, want two runners and then scale set 9", got)
	}
	if slices.Contains(f.compute.deleted, "rungar-vm-1") {
		t.Error("another scale set's runner was removed")
	}
}

// TestRemoveScaleSetThatIsNowhereIsNotFound checks removing a scale set that
// has no runners and that GitHub does not have is not found, as inspecting it
// is, and that removing one a second time is too.
func TestRemoveScaleSetThatIsNowhereIsNotFound(t *testing.T) {
	f := newFixture(t, "gone-vm/gone-vm-1")
	ctx := context.Background()

	if _, err := f.m.RemoveScaleSet(ctx, "nowhere", ""); !errors.Is(err, errdefs.ErrNotFound) {
		t.Errorf("RemoveScaleSet(nowhere) = %v, want an ErrNotFound", err)
	}

	if _, err := f.m.RemoveScaleSet(ctx, "gone-vm", ""); err != nil {
		t.Fatalf("RemoveScaleSet(gone-vm) = %v", err)
	}
	if _, err := f.m.RemoveScaleSet(ctx, "gone-vm", ""); !errors.Is(err, errdefs.ErrNotFound) {
		t.Errorf("RemoveScaleSet(gone-vm) again = %v, want an ErrNotFound", err)
	}
}

// TestRemoveScaleSetLeavesBusyRunners checks the scale set is kept on GitHub
// while a runner of it is running a job, and removed by a later call once it
// has finished.
func TestRemoveScaleSetLeavesBusyRunners(t *testing.T) {
	f := newFixture(t, "gone-vm/gone-vm-1")
	f.github.setBusy("gone-vm-1", true)

	got, err := f.m.RemoveScaleSet(context.Background(), "gone-vm", "")
	if err != nil {
		t.Fatalf("RemoveScaleSet() = %v", err)
	}
	if got.BusyLeft != 1 || got.ScaleSetID != 0 || len(f.github.deletedSetIDs) != 0 {
		t.Fatalf("removal = %+v, deleted = %v; want the busy runner and the scale set left",
			got, f.github.deletedSetIDs)
	}

	f.github.setBusy("gone-vm-1", false)

	got, err = f.m.RemoveScaleSet(context.Background(), "gone-vm", "")
	if err != nil {
		t.Fatalf("RemoveScaleSet() again = %v", err)
	}
	if len(got.Removed) != 1 || got.ScaleSetID != 9 || !slices.Equal(f.github.deletedSetIDs, []int{9}) {
		t.Errorf("removal = %+v, deleted = %v; want the runner and then scale set 9 removed",
			got, f.github.deletedSetIDs)
	}
}

// TestForgetLeavesTheMachine checks a runner forgotten is no longer counted,
// and its machine left where it is.
func TestForgetLeavesTheMachine(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 8, memoryGiB: 16})
	s := newTestScaleSet(fleet)

	r, err := s.createRunner(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	s.forget(r.Name)

	if got := s.runnerCount(); got != 0 {
		t.Errorf("runnerCount() = %d, want 0", got)
	}
	if fleet.providerOf(r.Name) == "" {
		t.Error("the forgotten runner's machine was deleted")
	}
}

// TestARunnerCanBeRemovedAsItStartsAJob checks that removing runners while
// they start jobs is safe for concurrent use: what a removal knows of a runner
// is read under the scale set's lock.
func TestARunnerCanBeRemovedAsItStartsAJob(t *testing.T) {
	const n = 20

	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 2 * n, memoryGiB: 4 * n})
	set := testScaleSetSpec(fleet)
	set.MaxRunners = n
	s := newScaleSetOver(fleet, &fakeGitHub{}, set, &fakeMetrics{}, discardLogger())
	ctx := context.Background()

	for range n {
		if _, err := s.createRunner(ctx); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	for _, r := range s.sortedRunners() {
		wg.Go(func() { _ = s.remove(ctx, r.Name, types.RemovalJobCompleted, false) })
		wg.Go(func() { s.markBusy(r.Name, types.Job{ID: "job"}) })
	}
	wg.Wait()
}
