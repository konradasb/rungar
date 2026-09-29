// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"errors"
	"maps"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/types"
)

func TestSortedRunnersAreInNameOrder(t *testing.T) {
	s := newScaleSet(testScaleSetSpec(newFakeScaleSetProviders()))
	for _, name := range []string{"c", "a", "b"} {
		s.runners[name] = &types.Runner{Name: name}
	}

	var got []string
	for _, r := range s.sortedRunners() {
		got = append(got, r.Name)
	}

	if want := []string{"a", "b", "c"}; !slices.Equal(got, want) {
		t.Errorf("sortedRunners() = %v, want %v", got, want)
	}
}

// TestSortedRunnersAreCopies checks that a caller cannot reach into the scale
// set's state through what it hands out.
func TestSortedRunnersAreCopies(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 8, memoryGiB: 16})
	s := newTestScaleSet(fleet)

	r, err := s.createRunner(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if r.Name == "" {
		t.Fatal("createRunner() returned an unnamed runner")
	}

	runners := s.sortedRunners()
	runners[0].State = types.RunnerBusy

	if got := s.sortedRunners()[0].State; got != types.RunnerStarting {
		t.Errorf("state = %q after a caller edited what sortedRunners returned; want %q",
			got, types.RunnerStarting)
	}
}

func TestRunnersByProviderCountsEachProvidersRunners(t *testing.T) {
	fleet := newFakeScaleSetProviders(
		providerRoom{name: "a", vcpus: 16, memoryGiB: 64},
		providerRoom{name: "b", vcpus: 16, memoryGiB: 64},
	)

	s := newScaleSet(testScaleSetSpec(fleet))
	s.runners = map[string]*types.Runner{
		"r1": {Name: "r1", Provider: "a"},
		"r2": {Name: "r2", Provider: "a"},
		"r3": {Name: "r3", Provider: "b"},
	}

	if got, want := s.runnersByProvider(), map[string]int{"a": 2, "b": 1}; !maps.Equal(got, want) {
		t.Errorf("runnersByProvider() = %v, want %v", got, want)
	}
}

// TestMachinesByProviderCountsMachinesNotYetDeleted checks the runners
// leaving the scale set count against their providers until their machines
// are deleted, while the scale set's own count leaves them out.
func TestMachinesByProviderCountsMachinesNotYetDeleted(t *testing.T) {
	fleet := newFakeScaleSetProviders(
		providerRoom{name: "a", vcpus: 16, memoryGiB: 64},
		providerRoom{name: "b", vcpus: 16, memoryGiB: 64},
	)

	s := newScaleSet(testScaleSetSpec(fleet))
	s.runners = map[string]*types.Runner{
		"r1": {Name: "r1", Provider: "a"},
	}
	s.leaving = map[string]*leavingRunner{
		"r2": {runner: types.Runner{Name: "r2", Provider: "a"}},
		"r3": {runner: types.Runner{Name: "r3", Provider: "b"}},
		"r4": {runner: types.Runner{Name: "r4", Provider: "b"}, deleted: true},
	}

	if got, want := s.machinesByProvider(), map[string]int{"a": 2, "b": 1}; !maps.Equal(got, want) {
		t.Errorf("machinesByProvider() = %v, want %v", got, want)
	}
	if got, want := s.runnersByProvider(), map[string]int{"a": 1}; !maps.Equal(got, want) {
		t.Errorf("runnersByProvider() = %v, want %v", got, want)
	}
	if got := s.runnerCount(); got != 1 {
		t.Errorf("runnerCount() = %d, want 1", got)
	}
}

func TestReconcileIsMeasured(t *testing.T) {
	s, _, rec := newScaling(t, 10, 0, 10)
	s.serving.Store(true)

	if err := s.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}

	if want := []bool{true}; !slices.Equal(rec.reconciles, want) {
		t.Errorf("reconciliations = %v, want %v", rec.reconciles, want)
	}
}

// TestForgetRunnersForgetsTheLastDecision checks a scale set that has lost its
// session keeps nothing of its runners or its last scaling decision, for the
// next session to start afresh.
func TestForgetRunnersForgetsTheLastDecision(t *testing.T) {
	s, _, _ := newScaling(t, 10, 1, 10)
	ctx := context.Background()

	if _, err := s.HandleDesiredRunnerCount(ctx, 2); err != nil {
		t.Fatal(err)
	}

	s.forgetRunners()

	if got := s.runnerCount(); got != 0 {
		t.Errorf("runnerCount() = %d, want 0", got)
	}
	if _, known := s.desiredCount(); known {
		t.Error("the last decision's target is still known")
	}

	s.mu.Lock()
	assignedKnown, minRunners := s.assignedKnown, s.minRunners
	s.mu.Unlock()
	if assignedKnown || minRunners != -1 {
		t.Errorf("assignedKnown = %v, minRunners = %d; want the last decision forgotten", assignedKnown, minRunners)
	}
}

