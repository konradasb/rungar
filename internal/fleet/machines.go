// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package fleet

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/konradasb/rungar/internal/types"
)

// List returns the machines carrying the selector's labels on the named
// providers, listing them in parallel. If some providers cannot be listed, it
// returns the machines of the others and an *UnreachableError. A name not in
// the fleet is an errdefs.ErrNotFound error, and nothing is listed: it is no
// provider, so not an unreachable one.
func (m *Manager) List(ctx context.Context, names []string, selector map[string]string) ([]types.Machine, error) {
	members := make([]*member, 0, len(names))
	for _, name := range names {
		found, err := m.member(name)
		if err != nil {
			return nil, err
		}
		members = append(members, found)
	}

	var (
		mu          sync.Mutex
		out         []types.Machine
		unreachable = map[string]error{}
		wg          sync.WaitGroup
	)

	for _, each := range members {
		wg.Go(func() {
			machines, err := each.list(ctx, selector)

			mu.Lock()
			defer mu.Unlock()

			if err != nil {
				unreachable[each.name] = err
				return
			}
			out = append(out, machines...)
		})
	}
	wg.Wait()

	if len(unreachable) > 0 {
		return out, &UnreachableError{Providers: unreachable}
	}

	return out, nil
}

// list returns the machines carrying the selector's labels on the provider,
// recording whether it is reachable.
func (m *member) list(ctx context.Context, selector map[string]string) ([]types.Machine, error) {
	machines, err := m.provider.List(ctx, selector)
	m.recordListResult(err)
	if err != nil {
		return nil, err
	}

	for i := range machines {
		machines[i].Provider = m.name
	}

	return machines, nil
}

// Delete deletes a machine on a provider. A machine already gone is not an
// error.
func (m *Manager) Delete(ctx context.Context, provider, machine string) error {
	found, err := m.member(provider)
	if err != nil {
		return err
	}

	if err := found.provider.Delete(ctx, machine); err != nil {
		return fmt.Errorf("provider %q: %w", provider, err)
	}

	return nil
}

// UnreachableError is List's error when some providers could not be listed:
// what is on them is unknown, not absent.
type UnreachableError struct {
	// Providers maps each provider that could not be listed to why.
	Providers map[string]error
}

func (e *UnreachableError) Error() string {
	names := slices.Sorted(maps.Keys(e.Providers))

	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("provider %q: %v", name, e.Providers[name]))
	}

	return "cannot list " + strings.Join(parts, "; ")
}

// Includes reports whether the named provider could not be listed.
func (e *UnreachableError) Includes(name string) bool {
	_, ok := e.Providers[name]
	return ok
}
