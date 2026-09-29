// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package fleet

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/konradasb/rungar/internal/events"
	"github.com/konradasb/rungar/internal/types"
)

// TestEachRefusalIsAnEventOfItsKind checks a full provider and a failing one
// are each recorded, as what they are, once for each runner refused.
func TestEachRefusalIsAnEventOfItsKind(t *testing.T) {
	a := &fake{creates: []error{errFull}}
	b := &fake{creates: []error{errInvalid}}
	c := &fake{creates: []error{errFailing}}
	rec := &recorder{}
	m, _ := testFleet(t, Config{Events: rec},
		ProviderConfig{Name: "a", Weight: 1, Backend: a}, ProviderConfig{Name: "b", Weight: 1, Backend: b},
		ProviderConfig{Name: "c", Weight: 1, Backend: c})

	m.ScaleSetProviders(scaleSet("s", 0, types.PlacementPack, "a", "b", "c")).Place(context.Background(), nil, machine("r1"))
	// s skips all three for now.
	m.ScaleSetProviders(scaleSet("s", 0, types.PlacementPack, "a", "b", "c")).Place(context.Background(), nil, machine("r2"))
	m.ScaleSetProviders(scaleSet("t", 0, types.PlacementPack, "b")).Place(context.Background(), nil, machine("r3"))

	recorded := rec.all()
	want := []struct {
		provider string
		action   events.Action
		scaleSet string
	}{
		{"a", events.ActionFull, "s"},
		{"b", events.ActionFailing, "s"},
		{"c", events.ActionFailing, "s"},
		{"b", events.ActionFailing, "t"},
	}
	if len(recorded) != len(want) {
		t.Fatalf("recorded %+v, want %d events", recorded, len(want))
	}
	for i, w := range want {
		e := recorded[i]
		if e.Kind != events.KindProvider || e.Provider != w.provider || e.Name != w.provider ||
			e.Action != w.action || e.ScaleSet != w.scaleSet {
			t.Errorf("event %d = %+v, want %s %s for %s", i, e, w.provider, w.action, w.scaleSet)
		}
		if e.Attributes["runner"] == "" || e.Attributes["error"] == "" {
			t.Errorf("event %d does not say which runner and why: %+v", i, e.Attributes)
		}
	}

	if got, want := recorded[2].Message,
		"Failed to create runner r1: connection reset; s tries the next provider, and this one again in 10s"; got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
}

// TestRefusalEventCountsRefusalsInARow checks a provider's refusals in a row
// are in a failing event's message and its refusals attribute.
func TestRefusalEventCountsRefusalsInARow(t *testing.T) {
	e := refusalEvent("s", "a", "r1", 20*time.Second, 2, errors.New("connection reset"))

	want := "Failed to create runner r1: connection reset; s tries the next provider, and this one again in 20s " +
		"(2 refusals in a row)"
	if e.Message != want {
		t.Errorf("message = %q, want %q", e.Message, want)
	}
	if got := e.Attributes["refusals"]; got != "2" {
		t.Errorf("refusals = %q, want 2", got)
	}
}
