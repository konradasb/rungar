// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package grpcapi

import (
	"context"
	"errors"
	"io"
	"slices"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/konradasb/rungar/internal/events"
	rungarv1 "github.com/konradasb/rungar/proto/rungar/v1"
)

// recordRunner records an event of a runner of rungar-vm.
func (f *fixture) recordRunner(name, provider string, action events.Action, at time.Time) {
	f.events.Record(events.Event{
		Time: at, Kind: events.KindRunner, Name: name, Action: action, ScaleSet: "rungar-vm", Provider: provider,
		Attributes: map[string]string{"reason": "job_completed"},
	})
}

// names returns the names of the events, in order.
func names(events []*rungarv1.Event) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.GetName())
	}

	return out
}

func TestListEventsSendsTheHistory(t *testing.T) {
	f := newFixture(t)
	t0 := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	f.recordRunner("rungar-vm-1", "compute1", events.ActionCreated, t0)
	f.recordRunner("rungar-vm-2", "compute2", events.ActionCreated, t0.Add(time.Minute))
	f.recordRunner("rungar-vm-1", "compute1", events.ActionRemoved, t0.Add(2*time.Minute))

	tests := []struct {
		name string
		req  *rungarv1.ListEventsRequest
		want []string
	}{
		{"everything", &rungarv1.ListEventsRequest{}, []string{"rungar-vm-1", "rungar-vm-2", "rungar-vm-1"}},
		{"by provider", &rungarv1.ListEventsRequest{Provider: "compute2"}, []string{"rungar-vm-2"}},
		{"by runner", &rungarv1.ListEventsRequest{Runner: "rungar-vm-1"}, []string{"rungar-vm-1", "rungar-vm-1"}},
		{"since", &rungarv1.ListEventsRequest{Since: timestamppb.New(t0.Add(time.Minute))},
			[]string{"rungar-vm-2", "rungar-vm-1"}},
		{"the last", &rungarv1.ListEventsRequest{Limit: 1}, []string{"rungar-vm-1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream, err := f.client.ListEvents(context.Background(), tt.req)
			if err != nil {
				t.Fatal(err)
			}

			resp, err := stream.Recv()
			if err != nil {
				t.Fatal(err)
			}
			if !resp.GetCaughtUp() {
				t.Error("the only batch is not marked caught up")
			}
			if got := names(resp.GetEvents()); !slices.Equal(got, tt.want) {
				t.Errorf("events = %v, want %v", got, tt.want)
			}
			if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
				t.Errorf("after the history: %v, want the stream to end", err)
			}
		})
	}

	stream, err := f.client.ListEvents(context.Background(), &rungarv1.ListEventsRequest{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}

	e := resp.GetEvents()[0]
	if e.GetKind() != rungarv1.EventKind_EVENT_KIND_RUNNER || e.GetAction() != rungarv1.EventAction_EVENT_ACTION_REMOVED ||
		e.GetScaleSet() != "rungar-vm" || e.GetProvider() != "compute1" || e.GetAttributes()["reason"] != "job_completed" ||
		!e.GetTime().AsTime().Equal(t0.Add(2*time.Minute)) {
		t.Errorf("event = %v, want rungar-vm-1's removal as recorded", e)
	}
}

// TestListEventsSendsALongHistoryInBatches checks a history longer than a
// batch arrives whole, the last batch alone marked caught up.
func TestListEventsSendsALongHistoryInBatches(t *testing.T) {
	f := newFixture(t)
	for range maxEventBatch + 1 {
		f.recordRunner("rungar-vm-1", "compute1", events.ActionCreated, time.Time{})
	}

	stream, err := f.client.ListEvents(context.Background(), &rungarv1.ListEventsRequest{})
	if err != nil {
		t.Fatal(err)
	}

	var sizes []int
	var caughtUp []bool
	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		sizes = append(sizes, len(resp.GetEvents()))
		caughtUp = append(caughtUp, resp.GetCaughtUp())
	}

	if !slices.Equal(sizes, []int{maxEventBatch, 1}) || !slices.Equal(caughtUp, []bool{false, true}) {
		t.Errorf("batches of %v, caught up %v; want %d then 1, the last caught up", sizes, caughtUp, maxEventBatch)
	}
}

