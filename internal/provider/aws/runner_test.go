// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package aws

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
	spec, err := testConfig().ParseRunner(node(t, `
instance_type: c7g.2xlarge
image: ami-0a1b2c3d4e5f60718
`))
	if err != nil {
		t.Fatalf("ParseRunner() = %v", err)
	}

	r, ok := spec.(RunnerSpec)
	if !ok {
		t.Fatalf("ParseRunner() = %T, want RunnerSpec", spec)
	}
	if r.DiskSize != defaultDiskSize || r.DiskType != defaultDiskType || r.UserData != defaultUserData {
		t.Errorf("ParseRunner() = %+v, want the default disk and user data", r)
	}
	if got := spec.Describe(); got != "c7g.2xlarge" {
		t.Errorf("Describe() = %q, want the instance type", got)
	}
}

func TestParseRunnerRefuses(t *testing.T) {
	const base = "instance_type: m7i.xlarge\nimage: ami-0a1b2c3d4e5f60718\n"

	tests := []struct {
		name, yaml, says string
	}{
		{"no instance type", "image: ami-0a1b2c3d4e5f60718", "instance_type"},
		{"bad instance type", "instance_type: M7i XLarge\nimage: ami-0a1b2c3d4e5f60718", "invalid instance_type"},
		{"no image", "instance_type: m7i.xlarge", "image"},
		{"image by name", "instance_type: m7i.xlarge\nimage: ubuntu-24.04", "want an AMI ID"},
		{"small disk", base + "disk_size: 512MiB", "disk_size"},
		{"disk over gp3's", base + "disk_size: 65TiB", "want 1GiB to 64TiB for gp3"},
		{"disk over gp2's", base + "disk_size: 17TiB\ndisk_type: gp2", "want 1GiB to 16TiB for gp2"},
		{"partial GiB", base + "disk_size: 10.5GiB", "whole number of GiB"},
		{"bad disk type", base + "disk_type: st1", "invalid disk_type"},
		{"name tag", base + "tags: {Name: x}", "runner's"},
		{"rungar tag", base + "tags: {rungar.sh/team: ci}", "Rungar's"},
		{"aws tag", base + "tags: {aws:team: ci}", "AWS's"},
		{"long tag key", base + "tags: {" + strings.Repeat("k", 129) + ": ci}", "keys are 1 to 128"},
		{"user data not a script", base + "user_data: '#cloud-config'", "starting with #!"},
		{"large user data", base + "user_data: '#!/bin/sh\n" + strings.Repeat("#", maxUserDataSize) + "'", "over the 10240"},
		{"bad instance type in a list", "instance_type: [m7i.xlarge, M6i]\nimage: ami-0a1b2c3d4e5f60718", "invalid instance_type"},
		{"instance type twice", "instance_type: [m7i.xlarge, m7i.xlarge]\nimage: ami-0a1b2c3d4e5f60718", "listed twice"},
		{"instance type a mapping", "instance_type: {m7i: xlarge}\nimage: ami-0a1b2c3d4e5f60718", "a list of strings"},
		{"bad launch template", base + "launch_template: lt", "invalid launch_template"},
		{"bad launch template version", base + "launch_template: runner-gpu:latest", "invalid launch_template"},
		{"template disk without image", "launch_template: runner-gpu\ndisk_size: 100GiB", "set image"},
		{"low IOPS", base + "disk_iops: 2000", "disk_iops 2000: want 3000 to 80000"},
		{"high IOPS", base + "disk_iops: 90000", "disk_iops 90000: want 3000 to 80000"},
		{"IOPS over the disk size", base + "disk_size: 8GiB\ndisk_iops: 5000", "at least 10GiB"},
		{"IOPS over the default disk size", base + "disk_iops: 30000", "at least 60GiB"},
		{"low throughput", base + "disk_throughput: 100", "want 125 to 2000 MiB/s"},
		{"high throughput", base + "disk_throughput: 2500", "want 125 to 2000 MiB/s"},
		{"throughput over the base IOPS", base + "disk_throughput: 1000", "disk_iops to at least 4000"},
		{"throughput over the IOPS", base + "disk_iops: 4000\ndisk_throughput: 1001", "disk_iops to at least 4004"},
		{"IOPS on gp2", base + "disk_type: gp2\ndisk_iops: 4000", "gp2 does not take"},
		{"throughput on gp2", base + "disk_type: gp2\ndisk_throughput: 250", "gp2 does not take"},
		{"template IOPS without image", "launch_template: runner-gpu\ndisk_iops: 4000", "set image"},
		{"template throughput without image", "launch_template: runner-gpu\ndisk_throughput: 250", "set image"},
		{"template throughput out of range", "launch_template: runner-gpu\nimage: ami-0a1b2c3d4e5f60718\n" +
			"disk_throughput: 3000", "want 125 to 2000 MiB/s"},
		{"unknown key", base + "vcpus: 4", "vcpus"},
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

// TestParseRunnerTakesDiskPerformance checks IOPS and throughput within gp3's
// limits are taken, and that a ratio to what a launch template leaves unknown
// is not checked.
func TestParseRunnerTakesDiskPerformance(t *testing.T) {
	const base = "instance_type: m7i.xlarge\nimage: ami-0a1b2c3d4e5f60718\n"

	tests := []struct {
		name, yaml string
	}{
		{"base IOPS on the smallest disk", base + "disk_size: 1GiB\ndisk_iops: 3000"},
		{"largest gp3 disk", base + "disk_size: 64TiB"},
		{"most IOPS for the default disk", base + "disk_iops: 25000"},
		{"most IOPS", base + "disk_size: 160GiB\ndisk_iops: 80000\ndisk_throughput: 2000"},
		{"most throughput at the base IOPS", base + "disk_throughput: 750"},
		{"base throughput on gp3", base + "disk_type: gp3\ndisk_throughput: 125"},
		{"template's IOPS and size", "launch_template: runner-gpu\nimage: ami-0a1b2c3d4e5f60718\n" +
			"disk_throughput: 2000"},
		{"template's size", "launch_template: runner-gpu\nimage: ami-0a1b2c3d4e5f60718\ndisk_iops: 80000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := testConfig().ParseRunner(node(t, tt.yaml)); err != nil {
				t.Errorf("ParseRunner() = %v, want nil", err)
			}
		})
	}
}

