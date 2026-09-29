// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package dicer

import (
	"strings"

	"github.com/opencontainers/go-digest"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/types"
)

// RunnerSpec is a dicer runner block: the instance, a virtual machine, a
// runner gets on a Dicer host. The runner's name, labels and registration are
// Rungar's, not the block's.
//
// Image, Kernel and Network keep the JSON names they had before they were
// renamed, so that a runner's revision does not change.
type RunnerSpec struct {
	// Image is the container image the instance boots from, which must carry
	// the Actions runner, as ghcr.io/actions/actions-runner does. Pinning it
	// to a digest, runner:latest@sha256:..., has every runner boot exactly
	// that image. It is required.
	Image string `yaml:"image" json:"ImageRef"`

	// Command is what the instance runs. Unset is the runner image's start
	// script, /home/runner/run.sh.
	Command []string `yaml:"command,omitempty"`

	// VCPUs is how many virtual CPUs the instance has. It is required.
	VCPUs int `yaml:"vcpus"`

	// Memory is the instance's memory, written as a size: 4GiB. A plain
	// number is bytes. It is required, at least 128MiB, and a whole number of
	// MiB, which the hypervisor needs.
	Memory types.Size `yaml:"memory"`

	// Disk is the instance's root disk, where a job's checkout and build
	// output go. It is sparse, so what a job does not use costs the host
	// nothing. Unset is 20GiB.
	Disk types.Size `yaml:"disk,omitempty"`

	// Kernel is the kernel the instance boots, as the host names it. Unset
	// takes the host's default. A name given must exist on every host the
	// runner may land on.
	Kernel string `yaml:"kernel,omitempty" json:"KernelName"`

	// Network is the network the instance joins, as the host names it.
	// Unset takes the host's default. A name given must exist on every host
	// the runner may land on.
	Network string `yaml:"network,omitempty" json:"NetworkName"`

	// Env is added to the runner's environment. The variables that
	// register the runner with GitHub override anything set here.
	Env map[string]string `yaml:"env,omitempty"`

	// Mounts are attached to the instance, named as they are on the host: a
	// build cache volume, a CA bundle.
	Mounts []Mount `yaml:"mounts,omitempty"`
}

var _ types.RunnerSpec = RunnerSpec{}

const (
	// defaultCommand is the start script of the official runner image.
	defaultCommand = "/home/runner/run.sh"

	// minMemory is the least memory a runner's instance boots with.
	minMemory types.Size = 128 << 20

	// defaultDisk is the root disk of a runner whose block sets none.
	defaultDisk types.Size = 20 << 30
)

// digestOf returns the digest an image reference is pinned to, or "".
func digestOf(ref string) string {
	if at := strings.LastIndex(ref, "@"); at >= 0 {
		return ref[at+1:]
	}

	return ""
}

// resources returns the vCPUs and memory the runner asks of a host.
func (s RunnerSpec) resources() types.Resources {
	return types.Resources{VCPUs: s.VCPUs, Memory: s.Memory}
}

// Describe returns the runner's size: "4 vCPU, 8 GiB".
func (s RunnerSpec) Describe() string {
	return s.resources().String()
}

// withDefaults returns the spec with the default command and disk where it
// sets none.
func (s RunnerSpec) withDefaults() RunnerSpec {
	if len(s.Command) == 0 {
		s.Command = []string{defaultCommand}
	}
	if s.Disk == 0 {
		s.Disk = defaultDisk
	}

	return s
}

// Validate checks the spec describes a runner Dicer can create.
func (s RunnerSpec) Validate() error {
	switch {
	case s.Image == "":
		return errdefs.InvalidArgument("a runner needs an image carrying the Actions runner, " +
			"such as ghcr.io/actions/actions-runner")
	case s.VCPUs <= 0:
		return errdefs.InvalidArgument("a runner needs at least one vCPU")
	case s.Memory <= 0:
		return errdefs.InvalidArgument("a runner needs memory, such as memory: 4GiB")
	case s.Memory < minMemory:
		return errdefs.InvalidArgument("memory %s is too little: a runner needs at least %s; "+
			"a plain number is bytes, so write a unit, such as memory: 4GiB", s.Memory, minMemory)
	case s.Memory%(1<<20) != 0:
		return errdefs.InvalidArgument("memory %s is not a whole number of MiB, which the hypervisor needs",
			s.Memory)
	case s.Disk <= 0:
		return errdefs.InvalidArgument("a runner needs a disk, such as disk: 20GiB")
	}

	for _, m := range s.Mounts {
		if err := m.Validate(); err != nil {
			return err
		}
	}

	return s.validateDigest()
}

// validateDigest checks that the digest the image is pinned to, if any,
// parses.
func (s RunnerSpec) validateDigest() error {
	if d := digestOf(s.Image); d != "" {
		if _, err := digest.Parse(d); err != nil {
			return errdefs.InvalidArgument("invalid image digest %q: want \"sha256:\" and a hex hash", d)
		}
	}

	return nil
}
