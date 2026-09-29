// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package types_test

import (
	"testing"

	"github.com/konradasb/rungar/internal/types"
)

func TestMachineStateAlive(t *testing.T) {
	for state, want := range map[types.MachineState]bool{
		types.MachineStarting: true,
		types.MachineRunning:  true,
		types.MachineStopped:  false,
	} {
		if got := state.Alive(); got != want {
			t.Errorf("%s.Alive() = %v, want %v", state, got, want)
		}
	}
}
