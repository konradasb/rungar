// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package proxmox

import (
	"maps"
	"testing"
	"time"

	"github.com/konradasb/rungar/internal/types"
)

func TestDescriptionRoundTrips(t *testing.T) {
	labels := types.RunnerLabels("gh-test", "a", "rungar-a", "rev")
	created := time.Date(2026, 9, 29, 10, 15, 2, 500, time.UTC)

	got, at, ok := parseDescription(vmDescription(labels, created))
	if !ok || !maps.Equal(got, labels) || !at.Equal(created.Truncate(time.Second)) {
		t.Errorf("parseDescription() = %v, %v, %v; want %v, %v", got, at, ok, labels, created)
	}

	for _, description := range []string{"", "notes about this VM", `{"other":1}`} {
		if _, _, ok := parseDescription(description); ok {
			t.Errorf("parseDescription(%q) = ok, want not Rungar's", description)
		}
	}
}

func TestVMStatusesAreMachineStates(t *testing.T) {
	tests := []struct {
		name string
		vm   resource
		want types.MachineState
	}{
		{"running", resource{Status: "running"}, types.MachineRunning},
		{"paused", resource{Status: "paused"}, types.MachineRunning},
		{"stopped", resource{Status: "stopped"}, types.MachineStopped},
		{"cloning", resource{Status: "stopped", Lock: "clone"}, types.MachineStarting},
		{"being created", resource{Status: "stopped", Lock: "create"}, types.MachineStarting},
		{"unknown", resource{Status: "unknown"}, types.MachineStopped},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := machineState(tt.vm); got != tt.want {
				t.Errorf("machineState(%+v) = %q, want %q", tt.vm, got, tt.want)
			}
		})
	}
}
