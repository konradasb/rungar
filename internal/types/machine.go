// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package types

import "time"

// MachineSpec is what a provider is asked to make for one runner.
type MachineSpec struct {
	// Name is the machine's name, and the runner's on GitHub.
	Name string

	// Labels mark the machine as Rungar's; List finds it by them.
	Labels map[string]string

	// JITConfig is the runner's just-in-time configuration: a credential
	// good for one registration, delivered however the backend allows.
	JITConfig string

	// Runner is the scale set's runner, as this provider parsed it.
	Runner RunnerSpec
}

// Machine is a machine on a provider.
type Machine struct {
	Name string

	// Provider is set by the fleet; a provider listing its own machines
	// leaves it empty.
	Provider string

	Labels map[string]string
	State  MachineState

	// Size describes the machine, as the backend does: "4 vCPU, 8 GiB".
	// Empty when the backend does not say.
	Size string

	// CreatedAt is zero when the backend does not say.
	CreatedAt time.Time
}

// MachineState is the state of a machine. A machine is started once.
type MachineState string

const (
	// MachineStarting is a machine that is booting.
	MachineStarting MachineState = "starting"

	// MachineRunning is a machine that is up.
	MachineRunning MachineState = "running"

	// MachineStopped is a machine that has stopped or failed, for good.
	MachineStopped MachineState = "stopped"
)

// Alive reports whether the machine is starting or running.
func (s MachineState) Alive() bool {
	return s == MachineStarting || s == MachineRunning
}
