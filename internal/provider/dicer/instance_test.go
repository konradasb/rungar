// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package dicer

import (
	"testing"

	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"

	"github.com/konradasb/rungar/internal/types"
)

// TestRunnerInstanceIsCreated checks the things that make an instance a
// runner, which are Rungar's to decide rather than the configuration's.
func TestRunnerInstanceIsCreated(t *testing.T) {
	p, d := newTestProvider(t)

	if err := p.Create(t.Context(), testMachine("set-abcd1234", "set")); err != nil {
		t.Fatalf("Create() = %v", err)
	}

	req := d.lastCreate(t)

	if req.GetName() != "set-abcd1234" || !req.GetStart() {
		t.Errorf("created %q with start=%v, want the runner's name, started", req.GetName(), req.GetStart())
	}
	if req.GetEnv()[types.JITConfigEnv] != "encoded-jit-config" {
		t.Errorf("the registration is not in %s; an instance without it never becomes a runner", types.JITConfigEnv)
	}
	if req.GetEnv()["HTTPS_PROXY"] != "http://proxy:3128" {
		t.Error("the configured environment was dropped")
	}
	if req.GetEnv()[allowRootEnv] != "1" {
		t.Error("the runner refuses to start as root unless told it may, and an instance has one user")
	}
	if !req.GetRemoveOnExit() {
		t.Error("a runner that stops while Rungar is not watching would leave its instance behind")
	}
	if mode := req.GetRestartPolicy().GetMode(); mode != dicerdv1.RestartMode_RESTART_MODE_NO {
		t.Errorf("restart policy = %s; a second boot would have an unregistered instance sit there", mode)
	}
	if !types.HasLabels(req.GetLabels(), types.ScaleSetSelector("i", "set")) {
		t.Error("the instance is not labelled as Rungar's, so a restarted daemon would not find it")
	}
	if req.GetDiskBytes() != 20*gib {
		t.Errorf("disk = %d, want the runner's", req.GetDiskBytes())
	}
	if len(req.GetCmd()) != 1 || req.GetCmd()[0] != "/home/runner/run.sh" {
		t.Errorf("command = %v, want the runner's", req.GetCmd())
	}
	if req.GetVcpus() != 4 || req.GetMemoryBytes() != 8*gib {
		t.Errorf("size = %d vCPU, %d bytes, want the runner's", req.GetVcpus(), req.GetMemoryBytes())
	}
	if len(req.GetMounts()) != 1 || req.GetMounts()[0].GetType() != dicerdv1.MountType_MOUNT_TYPE_VOLUME {
		t.Errorf("mounts = %v, want the configured volume", req.GetMounts())
	}
	if policy := req.GetPullPolicy(); policy != dicerdv1.PullPolicy_PULL_POLICY_NEVER {
		t.Errorf("pull policy = %s; a pull inside the create would outlast its timeout", policy)
	}
}

func TestMachineStateCountsAPausedInstanceAsRunning(t *testing.T) {
	for state, want := range map[dicerdv1.InstanceState]types.MachineState{
		dicerdv1.InstanceState_INSTANCE_STATE_STARTING:   types.MachineStarting,
		dicerdv1.InstanceState_INSTANCE_STATE_RUNNING:    types.MachineRunning,
		dicerdv1.InstanceState_INSTANCE_STATE_PAUSED:     types.MachineRunning,
		dicerdv1.InstanceState_INSTANCE_STATE_STOPPED:    types.MachineStopped,
		dicerdv1.InstanceState_INSTANCE_STATE_STOPPING:   types.MachineStopped,
		dicerdv1.InstanceState_INSTANCE_STATE_RESTARTING: types.MachineStopped,
		dicerdv1.InstanceState_INSTANCE_STATE_FAILED:     types.MachineStopped,
	} {
		t.Run(state.String(), func(t *testing.T) {
			if got := machineState(state); got != want {
				t.Errorf("machineState(%s) = %s, want %s", state, got, want)
			}
		})
	}
}
