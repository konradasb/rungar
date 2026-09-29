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

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/types"
)

// List returns the machines carrying the selector's labels on the named
// providers, listing them in parallel. If some providers cannot be listed, it
// returns the machines of the others and an *UnreachableError.
func (m *Manager) List(ctx context.Context, names []string, selector map[string]string) ([]types.Machine, error) {
	var (
		mu          sync.Mutex
		out         []types.Machine
		unreachable = map[string]error{}
		wg          sync.WaitGroup
	)

	for _, name := range names {
		wg.Go(func() {
			machines, err := m.list(ctx, name, selector)

			mu.Lock()
			defer mu.Unlock()

			if err != nil {
				unreachable[name] = err
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

// list returns the machines carrying the selector's labels on one provider,
// recording whether it answered.
func (m *Manager) list(ctx context.Context, name string, selector map[string]string) ([]types.Machine, error) {
	mem, ok := m.byName[name]
	if !ok {
		return nil, errdefs.NotFound("no such provider")
	}

	machines, err := mem.provider.List(ctx, selector)
	mem.answered(err)
	if err != nil {
		return nil, err
	}

	for i := range machines {
		machines[i].Provider = name
	}

	return machines, nil
}

// Delete removes a machine from a provider. A machine already gone is not an
// error.
func (m *Manager) Delete(ctx context.Context, provider, machine string) error {
	mem, err := m.member(provider)
	if err != nil {
		return err
	}

	if err := mem.provider.Delete(ctx, machine); err != nil {
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

// Unreachable reports whether the named provider could not be listed.
func (e *UnreachableError) Unreachable(name string) bool {
	_, ok := e.Providers[name]
	return ok
}
