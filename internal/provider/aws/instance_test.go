// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package aws

import (
	"testing"

	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/konradasb/rungar/internal/types"
)

func TestMachineStateCountsStoppingAsStopped(t *testing.T) {
	tests := map[ec2types.InstanceStateName]types.MachineState{
		ec2types.InstanceStateNamePending:  types.MachineStarting,
		ec2types.InstanceStateNameRunning:  types.MachineRunning,
		ec2types.InstanceStateNameStopping: types.MachineStopped,
		ec2types.InstanceStateNameStopped:  types.MachineStopped,
	}

	for state, want := range tests {
		t.Run(string(state), func(t *testing.T) {
			if got := machineState(state); got != want {
				t.Errorf("machineState(%s) = %s, want %s", state, got, want)
			}
		})
	}
}
