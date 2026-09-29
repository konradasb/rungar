// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package gcp

import (
	"context"
	"errors"
	"net/http"
	"path"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/provider"
)

// fallbackRunner returns a runner block of two machine types.
func fallbackRunner() RunnerSpec {
	r := testRunner()
	r.MachineTypes = provider.OneOrMore{"c3-standard-4", "n2-standard-4"}

	return r
}

// createdAs returns the zone and machine type of a runner's instance, or two
// empty strings.
func createdAs(f *fakeCompute, name string) (zone, machineType string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for zone, instances := range f.instances {
		if instance, ok := instances[name]; ok {
			return zone, path.Base(instance.MachineType)
		}
	}

	return "", ""
}

func TestCreateSpreadsRunnersAcrossZones(t *testing.T) {
	f := newFakeCompute()
	p := newTestProvider(t, testConfig(), f)

	names := []string{"rungar-x-1", "rungar-x-2", "rungar-x-3", "rungar-x-4"}
	want := []string{"europe-west1-b", "europe-west1-c", "europe-west1-d", "europe-west1-b"}
	for i, name := range names {
		if err := p.Create(context.Background(), testMachine(name, "rungar-x")); err != nil {
			t.Fatalf("Create(%s) = %v", name, err)
		}
		if zone, _ := createdAs(f, name); zone != want[i] {
			t.Errorf("%s created in %s, want %s", name, zone, want[i])
		}
	}
}

// TestCreateSpreadsEachScaleSetOnItsOwn checks one scale set's runners do
// not move another's round the zones.
func TestCreateSpreadsEachScaleSetOnItsOwn(t *testing.T) {
	f := newFakeCompute()
	p := newTestProvider(t, testConfig(), f)

	for _, spec := range []struct{ name, scaleSet string }{
		{"rungar-x-1", "rungar-x"},
		{"rungar-y-1", "rungar-y"},
		{"rungar-x-2", "rungar-x"},
	} {
		if err := p.Create(context.Background(), testMachine(spec.name, spec.scaleSet)); err != nil {
			t.Fatalf("Create(%s) = %v", spec.name, err)
		}
	}

	if zone, _ := createdAs(f, "rungar-x-2"); zone != "europe-west1-c" {
		t.Errorf("rungar-x-2 created in %s, want europe-west1-c, after rungar-x-1's", zone)
	}
}

func TestCreateTriesTheNextMachineTypeBeforeTheNextZone(t *testing.T) {
	f := newFakeCompute()
	f.failures["europe-west1-b/c3-standard-4"] = failure{code: "ZONE_RESOURCE_POOL_EXHAUSTED", leaves: true}
	p := newTestProvider(t, testConfig(), f)

	spec := testMachine("rungar-x-1", "rungar-x")
	spec.Runner = fallbackRunner()

	if err := p.Create(context.Background(), spec); err != nil {
		t.Fatalf("Create() = %v", err)
	}
	if zone, machineType := createdAs(f, spec.Name); zone != "europe-west1-b" || machineType != "n2-standard-4" {
		t.Errorf("created in %s as %s, want europe-west1-b as n2-standard-4", zone, machineType)
	}
}

// TestCreateDoesNotRetryAMachineTypeOutOfQuota checks a machine type whose
// quota is spent is not tried in the other zones, which share the quota,
// while the other machine types are.
func TestCreateDoesNotRetryAMachineTypeOutOfQuota(t *testing.T) {
	f := newFakeCompute()
	f.failures["europe-west1-b/c3-standard-4"] = failure{code: "QUOTA_EXCEEDED", leaves: true}
	f.failures["europe-west1-b/n2-standard-4"] = failure{code: "ZONE_RESOURCE_POOL_EXHAUSTED"}
	p := newTestProvider(t, testConfig(), f)

	spec := testMachine("rungar-x-1", "rungar-x")
	spec.Runner = fallbackRunner()

	if err := p.Create(context.Background(), spec); err != nil {
		t.Fatalf("Create() = %v", err)
	}
	if zone, machineType := createdAs(f, spec.Name); zone != "europe-west1-c" || machineType != "n2-standard-4" {
		t.Errorf("created in %s as %s, want europe-west1-c as n2-standard-4", zone, machineType)
	}
	if n := f.count(http.MethodPost, instancesPath); n != 3 {
		t.Errorf("made %d inserts, want 3: c3 in b, n2 in b and n2 in c", n)
	}
}

