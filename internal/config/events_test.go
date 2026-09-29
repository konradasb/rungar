// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package config

import (
	"strings"
	"testing"
)

// TestEventsUnsetAreKeptInTheLogsDirectory checks the event log's defaults:
// the service's logs directory, and the most recent 10000.
func TestEventsUnsetAreKeptInTheLogsDirectory(t *testing.T) {
	cfg, err := Load(writeConfig(t, valid))
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}

	want := Events{File: "/var/log/rungar/events.jsonl", MaxCount: 10000}
	if cfg.Events != want {
		t.Errorf("events = %+v, want %+v", cfg.Events, want)
	}
}

// TestEventsFileMustBeAbsoluteAndLimitsNonNegative checks the events keys are
// validated.
func TestEventsFileMustBeAbsoluteAndLimitsNonNegative(t *testing.T) {
	tests := []struct {
		name string
		body string
		says string
	}{
		{
			name: "all of it",
			body: valid + "events:\n  file: /srv/rungar/events.jsonl\n  max_count: 500\n  max_age: 168h\n",
		},
		{
			name: "a relative file",
			body: valid + "events:\n  file: events.jsonl\n",
			says: "want an absolute path",
		},
		{
			name: "a negative count",
			body: valid + "events:\n  max_count: -1\n",
			says: "events.max_count cannot be negative",
		},
		{
			name: "a negative age",
			body: valid + "events:\n  max_age: -1h\n",
			says: "events.max_age cannot be negative",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tt.body))
			switch {
			case tt.says == "" && err != nil:
				t.Fatalf("Load() = %v, want nil", err)
			case tt.says != "" && (err == nil || !strings.Contains(err.Error(), tt.says)):
				t.Fatalf("Load() = %v, want an error saying %q", err, tt.says)
			}
		})
	}
}
