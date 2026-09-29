// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package types_test

import (
	"testing"

	"github.com/konradasb/rungar/internal/types"
)

func TestResourcesStringNamesCPUsAndMemory(t *testing.T) {
	tests := []struct {
		resources types.Resources
		want      string
	}{
		{types.Resources{VCPUs: 2, Memory: 4 << 30}, "2 vCPU, 4 GiB"},
		{types.Resources{VCPUs: 1, Memory: 512 << 20}, "1 vCPU, 512 MiB"},
		{types.Resources{VCPUs: 8, Memory: 1536 << 20}, "8 vCPU, 1.5 GiB"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.resources.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}
