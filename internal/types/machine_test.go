// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package types_test

import (
	"testing"

	"github.com/konradasb/rungar/internal/types"
)

func TestMachineIsAliveUntilStopped(t *testing.T) {
	tests := []struct {
		state types.MachineState
		want  bool
	}{
		{types.MachineStarting, true},
		{types.MachineRunning, true},
		{types.MachineStopped, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.state), func(t *testing.T) {
			if got := tt.state.Alive(); got != tt.want {
				t.Errorf("Alive() = %v, want %v", got, tt.want)
			}
		})
	}
}
