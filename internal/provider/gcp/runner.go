// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package gcp

import (
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/provider"
	"github.com/konradasb/rungar/internal/types"
)

// RunnerSpec is a gcp runner block: the Compute Engine instance a runner gets.
// The runner's name, labels and registration are Rungar's, not the block's.
type RunnerSpec struct {
	// MachineTypes is the instance's machine type: e2-standard-4, or a custom
	// one such as e2-custom-4-8192. A list, such as [c3-standard-4,
	// n2-standard-4], is tried in order in each zone before the next zone: a
	// zone out of one type, or without it, may have the next, which on Spot
	// finds more capacity than more zones do. It is required, unless
	// instance_template gives it; with one, it is used over the template's.
	//
	// The JSON name is the one it had as a string, so that a runner's
	// revision does not change.
	MachineTypes provider.OneOrMore `yaml:"machine_type,omitempty" json:"MachineType"`

	// Image is the boot disk's source image, which must carry the Actions
	// runner: a URL such as projects/my-project/global/images/family/runner,
	// or the name of an image in the provider's project. It is required, and
	// refused with instance_template, whose disks are the instance's.
	Image string `yaml:"image,omitempty"`

	// DiskSize is the boot disk, where a job's checkout and build output go,
	// in whole GiB of at least 10GiB. Unset is 50GiB. Refused with
	// instance_template.
	DiskSize types.Size `yaml:"disk_size,omitempty"`

	// DiskType is the boot disk's type: pd-balanced, pd-ssd, pd-standard or
	// hyperdisk-balanced. Unset is pd-balanced. Refused with
	// instance_template.
	DiskType string `yaml:"disk_type,omitempty"`

	// InstanceTemplate is an instance template the instance is created from,
	// for what the other keys do not cover: GPUs, local SSDs, Shielded and
	// Confidential VMs, a minimum CPU platform. It is a URL such as
	// projects/my-project/global/instanceTemplates/runner-gpu, global or in
	// the provider's region, or the name of a global template in the
	// provider's project. The template gives the disks, and the network and
	// service account unless the provider sets them; its labels, metadata
	// and network tags are kept, with the runner's added. A template's
	// startup-script is replaced by startup_script.
	InstanceTemplate string `yaml:"instance_template,omitempty" json:",omitempty"`

	// Spot makes the instance a Spot VM: much cheaper, and deleted when
	// Compute Engine needs the capacity back, failing the job it runs. An
	// instance_template's Spot instances are Spot whatever this says.
	Spot bool `yaml:"spot,omitempty"`

	// NetworkTags are the instance's network tags, which firewall rules
	// select instances by.
	NetworkTags []string `yaml:"network_tags,omitempty"`

	// Labels are added to the instance's labels, for billing and search.
	// Keys and values are lower case letters, digits, _ and -, and keys
	// starting rungar_ are Rungar's.
	Labels map[string]string `yaml:"labels,omitempty"`

	// Metadata is added to the instance's metadata. startup-script and the
	// keys starting rungar- are Rungar's; see startup_script.
	Metadata map[string]string `yaml:"metadata,omitempty"`

	// StartupScript is the instance's startup-script: what starts the
	// runner. Unset runs the Actions runner in /home/runner as the runner
	// user, with the registration from the instance's metadata, and powers
	// the instance off when it exits.
	StartupScript string `yaml:"startup_script,omitempty"`
}

const (
	// defaultDiskSize is the boot disk of a runner whose block sets none.
	defaultDiskSize types.Size = 50 << 30

	// minDiskSize is the smallest boot disk Compute Engine creates.
	minDiskSize types.Size = 10 << 30

	// defaultDiskType is the boot disk type of a runner whose block sets
	// none.
	defaultDiskType = "pd-balanced"
)

// defaultStartupScript runs the Actions runner once, registered with the
// just-in-time configuration in the instance's metadata, and powers the
// instance off when it exits, for Rungar to delete.
const defaultStartupScript = `#!/bin/bash
# Rungar's startup script: runs the Actions runner once, then powers off.
set -u
trap 'shutdown -h now' EXIT
jit=$(curl -fsS -H 'Metadata-Flavor: Google' \
  http://metadata.google.internal/computeMetadata/v1/instance/attributes/` + jitConfigKey + `) || exit 1
export ACTIONS_RUNNER_INPUT_JITCONFIG="$jit" HOME=/home/runner
cd /home/runner && runuser -u runner -p -- ./run.sh
`

