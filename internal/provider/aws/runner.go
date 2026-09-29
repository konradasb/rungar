// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package aws

import (
	"cmp"
	"maps"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/provider"
	"github.com/konradasb/rungar/internal/types"
)

// RunnerSpec is an aws runner block: the EC2 instance a runner gets. The
// runner's name, labels and registration are Rungar's, not the block's.
type RunnerSpec struct {
	// InstanceTypes is the instance's type: m7i.xlarge, c7g.2xlarge. A list,
	// such as [m7i.xlarge, m6i.xlarge], is tried in order in each subnet
	// before the next subnet: a zone out of one type, or without it, may have
	// the next, which on Spot finds more capacity than more subnets do. It is
	// required, unless launch_template gives it; with one, it is used over the
	// template's.
	//
	// The JSON name is the one it had as a string, so that a runner's
	// revision does not change.
	InstanceTypes provider.OneOrMore `yaml:"instance_type,omitempty" json:"InstanceType"`

	// Image is the ID of the AMI the instance boots, which must carry the
	// Actions runner and be in the provider's region: ami-0a1b2c3d4e5f60718.
	// It is required, unless launch_template gives it; with one, it is used
	// over the template's.
	Image string `yaml:"image,omitempty"`

	// DiskSize is the root volume, where a job's checkout and build output
	// go, in whole GiB: at least the AMI's snapshot, and at most 64TiB on
	// gp3 or 16TiB on gp2. Unset is 50GiB, or with launch_template, the
	// template's; setting it there needs image, whose root device it is set
	// on.
	DiskSize types.Size `yaml:"disk_size,omitempty"`

	// DiskType is the root volume's type: gp3 or gp2. Unset is gp3, or with
	// launch_template, the template's; setting it there needs image.
	DiskType string `yaml:"disk_type,omitempty"`

	// DiskIOPS is the root volume's provisioned IOPS, which only gp3 takes:
	// 3000 to 80000, and over 3000 at most 500 per GiB of disk_size. Unset is
	// gp3's baseline of 3000, or with launch_template, the template's; setting
	// it there needs image. With launch_template and no disk_type, it is
	// checked against gp3's limits, and EC2 refuses it if the template's root
	// volume is gp2.
	DiskIOPS int32 `yaml:"disk_iops,omitempty" json:",omitempty"`

	// DiskThroughput is the root volume's provisioned throughput in MiB/s,
	// which only gp3 takes: 125 to 2000, and over 125 at most 0.25 per IOPS,
	// so 750 at the baseline 3000 IOPS. Unset is gp3's baseline of 125, or
	// with launch_template, the template's; setting it there needs image. With
	// launch_template and no disk_type, it is checked against gp3's limits,
	// against disk_iops only if that is set, and EC2 refuses it if the
	// template's root volume is not gp3.
	DiskThroughput int32 `yaml:"disk_throughput,omitempty" json:",omitempty"`

	// LaunchTemplate is an EC2 launch template the instance is created from,
	// for what the other keys do not cover: key pairs, extra volumes,
	// placement groups, capacity reservations. It is the template's ID or
	// name, followed by a colon and a version for other than its default
	// one: runner-gpu, lt-0a1b2c3d4e5f60718:3, runner-gpu:$Latest. The
	// subnet and user data are always Rungar's, and the security groups,
	// public IP and instance profile are the provider's when it sets them;
	// the template's tags and block devices are kept, with the runner's
	// added. A template's security groups go on its network interface, which
	// Rungar's replaces: EC2 refuses those at its top level. A template's
	// Spot instances are Spot whatever spot says.
	LaunchTemplate string `yaml:"launch_template,omitempty" json:",omitempty"`

	// Spot makes the instance a Spot Instance: much cheaper, and terminated
	// when EC2 needs the capacity back, failing the job it runs.
	Spot bool `yaml:"spot,omitempty"`

	// Tags are added to the instance's and its volumes' tags, for billing
	// and search. Name, and keys starting rungar.sh/ or aws:, are not
	// allowed.
	Tags map[string]string `yaml:"tags,omitempty"`

	// UserData is the script the instance runs at first boot: what starts
	// the runner. It must start with #!, and Rungar exports the registration
	// as ACTIONS_RUNNER_INPUT_JITCONFIG after that line. It is at most 10KiB,
	// leaving room in EC2's 16KiB for the registration. Unset runs the
	// Actions runner in /home/runner as the runner user, and powers the
	// instance off when it exits, which terminates it.
	UserData string `yaml:"user_data,omitempty"`
}

var _ types.RunnerSpec = RunnerSpec{}

