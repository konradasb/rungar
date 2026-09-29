// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/konradasb/rungar/internal/types"
)

// TestReconcileAdoptsAnUnknownMachine checks that a machine carrying this scale
// set's labels but unknown to the scale set is taken over rather than ignored,
// since it holds capacity either way.
func TestReconcileAdoptsAnUnknownMachine(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 16, memoryGiB: 64})
	fleet.put("a", "stray", types.MachineRunning, time.Now())

	s := newTestScaleSet(fleet)

	if err := s.reconcileFleet(context.Background()); err != nil {
		t.Fatalf("reconcileFleet() = %v", err)
	}

	if s.runnerCount() != 1 {
		t.Fatalf("runnerCount() = %d, want the stray adopted", s.runnerCount())
	}
	if got := s.sortedRunners()[0]; got.Name != "stray" || got.Provider != "a" {
		t.Errorf("adopted %+v, want the stray on provider a", got)
	}
}

func TestReconcileRemovesAnUnregisteredRunner(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 16, memoryGiB: 64})
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

	if s.runnerCount() != 0 {
		t.Errorf("runnerCount() = %d, want 0: a machine that never registered is of no use", s.runnerCount())
	}
	if fleet.providerOf(r.Name) != "" {
		t.Error("the unregistered machine was left on the provider")
	}
	if got := removals(t, s); !slices.Equal(got, []types.RemovalReason{types.RemovalUnregistered}) {
		t.Errorf("counted removals %v, want [unregistered]", got)
	}
}

// TestReconcileMarksAnOnlineRunnerIdle checks a runner is idle once GitHub
// says it is connected, and is kept however long it waits for a job.
func TestReconcileMarksAnOnlineRunnerIdle(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 16, memoryGiB: 64})
	s := newTestScaleSet(fleet, statusWith(says(true, true)))
	ctx := context.Background()

	r, err := s.createRunner(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.reconcileFleet(ctx); err != nil {
		t.Fatalf("reconcileFleet() = %v", err)
	}
	if got := s.sortedRunners()[0].State; got != types.RunnerIdle {
		t.Errorf("state = %q, want %q as soon as GitHub says it is online", got, types.RunnerIdle)
	}

	// Long after the start timeout, still waiting for a job.
	s.mu.Lock()
	s.runners[r.Name].CreatedAt = time.Now().Add(-time.Hour)
	s.mu.Unlock()

	if err := s.reconcileFleet(ctx); err != nil {
		t.Fatal(err)
	}
	if s.runnerCount() != 1 {
		t.Errorf("runnerCount() = %d, want the idle runner kept", s.runnerCount())
	}
}

// TestReconcileRemovesARunnerThatNeverConnects is the case this is for: a
// machine that boots into something that is never a runner. It would otherwise
// count towards the scale set, standing in for a runner that could take a job.
func TestReconcileRemovesARunnerThatNeverConnects(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 16, memoryGiB: 64})
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
	if s.runnerCount() != 1 || s.sortedRunners()[0].State != types.RunnerStarting {
		t.Fatal("a runner still booting was removed, or taken for idle")
	}

	s.mu.Lock()
	s.runners[r.Name].CreatedAt = time.Now().Add(-2 * time.Minute)
	s.mu.Unlock()

	if err := s.reconcileFleet(ctx); err != nil {
		t.Fatal(err)
	}
	if s.runnerCount() != 0 || fleet.providerOf(r.Name) != "" {
		t.Error("a runner still offline after the start timeout was kept")
	}
	if got := removals(t, s); !slices.Equal(got, []types.RemovalReason{types.RemovalNeverConnected}) {
		t.Errorf("counted removals %v, want [never_connected]", got)
	}
}

// TestReconcileRemovesARunnerThatStaysDisconnected checks an idle runner that
// loses its connection: kept through a blip, removed once it has been gone
// for the start timeout.
func TestReconcileRemovesARunnerThatStaysDisconnected(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 16, memoryGiB: 64})

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
	if s.runnerCount() != 1 {
		t.Fatal("an idle runner was removed the moment it went offline")
	}

	s.mu.Lock()
	s.offline[r.Name] = time.Now().Add(-2 * time.Minute)
	s.mu.Unlock()

	if err := s.reconcileFleet(ctx); err != nil {
		t.Fatal(err)
	}
	if s.runnerCount() != 0 {
		t.Error("a runner offline for longer than the start timeout was kept")
	}
	if got := removals(t, s); !slices.Equal(got, []types.RemovalReason{types.RemovalDisconnected}) {
		t.Errorf("counted removals %v, want [disconnected]", got)
	}
}

