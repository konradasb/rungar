// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	rungarv1 "github.com/konradasb/rungar/proto/rungar/v1"
)

// someEvents are a runner made and removed, and a provider passed over.
func someEvents() []*rungarv1.Event {
	at := timestamppb.New(time.Date(2026, 9, 28, 10, 15, 2, 0, time.Local))

	return []*rungarv1.Event{
		{
			Time: at, Kind: rungarv1.EventKind_EVENT_KIND_RUNNER, Name: "rungar-vm-x7k2p",
			Action: rungarv1.EventAction_EVENT_ACTION_CREATED, ScaleSet: "rungar-vm", Provider: "compute1",
			Message: "Runner created on compute1: 2 vCPU, 4 GiB",
		},
		{
			Time: at, Kind: rungarv1.EventKind_EVENT_KIND_PROVIDER, Name: "compute2",
			Action: rungarv1.EventAction_EVENT_ACTION_FAILING, ScaleSet: "rungar-vm", Provider: "compute2",
			Message:    "Failed to make runner rungar-vm-q9d1: out of disk; rungar-vm tries the next provider, and this one again in 15s",
			Attributes: map[string]string{"runner": "rungar-vm-q9d1"},
		},
		{
			Time: at, Kind: rungarv1.EventKind_EVENT_KIND_RUNNER, Name: "rungar-vm-x7k2p",
			Action: rungarv1.EventAction_EVENT_ACTION_REMOVED, ScaleSet: "rungar-vm", Provider: "compute1",
			Message:    "Runner removed from compute1: its job completed",
			Attributes: map[string]string{"reason": "job_completed"},
		},
	}
}

func TestEvents(t *testing.T) {
	h := newHarness(t)
	h.daemon.events = someEvents()

	if err := h.run("events"); err != nil {
		t.Fatal(err)
	}

	want := "" +
		"2026-09-28 10:15:02   Runner     rungar-vm-x7k2p   Created   Runner created on compute1: 2 vCPU, 4 GiB\n" +
		"2026-09-28 10:15:02   Provider   compute2          Failing   Failed to make runner rungar-vm-q9d1: out of disk; rungar-vm tries the next provider, and this one again in 15s\n" +
		"2026-09-28 10:15:02   Runner     rungar-vm-x7k2p   Removed   Runner removed from compute1: its job completed\n"
	if got := h.out.String(); got != want {
		t.Errorf("output:\n%s\nwant:\n%s", got, want)
	}
}

// TestEventsAsksWhatTheFlagsSay checks each flag narrows the request.
func TestEventsAsksWhatTheFlagsSay(t *testing.T) {
	h := newHarness(t)

	before := time.Now()
	err := h.run("events", "--scale-set", "rungar-vm", "--provider", "compute1", "--runner", "rungar-vm-x7k2p",
		"-n", "20", "--since", "1h")
	if err != nil {
		t.Fatal(err)
	}

	req := h.daemon.eventsAsked
	if req.GetScaleSet() != "rungar-vm" || req.GetProvider() != "compute1" || req.GetRunner() != "rungar-vm-x7k2p" ||
		req.GetLimit() != 20 || req.GetFollow() {
		t.Errorf("request = %v, want what the flags say", req)
	}
	if since := req.GetSince().AsTime(); since.Before(before.Add(-time.Hour-time.Second)) || since.After(time.Now().Add(-time.Hour)) {
		t.Errorf("since = %v, want an hour ago", since)
	}
}

func TestEventsRefusesANegativeTail(t *testing.T) {
	h := newHarness(t)

	if err := h.run("events", "-n", "-1"); err == nil || !strings.Contains(err.Error(), "--tail") {
		t.Errorf("events -n -1 = %v, want --tail refused", err)
	}
}

// TestEventsFollows checks a follower is written the history, then each new
// event, lined up with it.
func TestEventsFollows(t *testing.T) {
	h := newHarness(t)
	events := someEvents()
	h.daemon.events, h.daemon.followed = events[:1], events[1:]

	if err := h.run("events", "-f"); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSuffix(h.out.String(), "\n"), "\n")
	if len(lines) != 3 || !h.daemon.eventsAsked.GetFollow() {
		t.Fatalf("followed %d lines, asked %v; want the history then two more:\n%s", len(lines), h.daemon.eventsAsked, h.out)
	}
	if !strings.Contains(lines[1], "Failing   Failed to make runner") {
		t.Errorf("line = %q, want the provider failing", lines[1])
	}
}

// TestEventsAsJSON checks --json writes one object a line, by the API's field
// names, for scripts.
func TestEventsAsJSON(t *testing.T) {
	h := newHarness(t)
	h.daemon.events = someEvents()

	if err := h.run("events", "--json"); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSuffix(h.out.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("wrote %d lines, want one per event:\n%s", len(lines), h.out)
	}

	var e struct {
		Name       string            `json:"name"`
		Action     string            `json:"action"`
		ScaleSet   string            `json:"scale_set"`
		Attributes map[string]string `json:"attributes"`
	}
	if err := json.Unmarshal([]byte(lines[2]), &e); err != nil {
		t.Fatal(err)
	}
	if e.Name != "rungar-vm-x7k2p" || e.Action != "EVENT_ACTION_REMOVED" || e.ScaleSet != "rungar-vm" ||
		e.Attributes["reason"] != "job_completed" {
		t.Errorf("event = %+v, want rungar-vm-x7k2p's removal", e)
	}
}

func TestParseSince(t *testing.T) {
	now := time.Date(2026, 9, 28, 14, 30, 0, 0, time.UTC)

	tests := []struct {
		in   string
		want time.Time
	}{
		{"90m", now.Add(-90 * time.Minute)},
		{"2026-09-27", time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)},
		{"2026-09-27 08:15", time.Date(2026, 9, 27, 8, 15, 0, 0, time.UTC)},
		{"10:30", time.Date(2026, 9, 28, 10, 30, 0, 0, time.UTC)},
		{"10:30:15", time.Date(2026, 9, 28, 10, 30, 15, 0, time.UTC)},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := parseSince(tt.in, now)
			if err != nil || !got.Equal(tt.want) {
				t.Errorf("parseSince(%q) = %v, %v; want %v", tt.in, got, err, tt.want)
			}
		})
	}

	if _, err := parseSince("yesterday", now); err == nil {
		t.Error(`parseSince("yesterday") = nil, want an error`)
	}
}

func TestEventActionWord(t *testing.T) {
	for in, want := range map[rungarv1.EventAction]string{
		rungarv1.EventAction_EVENT_ACTION_CREATED:     "Created",
		rungarv1.EventAction_EVENT_ACTION_FULL:        "Full",
		rungarv1.EventAction_EVENT_ACTION_FAILING:     "Failing",
		rungarv1.EventAction_EVENT_ACTION_UNSPECIFIED: "-",
	} {
		if got := eventActionWord(in); got != want {
			t.Errorf("eventActionWord(%v) = %q, want %q", in, got, want)
		}
	}
}
