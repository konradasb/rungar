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

	ghscaleset "github.com/actions/scaleset"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/events"
	"github.com/konradasb/rungar/internal/types"
)

// orphan is a runner name the tests' scale set could have created.
const orphan = "rungar-vm-0badf00d"

// newAdoptedScaleSet returns an adopted scale set over an empty fleet, with
// GitHub listing offline as disconnected.
func newAdoptedScaleSet(t *testing.T, offline ...string) (*scaleSet, *fakeScaleSetProviders, *fakeGitHub) {
	t.Helper()

	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 8, memoryGiB: 16})
	gh := &fakeGitHub{offline: offline}
	s := newScaleSetOver(fleet, gh, testScaleSetSpec(fleet), &fakeMetrics{}, discardLogger())

	if err := s.adopt(context.Background()); err != nil {
		t.Fatal(err)
	}

	return s, fleet, gh
}

// TestRemoveOrphansWaitsForTheStartTimeout checks an orphan is removed, and
// counted, only once offline for longer than the start timeout.
func TestRemoveOrphansWaitsForTheStartTimeout(t *testing.T) {
	s, _, gh := newAdoptedScaleSet(t, orphan)
	now := time.Now()

	s.removeOrphans(context.Background(), now)
	s.removeOrphans(context.Background(), now.Add(s.spec.StartTimeout))

	if got := removedRegistrations(gh); len(got) != 0 {
		t.Fatalf("removed %v within the start timeout; it may be a runner still starting", got)
	}

	s.removeOrphans(context.Background(), now.Add(s.spec.StartTimeout+time.Second))

	if got := removedRegistrations(gh); !slices.Equal(got, []string{orphan}) {
		t.Errorf("removed %v, want %s", got, orphan)
	}
	if got := removals(t, s); !slices.Equal(got, []types.RemovalReason{types.RemovalOrphaned}) {
		t.Errorf("counted removals %v, want [orphaned]", got)
	}

	recorded := recordedBy(t, s.events)
	last := recorded[len(recorded)-1]
	if last.Action != events.ActionRemoved || last.Name != orphan ||
		last.Attributes["reason"] != string(types.RemovalOrphaned) {
		t.Errorf("last event = %+v, want the orphan's removal", last)
	}
	if len(s.orphans) != 0 {
		t.Errorf("orphans = %v, want the removed registration forgotten", s.orphans)
	}
}