// TestReconcileGivesANewRunnerTimeToBeListed checks a runner too new for
// GitHub's cached listing is not taken for one GitHub has dropped.
func TestReconcileGivesANewRunnerTimeToBeListed(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 16, memoryGiB: 64})
	s := newTestScaleSet(fleet, statusWith(says(false, false)))

	if _, err := s.createRunner(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.reconcileFleet(context.Background()); err != nil {
		t.Fatal(err)
	}

	if s.runnerCount() != 1 {
		t.Error("a runner just created was removed for not being listed yet")
	}
}

// TestReconcileLeavesARunnerAloneWhenGitHubIsSilent checks that GitHub failing
// to answer is not read as "this runner does not exist".
func TestReconcileLeavesARunnerAloneWhenGitHubIsSilent(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 16, memoryGiB: 64})
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

	if s.runnerCount() != 1 {
		t.Errorf("runnerCount() = %d; GitHub not answering says nothing about the runner", s.runnerCount())
	}
}

// TestReconcileLeavesRunnersAloneWhenGitHubIsNotAsked checks the conservative
// default: with no way to ask GitHub, a runner is not written off on the
// timeout alone.
func TestReconcileLeavesRunnersAloneWhenGitHubIsNotAsked(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 16, memoryGiB: 64})
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

	if s.runnerCount() != 1 {
		t.Errorf("runnerCount() = %d, want the runner kept when GitHub is not asked", s.runnerCount())
	}
}

// TestReconcileDoesNotTouchABusyRunner checks that a runner running somebody's
// job is never removed for being old.
func TestReconcileDoesNotTouchABusyRunner(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 16, memoryGiB: 64})
	s := newTestScaleSet(fleet, statusWith(says(false, false)))
	ctx := context.Background()

	r, err := s.createRunner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s.markBusy(r.Name, types.Job{ID: "job-1"})

	s.mu.Lock()
	s.runners[r.Name].CreatedAt = time.Now().Add(-time.Hour)
	s.mu.Unlock()

	if err := s.reconcileFleet(ctx); err != nil {
		t.Fatal(err)
	}

	if s.runnerCount() != 1 {
		t.Error("a busy runner was removed, failing somebody's job")
	}
}

func TestReconcileReportsAnUnreadableFleet(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 16, memoryGiB: 64})
	s := newTestScaleSet(fleet)

	r, err := s.createRunner(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	fleet.listErr = errors.New("provider unreachable")

	if err := s.reconcileFleet(context.Background()); err == nil {
		t.Fatal("reconcileFleet() = nil, want the error")
	}
	if s.runnerCount() != 1 {
		t.Errorf("runnerCount() = %d; a partition is not a reason to forget %q", s.runnerCount(), r.Name)
	}
}

// TestARunnersStateFollowsGitHub checks a runner's state follows GitHub's
// answer: a connected starting runner is idle, one running a job is busy, and
// one not connected is left to reconcile.
func TestARunnersStateFollowsGitHub(t *testing.T) {
	tests := []struct {
		name     string
		state    types.RunnerState
		onGitHub answer
		want     types.RunnerState
	}{
		{
			name:     "starting to idle once connected",
			state:    types.RunnerStarting,
			onGitHub: answer{Registered: true, Online: true},
			want:     types.RunnerIdle,
		},
		{
			name:     "idle to busy once running a job",
			state:    types.RunnerIdle,
			onGitHub: answer{Registered: true, Online: true, Busy: true},
			want:     types.RunnerBusy,
		},
		{
			name:     "busy stays busy until its job completes",
			state:    types.RunnerBusy,
			onGitHub: answer{Registered: true, Online: true},
			want:     types.RunnerBusy,
		},
		{
			name:     "starting stays starting while disconnected",
			state:    types.RunnerStarting,
			onGitHub: answer{Registered: true},
			want:     types.RunnerStarting,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const name = "rungar-vm-1"

			s := newTestScaleSet(newFakeScaleSetProviders(), statusWith(registered(map[string]answer{name: tt.onGitHub})))
			s.runners[name] = &types.Runner{Name: name, State: tt.state}

			if _, err := s.syncFromGitHub(context.Background(), name); err != nil {
				t.Fatal(err)
			}
			if got := s.sortedRunners()[0].State; got != tt.want {
				t.Errorf("state = %q, want %q", got, tt.want)
			}
		})
	}

	t.Run("a runner the scale set does not have is left unknown", func(t *testing.T) {
		s := newTestScaleSet(newFakeScaleSetProviders(), statusWith(says(true, true)))

		if _, err := s.syncFromGitHub(context.Background(), "absent"); err != nil {
			t.Fatal(err)
		}
		if got := s.runnerCount(); got != 0 {
			t.Errorf("runnerCount() = %d, want 0", got)
		}
	})
}

