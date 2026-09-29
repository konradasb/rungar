// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/types"
)

// TestCreateTriesTheNextProviderWhenOneIsFull checks that a provider refusing
// a runner as full is moved on from, rather than the runner given up.
func TestCreateTriesTheNextProviderWhenOneIsFull(t *testing.T) {
	fleet := newFleet(
		fakeProvider{name: "a", vcpus: 1, memoryGiB: 1},
		fakeProvider{name: "b", vcpus: 16, memoryGiB: 32},
	)
	s := newTestScaleSet(fleet)

	r, err := s.createRunner(context.Background())
	if err != nil {
		t.Fatalf("createRunner() = %v", err)
	}

	if r.Provider != "b" {
		t.Errorf("placed on %q, want %q, the one that took it", r.Provider, "b")
	}
	if r.Size != "2 vCPU, 4 GiB" {
		t.Errorf("size = %q, want b's runner as it describes it", r.Size)
	}
	if r.State != types.RunnerStarting {
		t.Errorf("state = %q, want %q", r.State, types.RunnerStarting)
	}
	if r.ScaleSet != testScaleSet {
		t.Errorf("scale set = %q, want %q", r.ScaleSet, testScaleSet)
	}
	if r.CreatedAt.IsZero() {
		t.Error("CreatedAt is zero, so the start timeout could never elapse")
	}
	if s.count() != 1 {
		t.Errorf("count() = %d, want 1", s.count())
	}

	if len(fleet.created) != 1 {
		t.Fatalf("started %d VMs, want 1", len(fleet.created))
	}
	if got := fleet.created[0].spec.JITConfig; got != "jit-"+r.Name {
		t.Errorf("the VM got %q, want the registration issued for its own name", got)
	}
}

// TestCreateRegistersBeforeBooting checks the ordering the design depends on:
// a VM must never be started without a registration, or it boots into
// something that will never be a runner.
func TestCreateRegistersBeforeBooting(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 8, memoryGiB: 16})

	wantErr := errors.New("github is down")
	s := newTestScaleSet(fleet, func(g *fakeGitHub) {
		g.jit = func(context.Context, string) (string, error) { return "", wantErr }
	})

	_, err := s.createRunner(context.Background())
	if !errors.Is(err, wantErr) {
		t.Fatalf("createRunner() = %v, want the GitHub error", err)
	}

	if len(fleet.created) != 0 {
		t.Error("a VM was started although no registration was issued for it")
	}
	if s.count() != 0 {
		t.Errorf("count() = %d, want 0: nothing was created", s.count())
	}
}

// TestCreateOnAFullFleet checks that a fleet with no room is reported as
// exactly that, since the scaler treats it as a wait rather than a failure.
func TestCreateOnAFullFleet(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 1, memoryGiB: 1})
	s := newTestScaleSet(fleet)

	_, err := s.createRunner(context.Background())
	if !errors.Is(err, errdefs.ErrNoCapacity) {
		t.Fatalf("createRunner() = %v, want an ErrNoCapacity", err)
	}
	if s.count() != 0 {
		t.Errorf("count() = %d, want 0", s.count())
	}
}

// TestCreateWhenTheProviderRefuses checks that a VM the provider would not start is
// not counted as a runner, which would hold capacity that does not exist.
func TestCreateWhenTheProviderRefuses(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 8, memoryGiB: 16})
	fleet.createErr = errors.New("out of disk")

	s := newTestScaleSet(fleet)

	if _, err := s.createRunner(context.Background()); err == nil {
		t.Fatal("createRunner() = nil, want the provider's error")
	}
	if s.count() != 0 {
		t.Errorf("count() = %d, want 0: the VM never started", s.count())
	}

	// A backend may keep a machine it could not boot; it is not left there.
	if len(fleet.deleted) != 1 || fleet.deleted[0].provider != "a" {
		t.Errorf("deleted %v, want the machine that did not start removed from a", fleet.deleted)
	}
}

