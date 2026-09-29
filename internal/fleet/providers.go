// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package fleet

import (
	"slices"

	"github.com/konradasb/rungar/internal/types"
)

// Providers returns the named providers, in the order given, as placement sees
// them: each with how many of machines are on it, and how it stands for each
// of the scale sets placed on it. It calls no provider; machines is what List
// found.
func (m *Manager) Providers(names []string, sets []types.ScaleSetSpec, machines []types.Machine) []types.Provider {
	runners := map[string]int{}
	for _, machine := range machines {
		runners[machine.Provider]++
	}

	states := m.Probe(names)
	out := make([]types.Provider, 0, len(states))

	for _, state := range states {
		mem, ok := m.byName[state.Name]
		if !ok {
			out = append(out, types.Provider{Snapshot: state})
			continue
		}

		// What is on it as listed, rather than as the scale sets count it.
		state.RunnerCount = runners[state.Name]

		out = append(out, mem.describe(state, sets))
	}

	return out
}

// describe returns the provider with state, and how it stands for each of
// the scale sets placed on it.
func (m *member) describe(state types.ProviderSnapshot, sets []types.ScaleSetSpec) types.Provider {
	p := types.Provider{Snapshot: state, Endpoint: m.endpoint, ConfiguredDisabled: m.configuredDisabled}

	for _, set := range sets {
		if !slices.Contains(set.ProviderNames(), m.name) {
			continue
		}

		p.ScaleSets = append(p.ScaleSets, m.scaleSetState(set.Name))
	}

	return p
}
