// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package events

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/konradasb/rungar/internal/types"
)

var t0 = time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

// openLog opens a log in a fresh directory, on a clock the test moves.
func openLog(t *testing.T, cfg Config) (*Log, *time.Time) {
	t.Helper()

	if cfg.Path == "" {
		cfg.Path = filepath.Join(t.TempDir(), "events.jsonl")
	}

	l, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })

	now := t0
	l.now = func() time.Time { return now }

	return l, &now
}

func runnerEvent(scaleSet, name, provider string, action types.EventAction) types.Event {
	return types.Event{Kind: types.KindRunner, Name: name, Action: action, ScaleSet: scaleSet, Provider: provider}
}

func actions(events []types.Event) []types.EventAction {
	out := make([]types.EventAction, 0, len(events))
	for _, e := range events {
		out = append(out, e.Action)
	}

	return out
}

func TestRecordAndList(t *testing.T) {
	l, now := openLog(t, Config{})

	l.Record(runnerEvent("small", "small-a", "compute1", types.ActionCreated))
	*now = now.Add(time.Minute)
	l.Record(runnerEvent("small", "small-a", "compute1", types.ActionRemoved))
	l.Record(types.Event{
		Kind: types.KindProvider, Name: "compute2", Action: types.ActionFailing,
		ScaleSet: "large", Provider: "compute2",
	})
	l.Record(runnerEvent("large", "large-b", "compute1", types.ActionAdopted))

	all := l.List(types.EventFilter{}, 0)
	if len(all) != 4 || !all[0].Time.Equal(t0) || !all[1].Time.Equal(t0.Add(time.Minute)) {
		t.Fatalf("List = %+v, want all four, stamped in order", all)
	}

	tests := []struct {
		name   string
		filter types.EventFilter
		limit  int
		want   []types.EventAction
	}{
		{"by scale set", types.EventFilter{ScaleSet: "large"}, 0,
			[]types.EventAction{types.ActionFailing, types.ActionAdopted}},
		{"by provider", types.EventFilter{Provider: "compute1"}, 0,
			[]types.EventAction{types.ActionCreated, types.ActionRemoved, types.ActionAdopted}},
		{"by runner", types.EventFilter{Runner: "small-a"}, 0,
			[]types.EventAction{types.ActionCreated, types.ActionRemoved}},
		{"a provider is no runner", types.EventFilter{Runner: "compute2"}, 0, []types.EventAction{}},
		{"since", types.EventFilter{Since: t0.Add(time.Minute)}, 0,
			[]types.EventAction{types.ActionRemoved, types.ActionFailing, types.ActionAdopted}},
		{"the last ones", types.EventFilter{}, 2, []types.EventAction{types.ActionFailing, types.ActionAdopted}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := actions(l.List(tt.filter, tt.limit)); !slices.Equal(got, tt.want) {
				t.Errorf("List = %v, want %v", got, tt.want)
			}
		})
	}
}

// What a reader is handed is its own: changing it changes nothing kept.
func TestEventsAreCopied(t *testing.T) {
	l, _ := openLog(t, Config{})

	e := runnerEvent("small", "small-a", "compute1", types.ActionRemoved)
	e.Attributes = map[string]string{"reason": "job_completed"}
	l.Record(e)
	e.Attributes["reason"] = "changed by the recorder"

	listed := l.List(types.EventFilter{}, 0)
	listed[0].Attributes["reason"] = "changed by a reader"

	if got := l.List(types.EventFilter{}, 0)[0].Attributes["reason"]; got != "job_completed" {
		t.Errorf("reason = %q, want the event as recorded", got)
	}
}

// Events outlive the daemon: a log opened again has them, and one written
// half-way when the daemon died loses only the half-written line.
func TestEventsSurviveAReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	l, _ := openLog(t, Config{Path: path})
	l.Record(runnerEvent("small", "small-a", "compute1", types.ActionCreated))
	l.Record(runnerEvent("small", "small-a", "compute1", types.ActionRemoved))
	_ = l.Close()

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"time":"2026-09-28T12:0`)
	_ = f.Close()

	reopened, _ := openLog(t, Config{Path: path})
	want := []types.EventAction{types.ActionCreated, types.ActionRemoved}
	if got := actions(reopened.List(types.EventFilter{}, 0)); !slices.Equal(got, want) {
		t.Errorf("after reopening = %v, want both events", got)
	}
}

