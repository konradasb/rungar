// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package gcp

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	yaml "gopkg.in/yaml.v3"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/provider"
	"github.com/konradasb/rungar/internal/types"
)

func TestParseRunnerFillsInDefaults(t *testing.T) {
	c := testConfig()

	spec, err := c.ParseRunner(node(t, `
machine_type: e2-custom-4-8192
image: projects/my-ci-project/global/images/family/runner
`))
	if err != nil {
		t.Fatalf("ParseRunner() = %v", err)
	}

	r, ok := spec.(RunnerSpec)
	if !ok {
		t.Fatalf("ParseRunner() = %T, want RunnerSpec", spec)
	}
	if r.DiskSize != defaultDiskSize || r.DiskType != defaultDiskType || r.StartupScript != defaultStartupScript {
		t.Errorf("ParseRunner() = %+v, want the default disk and startup script", r)
	}
	if got := spec.Describe(); got != "e2-custom-4-8192" {
		t.Errorf("Describe() = %q, want the machine type", got)
	}
}

func TestParseRunnerRefuses(t *testing.T) {
	const base = "machine_type: e2-standard-4\nimage: runner\n"

	tests := []struct {
		name, yaml, says string
	}{
		{"no machine type", "image: runner", "machine_type"},
		{"bad machine type", "machine_type: E2 Standard\nimage: runner", "invalid machine_type"},
		{"no image", "machine_type: e2-standard-4", "image"},
		{"small disk", base + "disk_size: 5GiB", "too small"},
		{"partial GiB", base + "disk_size: 10.5GiB", "whole number of GiB"},
		{"bad label", base + "labels: {Team: ci}", "label Team"},
		{"rungar label", base + "labels: {rungar_team: ci}", "Rungar's"},
		{"startup script metadata", base + "metadata: {startup-script: x}", "startup_script instead"},
		{"rungar metadata", base + "metadata: {rungar-jitconfig: x}", "Rungar's"},
		{"unknown key", base + "vcpus: 4", "vcpus"},
		{"bad template", "instance_template: Runner GPU", "invalid instance_template"},
		{"template and image", "instance_template: runner-gpu\nimage: runner", "image is set with instance_template"},
		{"template and disk size", "instance_template: runner-gpu\ndisk_size: 100GiB", "disk_size is set"},
		{"template and disk type", "instance_template: runner-gpu\ndisk_type: pd-ssd", "disk_type is set"},
		{
			"template in another region",
			"instance_template: projects/my-ci-project/regions/us-central1/instanceTemplates/runner",
			"not europe-west1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := testConfig().ParseRunner(node(t, tt.yaml))
			if !errors.Is(err, errdefs.ErrInvalidArgument) || !strings.Contains(err.Error(), tt.says) {
				t.Errorf("ParseRunner() = %v, want an invalid argument saying %q", err, tt.says)
			}
		})
	}
}

func TestParseRunnerFromAnInstanceTemplate(t *testing.T) {
	spec, err := testConfig().ParseRunner(node(t, "instance_template: runner-gpu\n"))
	if err != nil {
		t.Fatalf("ParseRunner() = %v", err)
	}

	// No default disk: it would be refused, and the template has the disks.
	r, ok := spec.(RunnerSpec)
	if !ok {
		t.Fatalf("ParseRunner() = %T, want RunnerSpec", spec)
	}
	if r.DiskSize != 0 || r.DiskType != "" || r.StartupScript != defaultStartupScript {
		t.Errorf("ParseRunner() = %+v, want the default startup script and no disk", r)
	}
}

// TestParseRunnerFromAnInstanceTemplateClearsTheProvidersImage checks a scale
// set can use a template under a provider whose runner block names an image.
func TestParseRunnerFromAnInstanceTemplateClearsTheProvidersImage(t *testing.T) {
	base := node(t, "machine_type: e2-standard-4\nimage: runner\n")

	if _, err := testConfig().ParseRunner(provider.Merge(base, node(t, "instance_template: runner-gpu\n"))); err == nil {
		t.Error("ParseRunner() of a template under the provider's image = nil, want it refused")
	}

	over := node(t, "instance_template: runner-gpu\nimage: null\n")
	if _, err := testConfig().ParseRunner(provider.Merge(base, over)); err != nil {
		t.Errorf("ParseRunner() with image: null = %v, want the image cleared", err)
	}
}

