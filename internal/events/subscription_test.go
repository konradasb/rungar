// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package events

import (
	"errors"
	"slices"
	"testing"
	"time"
)

// TestSubscriptionFollowsHistoryWithoutGapOrOverlap checks that a subscriber
// gets what happened so far and then what happens next, with nothing missed
// and nothing twice between the two.
func TestSubscriptionFollowsHistoryWithoutGapOrOverlap(t *testing.T) {
	l, _ := openLog(t, Config{})
	l.Record(runnerEvent("small", "small-a", "compute1", ActionCreated))
	l.Record(runnerEvent("large", "large-b", "compute1", ActionCreated))

	history, sub := l.Subscribe(Filter{ScaleSet: "small"}, 0)
	defer sub.Close()

	l.Record(runnerEvent("large", "large-b", "compute1", ActionRemoved))
	l.Record(runnerEvent("small", "small-a", "compute1", ActionRemoved))

	if got := actions(history); !slices.Equal(got, []Action{ActionCreated}) {
		t.Errorf("history = %v, want small-a's creation", got)
	}

	select {
	case e := <-sub.Events():
		if e.Name != "small-a" || e.Action != ActionRemoved {
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

// TestSlowSubscriberIsCutOff checks that a subscriber that does not keep up
// is cut off, rather than holding up the scale set that records the events.
func TestSlowSubscriberIsCutOff(t *testing.T) {
	l, _ := openLog(t, Config{})
	_, sub := l.Subscribe(Filter{}, 0)

	done := make(chan struct{})
	go func() {
		for range subscriptionBuffer + 10 {
			l.Record(runnerEvent("small", "small-a", "compute1", ActionCreated))
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

// TestCloseEndsSubscriptions checks that closing the log ends its
// subscriptions cleanly.
func TestCloseEndsSubscriptions(t *testing.T) {
	l, _ := openLog(t, Config{})
	_, sub := l.Subscribe(Filter{}, 0)

	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := <-sub.Events(); ok || sub.Err() != nil {
		t.Errorf("after Close: open %v, err %v; want a subscription ended cleanly", ok, sub.Err())
	}

	sub.Close() // closing an ended subscription is harmless
}

// TestSubscriptionAfterCloseHasEnded checks that subscribing to a closed log
// still returns the history, with a subscription already ended, so that a
// follower does not wait for events that will never come.
func TestSubscriptionAfterCloseHasEnded(t *testing.T) {
	l, _ := openLog(t, Config{})
	l.Record(runnerEvent("small", "small-a", "compute1", ActionCreated))
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	history, sub := l.Subscribe(Filter{}, 0)
	defer sub.Close()

	if len(history) != 1 {
		t.Errorf("history has %d events, want 1", len(history))
	}
	select {
	case _, ok := <-sub.Events():
		if ok {
			t.Error("received an event, want the subscription ended")
		}
	case <-time.After(time.Second):
		t.Fatal("the subscription did not end")
	}
	if err := sub.Err(); err != nil {
		t.Errorf("Err = %v, want nil", err)
	}
}