// The file's directory is made, as systemd's LogsDirectory would, so that a
// daemon run by hand needs nothing prepared.
func TestOpenMakesTheDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log", "rungar", "events.jsonl")
	l, _ := openLog(t, Config{Path: path})
	l.Record(runnerEvent("small", "small-a", "compute1", types.ActionCreated))

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the events file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != fileMode {
		t.Errorf("mode = %o, want %o", perm, fileMode)
	}
}

func TestRetentionByCount(t *testing.T) {
	const maxCount = 4

	path := filepath.Join(t.TempDir(), "events.jsonl")
	l, _ := openLog(t, Config{Path: path, MaxCount: maxCount})

	for range 10 {
		l.Record(runnerEvent("small", "small-a", "compute1", types.ActionCreated))
	}
	l.Record(runnerEvent("small", "small-a", "compute1", types.ActionRemoved))

	kept := l.List(types.EventFilter{}, 0)
	if len(kept) != maxCount || kept[maxCount-1].Action != types.ActionRemoved {
		t.Errorf("kept %d events ending in %v, want the last 4", len(kept), kept[len(kept)-1].Action)
	}

	// The file is compacted as it grows, not left to grow for ever.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if limit := maxCount + maxCount/4; strings.Count(string(data), "\n") > limit {
		t.Errorf("the file holds %d events, want at most %d", strings.Count(string(data), "\n"), limit)
	}
}

func TestRetentionByAge(t *testing.T) {
	l, now := openLog(t, Config{MaxAge: time.Hour})

	l.Record(runnerEvent("small", "small-a", "compute1", types.ActionCreated))
	*now = now.Add(2 * time.Hour)
	l.Record(runnerEvent("small", "small-a", "compute1", types.ActionRemoved))

	if got := actions(l.List(types.EventFilter{}, 0)); !slices.Equal(got, []types.EventAction{types.ActionRemoved}) {
		t.Errorf("kept %v, want only the event under an hour old", got)
	}
}

// A subscriber gets what happened so far and then what happens next, with
// nothing missed and nothing twice between the two.
func TestSubscribe(t *testing.T) {
	l, _ := openLog(t, Config{})
	l.Record(runnerEvent("small", "small-a", "compute1", types.ActionCreated))
	l.Record(runnerEvent("large", "large-b", "compute1", types.ActionCreated))

	history, sub := l.Subscribe(types.EventFilter{ScaleSet: "small"}, 0)
	defer sub.Close()

	l.Record(runnerEvent("large", "large-b", "compute1", types.ActionRemoved))
	l.Record(runnerEvent("small", "small-a", "compute1", types.ActionRemoved))

	if got := actions(history); !slices.Equal(got, []types.EventAction{types.ActionCreated}) {
		t.Errorf("history = %v, want small-a's creation", got)
	}

	select {
	case e := <-sub.Events():
		if e.Name != "small-a" || e.Action != types.ActionRemoved {
			t.Errorf("followed %+v, want small-a removed", e)
		}
	case <-time.After(time.Second):
		t.Fatal("the new event did not arrive")
	}

	select {
	case e := <-sub.Events():
		t.Errorf("followed %+v too, want only the scale set's events", e)
	default:
	}
}

// A subscriber that does not keep up is cut off, rather than holding up the
// scale set that records the events.
func TestSlowSubscriberIsCutOff(t *testing.T) {
	l, _ := openLog(t, Config{})
	_, sub := l.Subscribe(types.EventFilter{}, 0)

	done := make(chan struct{})
	go func() {
		for range subscriberBuffer + 10 {
			l.Record(runnerEvent("small", "small-a", "compute1", types.ActionCreated))
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Record waited on a subscriber that did not read")
	}

	for range sub.Events() {
		// Drain what was buffered; the channel is then closed.
	}
	if !errors.Is(sub.Err(), ErrFellBehind) {
		t.Errorf("Err = %v, want ErrFellBehind", sub.Err())
	}
}

func TestCloseEndsSubscriptions(t *testing.T) {
	l, _ := openLog(t, Config{})
	_, sub := l.Subscribe(types.EventFilter{}, 0)

	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if _, open := <-sub.Events(); open || sub.Err() != nil {
		t.Errorf("after Close: open %v, err %v; want a subscription ended cleanly", open, sub.Err())
	}

	sub.Close() // closing an ended subscription is harmless
}
