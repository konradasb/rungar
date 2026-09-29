// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package types

import "time"

// JITConfigEnv is the environment variable the Actions runner reads its
// just-in-time configuration from.
const JITConfigEnv = "ACTIONS_RUNNER_INPUT_JITCONFIG"

// MachineSpec is what a provider is asked to create for one runner.
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

	// Deadline is when the backend may delete the machine itself: a backstop,
	// later than Rungar would remove the runner for max_age, for when Rungar
	// is not running. Zero is never; a backend that cannot enforce one
	// ignores it.
	Deadline time.Time
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

	// MachineStopped is a machine that has ended: stopped or failed, for
	// good. A provider may leave an ended machine out of its listing
	// instead, which Rungar takes the same way.
	MachineStopped MachineState = "stopped"
)

// Alive reports whether the machine is starting or running.
func (s MachineState) Alive() bool {
	return s == MachineStarting || s == MachineRunning
}
