// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/events"
	"github.com/konradasb/rungar/internal/types"
)

// TestCreateTriesTheNextProviderWhenOneIsFull checks that a provider refusing
// a runner as full is moved on from, rather than the runner given up.
func TestCreateTriesTheNextProviderWhenOneIsFull(t *testing.T) {
	fleet := newFakeScaleSetProviders(
		providerRoom{name: "a", vcpus: 1, memoryGiB: 1},
		providerRoom{name: "b", vcpus: 16, memoryGiB: 32},
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
	if s.runnerCount() != 1 {
		t.Errorf("runnerCount() = %d, want 1", s.runnerCount())
	}

	if len(fleet.created) != 1 {
		t.Fatalf("created %d machines, want 1", len(fleet.created))
	}
	if got := fleet.created[0].spec.JITConfig; got != "jit-"+r.Name {
		t.Errorf("the machine got %q, want the registration issued for its own name", got)
	}
}

// TestCreateRegistersBeforeBooting checks the ordering the design depends on:
// a machine must never be created without a registration, or it boots into
// something that will never be a runner.
func TestCreateRegistersBeforeBooting(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 8, memoryGiB: 16})

	wantErr := errors.New("github is down")
	s := newTestScaleSet(fleet, func(g *fakeGitHub) {
		g.jit = func(context.Context, string) (string, error) { return "", wantErr }
	})

	_, err := s.createRunner(context.Background())
	if !errors.Is(err, wantErr) {
		t.Fatalf("createRunner() = %v, want the GitHub error", err)
	}

	if len(fleet.created) != 0 {
		t.Error("a machine was created although no registration was issued for it")
	}
	if s.runnerCount() != 0 {
		t.Errorf("runnerCount() = %d, want 0: nothing was created", s.runnerCount())
	}
}

// TestAFullFleetIsReportedAsNoCapacity checks that a fleet with no room is
// reported as exactly that, since the scaler treats it as a wait rather than a
// failure.
func TestAFullFleetIsReportedAsNoCapacity(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 1, memoryGiB: 1})
	s := newTestScaleSet(fleet)

	_, err := s.createRunner(context.Background())
	if !errors.Is(err, errdefs.ErrNoCapacity) {
		t.Fatalf("createRunner() = %v, want an ErrNoCapacity", err)
	}
	if s.runnerCount() != 0 {
		t.Errorf("runnerCount() = %d, want 0", s.runnerCount())
	}
}

// TestARefusedMachineIsNotCountedAndIsDeleted checks that a machine the
// provider would not create is not counted as a runner, which would hold
// capacity that does not exist.
func TestARefusedMachineIsNotCountedAndIsDeleted(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 8, memoryGiB: 16})
	fleet.createErr = errors.New("out of disk")

	s := newTestScaleSet(fleet)

	if _, err := s.createRunner(context.Background()); err == nil {
		t.Fatal("createRunner() = nil, want the provider's error")
	}
	if s.runnerCount() != 0 {
		t.Errorf("runnerCount() = %d, want 0: the machine was never created", s.runnerCount())
	}

	// A provider may keep a machine it could not boot; it is not left there.
	if len(fleet.deleted) != 1 || fleet.deleted[0].provider != "a" {
		t.Errorf("deleted %v, want the machine that was not created deleted from a", fleet.deleted)
	}
}

// TestCreateSpreadsAsProvidersFill checks that the scale set's runners on each
// provider are what placement spreads by, and that it goes on once every
// provider is full.
func TestCreateSpreadsAsProvidersFill(t *testing.T) {
	// Two providers, each with room for exactly one runner.
	fleet := newFakeScaleSetProviders(
		providerRoom{name: "a", vcpus: 2, memoryGiB: 4},
		providerRoom{name: "b", vcpus: 2, memoryGiB: 4},
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
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 8, memoryGiB: 16})
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