// machineTypePattern matches a machine type's name.
var machineTypePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*[a-z0-9]$`)

var _ types.RunnerSpec = RunnerSpec{}

// Describe returns the runner's machine types and instance template.
func (s RunnerSpec) Describe() string {
	description := s.MachineTypes.String()
	switch {
	case s.InstanceTemplate == "":
	case description == "":
		description = "template " + path.Base(s.InstanceTemplate)
	default:
		description += " from template " + path.Base(s.InstanceTemplate)
	}

	if s.Spot {
		return description + " (spot)"
	}

	return description
}

// withDefaults returns the spec with the default startup script, and the
// default disk unless an instance template gives the disks.
func (s RunnerSpec) withDefaults() RunnerSpec {
	if s.DiskSize == 0 && s.InstanceTemplate == "" {
		s.DiskSize = defaultDiskSize
	}
	if s.DiskType == "" && s.InstanceTemplate == "" {
		s.DiskType = defaultDiskType
	}
	if s.StartupScript == "" {
		s.StartupScript = defaultStartupScript
	}

	return s
}

// Validate checks the spec describes an instance Compute Engine can create.
func (s RunnerSpec) Validate() error {
	validate := s.validateDisk
	if s.InstanceTemplate != "" {
		validate = s.validateInstanceTemplate
	}
	if err := validate(); err != nil {
		return err
	}

	for i, machineType := range s.MachineTypes {
		switch {
		case !machineTypePattern.MatchString(machineType):
			return errdefs.InvalidArgument("invalid machine_type %q", machineType)
		case slices.Contains(s.MachineTypes[:i], machineType):
			return errdefs.InvalidArgument("machine_type %s is listed twice", machineType)
		}
	}

	for key, value := range s.Labels {
		switch {
		case !computeLabelKeyPattern.MatchString(key) || !computeLabelValuePattern.MatchString(value):
			return errdefs.InvalidArgument("label %s=%s: keys and values are lower case letters, digits, _ "+
				"and -, keys start with a letter, and both are at most 63 characters", key, value)
		case strings.HasPrefix(key, labelPrefix):
			return errdefs.InvalidArgument("label %s: labels starting %s are Rungar's", key, labelPrefix)
		}
	}

	for key := range s.Metadata {
		switch {
		case key == startupScriptKey:
			return errdefs.InvalidArgument("metadata %s: set startup_script instead", key)
		case strings.HasPrefix(key, metadataPrefix):
			return errdefs.InvalidArgument("metadata %s: keys starting %s are Rungar's", key, metadataPrefix)
		}
	}

	return nil
}

// validateDisk checks a runner without an instance template has a machine
// type and a boot disk Compute Engine can create.
func (s RunnerSpec) validateDisk() error {
	switch {
	case len(s.MachineTypes) == 0:
		return errdefs.InvalidArgument("a runner needs a machine_type, such as e2-standard-4, " +
			"or an instance_template")
	case s.Image == "":
		return errdefs.InvalidArgument("a runner needs an image carrying the Actions runner, " +
			"such as projects/my-project/global/images/family/runner, or an instance_template")
	case s.DiskSize < minDiskSize:
		return errdefs.InvalidArgument("disk_size %s is too small: Compute Engine needs at least %s",
			s.DiskSize, minDiskSize)
	case s.DiskSize%(1<<30) != 0:
		return errdefs.InvalidArgument("disk_size %s is not a whole number of GiB", s.DiskSize)
	}

	return nil
}

// validateInstanceTemplate checks a runner's instance template, and that the
// runner leaves the disks to it: an instance's disks are taken over the
// template's whole, local SSDs and all.
func (s RunnerSpec) validateInstanceTemplate() error {
	if _, ok := parseInstanceTemplateRef(s.InstanceTemplate, ""); !ok {
		return errdefs.InvalidArgument("invalid instance_template %q: want a template's name, or a URL "+
			"such as projects/my-project/global/instanceTemplates/runner", s.InstanceTemplate)
	}

	keys := []struct {
		name string
		set  bool
	}{
		{"image", s.Image != ""},
		{"disk_size", s.DiskSize != 0},
		{"disk_type", s.DiskType != ""},
	}
	for _, key := range keys {
		if key.set {
			return errdefs.InvalidArgument("%s is set with instance_template %s, whose disks the instance has: "+
				"set it in the template, and if the provider's runner block sets it, clear it in this "+
				"scale set's with %s: null", key.name, s.InstanceTemplate, key.name)
		}
	}

	return nil
}
