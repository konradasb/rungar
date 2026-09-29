// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package gcp

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"testing"
	"time"

	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/provider"
)

// gpuInstanceTemplate is a global template of a GPU instance: its own disks,
// network, labels, metadata and tags, stopped for host maintenance.
const gpuInstanceTemplate = "projects/my-ci-project/global/instanceTemplates/runner-gpu"

// addGPUInstanceTemplate gives the fake gpuInstanceTemplate.
func addGPUInstanceTemplate(f *fakeCompute) {
	f.instanceTemplates[gpuInstanceTemplate] = &compute.InstanceTemplate{
		Name: "runner-gpu",
		Properties: &compute.InstanceProperties{
			MachineType: "g2-standard-8",
			Labels:      map[string]string{"team": "ml"},
			Metadata: &compute.Metadata{Items: []*compute.MetadataItems{
				{Key: "enable-oslogin", Value: googleapi.String("true")},
				{Key: startupScriptKey, Value: googleapi.String("echo template")},
			}},
			Tags:       &compute.Tags{Items: []string{"gpu", "runners"}},
			Scheduling: &compute.Scheduling{OnHostMaintenance: "TERMINATE"},
			Disks: []*compute.AttachedDisk{
				{Boot: true, InitializeParams: &compute.AttachedDiskInitializeParams{
					Labels: map[string]string{"backup": "none"},
				}},
				{Type: "SCRATCH"},
			},
		},
	}
}

// instanceTemplateRunner returns a runner block of gpuInstanceTemplate, by
// name.
func instanceTemplateRunner() RunnerSpec {
	return RunnerSpec{InstanceTemplate: "runner-gpu"}.withDefaults()
}

func TestCreateFromAnInstanceTemplate(t *testing.T) {
	f := newFakeCompute()
	addGPUInstanceTemplate(f)
	p := newTestProvider(t, testConfig(), f)

	spec := testMachine("rungar-gpu-1a2b3c4d", "rungar-gpu")
	runner := instanceTemplateRunner()
	runner.Labels = map[string]string{"cost-centre": "ci"}
	runner.NetworkTags = []string{"runners", "egress"}
	spec.Runner = runner

	if err := p.Create(context.Background(), spec); err != nil {
		t.Fatalf("Create() = %v", err)
	}

	instance := f.instance("europe-west1-b", spec.Name)
	if instance == nil {
		t.Fatal("no instance in europe-west1-b")
	}

	checks := []struct {
		what      string
		got, want any
	}{
		{"template", f.sources[spec.Name], gpuInstanceTemplate},
		{"machine type", instance.MachineType, "https://www.googleapis.com/compute/v1/projects/p/zones/europe-west1-b/machineTypes/g2-standard-8"},
		// Left to the template, which Compute Engine would otherwise lose.
		{"disks", len(instance.Disks), 2}, // the template's two, and none of Rungar's
		{"network interfaces", len(instance.NetworkInterfaces), 0},
		{"host maintenance", instance.Scheduling.OnHostMaintenance, ""},
		{"restarted", *instance.Scheduling.AutomaticRestart, false},
		// Merged, since Compute Engine would take the instance's whole.
		{"template label", instance.Labels["team"], "ml"},
		{"runner label", instance.Labels["cost-centre"], "ci"},
		{"managed label", instance.Labels["rungar_sh_managed"], "true"},
		{"template metadata", metadataValue(instance, "enable-oslogin"), "true"},
		{"registration", metadataValue(instance, jitConfigKey), spec.JITConfig},
		{"startup script", metadataValue(instance, startupScriptKey), defaultStartupScript},
		{"tags", slices.Equal(instance.Tags.Items, []string{"egress", "gpu", "runners"}), true},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.what, c.got, c.want)
		}
	}

	// The template's disks are created without the instance's labels; Rungar
	// adds them, keeping the disk's own.
	f.mu.Lock()
	disk := f.disks["europe-west1-b"][spec.Name+"-0"]
	f.mu.Unlock()
	if disk.Labels["rungar_sh_scale-set"] != "rungar-gpu" || disk.Labels["backup"] != "none" {
		t.Errorf("boot disk labels = %v, want the instance's and its own", disk.Labels)
	}
}

