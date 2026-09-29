// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package types

import "github.com/konradasb/rungar/internal/errdefs"

// Placement is the order a scale set's providers are tried in.
type Placement string

const (
	// PlacementSpread tries the provider with the fewest of the scale set's
	// runners first, by weight.
	PlacementSpread Placement = "spread"

	// PlacementPack tries the providers in the scale set's order, highest
	// weight first.
	PlacementPack Placement = "pack"
)

// Validate reports whether p is a known placement.
func (p Placement) Validate() error {
	switch p {
	case PlacementSpread, PlacementPack:
		return nil
	default:
		return errdefs.InvalidArgument("unknown placement %q: want spread or pack", p)
	}
}
