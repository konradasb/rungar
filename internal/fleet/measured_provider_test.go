// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package fleet

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/types"
)

// observedCall is a provider call as a test checks it.
type observedCall struct {
	provider, call string
	result         types.CallResult
}

// callLog keeps the provider calls it is told of.
type callLog struct {
	mu    sync.Mutex
	calls []observedCall
}

func (l *callLog) ObserveProviderCall(provider, call string, result types.CallResult, _ time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.calls = append(l.calls, observedCall{provider: provider, call: call, result: result})
}

// TestProviderCallsAreObserved checks every call made to a provider is
// recorded with its result, except one its caller cancelled. A refused create
// is followed by a delete of what it may have left.
func TestProviderCallsAreObserved(t *testing.T) {
	full := &fake{creates: []error{errdefs.NoCapacity("out of memory")}}
	failing := &fake{listErr: errors.New("connection refused"), deleteErr: errors.New("boom")}
	calls := &callLog{}
	m, _ := testFleet(t, Config{Calls: calls},
		ProviderConfig{Name: "full", Backend: full}, ProviderConfig{Name: "failing", Backend: failing})
	ctx := context.Background()

	_, _ = m.List(ctx, []string{"full", "failing"}, map[string]string{})
	_ = m.Delete(ctx, "failing", "r1")

	providers := m.ScaleSetProviders(scaleSet("set", 0, types.PlacementPack, "full"))
	_, _, _ = providers.Place(ctx, map[string]int{}, machine("r2"))

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	failing.listErr = context.Canceled
	_, _ = m.List(cancelled, []string{"failing"}, map[string]string{})

	// The providers are listed at once, in either order.
	got := slices.Clone(calls.calls)
	slices.SortFunc(got, func(a, b observedCall) int {
		return cmp.Or(strings.Compare(a.provider, b.provider), strings.Compare(a.call, b.call))
	})

	want := []observedCall{
		{"failing", "delete", "error"},
		{"failing", "list", "error"},
		{"full", "create", "no_capacity"},
		{"full", "delete", "ok"},
		{"full", "list", "ok"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("calls = %v, want %v", got, want)
	}
}