func TestCreatePassesOverAZoneWithoutTheMachineType(t *testing.T) {
	f := newFakeCompute()
	f.notOffered["europe-west1-b/c3-standard-4"] = true
	p := newTestProvider(t, testConfig(), f)

	spec := testMachine("rungar-x-1", "rungar-x")
	spec.Runner = fallbackRunner()

	if err := p.Create(context.Background(), spec); err != nil {
		t.Fatalf("Create() = %v", err)
	}
	if zone, machineType := createdAs(f, spec.Name); zone != "europe-west1-b" || machineType != "n2-standard-4" {
		t.Errorf("created in %s as %s, want europe-west1-b as n2-standard-4", zone, machineType)
	}
	if n := f.count(http.MethodPost, instancesPath); n != 1 {
		t.Errorf("made %d inserts, want 1: none of c3 where it is not offered", n)
	}
}

func TestCreateOfAMachineTypeNoZoneOffers(t *testing.T) {
	f := newFakeCompute()
	for _, zone := range testConfig().Zones {
		f.notOffered[zone+"/e2-standard-4"] = true
	}
	p := newTestProvider(t, testConfig(), f)

	err := p.Create(context.Background(), testMachine("rungar-x-1", "rungar-x"))
	if !errors.Is(err, errdefs.ErrInvalidArgument) {
		t.Errorf("Create() = %v, want invalid argument", err)
	}
	if n := f.count(http.MethodPost, instancesPath); n != 0 {
		t.Errorf("made %d inserts, want none", n)
	}
}

func TestCreateAsksWhatAZoneOffersOnce(t *testing.T) {
	f := newFakeCompute()
	config := testConfig()
	config.Zones = config.Zones[:1]
	p := newTestProvider(t, config, f)

	for _, name := range []string{"rungar-x-1", "rungar-x-2"} {
		if err := p.Create(context.Background(), testMachine(name, "rungar-x")); err != nil {
			t.Fatalf("Create(%s) = %v", name, err)
		}
	}

	if n := f.count(http.MethodGet, machineTypePath); n != 1 {
		t.Errorf("asked about the machine type %d times, want once", n)
	}
}

// TestCreateWhenWhatAZoneOffersIsUnknown checks a zone that cannot be asked
// what it offers is tried all the same.
func TestCreateWhenWhatAZoneOffersIsUnknown(t *testing.T) {
	f := newFakeCompute()
	f.machineTypeFailure = http.StatusServiceUnavailable
	p := newTestProvider(t, testConfig(), f)

	spec := testMachine("rungar-x-1", "rungar-x")
	if err := p.Create(context.Background(), spec); err != nil {
		t.Fatalf("Create() = %v", err)
	}
	if zone, _ := createdAs(f, spec.Name); zone != "europe-west1-b" {
		t.Errorf("created in %q, want europe-west1-b", zone)
	}
}