// TestListEventsFollows checks a follower is sent the history, then each new
// event it picks.
func TestListEventsFollows(t *testing.T) {
	f := newFixture(t)
	f.recordRunner("rungar-vm-1", "compute1", events.ActionCreated, time.Time{})

	stream, err := f.client.ListEvents(t.Context(), &rungarv1.ListEventsRequest{Runner: "rungar-vm-1", Follow: true})
	if err != nil {
		t.Fatal(err)
	}

	history, err := stream.Recv()
	if err != nil || !history.GetCaughtUp() || len(history.GetEvents()) != 1 {
		t.Fatalf("history = %v, %v; want rungar-vm-1's creation, caught up", history, err)
	}

	f.recordRunner("rungar-vm-2", "compute1", events.ActionCreated, time.Time{})
	f.recordRunner("rungar-vm-1", "compute1", events.ActionRemoved, time.Time{})

	resp, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.GetEvents(); len(got) != 1 || got[0].GetName() != "rungar-vm-1" ||
		got[0].GetAction() != rungarv1.EventAction_EVENT_ACTION_REMOVED {
		t.Errorf("followed %v, want rungar-vm-1's removal alone", got)
	}
}

func TestListEventsRefusesANegativeLimit(t *testing.T) {
	f := newFixture(t)

	stream, err := f.client.ListEvents(context.Background(), &rungarv1.ListEventsRequest{Limit: -1})
	if err != nil {
		t.Fatal(err)
	}

	_, err = stream.Recv()
	wantCode(t, err, codes.InvalidArgument)
}

// TestEventActionToProto checks every action has its own value, and anything
// unknown is unspecified rather than taken for one.
func TestEventActionToProto(t *testing.T) {
	tests := []struct {
		in   events.Action
		want rungarv1.EventAction
	}{
		{events.ActionCreated, rungarv1.EventAction_EVENT_ACTION_CREATED},
		{events.ActionAdopted, rungarv1.EventAction_EVENT_ACTION_ADOPTED},
		{events.ActionRemoved, rungarv1.EventAction_EVENT_ACTION_REMOVED},
		{events.ActionLost, rungarv1.EventAction_EVENT_ACTION_LOST},
		{events.ActionConnected, rungarv1.EventAction_EVENT_ACTION_CONNECTED},
		{events.ActionJobStarted, rungarv1.EventAction_EVENT_ACTION_JOB_STARTED},
		{events.ActionFull, rungarv1.EventAction_EVENT_ACTION_FULL},
		{events.ActionFailing, rungarv1.EventAction_EVENT_ACTION_FAILING},
		{events.ActionPaused, rungarv1.EventAction_EVENT_ACTION_PAUSED},
		{events.ActionResumed, rungarv1.EventAction_EVENT_ACTION_RESUMED},
		{events.ActionMinRunnersChanged, rungarv1.EventAction_EVENT_ACTION_MIN_RUNNERS_CHANGED},
		{"something else", rungarv1.EventAction_EVENT_ACTION_UNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run(string(tt.in), func(t *testing.T) {
			if got := eventActionToProto(tt.in); got != tt.want {
				t.Errorf("eventActionToProto() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestEventKindToProto checks every kind has its own value, and anything
// unknown is unspecified rather than taken for one.
func TestEventKindToProto(t *testing.T) {
	tests := []struct {
		in   events.Kind
		want rungarv1.EventKind
	}{
		{events.KindRunner, rungarv1.EventKind_EVENT_KIND_RUNNER},
		{events.KindProvider, rungarv1.EventKind_EVENT_KIND_PROVIDER},
		{events.KindScaleSet, rungarv1.EventKind_EVENT_KIND_SCALE_SET},
		{"something else", rungarv1.EventKind_EVENT_KIND_UNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run(string(tt.in), func(t *testing.T) {
			if got := eventKindToProto(tt.in); got != tt.want {
				t.Errorf("eventKindToProto() = %v, want %v", got, tt.want)
			}
		})
	}
}