const (
	// defaultDiskSize is the root volume of a runner whose block sets none.
	defaultDiskSize types.Size = 50 << 30

	// maxGP3DiskSize and maxGP2DiskSize are the largest gp3 and gp2
	// volumes. See
	// https://docs.aws.amazon.com/ebs/latest/userguide/general-purpose.html.
	maxGP3DiskSize types.Size = 64 << 40
	maxGP2DiskSize types.Size = 16 << 40

	// defaultDiskType is the root volume type of a runner whose block sets
	// none.
	defaultDiskType = "gp3"

	// baseDiskIOPS and maxDiskIOPS are a gp3 volume's IOPS without
	// provisioning more, and the most it can be provisioned; above the base
	// it takes at most maxDiskIOPSPerGiB per GiB of its size. See
	// https://docs.aws.amazon.com/ebs/latest/userguide/general-purpose.html.
	baseDiskIOPS      = 3000
	maxDiskIOPS       = 80000
	maxDiskIOPSPerGiB = 500

	// baseDiskThroughput and maxDiskThroughput are a gp3 volume's MiB/s
	// without provisioning more, and the most it can be provisioned; above
	// the base it takes at most 0.25 MiB/s per IOPS, so needs
	// diskIOPSPerThroughput IOPS per MiB/s.
	baseDiskThroughput    = 125
	maxDiskThroughput     = 2000
	diskIOPSPerThroughput = 4

	// maxUserDataSize is the most user data a runner's block sets: EC2 takes
	// 16KiB, before encoding, and the registration exported into it is about
	// 4KiB.
	maxUserDataSize = 10 << 10
)

// defaultUserData runs the Actions runner once, registered with the
// just-in-time configuration Rungar exports after its first line, and powers
// the instance off when it exits: the instance is set to terminate on
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

// Describe returns the runner's instance types and launch template.
func (s RunnerSpec) Describe() string {
	description := s.InstanceTypes.String()
	switch {
	case s.LaunchTemplate == "":
	case description == "":
		description = "template " + s.LaunchTemplate
	default:
		description += " from template " + s.LaunchTemplate
	}

	if s.Spot {
		return description + " (spot)"
	}

	return description
}

// withDefaults returns the spec with the default user data, and the default
// disk unless a launch template gives the root volume.
func (s RunnerSpec) withDefaults() RunnerSpec {
	if s.DiskSize == 0 && s.LaunchTemplate == "" {
		s.DiskSize = defaultDiskSize
	}
	if s.DiskType == "" && s.LaunchTemplate == "" {
		s.DiskType = defaultDiskType
	}
	if s.UserData == "" {
		s.UserData = defaultUserData
	}

	return s
}

// setsRootVolume reports whether the runner sets the root volume, over the
// AMI's, or the launch template's.
func (s RunnerSpec) setsRootVolume() bool {
	return s.DiskSize != 0 || s.DiskType != "" || s.DiskIOPS != 0 || s.DiskThroughput != 0
}

// Validate checks the spec describes an instance EC2 can create.
func (s RunnerSpec) Validate() error {
	switch {
	case s.LaunchTemplate != "":
		if err := s.validateLaunchTemplate(); err != nil {
			return err
		}
	case len(s.InstanceTypes) == 0:
		return errdefs.InvalidArgument("a runner needs an instance_type, such as m7i.xlarge, " +
			"or a launch_template")
	case s.Image == "":
		return errdefs.InvalidArgument("a runner needs an image: the ID of an AMI carrying the Actions " +
			"runner, such as ami-0a1b2c3d4e5f60718, or a launch_template")
	}

	for i, instanceType := range s.InstanceTypes {
		switch {
		case !instanceTypePattern.MatchString(instanceType):
			return errdefs.InvalidArgument("invalid instance_type %q", instanceType)
		case slices.Contains(s.InstanceTypes[:i], instanceType):
			return errdefs.InvalidArgument("instance_type %s is listed twice", instanceType)
		}
	}

	// A launch template's volume type is not known until EC2 creates the
	// instance, which refuses a gp2 volume over gp2's size.
	maxDiskSize := maxGP3DiskSize
	if s.DiskType == "gp2" {
		maxDiskSize = maxGP2DiskSize
	}

	switch {
	case s.Image != "" && !imagePattern.MatchString(s.Image):
		return errdefs.InvalidArgument("invalid image %q: want an AMI ID, such as ami-0a1b2c3d4e5f60718", s.Image)
	case s.DiskSize != 0 && (s.DiskSize < 1<<30 || s.DiskSize > maxDiskSize):
		return errdefs.InvalidArgument("disk_size %s: want 1GiB to %s for %s", s.DiskSize, maxDiskSize,
			cmp.Or(s.DiskType, defaultDiskType))
	case s.DiskSize%(1<<30) != 0:
		return errdefs.InvalidArgument("disk_size %s is not a whole number of GiB", s.DiskSize)
	case s.DiskType != "" && s.DiskType != "gp3" && s.DiskType != "gp2":
		return errdefs.InvalidArgument("invalid disk_type %q: want gp3 or gp2", s.DiskType)
	case s.DiskType == "gp2" && (s.DiskIOPS != 0 || s.DiskThroughput != 0):
		return errdefs.InvalidArgument("disk_iops and disk_throughput are gp3's, which gp2 does not take: " +
			"set disk_type to gp3, or leave them unset")
	case !strings.HasPrefix(s.UserData, "#!"):
		return errdefs.InvalidArgument("user_data must be a script starting with #!, " +
			"after whose first line Rungar exports the registration")
	case len(s.UserData) > maxUserDataSize:
		return errdefs.InvalidArgument("user_data is %d bytes, over the %d that leave room in EC2's limit "+
			"for the registration: install what it sets up in the image instead", len(s.UserData), maxUserDataSize)
	}

	if err := s.validateDiskPerformance(); err != nil {
		return err
	}

	for _, key := range slices.Sorted(maps.Keys(s.Tags)) {
		value := s.Tags[key]
		switch {
		case key == "" || utf8.RuneCountInString(key) > maxTagKeyLength ||
			utf8.RuneCountInString(value) > maxTagValueLength:
			return errdefs.InvalidArgument("tag %s=%s: keys are 1 to %d characters, values at most %d",
				key, value, maxTagKeyLength, maxTagValueLength)
		case key == nameTag:
			return errdefs.InvalidArgument("tag %s: the instance's name is the runner's", key)
		case strings.HasPrefix(key, types.LabelPrefix):
			return errdefs.InvalidArgument("tag %s: tags starting %s are Rungar's", key, types.LabelPrefix)
		case strings.HasPrefix(strings.ToLower(key), "aws:"):
			return errdefs.InvalidArgument("tag %s: tags starting aws: are AWS's", key)
		}
	}

	return nil
}