func TestCreateBuildsTheInstanceFromTheRunnerBlock(t *testing.T) {
	f := newFakeCompute()
	config := testConfig()
	config.ServiceAccount = "runner@my-ci-project.iam.gserviceaccount.com"
	config.Scopes = []string{cloudPlatformScope}
	p := newTestProvider(t, config, f)

	spec := testMachine("rungar-c4-m8-1a2b3c4d", "rungar-c4-m8")
	runner := testRunner()
	runner.NetworkTags = []string{"runners"}
	runner.Labels = map[string]string{"team": "ci"}
	runner.Metadata = map[string]string{"enable-oslogin": "true"}
	spec.Runner = runner

	if err := p.Create(context.Background(), spec); err != nil {
		t.Fatalf("Create() = %v", err)
	}

	instance := f.instance("europe-west1-b", spec.Name)
	if instance == nil {
		t.Fatal("no instance in europe-west1-b, the first zone")
	}

	checks := []struct {
		what      string
		got, want any
	}{
		{"machine type", instance.MachineType, "https://www.googleapis.com/compute/v1/projects/p/zones/europe-west1-b/machineTypes/e2-standard-4"},
		{"image", instance.Disks[0].InitializeParams.SourceImage, "projects/my-ci-project/global/images/runner-image"},
		{"disk size", instance.Disks[0].InitializeParams.DiskSizeGb, int64(50)},
		{"disk type", instance.Disks[0].InitializeParams.DiskType, "zones/europe-west1-b/diskTypes/pd-balanced"},
		{"network", instance.NetworkInterfaces[0].Network, "global/networks/default"},
		{"external address", len(instance.NetworkInterfaces[0].AccessConfigs), 1},
		{"restarted", *instance.Scheduling.AutomaticRestart, false},
		{"host maintenance", instance.Scheduling.OnHostMaintenance, "MIGRATE"},
		{"tags", strings.Join(instance.Tags.Items, ","), "runners"},
		{"service account", instance.ServiceAccounts[0].Email, config.ServiceAccount},
		{"registration", metadataValue(instance, jitConfigKey), spec.JITConfig},
		{"startup script", metadataValue(instance, startupScriptKey), defaultStartupScript},
		{"extra metadata", metadataValue(instance, "enable-oslogin"), "true"},
		{"managed label", instance.Labels["rungar_sh_managed"], "true"},
		{"scale set label", instance.Labels["rungar_sh_scale-set"], "rungar-c4-m8"},
		{"extra label", instance.Labels["team"], "ci"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.what, c.got, c.want)
		}
	}
}

func TestCreateOnSpot(t *testing.T) {
	f := newFakeCompute()
	p := newTestProvider(t, testConfig(), f)

	spec := testMachine("rungar-spot-1", "rungar-spot")
	runner := testRunner()
	runner.Spot = true
	spec.Runner = runner

	if err := p.Create(context.Background(), spec); err != nil {
		t.Fatalf("Create() = %v", err)
	}

	s := f.instance("europe-west1-b", spec.Name).Scheduling
	if s.ProvisioningModel != "SPOT" || s.InstanceTerminationAction != "DELETE" || s.OnHostMaintenance != "TERMINATE" {
		t.Errorf("scheduling = %+v, want a Spot instance deleted when taken back and stopped when maintained", s)
	}
}

func TestCreateWithoutAnExternalAddress(t *testing.T) {
	f := newFakeCompute()
	config := testConfig()
	config.ExternalIP = new(bool)
	config.Subnetwork = "runners"
	p := newTestProvider(t, config, f)

	spec := testMachine("rungar-private-1", "rungar-private")
	if err := p.Create(context.Background(), spec); err != nil {
		t.Fatalf("Create() = %v", err)
	}

	networkInterface := f.instance("europe-west1-b", spec.Name).NetworkInterfaces[0]
	if len(networkInterface.AccessConfigs) != 0 || networkInterface.Network != "" || networkInterface.Subnetwork != "regions/europe-west1/subnetworks/runners" {
		t.Errorf("network interface = %+v, want the subnetwork and no external address", networkInterface)
	}
}

func TestCreateTriesTheNextZoneWhenOneIsOutOfStock(t *testing.T) {
	f := newFakeCompute()
	f.failures["europe-west1-b"] = failure{code: "ZONE_RESOURCE_POOL_EXHAUSTED", leaves: true}
	f.failures["europe-west1-c"] = failure{code: "ZONE_RESOURCE_POOL_EXHAUSTED_WITH_DETAILS"}
	p := newTestProvider(t, testConfig(), f)

	spec := testMachine("rungar-c4-m8-1a2b3c4d", "rungar-c4-m8")
	if err := p.Create(context.Background(), spec); err != nil {
		t.Fatalf("Create() = %v, want the instance created in europe-west1-d", err)
	}

	if f.instance("europe-west1-d", spec.Name) == nil {
		t.Error("no instance in europe-west1-d")
	}
	if f.instance("europe-west1-b", spec.Name) != nil {
		t.Error("the instance europe-west1-b left when out of stock was not deleted")
	}
}