// TestCreateDeletesAStrandedMachine checks that a machine a provider may have
// left, and could not delete, is deleted and kept from adoption.
func TestCreateDeletesAStrandedMachine(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 8, memoryGiB: 16})
	fleet.failLeavingMachine = errors.New("the hypervisor crashed, and the machine could not be deleted")

	d := &registrationRemovals{}
	s := newTestScaleSet(fleet, removingRegistrationsWith(d))

	if _, err := s.createRunner(context.Background()); err == nil {
		t.Fatal("createRunner() = nil, want the provider's failure")
	}

	got := d.got()
	if len(got) != 1 {
		t.Fatalf("removed registrations %v, want the runner no machine was created for", got)
	}
	if len(fleet.deleted) != 1 || fleet.deleted[0] != (deletedMachine{provider: "a", name: got[0]}) {
		t.Errorf("deleted %v, want %s's stranded machine deleted from a", fleet.deleted, got[0])
	}

	s.mu.Lock()
	_, kept := s.left[got[0]]
	s.mu.Unlock()
	if !kept {
		t.Errorf("%s is not kept from adoption, should its machine be listed before it is gone", got[0])
	}
}

// TestCreateIsConcurrencySafe runs several placements at once, which is what
// a burst of queued jobs produces.
func TestCreateIsConcurrencySafe(t *testing.T) {
	fleet := newFakeScaleSetProviders(
		providerRoom{name: "a", vcpus: 64, memoryGiB: 256},
		providerRoom{name: "b", vcpus: 64, memoryGiB: 256},
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

	if s.runnerCount() != n {
		t.Errorf("runnerCount() = %d, want %d", s.runnerCount(), n)
	}

	names := map[string]bool{}
	for _, r := range s.sortedRunners() {
		if names[r.Name] {
			t.Fatalf("runner %q was created twice", r.Name)
		}
		names[r.Name] = true
	}
}

// TestASlowProviderHoldsUpOnlyItsOwnRunner checks a slow create, such as a cold
// provider pulling an image, does not hold up other runners.
func TestASlowProviderHoldsUpOnlyItsOwnRunner(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 64, memoryGiB: 256})
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
		t.Errorf("five creates of 300ms each took %v: they were created one after another", elapsed)
	}
}

// stuckScaleSetProviders are providers that never answer a create of their
// own accord.
type stuckScaleSetProviders struct{ *fakeScaleSetProviders }

func (f stuckScaleSetProviders) Place(ctx context.Context, _ map[string]int,
	_ func(string) types.MachineSpec,
) (string, func(), error) {
	<-ctx.Done()
	return "", nil, fmt.Errorf("provider %q: %w", "a", ctx.Err())
}

// TestCreateIsBounded checks that a provider which never answers a create does
// not hold the scale set up for good: the listener's context is never
// cancelled, so the bound has to come from here.
func TestCreateIsBounded(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 8, memoryGiB: 16})
	set := testScaleSetSpec(fleet)
	set.StartTimeout = 50 * time.Millisecond

	s := newScaleSetOver(stuckScaleSetProviders{fleet}, &fakeGitHub{}, set, &fakeMetrics{}, discardLogger())

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

// TestAMachineCarriesTheRunnersRegistrationAndLabels checks a provider is given
// the runner's registration, its labels, and the runner spec as the provider
// parsed it.
func TestAMachineCarriesTheRunnersRegistrationAndLabels(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a"})
	s := newTestScaleSet(fleet)

	spec := s.machineSpec("rungar-vm-abcd1234", "encoded-jit-config", "a")

	if spec.Name != "rungar-vm-abcd1234" || spec.JITConfig != "encoded-jit-config" {
		t.Errorf("spec = %+v, want the runner's name and its registration", spec)
	}
	if !types.HasLabels(spec.Labels, types.ScaleSetSelector(testInstallation, testScaleSet)) {
		t.Error("the machine is not labelled as this installation's, so a restarted daemon would not find it")
	}
	if got := spec.Labels[types.LabelRunner]; got != "rungar-vm-abcd1234" {
		t.Errorf("runner label = %q, want the runner's name", got)
	}
	if spec.Runner != testScaleSetSpec(fleet).RunnerSpecs["a"] {
		t.Errorf("runner = %v, want the scale set's, as the provider read it", spec.Runner)
	}
}

