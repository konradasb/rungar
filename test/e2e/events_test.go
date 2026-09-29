// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

//go:build e2e

package e2e

import (
	"fmt"
	"strings"
	"testing"
)

// event is what the tests read of an event in rungar events --json.
type event struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Action   string `json:"action"`
	ScaleSet string `json:"scale_set"`
	Provider string `json:"provider"`
}

// events lists the tests' scale set's events, oldest first.
func (e *environment) events(t *testing.T, args ...string) []event {
	t.Helper()

	out := e.rungar(t, append([]string{"events", "--scale-set", scaleSetName, "--json"}, args...)...)

	var events []event
	for line := range strings.Lines(out) {
		events = append(events, decode[event](t, line, "an event"))
	}

	return events
}

// TestEventsSayWhereEachRunnerCameFrom checks every runner the scale set has
// is accounted for by its events: made by this daemon, or adopted by it, on
// the provider it is on.
func TestEventsSayWhereEachRunnerCameFrom(t *testing.T) {
	ready := env.waitForReady(t)
	events := env.events(t)

	for _, r := range ready {
		found := false
		for _, e := range events {
			if e.Kind == "EVENT_KIND_RUNNER" && e.Name == r.Name && e.Provider == r.Provider &&
				(e.Action == "EVENT_ACTION_CREATED" || e.Action == "EVENT_ACTION_ADOPTED") {
				found = true
			}
		}
		if !found {
			t.Errorf("no event says runner %s was created or adopted on %s:\n%s", r.Name, r.Provider, describeEvents(events))
		}
	}
}

// TestEventsNarrowToARunner checks --runner picks one runner's events alone.
func TestEventsNarrowToARunner(t *testing.T) {
	r := env.waitForReady(t)[0]

	events := env.events(t, "--runner", r.Name)
	if len(events) == 0 {
		t.Fatalf("no events of runner %s", r.Name)
	}
	for _, e := range events {
		if e.Name != r.Name {
			t.Errorf("--runner %s listed an event of %s", r.Name, e.Name)
		}
	}
}

// describeEvents says what events say happened, for a failure message.
func describeEvents(events []event) string {
	var b strings.Builder
	for _, e := range events {
		fmt.Fprintf(&b, "  %s %s %s on %s\n", e.Kind, e.Name, e.Action, e.Provider)
	}

	return b.String()
}