// TestCreateSpreadsAsProvidersFill checks that the scale set's runners on each
// provider are what placement spreads by, and that it goes on once every
// provider is full.
func TestCreateSpreadsAsProvidersFill(t *testing.T) {
	// Two providers, each with room for exactly one runner.
	fleet := newFleet(
		fakeProvider{name: "a", vcpus: 2, memoryGiB: 4},
		fakeProvider{name: "b", vcpus: 2, memoryGiB: 4},
	)
	s := newTestScaleSet(fleet)
	ctx := context.Background()

	first, err := s.createRunner(ctx)
	if err != nil {
		t.Fatalf("createRunner() = %v", err)
	}
	second, err := s.createRunner(ctx)
	if err != nil {
		t.Fatalf("second createRunner() = %v", err)
	}
	if second.Provider == first.Provider {
		t.Errorf("both runners went to %q although %q had none", first.Provider, second.Provider)
	}

	if _, err := s.createRunner(ctx); !errors.Is(err, errdefs.ErrNoCapacity) {
		t.Errorf("third createRunner() = %v, want an ErrNoCapacity: both providers are full", err)
	}
}

// TestCreateDoesNotRegisterWhenNoProviderCanBeTried checks that a runner is not
// registered with GitHub, only to be removed again, when placement would try
// no provider at all.
func TestCreateDoesNotRegisterWhenNoProviderCanBeTried(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 8, memoryGiB: 16})
	fleet.canPlaceErr = errdefs.NoCapacity("every provider is disabled")

	var registrations int
	s := newTestScaleSet(fleet, func(g *fakeGitHub) {
		g.jit = func(_ context.Context, name string) (string, error) {
			registrations++
			return "jit-" + name, nil
		}
	})

	if _, err := s.createRunner(context.Background()); !errors.Is(err, errdefs.ErrNoCapacity) {
		t.Fatalf("createRunner() = %v, want an ErrNoCapacity", err)
	}
	if registrations != 0 {
		t.Errorf("registered %d runners with GitHub, want none: there was no provider to try", registrations)
	}
}

// TestCreateDiscardsWhatAFailedProviderLeft checks that a machine a provider
// may have left, and could not remove, is removed and kept from adoption.
func TestCreateDiscardsWhatAFailedProviderLeft(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 8, memoryGiB: 16})
	fleet.leftover = errors.New("the hypervisor crashed, and the machine could not be removed")

	d := &deregistrations{}
	s := newTestScaleSet(fleet, deregisteringWith(d))

	if _, err := s.createRunner(context.Background()); err == nil {
		t.Fatal("createRunner() = nil, want the provider's failure")
	}

	got := d.got()
	if len(got) != 1 {
		t.Fatalf("deregistered %v, want the runner no machine was made for", got)
	}
	if len(fleet.deleted) != 1 || fleet.deleted[0] != (deletedMachine{provider: "a", name: got[0]}) {
		t.Errorf("deleted %v, want %s's leftover removed from a", fleet.deleted, got[0])
	}

	s.mu.Lock()
	_, kept := s.removed[got[0]]
	s.mu.Unlock()
	if !kept {
		t.Errorf("%s is not kept from adoption, should its machine be listed before it is gone", got[0])
	}
}

func TestRemoveDeletesTheVM(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 8, memoryGiB: 16})
	s := newTestScaleSet(fleet)
	ctx := context.Background()

	r, err := s.createRunner(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.removeRunner(ctx, r.Name, "job_completed"); err != nil {
		t.Fatalf("removeRunner() = %v", err)
	}

	if s.count() != 0 {
		t.Errorf("count() = %d, want 0", s.count())
	}
	if fleet.count() != 0 {
		t.Errorf("the fleet still holds %d VMs", fleet.count())
	}
	if len(fleet.deleted) != 1 || fleet.deleted[0].name != r.Name {
		t.Errorf("deleted = %v, want just %q", fleet.deleted, r.Name)
	}
}