// TestMachineDeadlineFollowsMaxAge checks a machine's deadline falls after
// max_age and a reconcile pass, for Rungar to remove an expired runner before
// its provider does, and that there is none without max_age.
func TestMachineDeadlineFollowsMaxAge(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		maxAge time.Duration
		want   time.Time
	}{
		{name: "without max_age", maxAge: 0},
		{name: "with max_age", maxAge: time.Hour, want: now.Add(time.Hour + time.Minute + deadlineGrace)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestScaleSet(newFakeScaleSetProviders(providerRoom{name: "a"}))
			s.spec.MaxAge = tt.maxAge
			s.now = func() time.Time { return now }

			if got := s.machineSpec("rungar-vm-abcd1234", "encoded-jit-config", "a").Deadline; !got.Equal(tt.want) {
				t.Errorf("Deadline = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRunnerNameIsValidOrRefused(t *testing.T) {
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

// TestARefusedCreateRemovesItsRegistration checks that GitHub is not left
// listing a runner whose machine was never created.
func TestARefusedCreateRemovesItsRegistration(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 8, memoryGiB: 16})
	fleet.createErr = errors.New("provider refused")

	d := &registrationRemovals{}
	s := newTestScaleSet(fleet, removingRegistrationsWith(d))

	if _, err := s.createRunner(context.Background()); err == nil {
		t.Fatal("createRunner() = nil, want the provider's refusal")
	}

	if got := d.got(); len(got) != 1 {
		t.Errorf("removed registrations %v, want the runner whose machine was not created", got)
	}
}

// TestRunnerNamesAreToldApartByScaleSet checks a runner name is recognised as
// its own scale set's alone, however long that scale set's name.
func TestRunnerNamesAreToldApartByScaleSet(t *testing.T) {
	long := strings.Repeat("x", 70)
	created, err := runnerName(long)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		test           string
		scaleSet, name string
		want           bool
	}{
		{"the scale set's", "rungar-vm", "rungar-vm-0badf00d", true},
		{"suffix too short", "rungar-vm", "rungar-vm-0badf00", false},
		{"suffix too long", "rungar-vm", "rungar-vm-0badf00dd", false},
		{"suffix not hex", "rungar-vm", "rungar-vm-xyzxyzxy", false},
		{"a scale set whose name is a prefix", "rungar", "rungar-vm-0badf00d", false},
		{"another scale set's", "rungar-vm", "other-vm-0badf00d", false},
		{"a long scale set name, truncated", long, created, true},
	}
	for _, tt := range tests {
		t.Run(tt.test, func(t *testing.T) {
			if got := isRunnerNameOf(tt.scaleSet, tt.name); got != tt.want {
				t.Errorf("isRunnerNameOf(%q, %q) = %v, want %v", tt.scaleSet, tt.name, got, tt.want)
			}
		})
	}
}

// TestARunnerBeingCreatedIsNeverAdopted checks that reconciliation, running
// while runners are created and start jobs, never takes one for an unknown
// machine: a runner is counted in the same step as it stops being created.
func TestARunnerBeingCreatedIsNeverAdopted(t *testing.T) {
	const n = 20

	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 2 * n, memoryGiB: 4 * n})
	set := testScaleSetSpec(fleet)
	set.MaxRunners = n
	s := newScaleSetOver(fleet, &fakeGitHub{}, set, &fakeMetrics{}, discardLogger())
	ctx := context.Background()

	if err := s.adopt(ctx); err != nil {
		t.Fatalf("adopt() = %v", err)
	}

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			select {
			case <-done:
				return
			default:
			}

			_ = s.reconcileFleet(ctx)
			markAllBusy(s)
		}
	})

	for range n {
		if _, err := s.createRunner(ctx); err != nil {
			t.Errorf("createRunner() = %v", err)
		}
	}
	close(done)
	wg.Wait()

	for _, h := range happenings(recordedBy(t, s.events)) {
		if h.action == events.ActionAdopted {
			t.Errorf("runner %s was adopted while it was being created", h.name)
		}
	}
	if got := s.runnerCount(); got != n {
		t.Errorf("runnerCount() = %d, want %d", got, n)
	}
}
