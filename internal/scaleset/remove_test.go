// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/types"
)

// deregistrations records which runners' registrations were removed.
type deregistrations struct {
	mu    sync.Mutex
	names []string
}

func (d *deregistrations) remove(_ context.Context, name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.names = append(d.names, name)

	return nil
}

func (d *deregistrations) got() []string {
	d.mu.Lock()
	defer d.mu.Unlock()

	return slices.Clone(d.names)
}

func deregisteringWith(d *deregistrations) func(*fakeGitHub) {
	return func(o *fakeGitHub) { o.removeRunner = d.remove }
}

// TestAFailedStartRemovesItsRegistration checks that GitHub is not left
// listing a runner whose VM never started.
func TestAFailedStartRemovesItsRegistration(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 8, memoryGiB: 16})
	fleet.createErr = errors.New("provider refused")

	d := &deregistrations{}
	s := newTestScaleSet(fleet, deregisteringWith(d))

	if _, err := s.createRunner(context.Background()); err == nil {
		t.Fatal("createRunner() = nil, want the provider's refusal")
	}

	if got := d.got(); len(got) != 1 {
		t.Errorf("deregistered %v, want the runner whose VM did not start", got)
	}
}

func TestRemovingAnIdleRunnerRemovesItsRegistration(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 8, memoryGiB: 16})
	d := &deregistrations{}
	s := newTestScaleSet(fleet, deregisteringWith(d))

	r, err := s.createRunner(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if err := s.removeRunner(context.Background(), r.Name, "job_completed"); err != nil {
		t.Fatal(err)
	}

	if got := d.got(); !slices.Equal(got, []string{r.Name}) {
		t.Errorf("deregistered %v, want %s: it never ran a job, so its registration is left behind", got, r.Name)
	}
}

// TestAFinishedJobLeavesTheRegistrationAlone checks that a runner that ran
// its job is not deregistered: it removed its own registration, and asking
// GitHub again would be a wasted call on every job.
func TestAFinishedJobLeavesTheRegistrationAlone(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 8, memoryGiB: 16})
	d := &deregistrations{}
	s := newTestScaleSet(fleet, deregisteringWith(d))

	r, err := s.createRunner(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	s.jobStarted(r.Name, "job-1")

	if err := s.removeRunner(context.Background(), r.Name, "job_completed"); err != nil {
		t.Fatal(err)
	}

	if got := d.got(); len(got) != 0 {
		t.Errorf("deregistered %v, want nothing after a job", got)
	}
}

// TestAVanishedIdleRunnerRemovesItsRegistration checks the case of a VM that
// died without ever taking a job.
func TestAVanishedIdleRunnerRemovesItsRegistration(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 8, memoryGiB: 16})
	d := &deregistrations{}
	s := newTestScaleSet(fleet, deregisteringWith(d))

	r, err := s.createRunner(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	fleet.mu.Lock()
	fleet.machines["a"] = nil
	fleet.mu.Unlock()

	if err := s.reconcileFleet(context.Background()); err != nil {
		t.Fatal(err)
	}

	if got := d.got(); !slices.Equal(got, []string{r.Name}) {
		t.Errorf("deregistered %v, want %s", got, r.Name)
	}
}

// TestDeregistrationComesBeforeTheVMGoes checks the order that makes removing
// an idle runner safe: once GitHub has no registration for it, no job can be
// given to it while its VM is removed.
func TestDeregistrationComesBeforeTheVMGoes(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 8, memoryGiB: 16})

	var vmPresentAtDeregistration bool
	s := newTestScaleSet(fleet, func(o *fakeGitHub) {
		o.removeRunner = func(_ context.Context, name string) error {
			vmPresentAtDeregistration = fleet.providerOf(name) != ""
			return nil
		}
	})

	r, err := s.createRunner(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.removeRunner(context.Background(), r.Name, "job_completed"); err != nil {
		t.Fatal(err)
	}

	if !vmPresentAtDeregistration {
		t.Error("the VM was removed before the registration: GitHub could have given it a job in between")
	}
	if fleet.providerOf(r.Name) != "" {
		t.Error("the VM was not removed")
	}
}