// TestParseRunnerCountsTagCharacters checks a tag's length is counted in
// characters, as EC2 counts it, not bytes.
func TestParseRunnerCountsTagCharacters(t *testing.T) {
	body := "instance_type: m7i.xlarge\nimage: ami-0a1b2c3d4e5f60718\ntags: {" + strings.Repeat("ž", 128) + ": ci}"

	if _, err := testConfig().ParseRunner(node(t, body)); err != nil {
		t.Errorf("ParseRunner() with a 128-character tag key = %v, want nil", err)
	}
}

// TestParseRunnerLeavesTheDiskToALaunchTemplate checks a runner with a
// launch template needs neither an instance type nor an image, and gets no
// default root volume over the template's.
func TestParseRunnerLeavesTheDiskToALaunchTemplate(t *testing.T) {
	spec, err := testConfig().ParseRunner(node(t, "launch_template: runner-gpu:3"))
	if err != nil {
		t.Fatalf("ParseRunner() = %v", err)
	}

	r, ok := spec.(RunnerSpec)
	if !ok {
		t.Fatalf("ParseRunner() = %T, want RunnerSpec", spec)
	}
	if r.setsRootVolume() {
		t.Errorf("ParseRunner() = %+v, want no root volume over the template's", r)
	}
	if r.UserData != defaultUserData {
		t.Error("ParseRunner() has no user data, want the default")
	}
}

func TestDescribeNamesTypesTemplateAndSpot(t *testing.T) {
	tests := []struct {
		name string
		edit func(*RunnerSpec)
		want string
	}{
		{"one type", func(*RunnerSpec) {}, "m7i.xlarge"},
		{"spot", func(r *RunnerSpec) { r.Spot = true }, "m7i.xlarge (spot)"},
		{"several types", func(r *RunnerSpec) { r.InstanceTypes = provider.OneOrMore{"m7i.xlarge", "m6i.xlarge"} },
			"m7i.xlarge or m6i.xlarge"},
		{"template", func(r *RunnerSpec) { r.InstanceTypes, r.LaunchTemplate = nil, "runner-gpu" }, "template runner-gpu"},
		{"type from template", func(r *RunnerSpec) { r.LaunchTemplate = "runner-gpu:3" },
			"m7i.xlarge from template runner-gpu:3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := testRunner()
			tt.edit(&r)
			if got := r.Describe(); got != tt.want {
				t.Errorf("Describe() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestRunnerRevisionIsAsBefore checks a runner block of one instance type and
// no launch template has the revision it had when the instance type was a
// string and there was no template, so an upgrade does not replace every
// idle runner.
func TestRunnerRevisionIsAsBefore(t *testing.T) {
	// The JSON a runner block was before: these fields, in this order.
	type before struct {
		InstanceType string
		Image        string
		DiskSize     types.Size
		DiskType     string
		Spot         bool
		Tags         map[string]string
		UserData     string
	}

	r := testRunner()
	old := before{
		InstanceType: r.InstanceTypes[0],
		Image:        r.Image,
		DiskSize:     r.DiskSize,
		DiskType:     r.DiskType,
		UserData:     r.UserData,
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
}

// rawSpec is a runner spec whose JSON is given.
type rawSpec json.RawMessage

func (rawSpec) Describe() string               { return "" }
func (r rawSpec) MarshalJSON() ([]byte, error) { return r, nil }

func TestInstanceTypesReadsOneOrSeveral(t *testing.T) {
	tests := []struct {
		yaml string
		want provider.OneOrMore
	}{
		{"instance_type: m7i.xlarge", provider.OneOrMore{"m7i.xlarge"}},
		{"instance_type: [m7i.xlarge, m6i.xlarge]", provider.OneOrMore{"m7i.xlarge", "m6i.xlarge"}},
	}

	for _, tt := range tests {
		t.Run(tt.yaml, func(t *testing.T) {
			var r RunnerSpec
			if err := yaml.Unmarshal([]byte(tt.yaml), &r); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(r.InstanceTypes, tt.want) {
				t.Errorf("read %v, want %v", r.InstanceTypes, tt.want)
			}

			out, err := yaml.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			var again RunnerSpec
			if err := yaml.Unmarshal(out, &again); err != nil || !slices.Equal(again.InstanceTypes, tt.want) {
				t.Errorf("written as %q, read back as %v, %v", out, again.InstanceTypes, err)
			}
		})
	}
}