// TestCreateFromAnInstanceTemplateWhoseDisksCannotBeLabelled checks the labels
// on a template's disks are worth a warning, not the runner.
func TestCreateFromAnInstanceTemplateWhoseDisksCannotBeLabelled(t *testing.T) {
	f := newFakeCompute()
	addGPUInstanceTemplate(f)
	f.diskLabelFailure = http.StatusForbidden
	p := newTestProvider(t, testConfig(), f)

	spec := testMachine("rungar-gpu-1", "rungar-gpu")
	spec.Runner = instanceTemplateRunner()

	if err := p.Create(context.Background(), spec); err != nil {
		t.Errorf("Create() = %v, want nil", err)
	}
}

// TestCreateFromAnInstanceTemplateBoundsEachLabelRequest checks each request
// labelling a template's disks has the provider's timeout to itself, rather
// than all of them sharing it.
func TestCreateFromAnInstanceTemplateBoundsEachLabelRequest(t *testing.T) {
	f := newFakeCompute()
	addGPUInstanceTemplate(f)
	f.diskDelay = 150 * time.Millisecond // two requests: 300ms in all
	config := testConfig()
	config.Timeout = 250 * time.Millisecond
	p := newTestProvider(t, config, f)

	spec := testMachine("rungar-gpu-1", "rungar-gpu")
	spec.Runner = instanceTemplateRunner()

	if err := p.Create(context.Background(), spec); err != nil {
		t.Fatalf("Create() = %v", err)
	}

	f.mu.Lock()
	disk := f.disks["europe-west1-b"][spec.Name+"-0"]
	f.mu.Unlock()
	if disk.Labels["rungar_sh_scale-set"] != "rungar-gpu" {
		t.Errorf("boot disk labels = %v, want the instance's", disk.Labels)
	}
}

func TestCreateFromAnInstanceTemplateWithOverrides(t *testing.T) {
	f := newFakeCompute()
	addGPUInstanceTemplate(f)
	config := testConfig()
	config.Subnetwork = "runners"
	config.ServiceAccount = "runner@my-ci-project.iam.gserviceaccount.com"
	config.Scopes = []string{cloudPlatformScope}
	p := newTestProvider(t, config, f)

	spec := testMachine("rungar-gpu-1a2b3c4d", "rungar-gpu")
	runner := instanceTemplateRunner()
	runner.MachineTypes = provider.OneOrMore{"g2-standard-16"}
	runner.InstanceTemplate = gpuInstanceTemplate
	spec.Runner = runner

	if err := p.Create(context.Background(), spec); err != nil {
		t.Fatalf("Create() = %v", err)
	}

	instance := f.instance("europe-west1-b", spec.Name)
	if got := instance.MachineType; got != "https://www.googleapis.com/compute/v1/projects/p/zones/europe-west1-b/machineTypes/g2-standard-16" {
		t.Errorf("machine type = %s, want the runner's over the template's", got)
	}
	if len(instance.NetworkInterfaces) != 1 || instance.NetworkInterfaces[0].Subnetwork != "regions/europe-west1/subnetworks/runners" {
		t.Errorf("network interfaces = %+v, want the provider's subnetwork", instance.NetworkInterfaces)
	}
	if len(instance.ServiceAccounts) != 1 || instance.ServiceAccounts[0].Email != config.ServiceAccount {
		t.Errorf("service accounts = %+v, want the provider's", instance.ServiceAccounts)
	}
}

// TestCreateFromASpotInstanceTemplate checks a Spot template's instances are
// deleted when taken back, as the provider's own are, not stopped as the
// template may say.
func TestCreateFromASpotInstanceTemplate(t *testing.T) {
	f := newFakeCompute()
	f.instanceTemplates[gpuInstanceTemplate] = &compute.InstanceTemplate{Properties: &compute.InstanceProperties{
		MachineType: "e2-standard-4",
		Scheduling:  &compute.Scheduling{ProvisioningModel: "SPOT", InstanceTerminationAction: "STOP"},
	}}
	p := newTestProvider(t, testConfig(), f)

	spec := testMachine("rungar-spot-1", "rungar-spot")
	spec.Runner = instanceTemplateRunner()

	if err := p.Create(context.Background(), spec); err != nil {
		t.Fatalf("Create() = %v", err)
	}

	s := f.instance("europe-west1-b", spec.Name).Scheduling
	if s.ProvisioningModel != "SPOT" || s.InstanceTerminationAction != "DELETE" {
		t.Errorf("scheduling = %+v, want a Spot instance deleted when taken back", s)
	}
}