// TestRemoveAnUnknownRunner checks that a completion GitHub reports twice, or
// for a runner this daemon never had, is not an error.
func TestRemoveAnUnknownRunner(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 8, memoryGiB: 16})
	s := newTestScaleSet(fleet)

	if err := s.removeRunner(context.Background(), "never-existed", "job_completed"); err != nil {
		t.Errorf("removeRunner() = %v, want nil for a runner that is already gone", err)
	}
	if len(fleet.deleted) != 0 {
		t.Error("a delete was sent for a runner the manager does not know")
	}
}

// TestRemoveWhenTheProviderFails checks that a runner whose VM could not be
// removed is still forgotten: it is not this manager's any more, and holding
// it would block the scale set's ceiling forever.
func TestRemoveWhenTheProviderFails(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 8, memoryGiB: 16})
	s := newTestScaleSet(fleet)
	ctx := context.Background()

	r, err := s.createRunner(ctx)
	if err != nil {
		t.Fatal(err)
	}

	fleet.deleteErr = errors.New("provider unreachable")

	if err := s.removeRunner(ctx, r.Name, "job_completed"); err == nil {
		t.Fatal("removeRunner() = nil, want the provider's error")
	}
	if s.count() != 0 {
		t.Errorf("count() = %d, want 0: the runner is no longer this manager's", s.count())
	}
}

func TestListIsSortedByName(t *testing.T) {
	s := &scaleSet{
		spec:   testScaleSetSpec(newFleet()),
		logger: discardLogger(),
		runners: map[string]*types.Runner{
			"c": {Name: "c"},
			"a": {Name: "a"},
			"b": {Name: "b"},
		},
	}

	var got []string
	for _, r := range s.list() {
		got = append(got, r.Name)
	}

	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("list() = %v, want it in name order", got)
	}
}

// TestListReturnsCopies checks that a caller cannot reach into the manager's
// state through what it hands out.
func TestListReturnsCopies(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 8, memoryGiB: 16})
	s := newTestScaleSet(fleet)

	r, err := s.createRunner(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if r.Name == "" {
		t.Fatal("createRunner() returned an unnamed runner")
	}

	list := s.list()
	list[0].State = types.RunnerBusy

	if got := s.list()[0].State; got != types.RunnerStarting {
		t.Errorf("state = %q after a caller edited what List returned; want %q",
			got, types.RunnerStarting)
	}
}

func TestCounts(t *testing.T) {
	fleet := newFleet(
		fakeProvider{name: "a", vcpus: 16, memoryGiB: 64},
		fakeProvider{name: "b", vcpus: 16, memoryGiB: 64},
	)

	s := &scaleSet{
		spec:   testScaleSetSpec(fleet),
		logger: discardLogger(),
		fleet:  fleet,
		runners: map[string]*types.Runner{
			"r1": {Name: "r1", Provider: "a"},
			"r2": {Name: "r2", Provider: "a"},
			"r3": {Name: "r3", Provider: "b"},
		},
	}

	if got, want := s.runnersPerProvider(), map[string]int{"a": 2, "b": 1}; !maps.Equal(got, want) {
		t.Errorf("counts() = %v, want %v", got, want)
	}
}

// TestCreateIsConcurrencySafe runs several placements at once, which is what
// a burst of queued jobs produces.
func TestCreateIsConcurrencySafe(t *testing.T) {
	fleet := newFleet(
		fakeProvider{name: "a", vcpus: 64, memoryGiB: 256},
		fakeProvider{name: "b", vcpus: 64, memoryGiB: 256},
	)
	s := newTestScaleSet(fleet)

	const n = 8

	errs := make(chan error, n)
	for range n {
		go func() {
			_, err := s.createRunner(context.Background())
			errs <- err
		}()
	}

	for range n {
		if err := <-errs; err != nil {
			t.Fatalf("createRunner() = %v", err)
		}
	}

	if s.count() != n {
		t.Errorf("count() = %d, want %d", s.count(), n)
	}

	names := map[string]bool{}
	for _, r := range s.list() {
		if names[r.Name] {
			t.Fatalf("runner %q was created twice", r.Name)
		}
		names[r.Name] = true
	}
}