// TestCreateTriesTheNextZoneWhenOneRefuses checks an operation failing for a
// reason that says nothing of the zone sends the runner on to the next zone.
func TestCreateTriesTheNextZoneWhenOneRefuses(t *testing.T) {
	f := newFakeCompute()
	f.failures["europe-west1-b"] = failure{code: "INTERNAL_ERROR"}
	f.failures["europe-west1-c"] = failure{code: "INTERNAL_ERROR", leaves: true}
	p := newTestProvider(t, testConfig(), f)

	spec := testMachine("rungar-c4-m8-1a2b3c4d", "rungar-c4-m8")
	if err := p.Create(context.Background(), spec); err != nil {
		t.Fatalf("Create() = %v, want the instance created in europe-west1-d", err)
	}

	if f.instance("europe-west1-d", spec.Name) == nil {
		t.Error("no instance in europe-west1-d")
	}
	if f.instance("europe-west1-c", spec.Name) != nil {
		t.Error("the instance europe-west1-c left when refusing was not deleted")
	}
}

// TestCreateOfANameTakenInTheZone checks an instance whose name the zone
// already has is refused, not taken for the runner's.
func TestCreateOfANameTakenInTheZone(t *testing.T) {
	f := newFakeCompute()
	config := testConfig()
	config.Zones = config.Zones[:1]
	p := newTestProvider(t, config, f)

	spec := testMachine("rungar-x-1", "rungar-x")
	if err := p.Create(context.Background(), spec); err != nil {
		t.Fatalf("Create() = %v", err)
	}

	err := p.Create(context.Background(), spec)
	if err == nil || errors.Is(err, errdefs.ErrNoCapacity) {
		t.Fatalf("Create() of a name taken = %v, want an error, not no capacity", err)
	}
	if f.operations != 1 {
		t.Errorf("started %d operations, want 1", f.operations)
	}
}

// insertsIn returns a pattern matching the path of inserts in a zone.
func insertsIn(zone string) *regexp.Regexp {
	return regexp.MustCompile(`^/projects/[^/]+/zones/` + regexp.QuoteMeta(zone) + `/instances$`)
}

// TestCreateResendsAnInsertWhoseAnswerIsLost checks an insert Compute Engine
// carried out, but whose answer was lost, is sent again with the same request
// ID, which has Compute Engine answer it without creating a second instance.
func TestCreateResendsAnInsertWhoseAnswerIsLost(t *testing.T) {
	f := newFakeCompute()
	f.lostAnswers["europe-west1-b"] = 1
	p := newTestProvider(t, testConfig(), f)

	spec := testMachine("rungar-c4-m8-1a2b3c4d", "rungar-c4-m8")
	if err := p.Create(context.Background(), spec); err != nil {
		t.Fatalf("Create() = %v", err)
	}

	if n := f.count(http.MethodPost, insertsIn("europe-west1-b")); n != 2 {
		t.Errorf("sent %d inserts to europe-west1-b, want 2", n)
	}
	if n := f.count(http.MethodPost, instancesPath); n != 2 {
		t.Errorf("sent %d inserts, want 2, none to another zone", n)
	}
	if zone, _ := createdAs(f, spec.Name); zone != "europe-west1-b" {
		t.Errorf("created in %q, want europe-west1-b", zone)
	}
	if f.operations != 1 {
		t.Errorf("started %d operations, want 1: the repeat is answered with the first's", f.operations)
	}
}

// TestCreateStopsWhenComputeEngineDoesNotSayWhetherItCreatedTheInstance
// checks an insert whose outcome stays unknown is sent a bounded number of
// times, in its zone alone: the next zone could create a second instance.
func TestCreateStopsWhenComputeEngineDoesNotSayWhetherItCreatedTheInstance(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(f *fakeCompute)
	}{
		{"server error", func(f *fakeCompute) {
			f.failures["europe-west1-b"] = failure{status: http.StatusServiceUnavailable, reason: "backendError"}
		}},
		{"every answer lost", func(f *fakeCompute) {
			f.lostAnswers["europe-west1-b"] = maxInsertRequests
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeCompute()
			tt.prepare(f)
			p := newTestProvider(t, testConfig(), f)

			err := p.Create(context.Background(), testMachine("rungar-c4-m8-1a2b3c4d", "rungar-c4-m8"))
			if err == nil || !strings.Contains(err.Error(), "Compute Engine did not say whether it created the instance") {
				t.Fatalf("Create() = %v, want Compute Engine not saying whether it created the instance", err)
			}
			if errors.Is(err, errdefs.ErrNoCapacity) || errors.Is(err, errdefs.ErrInvalidArgument) {
				t.Errorf("Create() = %v, want neither no capacity nor invalid argument", err)
			}

			if n := f.count(http.MethodPost, instancesPath); n != maxInsertRequests {
				t.Errorf("sent %d inserts, want %d", n, maxInsertRequests)
			}
			if n := f.count(http.MethodPost, insertsIn("europe-west1-b")); n != maxInsertRequests {
				t.Errorf("sent %d inserts to europe-west1-b, want all %d", n, maxInsertRequests)
			}
		})
	}
}