// validateDiskPerformance checks the root volume's IOPS and throughput are
// within gp3's limits. A ratio to a size or IOPS the runner leaves to a launch
// template is not checked: the template's is not known until EC2 creates the
// instance.
func (s RunnerSpec) validateDiskPerformance() error {
	iops := s.DiskIOPS
	if iops == 0 && s.LaunchTemplate == "" {
		iops = baseDiskIOPS
	}
	sizeGiB := int64(s.DiskSize >> 30)

	switch {
	case s.DiskIOPS != 0 && (s.DiskIOPS < baseDiskIOPS || s.DiskIOPS > maxDiskIOPS):
		return errdefs.InvalidArgument("disk_iops %d: want %d to %d", s.DiskIOPS, baseDiskIOPS, maxDiskIOPS)
	case s.DiskIOPS > baseDiskIOPS && sizeGiB != 0 && int64(s.DiskIOPS) > maxDiskIOPSPerGiB*sizeGiB:
		return errdefs.InvalidArgument("disk_iops %d is over gp3's %d per GiB of disk_size %s: "+
			"set disk_size to at least %dGiB, or lower disk_iops", s.DiskIOPS, maxDiskIOPSPerGiB, s.DiskSize,
			(int64(s.DiskIOPS)+maxDiskIOPSPerGiB-1)/maxDiskIOPSPerGiB)
	case s.DiskThroughput != 0 && (s.DiskThroughput < baseDiskThroughput || s.DiskThroughput > maxDiskThroughput):
		return errdefs.InvalidArgument("disk_throughput %d: want %d to %d MiB/s",
			s.DiskThroughput, baseDiskThroughput, maxDiskThroughput)
	case s.DiskThroughput > baseDiskThroughput && iops != 0 && s.DiskThroughput*diskIOPSPerThroughput > iops:
		return errdefs.InvalidArgument("disk_throughput %d MiB/s is over gp3's 0.25 MiB/s per IOPS of %d IOPS: "+
			"set disk_iops to at least %d, or lower disk_throughput",
			s.DiskThroughput, iops, s.DiskThroughput*diskIOPSPerThroughput)
	}

	return nil
}

// validateLaunchTemplate checks a runner's launch template, and that a root
// volume it sets has the image whose root device it is set on: the
// template's AMI is not known until EC2 creates the instance.
func (s RunnerSpec) validateLaunchTemplate() error {
	if _, ok := parseLaunchTemplateRef(s.LaunchTemplate); !ok {
		return errdefs.InvalidArgument("invalid launch_template %q: want a template's ID or name, "+
			"followed by :version for other than its default one, such as runner-gpu or "+
			"lt-0a1b2c3d4e5f60718:3", s.LaunchTemplate)
	}

	if s.setsRootVolume() && s.Image == "" {
		return errdefs.InvalidArgument("disk_size, disk_type, disk_iops and disk_throughput set the root "+
			"volume of the image, which launch_template %s leaves unset here: set image, or set the root "+
			"volume in the template and, if the provider's runner block sets any of them, clear them in "+
			"this scale set's with null, such as disk_iops: null", s.LaunchTemplate)
	}

	return nil
}
