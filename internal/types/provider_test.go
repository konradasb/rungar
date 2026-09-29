// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package types_test

import (
	"testing"

	"github.com/konradasb/rungar/internal/types"
)

func TestProviderSnapshotUsable(t *testing.T) {
	tests := []struct {
		name string
		s    types.ProviderSnapshot
		want bool
	}{
		{"answering", types.ProviderSnapshot{Reachable: true}, true},
		{"not answering", types.ProviderSnapshot{}, false},
		{"disabled", types.ProviderSnapshot{Reachable: true, Disabled: true}, false},
		// The limit is checked when a place is claimed, not here.
		{"at its limit", types.ProviderSnapshot{Reachable: true, MaxRunners: 2, RunnerCount: 2}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.s.Usable(); got != tt.want {
				t.Errorf("Usable() = %v, want %v", got, tt.want)
			}
		})
	}
}