func TestDescribeNamesMachineTypesAndTemplate(t *testing.T) {
	tests := []struct {
		runner RunnerSpec
		want   string
	}{
		{RunnerSpec{MachineTypes: provider.OneOrMore{"e2-standard-4"}}, "e2-standard-4"},
		{RunnerSpec{MachineTypes: provider.OneOrMore{"e2-standard-4"}, Spot: true}, "e2-standard-4 (spot)"},
		{RunnerSpec{MachineTypes: provider.OneOrMore{"c3-standard-4", "n2-standard-4"}}, "c3-standard-4 or n2-standard-4"},
		{RunnerSpec{InstanceTemplate: "runner-gpu"}, "template runner-gpu"},
		{
			RunnerSpec{MachineTypes: provider.OneOrMore{"g2-standard-16"}, InstanceTemplate: "projects/p/global/instanceTemplates/runner-gpu", Spot: true},
			"g2-standard-16 from template runner-gpu (spot)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.runner.Describe(); got != tt.want {
				t.Errorf("Describe() of %+v = %q, want %q", tt.runner, got, tt.want)
			}
		})
	}
}

func TestMachineTypesReadsOneOrSeveral(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want provider.OneOrMore
	}{
		{"one", "machine_type: e2-standard-4", provider.OneOrMore{"e2-standard-4"}},
		{"several", "machine_type: [c3-standard-4, n2-standard-4]", provider.OneOrMore{"c3-standard-4", "n2-standard-4"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var r RunnerSpec
			if err := yaml.Unmarshal([]byte(tt.yaml), &r); err != nil {
				t.Fatalf("%s: %v", tt.yaml, err)
			}
			if !slices.Equal(r.MachineTypes, tt.want) {
				t.Errorf("%s = %v, want %v", tt.yaml, r.MachineTypes, tt.want)
			}

			out, err := yaml.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			var again RunnerSpec
			if err := yaml.Unmarshal(out, &again); err != nil || !slices.Equal(again.MachineTypes, tt.want) {
				t.Errorf("%s written as %q, read back as %v, %v", tt.yaml, out, again.MachineTypes, err)
			}
		})
	}

	t.Run("a mapping", func(t *testing.T) {
		var r RunnerSpec
		if err := yaml.Unmarshal([]byte("machine_type: {a: b}"), &r); err == nil {
			t.Error("machine_type of a mapping read, want an error")
		}
	})
}

// TestRunnerRevisionIsAsBefore checks a runner block of one machine type and
// no template has the revision it had when the machine type was a string and
// there was no template, so an upgrade does not replace every idle runner.
func TestRunnerRevisionIsAsBefore(t *testing.T) {
	// The JSON a runner block was before: these fields, in this order.
	type before struct {
		MachineType   string
		Image         string
		DiskSize      types.Size
		DiskType      string
		Spot          bool
		NetworkTags   []string
		Labels        map[string]string
		Metadata      map[string]string
		StartupScript string
	}

	r := testRunner()
	old := before{
		MachineType:   r.MachineTypes[0],
		Image:         r.Image,
		DiskSize:      r.DiskSize,
		DiskType:      r.DiskType,
		StartupScript: r.StartupScript,
	}

	got, err := types.RunnerRevision(r)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	want, err := types.RunnerRevision(rawSpec(b))
	if err != nil {
		t.Fatal(err)
	}

	if got != want {
		gotJSON, _ := json.Marshal(r)
		t.Errorf("revision %s, want %s:\n got %s\nwant %s", got, want, gotJSON, b)
	}
	if strings.Contains(string(b), "InstanceTemplate") {
		t.Error("the old JSON has a template")
	}
}

// rawSpec is a runner spec whose JSON is given.
type rawSpec json.RawMessage

func (rawSpec) Describe() string               { return "" }
func (r rawSpec) MarshalJSON() ([]byte, error) { return r, nil }