// TestRemoveOrphansLeavesWhatIsNotAnOrphan checks known, starting, foreign
// and unrecognised registrations are left.
func TestRemoveOrphansLeavesWhatIsNotAnOrphan(t *testing.T) {
	const (
		known        = "rungar-vm-00000001"
		creating     = "rungar-vm-00000002"
		foreign      = "rungar-vm-00000003"
		unrecognised = "build-box-1"
	)

	s, fleet, gh := newAdoptedScaleSet(t, known, creating, foreign, unrecognised)
	gh.registrations = map[string]int{known: 1, creating: 1, foreign: 2, unrecognised: 1}

	fleet.put("a", known, types.MachineRunning, time.Now())
	if err := s.reconcileFleet(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.creating[creating] = true
	s.mu.Unlock()

	now := time.Now()
	s.removeOrphans(context.Background(), now)
	s.removeOrphans(context.Background(), now.Add(time.Hour))

	if got := removedRegistrations(gh); len(got) != 0 {
		t.Errorf("removed %v, want nothing", got)
	}
}

// TestRemoveOrphansWaitsForEveryProvider checks nothing is removed while a
// provider cannot be listed, until it is written off.
func TestRemoveOrphansWaitsForEveryProvider(t *testing.T) {
	s, fleet, gh := newAdoptedScaleSet(t, orphan)
	fleet.unreachable = map[string]bool{"b": true}
	fleet.put("b", "rungar-vm-00000001", types.MachineRunning, time.Now())

	s.mu.Lock()
	s.orphans[orphan] = time.Now().Add(-time.Hour)
	s.mu.Unlock()

	if err := s.reconcileFleet(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := removedRegistrations(gh); len(got) != 0 {
		t.Fatalf("removed %v while provider b could not be listed", got)
	}

	s.mu.Lock()
	s.unreachable["b"] = time.Now().Add(-unreachableGrace - time.Second)
	s.mu.Unlock()

	if err := s.reconcileFleet(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := removedRegistrations(gh); !slices.Equal(got, []string{orphan}) {
		t.Errorf("removed %v once provider b was written off, want %s", got, orphan)
	}
}

// TestRemoveOrphansRestartsTheTimeoutOnReconnect checks the start timeout
// counts from when a registration was last found offline.
func TestRemoveOrphansRestartsTheTimeoutOnReconnect(t *testing.T) {
	s, _, gh := newAdoptedScaleSet(t, orphan)
	now := time.Now()

	s.removeOrphans(context.Background(), now)

	gh.mu.Lock()
	gh.offline = nil
	gh.mu.Unlock()
	s.removeOrphans(context.Background(), now.Add(time.Second))

	gh.mu.Lock()
	gh.offline = []string{orphan}
	gh.mu.Unlock()
	s.removeOrphans(context.Background(), now.Add(2*time.Second))
	s.removeOrphans(context.Background(), now.Add(s.spec.StartTimeout+time.Second))

	if got := removedRegistrations(gh); len(got) != 0 {
		t.Errorf("removed %v, want the start timeout counted again", got)
	}
}

func TestRemoveOrphanRefusesAKnownRunner(t *testing.T) {
	s, fleet, gh := newAdoptedScaleSet(t)
	fleet.put("a", orphan, types.MachineRunning, time.Now())
	if err := s.reconcileFleet(context.Background()); err != nil {
		t.Fatal(err)
	}

	ref := &ghscaleset.RunnerReference{ID: 0, Name: orphan, RunnerScaleSetID: 1}
	gh.runnerIDs = []string{orphan}

	err := s.removeOrphan(context.Background(), ref, types.RemovalRequested)
	if !errors.Is(err, errdefs.ErrUnavailable) {
		t.Errorf("removeOrphan() = %v, want an ErrUnavailable", err)
	}
	if got := removedRegistrations(gh); len(got) != 0 {
		t.Errorf("removed %v, want nothing", got)
	}
}

func TestRemoveRunnerRemovesARegistrationWithNoMachine(t *testing.T) {
	f := newFixture(t)
	f.adopt(t)
	f.onGitHub[orphan] = answer{Registered: true}

	r, err := f.m.RemoveRunner(context.Background(), orphan)
	if err != nil {
		t.Fatalf("RemoveRunner() = %v", err)
	}
	if r.ScaleSet != "rungar-vm" || r.Provider != "" {
		t.Errorf("removed runner = %+v, want one of rungar-vm with no provider", r)
	}
	if got := removedRegistrations(f.github); !slices.Equal(got, []string{orphan}) {
		t.Errorf("removed registrations %v, want %s", got, orphan)
	}
	if len(f.compute.deleted) != 0 {
		t.Errorf("deleted machines %v, want none", f.compute.deleted)
	}
}

// TestRemoveRunnerRefusesAnUnsafeOrphan checks rm leaves a registration that
// is connected, running a job, or of another scale set.
func TestRemoveRunnerRefusesAnUnsafeOrphan(t *testing.T) {
	tests := []struct {
		name     string
		onGitHub answer
		setID    int
		want     error
	}{
		{"connected", answer{Registered: true, Online: true}, 1, errdefs.ErrInvalidArgument},
		{"running a job", answer{Registered: true, Online: true, Busy: true}, 1, errdefs.ErrBusy},
		{"another scale set's", answer{Registered: true}, 99, errdefs.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.adopt(t)
			f.onGitHub[orphan] = tt.onGitHub
			f.github.registrations = map[string]int{orphan: tt.setID}

			if _, err := f.m.RemoveRunner(context.Background(), orphan); !errors.Is(err, tt.want) {
				t.Errorf("RemoveRunner() = %v, want %v", err, tt.want)
			}
			if got := removedRegistrations(f.github); len(got) != 0 {
				t.Errorf("removed registrations %v, want none", got)
			}
		})
	}
}

// TestForgetRunnersWaitsForAnOrphanRemovalOnRequest checks that a scale set
// losing its session forgets its runners only once an orphan's removal on
// request under way has finished, and that none starts after.
func TestForgetRunnersWaitsForAnOrphanRemovalOnRequest(t *testing.T) {
	s, _, gh := newAdoptedScaleSet(t)
	ctx := context.Background()

	removing := newGate()
	var calls atomic.Int32
	gh.removeRunner = func(context.Context, string) error {
		calls.Add(1)
		removing.pass()

		return nil
	}
	ref := &ghscaleset.RunnerReference{ID: 0, Name: orphan, RunnerScaleSetID: 1}
	gh.runnerIDs = []string{orphan}

	removed := make(chan error, 1)
	go func() { removed <- s.removeOrphanOnRequest(ctx, ref) }()
	<-removing.reached

	forgotten := make(chan struct{})
	go func() {
		s.forgetRunners()
		close(forgotten)
	}()

	// A forget that did not wait would most likely be done by now; the wait
	// can miss one, never fail one that waits.
	select {
	case <-forgotten:
		t.Error("the runners were forgotten while an orphan's removal on request was under way")
	case <-time.After(50 * time.Millisecond):
	}

	close(removing.released)
	if err := <-removed; err != nil {
		t.Fatalf("removeOrphanOnRequest() = %v", err)
	}
	<-forgotten

	if err := s.removeOrphanOnRequest(ctx, ref); !errors.Is(err, errdefs.ErrUnavailable) {
		t.Errorf("removeOrphanOnRequest() after the session was lost = %v, want ErrUnavailable", err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("asked GitHub to remove %d registrations, want 1: one was removed after the session was lost", got)
	}
}
