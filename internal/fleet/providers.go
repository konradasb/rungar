// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package fleet

import "github.com/konradasb/rungar/internal/types"

// Providers returns the named providers, in the order given, as placement sees
// them: each with how many of machines are on it, and how it stands for each
// of the scale sets placed on it. It calls no provider; machines is what List
// found.
func (m *Manager) Providers(names []string, sets []types.ScaleSetSpec, machines []types.Machine) []types.Provider {
	runners := map[string]int{}
	for _, machine := range machines {
		runners[machine.Provider]++
	}

	snapshots := m.Snapshots(names)
	out := make([]types.Provider, 0, len(snapshots))

	for _, snapshot := range snapshots {
		found, err := m.member(snapshot.Name)
		if err != nil {
			out = append(out, types.Provider{Snapshot: snapshot})
			continue
		}

		// What is on it as listed, rather than as the scale sets count it.
		snapshot.RunnerCount = runners[snapshot.Name]

		out = append(out, found.describe(snapshot, sets))
	}

	return out
}
