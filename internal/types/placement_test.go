// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package types_test

import (
	"errors"
	"testing"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/types"
)

func TestPlacementValidate(t *testing.T) {
	for _, ok := range []types.Placement{types.PlacementSpread, types.PlacementPack} {
		if err := ok.Validate(); err != nil {
			t.Errorf("Validate(%q) = %v", ok, err)
		}
	}
	for _, bad := range []types.Placement{"", "random"} {
		if err := bad.Validate(); !errors.Is(err, errdefs.ErrInvalidArgument) {
			t.Errorf("Validate(%q) = %v, want an ErrInvalidArgument", bad, err)
		}
	}
}
