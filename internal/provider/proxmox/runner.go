// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package proxmox

import (
	"fmt"
	"path"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/types"
)

// RunnerSpec is a proxmox runner block: the virtual machine a runner gets,
// cloned from a template. The runner's name, labels and registration are
// Rungar's, not the block's.
type RunnerSpec struct {
	// Template is the VMID of the template runners are cloned from. It must
	// run the QEMU guest agent, and start the Actions runner with the
	// registration Rungar writes to jit_path; see the provider's reference.
	// It is required.
	Template int `yaml:"template"`

	// Cores is how many CPU cores the VM has. It is required.
	Cores int `yaml:"cores"`

	// Memory is the VM's memory, written as a size: 4GiB. It is required,
	// at least 128MiB, and a whole number of MiB.
	Memory types.Size `yaml:"memory"`

	// FullClone copies the template's disks, rather than making a linked
	// clone that shares them. A linked clone is quick, and needs the
	// template's storage to support it. Unset is false.
	FullClone bool `yaml:"full_clone,omitempty"`

	// Storage is where a full clone's disks go. Unset is the template's
	// storage. Only for a full clone.
	Storage string `yaml:"storage,omitempty"`

	// Pool is the resource pool runners are put in. Unset is none.
	Pool string `yaml:"pool,omitempty"`

	// JITPath is the file in the VM the runner's registration is written to,
	// through the guest agent, once it answers. Unset is
	// /run/rungar/jitconfig.
	JITPath string `yaml:"jit_path,omitempty"`
}

const (
	// defaultJITPath is where the registration is written in a VM whose
	// block sets no jit_path.
	defaultJITPath = "/run/rungar/jitconfig"

	// minMemory is the least memory a runner's VM is given.
	minMemory types.Size = 128 << 20

	// minVMID is the lowest VMID Proxmox VE allows.
	minVMID = 100
)

// Describe returns the runner's size: "4 cores, 8 GiB".
func (s RunnerSpec) Describe() string {
	return describe(s.Cores, s.Memory.Bytes())
}

// Validate checks the spec describes a VM Proxmox VE can clone.
func (s RunnerSpec) Validate() error {
	switch {
	case s.Template == 0:
		return errdefs.InvalidArgument("a runner needs a template: the VMID of the template VM it is cloned from")
	case s.Template < minVMID:
		return errdefs.InvalidArgument("template %d is not a VMID: they start at %d", s.Template, minVMID)
	case s.Cores <= 0:
		return errdefs.InvalidArgument("a runner needs at least one core")
	case s.Memory <= 0:
		return errdefs.InvalidArgument("a runner needs memory, e.g. memory: 4GiB")
	case s.Memory < minMemory:
		return errdefs.InvalidArgument("memory %s is too little: a runner needs at least %s; "+
			"a plain number is bytes, so write a unit, e.g. memory: 4GiB", s.Memory, minMemory)
	case s.Memory%(1<<20) != 0:
		return errdefs.InvalidArgument("memory %s is not a whole number of MiB", s.Memory)
	case s.Storage != "" && !s.FullClone:
		return errdefs.InvalidArgument("storage is only for a full clone: set full_clone, or leave storage out")
	case !path.IsAbs(s.JITPath):
		return errdefs.InvalidArgument("jit_path %q must be an absolute path in the VM", s.JITPath)
	}

	return nil
}

// memoryMiB returns the VM's memory in MiB, as Proxmox VE configures it.
func (s RunnerSpec) memoryMiB() int64 {
	return s.Memory.Bytes() >> 20
}

// describe returns a size for a person: "4 cores, 8 GiB".
func describe(cores int, memory int64) string {
	return fmt.Sprintf("%d cores, %s", cores, formatBytes(memory))
}