// TestReconcileLeavesARunnerBeingRemovedAlone checks a reconcile pass during a
// runner's removal does not adopt its machine, still being deleted, and delete
// it again.
func TestReconcileLeavesARunnerBeingRemovedAlone(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 8, memoryGiB: 16})
	fleet.deleteGate = newGate()

	s := newTestScaleSet(fleet, statusWith(func(context.Context, string) (answer, error) {
		return answer{}, nil
	}))
	ctx := context.Background()

	r, err := s.createRunner(ctx)
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- s.remove(ctx, r.Name, types.RemovalJobCompleted, false) }()

	// The machine is being deleted: listed still, but no longer the scale
	// set's.
	<-fleet.deleteGate.reached
	if err := s.reconcileFleet(ctx); err != nil {
		t.Fatal(err)
	}
	close(fleet.deleteGate.released)

	if err := <-done; err != nil {
		t.Fatalf("remove() = %v", err)
	}
	if got := len(fleet.deleted); got != 1 {
		t.Errorf("the machine was deleted %d times, want once", got)
	}
	if got := s.runnerCount(); got != 0 {
		t.Errorf("runnerCount() = %d, want 0: the runner being removed was adopted again", got)
	}
}

// TestReconcileAdoptsWhatARemovalLeft checks the other side: a machine whose
// deletion failed is not left alone for good, but adopted, and removed again,
// once the removal is old enough.
func TestReconcileAdoptsWhatARemovalLeft(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 8, memoryGiB: 16})
	fleet.put("a", "rungar-vm-left", types.MachineRunning, time.Now())

	s := newTestScaleSet(fleet)
	s.left = map[string]time.Time{"rungar-vm-left": time.Now().Add(-2 * leftGrace)}

	if err := s.reconcileFleet(context.Background()); err != nil {
		t.Fatal(err)
	}

	if got := s.runnerCount(); got != 1 {
		t.Errorf("runnerCount() = %d, want the machine a failed removal left adopted", got)
	}
}

// TestReconcileLeavesARunnerBeingCreatedAlone checks a reconcile pass does not
// adopt a machine listed while it is still being created, nor remove it for
// GitHub not having its runner yet.
func TestReconcileLeavesARunnerBeingCreatedAlone(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 8, memoryGiB: 16})
	fleet.createGate = newGate()

	s := newTestScaleSet(fleet, statusWith(func(context.Context, string) (answer, error) {
		return answer{}, nil
	}))
	ctx := context.Background()

	done := make(chan error, 1)
	go func() {
		_, err := s.createRunner(ctx)
		done <- err
	}()

	// The machine is listed, but its create has not returned.
	<-fleet.createGate.reached
	if err := s.reconcileFleet(ctx); err != nil {
		t.Fatal(err)
	}
	close(fleet.createGate.released)

	if err := <-done; err != nil {
		t.Fatalf("createRunner() = %v", err)
	}
	if len(fleet.deleted) != 0 {
		t.Errorf("deleted %v: a runner being created was taken for an unknown machine and deleted", fleet.deleted)
	}
	runners := s.sortedRunners()
	if len(runners) != 1 || runners[0].Adopted || runners[0].State != types.RunnerStarting {
		t.Errorf("runners = %+v, want the one created, starting and not adopted", runners)
	}
}

// TestReconcileReplacesOutdatedRunnersOneAtATime checks that runners created
// from an older spec are replaced, one per pass, so that the reserve is never
// emptied at once.
func TestReconcileReplacesOutdatedRunnersOneAtATime(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 16, memoryGiB: 64})
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
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 16, memoryGiB: 64})
	s := newTestScaleSet(fleet, statusWith(says(true, false)))
	ctx := context.Background()

	r, err := s.createRunner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s.markBusy(r.Name, types.Job{ID: "job-1"})

	if err := s.reconcileFleet(ctx); err != nil {
		t.Fatal(err)
	}
	if s.runnerCount() != 1 {
		t.Fatal("a busy runner was removed the moment it went offline")
	}

	s.mu.Lock()
	s.offline[r.Name] = time.Now().Add(-10 * time.Minute)
	s.mu.Unlock()

	if err := s.reconcileFleet(ctx); err != nil {
		t.Fatal(err)
	}
	if s.runnerCount() != 0 || fleet.providerOf(r.Name) != "" {
		t.Error("a busy runner offline for longer than the start timeout was kept")
	}
	if got := removals(t, s); !slices.Equal(got, []types.RemovalReason{types.RemovalStuck}) {
		t.Errorf("counted removals %v, want [%s]", got, types.RemovalStuck)
	}
}

