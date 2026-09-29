// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package dicer

import (
	"maps"

	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"

	"github.com/konradasb/rungar/internal/types"
)

// allowRootEnv lets the Actions runner start as root, which it otherwise
// refuses to do.
const allowRootEnv = "RUNNER_ALLOW_RUNASROOT"

// createInstanceRequest returns the instance a runner gets. It is never
// restarted, since its registration is good for one use, and is deleted when
// it stops, so that a runner whose job ends while Rungar is down leaves
// nothing behind. Its image must be on the host already: the daemon is not to
// pull it, so that creating the instance takes only as long as booting it.
func createInstanceRequest(spec types.MachineSpec, runner RunnerSpec) *dicerdv1.CreateInstanceRequest {
	env := make(map[string]string, len(runner.Env)+2)

	// The instance, a virtual machine, is the isolation, so the runner may
	// run as root, unless the configured environment says otherwise.
	env[allowRootEnv] = "1"
	maps.Copy(env, runner.Env)

	// The registration cannot be overridden.
	env[types.JITConfigEnv] = spec.JITConfig

	mounts := make([]*dicerdv1.Mount, 0, len(runner.Mounts))
	for _, m := range runner.Mounts {
		mounts = append(mounts, m.proto())
	}

	return &dicerdv1.CreateInstanceRequest{
		Name:          spec.Name,
		ImageRef:      runner.Image,
		Vcpus:         int32(runner.VCPUs),
		MemoryBytes:   runner.Memory.Bytes(),
		DiskBytes:     runner.Disk.Bytes(),
		KernelName:    runner.Kernel,
		NetworkName:   runner.Network,
		Mounts:        mounts,
		Env:           env,
		Cmd:           runner.Command,
		Labels:        maps.Clone(spec.Labels),
		RestartPolicy: &dicerdv1.RestartPolicy{Mode: dicerdv1.RestartMode_RESTART_MODE_NO},
		RemoveOnExit:  true,
		PullPolicy:    dicerdv1.PullPolicy_PULL_POLICY_NEVER,
		Start:         true,
	}
}

// machineOf converts an instance to a machine.
func machineOf(instance *dicerdv1.Instance) types.Machine {
	m := types.Machine{
		Name:   instance.GetName(),
		Labels: instance.GetLabels(),
		State:  machineState(instance.GetState()),
		Size: types.Resources{
			VCPUs:  int(instance.GetVcpus()),
			Memory: types.Size(instance.GetMemoryBytes()),
		}.String(),
	}

	// An unset timestamp would read as 1970.
	if created := instance.GetCreateTime(); created != nil {
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