// TestForgetRunnersWaitsForAReconcilePass checks that a scale set losing its
// session forgets its runners only once a reconcile pass under way has
// finished, so that the pass does not adopt runners after they are forgotten,
// and that no pass starts after.
func TestForgetRunnersWaitsForAReconcilePass(t *testing.T) {
	f := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 16, memoryGiB: 64})
	f.put("a", "known", types.MachineRunning, time.Now())
	s := newTestScaleSet(f)
	ctx := context.Background()

	if err := s.adopt(ctx); err != nil {
		t.Fatalf("adopt() = %v", err)
	}

	// A machine the pass under way finds, and adopts.
	f.put("a", "found", types.MachineRunning, time.Now())
	listed := newGate()
	f.mu.Lock()
	f.listGate = listed
	f.mu.Unlock()

	passed := make(chan error, 1)
	go func() { passed <- s.reconcile(ctx) }()
	<-listed.reached

	forgotten := make(chan struct{})
	go func() {
		s.forgetRunners()
		close(forgotten)
	}()

	// A forget that did not wait would most likely be done by now; the wait
	// can miss one, never fail one that waits.
	select {
	case <-forgotten:
		t.Error("the runners were forgotten while a reconcile pass was under way")
	case <-time.After(50 * time.Millisecond):
	}

	close(listed.released)
	if err := <-passed; err != nil {
		t.Fatalf("reconcile() = %v", err)
	}
	<-forgotten

	if got := s.runnerCount(); got != 0 {
		t.Errorf("runnerCount() = %d, want 0: the pass adopted runners after they were forgotten", got)
	}
	if err := s.reconcile(ctx); !errors.Is(err, errdefs.ErrUnavailable) {
		t.Errorf("reconcile() after the session was lost = %v, want ErrUnavailable", err)
	}
	if got := s.runnerCount(); got != 0 {
		t.Errorf("runnerCount() = %d after a pass once the session was lost, want 0", got)
	}
}

// TestForgetRunnersWaitsForARemovalOnRequest checks that a scale set losing
// its session forgets its runners only once a removal on request under way
// has finished, and that none starts after.
func TestForgetRunnersWaitsForARemovalOnRequest(t *testing.T) {
	f := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 16, memoryGiB: 64})
	f.put("a", "known", types.MachineRunning, time.Now())
	f.put("a", "other", types.MachineRunning, time.Now())
	s := newTestScaleSet(f)
	ctx := context.Background()

	if err := s.adopt(ctx); err != nil {
		t.Fatalf("adopt() = %v", err)
	}

	deleting := newGate()
	f.mu.Lock()
	f.deleteGate = deleting
	f.mu.Unlock()

	known, other := f.machine(t, "known"), f.machine(t, "other")
	removed := make(chan error, 1)
	go func() { removed <- s.removeUnlessBusy(ctx, known) }()
	<-deleting.reached

	forgotten := make(chan struct{})
	go func() {
		s.forgetRunners()
		close(forgotten)
	}()

	// A forget that did not wait would most likely be done by now; the wait
	// can miss one, never fail one that waits.
	select {
	case <-forgotten:
		t.Error("the runners were forgotten while a removal on request was under way")
	case <-time.After(50 * time.Millisecond):
	}

	close(deleting.released)
	if err := <-removed; err != nil {
		t.Fatalf("removeUnlessBusy() = %v", err)
	}
	<-forgotten

	if err := s.removeUnlessBusy(ctx, other); !errors.Is(err, errdefs.ErrUnavailable) {
		t.Errorf("removeUnlessBusy() after the session was lost = %v, want ErrUnavailable", err)
	}
	if f.providerOf("other") == "" {
		t.Error("a runner was removed after the session was lost")
	}
}

// TestForgetRunnersWaitsForAScalingDecision checks that a scale set losing its
// session forgets its runners only once a scaling decision under way has
// finished, so that a runner it creates is not counted after they are
// forgotten.
func TestForgetRunnersWaitsForAScalingDecision(t *testing.T) {
	f := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 16, memoryGiB: 64})
	s := newTestScaleSet(f)
	ctx := context.Background()

	if err := s.adopt(ctx); err != nil {
		t.Fatalf("adopt() = %v", err)
	}

	creating := newGate()
	f.mu.Lock()
	f.createGate = creating
	f.mu.Unlock()

	decided := make(chan struct{})
	go func() {
		s.scaleTo(ctx, 1)
		close(decided)
	}()
	<-creating.reached

	forgotten := make(chan struct{})
	go func() {
		s.forgetRunners()
		close(forgotten)
	}()

	// A forget that did not wait would most likely be done by now; the wait
	// can miss one, never fail one that waits.
	select {
	case <-forgotten:
		t.Error("the runners were forgotten while a scaling decision was under way")
	case <-time.After(50 * time.Millisecond):
	}

	close(creating.released)
	<-decided
	<-forgotten

	if got := s.runnerCount(); got != 0 {
		t.Errorf("runnerCount() = %d, want 0: the decision counted a runner after they were forgotten", got)
	}
	if _, known := s.desiredCount(); known {
		t.Error("the decision's target is still known after the runners were forgotten")
	}
}

// TestServingEndsAsSoonAsTheSessionIsLost checks a scale set losing its
// session stops serving at once, not once a reconcile pass under way has
// finished, so that nothing else starts on the fleet meanwhile.
func TestServingEndsAsSoonAsTheSessionIsLost(t *testing.T) {
	f := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 16, memoryGiB: 64})
	s := newTestScaleSet(f)
	sessions := &fakeSessions{}

	serveScaleSet(t, s, sessions)
	waitFor(t, "the scale set to listen", func() bool { return s.currentPhase() == types.ScaleSetListening })

	listed := newGate()
	release := sync.OnceFunc(func() { close(listed.released) })
	// Run before serveScaleSet's cleanup, which waits for the pass.
	t.Cleanup(release)
	f.mu.Lock()
	f.listGate = listed
	f.mu.Unlock()

	s.wake <- struct{}{}
	<-listed.reached

	sessions.setOther(true)
	waitFor(t, "the scale set to stop serving", func() bool { return !s.serving.Load() })

	release()
	waitFor(t, "the scale set to stand by", func() bool {
		return s.currentPhase() == types.ScaleSetWaitingForSession
	})
}