// TestCreatePausesBetweenResends checks an insert whose outcome is unknown is
// sent again after a pause, doubling each time, not at once.
func TestCreatePausesBetweenResends(t *testing.T) {
	f := newFakeCompute()
	f.lostAnswers["europe-west1-b"] = maxInsertRequests - 1
	p := newTestProvider(t, testConfig(), f)
	p.retryDelay = 20 * time.Millisecond

	start := time.Now()
	if err := p.Create(context.Background(), testMachine("rungar-x-1", "rungar-x")); err != nil {
		t.Fatalf("Create() = %v", err)
	}

	// Two pauses: 20ms, then 40ms.
	if elapsed := time.Since(start); elapsed < 60*time.Millisecond {
		t.Errorf("Create() took %v, want at least 60ms of pauses", elapsed)
	}
	if n := f.count(http.MethodPost, instancesPath); n != maxInsertRequests {
		t.Errorf("sent %d inserts, want %d", n, maxInsertRequests)
	}
}

// singleZoneProvider returns a provider of europe-west1-b alone over the fake,
// one of several gcp providers in one project.
func singleZoneProvider(t *testing.T, f *fakeCompute) *Provider {
	t.Helper()

	config := testConfig()
	config.Zones = config.Zones[:1]

	return newTestProvider(t, config, f)
}

// TestCreateOnAnotherProviderAfterAStockout checks a runner one provider was
// refused for a stockout is really tried by another provider in the project,
// under the same name, in the same zone, as of the same machine type: its
// insert is not taken for a repeat of the first's and answered with its
// stockout.
func TestCreateOnAnotherProviderAfterAStockout(t *testing.T) {
	f := newFakeCompute()
	spot, standard := singleZoneProvider(t, f), singleZoneProvider(t, f)
	spec := testMachine("rungar-x-1", "rungar-x")

	f.failures["europe-west1-b"] = failure{code: "ZONE_RESOURCE_POOL_EXHAUSTED"}
	if err := spot.Create(context.Background(), spec); !errors.Is(err, errdefs.ErrNoCapacity) {
		t.Fatalf("Create() on the first provider = %v, want no capacity", err)
	}

	f.mu.Lock()
	delete(f.failures, "europe-west1-b")
	f.mu.Unlock()

	if err := standard.Create(context.Background(), spec); err != nil {
		t.Fatalf("Create() on the second provider = %v, want nil", err)
	}
	if f.instance("europe-west1-b", spec.Name) == nil {
		t.Error("no instance in europe-west1-b")
	}
}

// TestCreateOnAnotherProviderAfterALostAnswer checks a runner whose instance
// one provider created without learning it, and which was then deleted, gets
// an instance of its own from another provider in the project, rather than
// the first's operation and no instance.
func TestCreateOnAnotherProviderAfterALostAnswer(t *testing.T) {
	f := newFakeCompute()
	first, second := singleZoneProvider(t, f), singleZoneProvider(t, f)
	spec := testMachine("rungar-x-1", "rungar-x")

	f.lostAnswers["europe-west1-b"] = maxInsertRequests
	err := first.Create(context.Background(), spec)
	if err == nil || !strings.Contains(err.Error(), "did not say whether it created the instance") {
		t.Fatalf("Create() on the first provider = %v, want Compute Engine not saying whether it created the instance", err)
	}

	// The fleet deletes what the first provider may have left.
	if err := first.Delete(context.Background(), spec.Name); err != nil {
		t.Fatalf("Delete() = %v", err)
	}

	if err := second.Create(context.Background(), spec); err != nil {
		t.Fatalf("Create() on the second provider = %v, want nil", err)
	}
	if f.instance("europe-west1-b", spec.Name) == nil {
		t.Error("no instance in europe-west1-b: the second provider was answered with the first's operation")
	}
}

