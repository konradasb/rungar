// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package events

import (
	"testing"
	"time"
)

// TestFilterSelectsMatchingEvents checks a filter selects an event only when
// every field it sets matches.
func TestFilterSelectsMatchingEvents(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	runner := Event{
		Time:     now,
		Kind:     KindRunner,
		Name:     "rungar-vm-abcd1234",
		Action:   ActionCreated,
		ScaleSet: "rungar-vm",
		Provider: "dicer",
	}
	provider := Event{
		Time:     now,
		Kind:     KindProvider,
		Name:     "rungar-vm-abcd1234",
		Action:   ActionFull,
		ScaleSet: "rungar-vm",
		Provider: "rungar-vm-abcd1234",
	}

	tests := []struct {
		name   string
		filter Filter
		event  Event
		want   bool
	}{
		{"zero filter selects every event", Filter{}, runner, true},
		{"same scale set", Filter{ScaleSet: "rungar-vm"}, runner, true},
		{"other scale set", Filter{ScaleSet: "other"}, runner, false},
		{"same provider", Filter{Provider: "dicer"}, runner, true},
		{"other provider", Filter{Provider: "other"}, runner, false},
		{"same runner", Filter{Runner: "rungar-vm-abcd1234"}, runner, true},
		{"other runner", Filter{Runner: "other"}, runner, false},
		{
			// A runner filter selects runner events only, even when another
			// kind of event has the same name.
			"runner filter skips other kinds",
			Filter{Runner: "rungar-vm-abcd1234"}, provider, false,
		},
		{"recorded at since", Filter{Since: now}, runner, true},
		{"recorded after since", Filter{Since: now.Add(-time.Second)}, runner, true},
		{"recorded before since", Filter{Since: now.Add(time.Second)}, runner, false},
		{
			"every field matches",
			Filter{ScaleSet: "rungar-vm", Provider: "dicer", Runner: "rungar-vm-abcd1234", Since: now},
			runner, true,
		},
		{
			"one field of several does not match",
			Filter{ScaleSet: "rungar-vm", Provider: "other", Runner: "rungar-vm-abcd1234"},
			runner, false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.filter.Matches(tt.event); got != tt.want {
				t.Errorf("Matches() = %v, want %v", got, tt.want)
			}
		})
	}
}
