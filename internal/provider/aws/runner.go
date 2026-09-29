// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package aws

import (
	"regexp"
	"strings"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/types"
)

// RunnerSpec is an aws runner block: the EC2 instance a runner gets. The
// runner's name, labels and registration are Rungar's, not the block's.
type RunnerSpec struct {
	// InstanceType is the instance's type: m7i.xlarge, c7g.2xlarge. It is
	// required.
	InstanceType string `yaml:"instance_type"`

	// Image is the ID of the AMI the instance boots, which must carry the
	// Actions runner and be in the provider's region: ami-0a1b2c3d4e5f60718.
	// It is required.
	Image string `yaml:"image"`

	// DiskSize is the root volume, where a job's checkout and build output
	// go, in whole GiB, and at least the AMI's snapshot. Unset is 50GiB.
	DiskSize types.Size `yaml:"disk_size,omitempty"`

	// DiskType is the root volume's type: gp3 or gp2. Unset is gp3.
	DiskType string `yaml:"disk_type,omitempty"`

	// Spot makes the instance a Spot Instance: much cheaper, and terminated
	// when EC2 needs the capacity back, failing the job it runs.
	Spot bool `yaml:"spot,omitempty"`

	// Tags are added to the instance's and its volume's tags, for billing
	// and search. Name, and keys starting rungar.sh/ or aws:, are not
	// allowed.
	Tags map[string]string `yaml:"tags,omitempty"`

	// UserData is the script the instance runs at first boot: what starts
	// the runner. It must start with #!, and Rungar exports the registration
	// as ACTIONS_RUNNER_INPUT_JITCONFIG after that line. Unset runs the
	// Actions runner in /home/runner as the runner user, and powers the
	// instance off when it exits, which terminates it.
	UserData string `yaml:"user_data,omitempty"`
}

const (
	// defaultDiskSize is the root volume of a runner whose block sets none.
	defaultDiskSize types.Size = 50 << 30

	// maxDiskSize is the largest gp2 or gp3 volume.
	maxDiskSize types.Size = 16 << 40

	// defaultDiskType is the root volume type of a runner whose block sets
	// none.
	defaultDiskType = "gp3"

	// labelPrefix starts the keys of Rungar's labels, which are kept as tags.
	labelPrefix = "rungar.sh/"

	// nameTag is the tag EC2's console shows as an instance's name, and
	// which Delete finds an instance by.
	nameTag = "Name"

	// maxTagKeyLen and maxTagValueLen are the most characters a tag's key
	// and value have.
	maxTagKeyLen   = 128
	maxTagValueLen = 256
)

// defaultUserData runs the Actions runner once, registered with the
// just-in-time configuration Rungar exports after its first line, and powers
// the instance off when it exits: the instance is made to terminate on
// shutdown.
const defaultUserData = `#!/bin/bash
# Rungar's user data: runs the Actions runner once, then powers off.
set -u
trap 'shutdown -h now' EXIT
export HOME=/home/runner
cd /home/runner && runuser -u runner -p -- ./run.sh
`

var (
	// instanceTypePattern matches an instance type's name.
	instanceTypePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*\.[a-z0-9-]+$`)

	// imagePattern matches an AMI's ID.
	imagePattern = regexp.MustCompile(`^ami-[0-9a-f]{8,17}$`)
)

// Describe returns the runner's instance type.
func (s RunnerSpec) Describe() string {
	if s.Spot {
		return s.InstanceType + " (spot)"
	}

	return s.InstanceType
}

// withDefaults returns the spec with the default disk and user data.
func (s RunnerSpec) withDefaults() RunnerSpec {
	if s.DiskSize == 0 {
		s.DiskSize = defaultDiskSize
	}
	if s.DiskType == "" {
		s.DiskType = defaultDiskType
	}
	if s.UserData == "" {
		s.UserData = defaultUserData
	}

	return s
}

// Validate checks the spec describes an instance EC2 can make.
func (s RunnerSpec) Validate() error {
	switch {
	case s.InstanceType == "":
		return errdefs.InvalidArgument("a runner needs an instance_type, such as m7i.xlarge")
	case !instanceTypePattern.MatchString(s.InstanceType):
		return errdefs.InvalidArgument("invalid instance_type %q", s.InstanceType)
	case s.Image == "":
		return errdefs.InvalidArgument("a runner needs an image: the ID of an AMI carrying the Actions runner, " +
			"such as ami-0a1b2c3d4e5f60718")
	case !imagePattern.MatchString(s.Image):
		return errdefs.InvalidArgument("invalid image %q: want an AMI ID, such as ami-0a1b2c3d4e5f60718", s.Image)
	case s.DiskSize < 1<<30 || s.DiskSize > maxDiskSize:
		return errdefs.InvalidArgument("disk_size %s: want 1GiB to %s", s.DiskSize, maxDiskSize)
	case s.DiskSize%(1<<30) != 0:
		return errdefs.InvalidArgument("disk_size %s is not a whole number of GiB", s.DiskSize)
	case s.DiskType != "gp3" && s.DiskType != "gp2":
		return errdefs.InvalidArgument("invalid disk_type %q: want gp3 or gp2", s.DiskType)
	case !strings.HasPrefix(s.UserData, "#!"):
		return errdefs.InvalidArgument("user_data must be a script starting with #!, " +
			"after whose first line Rungar exports the registration")
	}

	for key, value := range s.Tags {
		switch {
		case key == "" || len(key) > maxTagKeyLen || len(value) > maxTagValueLen:
			return errdefs.InvalidArgument("tag %s=%s: keys are 1 to %d characters, values at most %d",
				key, value, maxTagKeyLen, maxTagValueLen)
		case key == nameTag:
			return errdefs.InvalidArgument("tag %s: the instance's name is the runner's", key)
		case strings.HasPrefix(key, labelPrefix):
			return errdefs.InvalidArgument("tag %s: tags starting %s are Rungar's", key, labelPrefix)
		case strings.HasPrefix(strings.ToLower(key), "aws:"):
			return errdefs.InvalidArgument("tag %s: tags starting aws: are AWS's", key)
		}
	}

	return nil
}