func TestCreateReadsAnInstanceTemplateOnce(t *testing.T) {
	f := newFakeCompute()
	addGPUInstanceTemplate(f)
	p := newTestProvider(t, testConfig(), f)

	for _, name := range []string{"rungar-gpu-1", "rungar-gpu-2"} {
		spec := testMachine(name, "rungar-gpu")
		spec.Runner = instanceTemplateRunner()
		if err := p.Create(context.Background(), spec); err != nil {
			t.Fatalf("Create(%s) = %v", name, err)
		}
	}

	if n := f.count(http.MethodGet, instanceTemplatePath); n != 1 {
		t.Errorf("read the template %d times, want once: a template cannot change", n)
	}
}

func TestCreateFromARegionalInstanceTemplate(t *testing.T) {
	const regional = "projects/shared-ci/regions/europe-west1/instanceTemplates/runner"

	f := newFakeCompute()
	f.instanceTemplates[regional] = &compute.InstanceTemplate{Properties: &compute.InstanceProperties{MachineType: "n2-standard-4"}}
	p := newTestProvider(t, testConfig(), f)

	spec := testMachine("rungar-x-1", "rungar-x")
	runner := instanceTemplateRunner()
	runner.InstanceTemplate = "https://www.googleapis.com/compute/v1/" + regional
	spec.Runner = runner

	if err := p.Create(context.Background(), spec); err != nil {
		t.Fatalf("Create() = %v", err)
	}
	if got := f.sources[spec.Name]; got != regional {
		t.Errorf("created from %q, want %q", got, regional)
	}
}

func TestCreateFromAMissingInstanceTemplate(t *testing.T) {
	f := newFakeCompute()
	p := newTestProvider(t, testConfig(), f)

	spec := testMachine("rungar-gpu-1", "rungar-gpu")
	spec.Runner = instanceTemplateRunner()

	err := p.Create(context.Background(), spec)
	if !errors.Is(err, errdefs.ErrInvalidArgument) {
		t.Errorf("Create() = %v, want invalid argument: no zone can create it", err)
	}
	if n := f.count(http.MethodPost, instancesPath); n != 0 {
		t.Errorf("tried %d zones, want none", n)
	}
}

func TestInstanceTemplateIsNamedByNameOrURL(t *testing.T) {
	tests := []struct {
		in   string
		want instanceTemplateRef
		ok   bool
	}{
		{"runner-gpu", instanceTemplateRef{"my-ci", "", "runner-gpu"}, true},
		{"projects/shared/global/instanceTemplates/runner", instanceTemplateRef{"shared", "", "runner"}, true},
		{"projects/shared/regions/europe-west1/instanceTemplates/runner", instanceTemplateRef{"shared", "europe-west1", "runner"}, true},
		{"https://www.googleapis.com/compute/v1/projects/shared/global/instanceTemplates/runner", instanceTemplateRef{"shared", "", "runner"}, true},
		{"https://compute.googleapis.com/compute/v1/projects/shared/global/instanceTemplates/runner", instanceTemplateRef{"shared", "", "runner"}, true},
		{"Runner GPU", instanceTemplateRef{}, false},
		{"projects/shared/global/images/runner", instanceTemplateRef{}, false},
		{"global/instanceTemplates/runner", instanceTemplateRef{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, ok := parseInstanceTemplateRef(tt.in, "my-ci")
			if got != tt.want || ok != tt.ok {
				t.Errorf("parseInstanceTemplateRef(%q) = %+v, %v; want %+v, %v", tt.in, got, ok, tt.want, tt.ok)
			}
		})
	}
}
