// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package provider_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/konradasb/rungar/internal/provider"
)

func TestInFlightCancelCancelsCallsInFlight(t *testing.T) {
	var f provider.InFlight

	ctx, cancel := f.Context(t.Context())
	defer cancel()

	f.Cancel()
	f.Cancel()

	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("a call in flight was not cancelled")
	}

	after, cancelAfter := f.Context(t.Context())
	defer cancelAfter()

	select {
	case <-after.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("a call made after Cancel was not cancelled")
	}
}

func TestInFlightLeavesCallsAloneUntilCancelled(t *testing.T) {
	var f provider.InFlight

	ctx, cancel := f.Context(t.Context())
	if err := ctx.Err(); err != nil {
		t.Errorf("a new call's context is %v, want it live", err)
	}

	cancel()
	if err := ctx.Err(); !errors.Is(err, context.Canceled) {
		t.Errorf("a released call's context is %v, want it cancelled", err)
	}
}