// TestReconcileCarriesOnWithoutAProvider checks that one provider being down
// does not stop the rest of the fleet being reconciled, and does not have its
// runners taken for gone.
func TestReconcileCarriesOnWithoutAProvider(t *testing.T) {
	fleet := newFakeScaleSetProviders(
		providerRoom{name: "a", vcpus: 8, memoryGiB: 16},
		providerRoom{name: "b", vcpus: 8, memoryGiB: 16},
	)
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
	for _, r := range s.sortedRunners() {
		names = append(names, r.Name)
	}
	if !slices.Equal(names, []string{"rungar-vm-on-b"}) {
		t.Errorf("runners = %v, want the vanished one forgotten and the unreachable provider's kept", names)
	}
}

// TestRunnersOnALongLostProviderAreForgotten checks the runners of a provider
// that stays down are forgotten, so that they are replaced elsewhere.
func TestRunnersOnALongLostProviderAreForgotten(t *testing.T) {
	fleet := newFakeScaleSetProviders(
		providerRoom{name: "a", vcpus: 8, memoryGiB: 16},
		providerRoom{name: "b", vcpus: 8, memoryGiB: 16},
	)
	fleet.put("b", "rungar-vm-on-b", types.MachineRunning, time.Now())

	d := &registrationRemovals{}
	s := newTestScaleSet(fleet, removingRegistrationsWith(d))
	if err := s.adopt(context.Background()); err != nil {
		t.Fatal(err)
	}

	fleet.unreachable = map[string]bool{"b": true}

	// Just gone: kept.
	if err := s.reconcileFleet(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.runnerCount() != 1 {
		t.Fatal("a runner was forgotten the moment its provider became unreachable")
	}

	// Gone for longer than the grace: forgotten, so it can be replaced.
	s.mu.Lock()
	s.unreachable["b"] = time.Now().Add(-unreachableGrace - time.Minute)
	s.mu.Unlock()

	if err := s.reconcileFleet(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.runnerCount() != 0 {
		t.Error("a runner on a provider unreachable for longer than the grace is still counted")
	}
	if got := d.got(); !slices.Equal(got, []string{"rungar-vm-on-b"}) {
		t.Errorf("removed registrations %v, want the forgotten runner", got)
	}
}

// TestReconcileMarksBusyWhatGitHubSaysIs checks a runner GitHub reports running
// a job is marked busy, with or without the job's message.
func TestReconcileMarksBusyWhatGitHubSaysIs(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 16, memoryGiB: 64})
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

	if got := s.sortedRunners()[0].State; got != types.RunnerBusy {
		t.Errorf("state = %s, want busy", got)
	}
}

// TestARunnerCreatedDuringAPassIsNotTakenForGone checks a runner created after
// the providers were listed, and so missing from the listing, is not let go of
// as gone.
func TestARunnerCreatedDuringAPassIsNotTakenForGone(t *testing.T) {
	f := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 16, memoryGiB: 64})
	s := newTestScaleSet(f)
	ctx := context.Background()

	if err := s.adopt(ctx); err != nil {
		t.Fatalf("adopt() = %v", err)
	}

	// The pass asks the time first once the providers are listed.
	var armed atomic.Bool
	s.now = func() time.Time {
		if armed.CompareAndSwap(true, false) {
			if _, err := s.createRunner(ctx); err != nil {
				t.Errorf("createRunner() = %v", err)
			}
		}

		return time.Now()
	}

	armed.Store(true)
	if err := s.reconcileFleet(ctx); err != nil {
		t.Fatalf("reconcileFleet() = %v", err)
	}

	if got := s.runnerCount(); got != 1 {
		t.Errorf("runnerCount() = %d, want the runner created during the pass", got)
	}
	if got := f.count(); got != 1 {
		t.Errorf("the fleet has %d machines, want the runner's", got)
	}
}