// TestASlowProviderHoldsUpOnlyItsOwnRunner checks a slow create, such as a cold
// provider pulling an image, does not hold up other runners.
func TestASlowProviderHoldsUpOnlyItsOwnRunner(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 64, memoryGiB: 256})
	fleet.createDelay = 300 * time.Millisecond

	s := newTestScaleSet(fleet)

	start := time.Now()

	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			if _, err := s.createRunner(context.Background()); err != nil {
				t.Errorf("createRunner() = %v", err)
			}
		})
	}
	wg.Wait()

	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("five creates of 300ms each took %v: they were made one after another", elapsed)
	}
}

// stuckFleet is a fleet whose providers never answer a create of their own
// accord.
type stuckFleet struct{ *fakeFleet }

func (f stuckFleet) Place(ctx context.Context, _ map[string]int,
	_ func(string) types.MachineSpec,
) (string, func(), error) {
	<-ctx.Done()
	return "", nil, fmt.Errorf("provider %q: %w", "a", ctx.Err())
}

// TestCreateIsBounded checks that a provider which never answers a create does
// not hold the scale set up for good: the listener's context is never
// cancelled, so the bound has to come from here.
func TestCreateIsBounded(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a", vcpus: 8, memoryGiB: 16})
	set := testScaleSetSpec(fleet)
	set.StartTimeout = 50 * time.Millisecond

	s := newScaleSetOver(stuckFleet{fleet}, &fakeGitHub{}, set, &recorder{}, discardLogger())

	done := make(chan error, 1)
	go func() {
		_, err := s.createRunner(context.Background())
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("createRunner() = %v, want it to give up at the deadline", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("createRunner() never returned from a provider that does not answer")
	}
}

// TestMachineSpec checks a provider is given the runner's registration, its
// labels, and the runner spec as the provider parsed it.
func TestMachineSpec(t *testing.T) {
	fleet := newFleet(fakeProvider{name: "a"})
	s := newTestScaleSet(fleet)

	spec := s.machineSpec("rungar-vm-abcd1234", "encoded-jit-config", "a")

	if spec.Name != "rungar-vm-abcd1234" || spec.JITConfig != "encoded-jit-config" {
		t.Errorf("spec = %+v, want the runner's name and its registration", spec)
	}
	if !types.Matches(spec.Labels, types.ScaleSetSelector(testInstallation, testScaleSet)) {
		t.Error("the machine is not labelled as this installation's, so a restarted daemon would not find it")
	}
	if got := spec.Labels[types.LabelRunner]; got != "rungar-vm-abcd1234" {
		t.Errorf("runner label = %q, want the runner's name", got)
	}
	if spec.Runner != testScaleSetSpec(fleet).RunnerSpecs["a"] {
		t.Errorf("runner = %v, want the scale set's, as the provider read it", spec.Runner)
	}
}

func TestRunnerName(t *testing.T) {
	tests := []struct {
		name     string
		scaleSet string
		ok       bool
	}{
		{name: "an ordinary name", scaleSet: "rungar-vm", ok: true},
		{name: "dots are allowed", scaleSet: "vm.linux", ok: true},
		{name: "a very long name is truncated rather than refused", scaleSet: strings.Repeat("a", 300), ok: true},
		{name: "a name that is not an RFC 1123 name", scaleSet: "-leading-hyphen"},
		{name: "a name with a slash", scaleSet: "org/set"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := runnerName(tt.scaleSet)
			if !tt.ok {
				if err == nil {
					t.Fatalf("runnerName(%q) = %q, want an error", tt.scaleSet, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("runnerName(%q) = %v", tt.scaleSet, err)
			}
			if !types.ValidRunnerName(got) {
				t.Errorf("runnerName(%q) = %q, which is not a valid name", tt.scaleSet, got)
			}
		})
	}
}

func TestRunnerNamesDoNotRepeat(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		name, err := runnerName("rungar-vm")
		if err != nil {
			t.Fatal(err)
		}
		if seen[name] {
			t.Fatalf("runnerName() returned %q twice", name)
		}
		seen[name] = true
	}
}
