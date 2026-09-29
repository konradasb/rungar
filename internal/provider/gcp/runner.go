// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package gcp

import (
	"regexp"
	"strings"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/types"
)

// RunnerSpec is a gcp runner block: the Compute Engine instance a runner gets.
// The runner's name, labels and registration are Rungar's, not the block's.
type RunnerSpec struct {
	// MachineType is the instance's machine type: e2-standard-4, or a custom
	// one such as e2-custom-4-8192. It is required.
	MachineType string `yaml:"machine_type"`

	// Image is the boot disk's source image, which must carry the Actions
	// runner: a URL such as projects/my-project/global/images/family/runner,
	// or the name of an image in the provider's project. It is required.
	Image string `yaml:"image"`

	// DiskSize is the boot disk, where a job's checkout and build output go,
	// in whole GiB of at least 10GiB. Unset is 50GiB.
	DiskSize types.Size `yaml:"disk_size,omitempty"`

	// DiskType is the boot disk's type: pd-balanced, pd-ssd, pd-standard or
	// hyperdisk-balanced. Unset is pd-balanced.
	DiskType string `yaml:"disk_type,omitempty"`

	// Spot makes the instance a Spot VM: much cheaper, and deleted when
	// Compute Engine needs the capacity back, failing the job it runs.
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

	// minDiskSize is the smallest boot disk Compute Engine makes.
	minDiskSize types.Size = 10 << 30

	// defaultDiskType is the boot disk type of a runner whose block sets
	// none.
	defaultDiskType = "pd-balanced"

	// metadataPrefix starts the metadata keys Rungar sets.
	metadataPrefix = "rungar-"

	// startupScriptKey is the metadata key the guest environment runs at
	// boot.
	startupScriptKey = "startup-script"
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

var (
	// machineTypePattern matches a machine type's name.
	machineTypePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*[a-z0-9]$`)

	// gceLabelKeyPattern and gceLabelValuePattern match what Compute Engine
	// allows of a label.
	gceLabelKeyPattern   = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)
	gceLabelValuePattern = regexp.MustCompile(`^[a-z0-9_-]{0,63}$`)
)

// Describe returns the runner's machine type.
func (s RunnerSpec) Describe() string {
	if s.Spot {
		return s.MachineType + " (spot)"
	}

	return s.MachineType
}

// withDefaults returns the spec with the default disk and startup script.
func (s RunnerSpec) withDefaults() RunnerSpec {
	if s.DiskSize == 0 {
		s.DiskSize = defaultDiskSize
	}
	if s.DiskType == "" {
		s.DiskType = defaultDiskType
	}
	if s.StartupScript == "" {
		s.StartupScript = defaultStartupScript
	}

	return s
}

// Validate checks the spec describes an instance Compute Engine can make.
func (s RunnerSpec) Validate() error {
	switch {
	case s.MachineType == "":
		return errdefs.InvalidArgument("a runner needs a machine_type, such as e2-standard-4")
	case !machineTypePattern.MatchString(s.MachineType):
		return errdefs.InvalidArgument("invalid machine_type %q", s.MachineType)
	case s.Image == "":
		return errdefs.InvalidArgument("a runner needs an image carrying the Actions runner, " +
			"such as projects/my-project/global/images/family/runner")
	case s.DiskSize < minDiskSize:
		return errdefs.InvalidArgument("disk_size %s is too small: Compute Engine needs at least %s",
			s.DiskSize, minDiskSize)
	case s.DiskSize%(1<<30) != 0:
		return errdefs.InvalidArgument("disk_size %s is not a whole number of GiB", s.DiskSize)
	}

	for key, value := range s.Labels {
		switch {
		case !gceLabelKeyPattern.MatchString(key) || !gceLabelValuePattern.MatchString(value):
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
