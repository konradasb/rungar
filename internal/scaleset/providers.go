// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"

	"github.com/konradasb/rungar/internal/types"
)

// Providers returns every provider in configuration order, or only the one
// named, as placement sees it, with the installation's runners on it and how
// it stands for each scale set placed on it.
func (m *Manager) Providers(ctx context.Context, only string) ([]types.Provider, error) {
	names, err := m.fleet.Names(only)
	if err != nil {
		return nil, err
	}

	// A provider that cannot be listed is still returned, as unreachable.
	machines, _ := m.fleet.List(ctx, names, m.runnerSelector(""))

	return m.fleet.Providers(names, m.specs, machines), nil
}

// ProvidersAndRunners returns every provider, as Providers does, and every
// runner, as Runners does, from one listing of the fleet.
func (m *Manager) ProvidersAndRunners(ctx context.Context) ([]types.Provider, types.RunnerList, error) {
	names, err := m.fleet.Names("")
	if err != nil {
		return nil, types.RunnerList{}, err
	}

	machines, err := m.fleet.List(ctx, names, m.runnerSelector(""))
	unreachable, err := unreachableOf(err)
	if err != nil {
		return nil, types.RunnerList{}, err
	}

	return m.fleet.Providers(names, m.specs, machines), m.runnersFrom(ctx, machines, unreachable), nil
}

// SetProviderDisabled takes a provider out of placement, or puts it back, until
// the daemon restarts, and returns it.
func (m *Manager) SetProviderDisabled(ctx context.Context, name string, disabled bool) (types.Provider, error) {
	if err := m.fleet.SetDisabled(name, disabled); err != nil {
		return types.Provider{}, err
	}

	providers, err := m.Providers(ctx, name)
	if err != nil {
		return types.Provider{}, err
	}

	return providers[0], nil
}