// TestARunnerGitHubSaysIsBusyIsLeft checks a runner GitHub has given a job is
// not removed before the job's message arrives.
func TestARunnerGitHubSaysIsBusyIsLeft(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 8, memoryGiB: 16})
	s := newTestScaleSet(fleet, func(o *fakeGitHub) {
		o.removeRunner = func(context.Context, string) error { return errdefs.Busy("running a job") }
	})

	r, err := s.createRunner(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if err := s.removeRunner(context.Background(), r.Name, "job_completed"); !errors.Is(err, errdefs.ErrBusy) {
		t.Errorf("removeRunner() = %v, want an ErrBusy", err)
	}
	if fleet.providerOf(r.Name) == "" {
		t.Fatal("the VM of a runner running a job was removed")
	}
	if got := s.list()[0].State; got != types.RunnerBusy {
		t.Errorf("state = %s, want busy: GitHub said so", got)
	}
}

// TestARunnerIsLeftWhenGitHubCannotBeAsked checks that without GitHub's word
// that no job can reach a runner, its VM is not removed.
func TestARunnerIsLeftWhenGitHubCannotBeAsked(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 8, memoryGiB: 16})
	s := newTestScaleSet(fleet, func(o *fakeGitHub) {
		o.removeRunner = func(context.Context, string) error { return errors.New("GitHub timed out") }
	})

	r, err := s.createRunner(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if err := s.removeRunner(context.Background(), r.Name, "job_completed"); err == nil {
		t.Error("removeRunner() = nil, want GitHub's failure")
	}
	if fleet.providerOf(r.Name) == "" || s.count() != 1 {
		t.Error("the runner was removed without GitHub saying no job could reach it")
	}
}

// TestAStoppedVMIsRemovedWhateverGitHubSays checks that a VM with no runner
// left in it is removed even when its registration cannot be.
func TestAStoppedVMIsRemovedWhateverGitHubSays(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 8, memoryGiB: 16})
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

	if fleet.providerOf(r.Name) != "" || s.count() != 0 {
		t.Error("a stopped VM was kept because GitHub could not be asked about its registration")
	}
}

// TestRemoveIdleKeepsTheFreshest checks the order runners are given back in:
// those made from an older spec first, then the oldest.
func TestRemoveIdleKeepsTheFreshest(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 16, memoryGiB: 32})
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
	fleet := newFleet(fakeProvider{name: "a", vcpus: 16, memoryGiB: 32})
	s := newTestScaleSet(fleet, func(o *fakeGitHub) {
		o.removeRunner = func(context.Context, string) error { return errdefs.Busy("running a job") }
	})

	if _, err := s.createRunner(context.Background()); err != nil {
		t.Fatal(err)
	}

	if got := s.removeIdle(context.Background(), 1); got != 0 || s.count() != 1 {
		t.Errorf("removeIdle() = %d with %d left; a runner running a job was removed", got, s.count())
	}
}

// TestRemoveUnlessBusyRefusesBusyAndUnknown checks a runner removed on request
// is deregistered first, and that one running a job or unknown is refused.
func TestRemoveUnlessBusyRefusesBusyAndUnknown(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 8, memoryGiB: 16})

	d := &deregistrations{}
	s := newTestScaleSet(fleet, deregisteringWith(d))
	ctx := context.Background()

	idle, err := s.createRunner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	busy, err := s.createRunner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s.jobStarted(busy.Name, "job-1")

	if err := s.removeUnlessBusy(ctx, busy.Name); !errors.Is(err, errdefs.ErrBusy) {
		t.Errorf("removeUnlessBusy() of a busy runner = %v, want an ErrBusy", err)
	}
	if err := s.removeUnlessBusy(ctx, "nowhere"); !errors.Is(err, errdefs.ErrNotFound) {
		t.Errorf("removeUnlessBusy() of a runner it does not have = %v, want an ErrNotFound", err)
	}

	if err := s.removeUnlessBusy(ctx, idle.Name); err != nil {
		t.Fatalf("removeUnlessBusy() = %v", err)
	}
	if got := d.got(); !slices.Equal(got, []string{idle.Name}) {
		t.Errorf("deregistered %v, want %s alone", got, idle.Name)
	}
	if s.count() != 1 {
		t.Errorf("count() = %d, want the busy runner left", s.count())
	}
	if got := removals(t, s); !slices.Equal(got, []types.RemovalReason{types.RemovalRequested}) {
		t.Errorf("counted removals %v, want [removed]", got)
	}
}
