// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package gcp

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"google.golang.org/api/compute/v1"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/types"
)

var (
	insertPath = regexp.MustCompile(`/instances$`)
	deletePath = regexp.MustCompile(`/instances/[^/]+$`)
)

// metadataValue returns an instance's metadata item, or "".
func metadataValue(inst *compute.Instance, key string) string {
	for _, item := range inst.Metadata.Items {
		if item.Key == key && item.Value != nil {
			return *item.Value
		}
	}

	return ""
}

func TestCreateMakesTheInstance(t *testing.T) {
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

	inst := f.instance("europe-west1-b", spec.Name)
	if inst == nil {
		t.Fatal("no instance in europe-west1-b, the first zone")
	}

	checks := []struct {
		what      string
		got, want any
	}{
		{"machine type", inst.MachineType, "https://www.googleapis.com/compute/v1/projects/p/zones/europe-west1-b/machineTypes/e2-standard-4"},
		{"image", inst.Disks[0].InitializeParams.SourceImage, "projects/my-ci-project/global/images/runner-image"},
		{"disk size", inst.Disks[0].InitializeParams.DiskSizeGb, int64(50)},
		{"disk type", inst.Disks[0].InitializeParams.DiskType, "zones/europe-west1-b/diskTypes/pd-balanced"},
		{"network", inst.NetworkInterfaces[0].Network, "global/networks/default"},
		{"external address", len(inst.NetworkInterfaces[0].AccessConfigs), 1},
		{"restarted", *inst.Scheduling.AutomaticRestart, false},
		{"tags", strings.Join(inst.Tags.Items, ","), "runners"},
		{"service account", inst.ServiceAccounts[0].Email, config.ServiceAccount},
		{"registration", metadataValue(inst, jitConfigKey), spec.JITConfig},
		{"startup script", metadataValue(inst, startupScriptKey), defaultStartupScript},
		{"extra metadata", metadataValue(inst, "enable-oslogin"), "true"},
		{"managed label", inst.Labels["rungar_sh_managed"], "true"},
		{"scale set label", inst.Labels["rungar_sh_scale-set"], "rungar-c4-m8"},
		{"extra label", inst.Labels["team"], "ci"},
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
	if s.ProvisioningModel != "SPOT" || s.InstanceTerminationAction != "DELETE" {
		t.Errorf("scheduling = %+v, want a Spot instance deleted when taken back", s)
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

	nic := f.instance("europe-west1-b", spec.Name).NetworkInterfaces[0]
	if len(nic.AccessConfigs) != 0 || nic.Network != "" || nic.Subnetwork != "regions/europe-west1/subnetworks/runners" {
		t.Errorf("network interface = %+v, want the subnetwork and no external address", nic)
	}
}

func TestCreateTriesTheNextZoneWhenOneIsFull(t *testing.T) {
	f := newFakeCompute()
	f.failures["europe-west1-b"] = failure{code: "ZONE_RESOURCE_POOL_EXHAUSTED", leaves: true}
	f.failures["europe-west1-c"] = failure{status: http.StatusForbidden, reason: "quotaExceeded"}
	p := newTestProvider(t, testConfig(), f)

	spec := testMachine("rungar-c4-m8-1a2b3c4d", "rungar-c4-m8")
	if err := p.Create(context.Background(), spec); err != nil {
		t.Fatalf("Create() = %v, want the instance made in europe-west1-d", err)
	}

	if f.instance("europe-west1-d", spec.Name) == nil {
		t.Error("no instance in europe-west1-d")
	}
	if f.instance("europe-west1-b", spec.Name) != nil {
		t.Error("the instance europe-west1-b left when out of stock was not removed")
	}
}

func TestCreateIsNoCapacityWhenEveryZoneIsFull(t *testing.T) {
	f := newFakeCompute()
	f.failures["europe-west1-b"] = failure{code: "ZONE_RESOURCE_POOL_EXHAUSTED_WITH_DETAILS"}
	f.failures["europe-west1-c"] = failure{code: "QUOTA_EXCEEDED"}
	f.failures["europe-west1-d"] = failure{status: http.StatusForbidden, reason: "quotaExceeded"}
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

func TestCreateSaysWhyItRefused(t *testing.T) {
	tests := []struct {
		name    string
		failure failure
		invalid bool
	}{
		{"invalid request", failure{status: http.StatusBadRequest, reason: "invalid"}, true},
		{"image not found", failure{status: http.StatusNotFound, reason: "notFound"}, true},
		{"invalid field", failure{code: "INVALID_FIELD_VALUE"}, true},
		{"missing resource", failure{code: "RESOURCE_NOT_FOUND"}, true},
		{"forbidden", failure{status: http.StatusForbidden, reason: "forbidden"}, false},
		{"internal error", failure{code: "INTERNAL_ERROR"}, false},
		{"server error", failure{status: http.StatusInternalServerError, reason: "backendError"}, false},
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

			// Only a full zone sends the runner to the next.
			if n := f.count(http.MethodPost, insertPath); n != 1 {
				t.Errorf("tried %d zones, want 1", n)
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

func TestListReturnsTheScaleSetsMachines(t *testing.T) {
	f := newFakeCompute()
	f.failures["europe-west1-b"] = failure{code: "ZONE_RESOURCE_POOL_EXHAUSTED"}
	p := newTestProvider(t, testConfig(), f)
	ctx := context.Background()

	mine := testMachine("rungar-c4-m8-1a2b3c4d", "rungar-c4-m8")
	other := testMachine("rungar-c2-m4-5e6f7a8b", "rungar-c2-m4")
	for _, spec := range []types.MachineSpec{mine, other} {
		if err := p.Create(ctx, spec); err != nil {
			t.Fatalf("Create(%s) = %v", spec.Name, err)
		}
	}

	// An instance of somebody else's, labelled the same, but not by Rungar.
	f.mu.Lock()
	f.instances["europe-west1-d"] = map[string]*compute.Instance{"stranger": {
		Name:     "stranger",
		Labels:   gceLabels(mine.Labels),
		Metadata: &compute.Metadata{},
	}}
	f.mu.Unlock()

	selector := types.ScaleSetSelector("gh-4e3651f01be5", "rungar-c4-m8")
	got, err := p.List(ctx, selector)
	if err != nil {
		t.Fatalf("List() = %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("List() = %+v, want the scale set's one machine", got)
	}

	m := got[0]
	want := types.Machine{
		Name:      mine.Name,
		Labels:    mine.Labels,
		State:     types.MachineStarting,
		Size:      "e2-standard-4",
		CreatedAt: time.Date(2026, 9, 29, 17, 0, 0, 0, time.UTC),
	}
	if m.Name != want.Name || !maps.Equal(m.Labels, want.Labels) || m.State != want.State ||
		m.Size != want.Size || !m.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("List()[0] = %+v, want %+v", m, want)
	}

	f.mu.Lock()
	filters := f.filters
	f.mu.Unlock()
	if last := filters[len(filters)-1]; last != labelFilter(selector) {
		t.Errorf("filter = %q, want %q", last, labelFilter(selector))
	}
}

func TestListFailsWhenAZoneCannotBeListed(t *testing.T) {
	f := newFakeCompute()
	f.listFailures["europe-west1-c"] = http.StatusInternalServerError
	p := newTestProvider(t, testConfig(), f)

	if _, err := p.List(context.Background(), map[string]string{types.LabelManaged: "true"}); err == nil {
		t.Error("List() = nil error, want one: what europe-west1-c has is unknown")
	}
}

func TestDelete(t *testing.T) {
	f := newFakeCompute()
	f.failures["europe-west1-b"] = failure{code: "ZONE_RESOURCE_POOL_EXHAUSTED"}
	p := newTestProvider(t, testConfig(), f)
	ctx := context.Background()

	spec := testMachine("rungar-c4-m8-1a2b3c4d", "rungar-c4-m8")
	if err := p.Create(ctx, spec); err != nil {
		t.Fatalf("Create() = %v", err)
	}

	if err := p.Delete(ctx, spec.Name); err != nil {
		t.Fatalf("Delete() = %v", err)
	}
	if f.instance("europe-west1-c", spec.Name) != nil {
		t.Error("the instance is still in europe-west1-c")
	}

	deletes := f.count(http.MethodDelete, deletePath)
	if err := p.Delete(ctx, spec.Name); err != nil {
		t.Errorf("Delete() of an instance already gone = %v, want nil", err)
	}
	if n := f.count(http.MethodDelete, deletePath); n != deletes {
		t.Errorf("deleted %d more times an instance no zone has", n-deletes)
	}
}
