// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package fleet

import (
	"cmp"
	"slices"

	"github.com/konradasb/rungar/internal/types"
)

// order sorts members, given in the scale set's order, into the order Place
// tries them. Pack puts the highest weight first; spread puts first the
// fewest of the scale set's runners per unit of weight. Ties keep the scale
// set's order.
func order(members []*member, runners map[string]int, placement types.Placement) {
	slices.SortStableFunc(members, func(a, b *member) int {
		if placement == types.PlacementPack {
			return cmp.Compare(b.weight, a.weight)
		}

		return cmp.Compare(float64(runners[a.name])/a.weight, float64(runners[b.name])/b.weight)
	})
}
