// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package types_test

import (
	"errors"
	"testing"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/types"
)

func TestPlacementIsSpreadOrPack(t *testing.T) {
	tests := []struct {
		name      string
		placement types.Placement
		valid     bool
	}{
		{"spread", types.PlacementSpread, true},
		{"pack", types.PlacementPack, true},
		{"empty", "", false},
		{"unknown", "random", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.placement.Validate()
			switch {
			case tt.valid && err != nil:
				t.Errorf("Validate() = %v, want nil", err)
			case !tt.valid && !errors.Is(err, errdefs.ErrInvalidArgument):
				t.Errorf("Validate() = %v, want an ErrInvalidArgument", err)
			}
		})
	}
}