// TestCreateStopsWhenAZoneMayStillCreateIt checks a runner whose instance a
// zone accepted, but never said it created, is not created in the next zone
// too.
func TestCreateStopsWhenAZoneMayStillCreateIt(t *testing.T) {
	f := newFakeCompute()
	f.waitFailures["europe-west1-b"] = []int{http.StatusServiceUnavailable, http.StatusForbidden}
	p := newTestProvider(t, testConfig(), f)

	err := p.Create(context.Background(), testMachine("rungar-c4-m8-1a2b3c4d", "rungar-c4-m8"))
	if err == nil || !strings.Contains(err.Error(), "did not say whether it created the instance") {
		t.Fatalf("Create() = %v, want Compute Engine not saying whether it created the instance", err)
	}
	if n := f.count(http.MethodPost, instancesPath); n != 1 {
		t.Errorf("sent %d inserts, want 1", n)
	}
	if n := f.count(http.MethodPost, waitPath); n != 2 {
		t.Errorf("waited %d times, want 2: again after the server error, not after the refusal", n)
	}
}

// TestCreateWaitsAgainWhenAWaitFails checks a wait for an operation that
// fails for a reason that may pass is made again, not taken for an unknown
// outcome: the fleet would delete the instance being created.
func TestCreateWaitsAgainWhenAWaitFails(t *testing.T) {
	f := newFakeCompute()
	f.waitFailures["europe-west1-b"] = []int{http.StatusServiceUnavailable, http.StatusTooManyRequests}
	f.runningWaits["europe-west1-b"] = 1
	p := newTestProvider(t, testConfig(), f)

	spec := testMachine("rungar-c4-m8-1a2b3c4d", "rungar-c4-m8")
	if err := p.Create(context.Background(), spec); err != nil {
		t.Fatalf("Create() = %v, want nil", err)
	}
	if f.instance("europe-west1-b", spec.Name) == nil {
		t.Error("no instance in europe-west1-b")
	}
	if n := f.count(http.MethodPost, waitPath); n != 4 {
		t.Errorf("waited %d times, want 4: two failures, the operation running, then done", n)
	}
}

// TestCreateStopsWhenAnInsertTimesOut checks an insert request is bounded by
// the provider's timeout, and that a zone that may have taken it, having never
// answered, is asked again a bounded number of times and not followed by the
// next.
func TestCreateStopsWhenAnInsertTimesOut(t *testing.T) {
	f := newFakeCompute()
	f.hangingInserts["europe-west1-b"] = true
	config := testConfig()
	config.Timeout = 100 * time.Millisecond
	p := newTestProvider(t, config, f)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := p.Create(ctx, testMachine("rungar-c4-m8-1a2b3c4d", "rungar-c4-m8"))
	if err == nil || errors.Is(err, errdefs.ErrNoCapacity) || ctx.Err() != nil {
		t.Fatalf("Create() = %v, want an error before the context's end", err)
	}
	if n := f.count(http.MethodPost, insertsIn("europe-west1-b")); n != maxInsertRequests {
		t.Errorf("sent %d inserts to europe-west1-b, want %d", n, maxInsertRequests)
	}
	if n := f.count(http.MethodPost, instancesPath); n != maxInsertRequests {
		t.Errorf("sent %d inserts, want %d, none to another zone", n, maxInsertRequests)
	}
}

