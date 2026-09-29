// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package dicer

import (
	"maps"

	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"

	"github.com/konradasb/rungar/internal/types"
)

const (
	// jitConfigEnv is the variable the Actions runner reads its
	// just-in-time configuration from.
	jitConfigEnv = "ACTIONS_RUNNER_INPUT_JITCONFIG"

	// allowRootEnv lets the Actions runner start as root, which it
	// otherwise refuses to do.
	allowRootEnv = "RUNNER_ALLOW_RUNASROOT"
)

// instanceRequest returns the instance a runner gets. It is never restarted,
// since its registration is good for one use, and is removed when it stops,
// so that a runner whose job ends while Rungar is down leaves nothing behind.
func instanceRequest(spec types.MachineSpec, runner RunnerSpec) *dicerdv1.CreateInstanceRequest {
	env := make(map[string]string, len(runner.Env)+2)

	// The VM is the isolation, so the runner may run as root, unless the
	// configured environment says otherwise.
	env[allowRootEnv] = "1"
	maps.Copy(env, runner.Env)

	// The registration cannot be overridden.
	env[jitConfigEnv] = spec.JITConfig

	mounts := make([]*dicerdv1.Mount, 0, len(runner.Mounts))
	for _, m := range runner.Mounts {
		mounts = append(mounts, m.Proto())
	}

	return &dicerdv1.CreateInstanceRequest{
		Name:          spec.Name,
		ImageRef:      runner.ImageRef,
		Vcpus:         int32(runner.VCPUs),
		MemoryBytes:   runner.Memory.Bytes(),
		DiskBytes:     runner.Disk.Bytes(),
		KernelName:    runner.KernelName,
		NetworkName:   runner.NetworkName,
		Mounts:        mounts,
		Env:           env,
		Cmd:           runner.Command,
		Labels:        maps.Clone(spec.Labels),
		RestartPolicy: &dicerdv1.RestartPolicy{Mode: dicerdv1.RestartMode_RESTART_MODE_NO},
		RemoveOnExit:  true,
		Start:         true,
	}
}

// machineOf converts an instance to a machine.
func machineOf(inst *dicerdv1.Instance) types.Machine {
	m := types.Machine{
		Name:   inst.GetName(),
		Labels: inst.GetLabels(),
		State:  machineState(inst.GetState()),
		Size: types.Resources{
			VCPUs:       int(inst.GetVcpus()),
			MemoryBytes: inst.GetMemoryBytes(),
		}.String(),
	}

	// An unset timestamp would read as 1970.
	if created := inst.GetCreateTime(); created != nil {
		m.CreatedAt = created.AsTime()
	}

	return m
}

// machineState converts an instance's state to a machine's. A paused
// instance still holds its runner, so it counts as running.
func machineState(s dicerdv1.InstanceState) types.MachineState {
	switch s {
	case dicerdv1.InstanceState_INSTANCE_STATE_STARTING:
		return types.MachineStarting
	case dicerdv1.InstanceState_INSTANCE_STATE_RUNNING,
		dicerdv1.InstanceState_INSTANCE_STATE_PAUSED:
		return types.MachineRunning
	default:
		return types.MachineStopped
	}
}