func TestCreateIsNoCapacityWhenEveryZoneIsOutOfStock(t *testing.T) {
	f := newFakeCompute()
	f.failures["europe-west1-b"] = failure{code: "ZONE_RESOURCE_POOL_EXHAUSTED_WITH_DETAILS"}
	f.failures["europe-west1-c"] = failure{code: "ZONE_RESOURCE_POOL_EXHAUSTED"}
	f.failures["europe-west1-d"] = failure{code: "ZONE_RESOURCE_POOL_EXHAUSTED"}
	p := newTestProvider(t, testConfig(), f)

	err := p.Create(context.Background(), testMachine("rungar-c4-m8-1a2b3c4d", "rungar-c4-m8"))
	if !errors.Is(err, errdefs.ErrNoCapacity) {
		t.Fatalf("Create() = %v, want no capacity", err)
	}
	for _, zone := range testConfig().Zones {
		if !strings.Contains(err.Error(), zone) {
			t.Errorf("error %q does not say why %s refused", err, zone)
		}
	}
}

// TestCreateStopsAtAnExceededQuota checks the other zones are not tried: a
// quota is the region's or the project's, so they would refuse the same.
func TestCreateStopsAtAnExceededQuota(t *testing.T) {
	tests := []struct {
		name    string
		failure failure
	}{
		// As Compute Engine says it: the operation fails.
		{"operation", failure{code: "QUOTA_EXCEEDED", leaves: true}},
		{"request", failure{status: http.StatusForbidden, reason: "quotaExceeded"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeCompute()
			f.failures["europe-west1-b"] = tt.failure
			p := newTestProvider(t, testConfig(), f)

			spec := testMachine("rungar-c4-m8-1a2b3c4d", "rungar-c4-m8")
			if err := p.Create(context.Background(), spec); !errors.Is(err, errdefs.ErrNoCapacity) {
				t.Fatalf("Create() = %v, want no capacity", err)
			}

			if n := f.count(http.MethodPost, instancesPath); n != 1 {
				t.Errorf("tried %d zones, want 1", n)
			}
			if f.instance("europe-west1-b", spec.Name) != nil {
				t.Error("the instance europe-west1-b left when out of quota was not deleted")
			}
		})
	}
}

func TestCreateSaysWhyItRefused(t *testing.T) {
	tests := []struct {
		name    string
		failure failure
		invalid bool
		tried   int
	}{
		{"invalid request", failure{status: http.StatusBadRequest, reason: "invalid"}, true, 1},
		{"image not found", failure{status: http.StatusNotFound, reason: "notFound"}, true, 1},
		{"invalid field", failure{code: "INVALID_FIELD_VALUE"}, true, 1},
		{"missing resource", failure{code: "RESOURCE_NOT_FOUND"}, true, 1},
		{"forbidden", failure{status: http.StatusForbidden, reason: "forbidden"}, false, 1},
		{"rate limited", failure{status: http.StatusTooManyRequests, reason: "rateLimitExceeded"}, false, 1},
		{"internal error", failure{code: "INTERNAL_ERROR", leaves: true}, false, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeCompute()
			for _, zone := range testConfig().Zones {
				f.failures[zone] = tt.failure
			}
			p := newTestProvider(t, testConfig(), f)

			err := p.Create(context.Background(), testMachine("rungar-x-1", "rungar-x"))
			if err == nil {
				t.Fatal("Create() = nil, want an error")
			}

			if errors.Is(err, errdefs.ErrNoCapacity) {
				t.Errorf("Create() = %v, want not no capacity: only a full zone is", err)
			}
			if got := errors.Is(err, errdefs.ErrInvalidArgument); got != tt.invalid {
				t.Errorf("Create() = %v; invalid argument %v, want %v", err, got, tt.invalid)
			}

			// A runner that can never be created, or whose request every zone
			// would refuse, is tried in one zone; one whose operation a zone
			// refused, in every zone.
			if n := f.count(http.MethodPost, instancesPath); n != tt.tried {
				t.Errorf("tried %d zones, want %d", n, tt.tried)
			}
			for _, zone := range testConfig().Zones {
				if f.instance(zone, "rungar-x-1") != nil {
					t.Errorf("the instance %s left was not deleted", zone)
				}
			}
		})
	}
}

func TestCreateRefusesAnotherTypesRunner(t *testing.T) {
	p := newTestProvider(t, testConfig(), newFakeCompute())

	spec := testMachine("rungar-x-1", "rungar-x")
	spec.Runner = otherRunner{}

	if err := p.Create(context.Background(), spec); !errors.Is(err, errdefs.ErrInvalidArgument) {
		t.Errorf("Create() = %v, want invalid argument", err)
	}
}
