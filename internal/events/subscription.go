// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package events

import (
	"errors"
)

// subscriptionBuffer is how many events a subscription may fall behind by
// before it is cut off.
const subscriptionBuffer = 256

// ErrFellBehind ends a subscription that did not keep up with the events.
var ErrFellBehind = errors.New("events came faster than they were read; follow again to catch up")

// Subscribe returns the events f picks so far, as List does, and a
// subscription to later ones, with no gap or overlap. The caller must Close
// the subscription. After the log's Close, the subscription has ended already.
func (l *Log) Subscribe(f Filter, limit int) ([]Event, *Subscription) {
	sub := &Subscription{
		filter: f,
		events: make(chan Event, subscriptionBuffer),
		done:   make(chan struct{}),
		log:    l,
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	l.subscriptions[sub] = struct{}{}
	if l.closed {
		sub.end(nil)
	}

	return l.list(f, limit), sub
}

// Subscription receives the events recorded after it was made.
type Subscription struct {
	filter Filter
	events chan Event
	log    *Log

	// done is closed when the subscription ends; err says why, nil for a
	// subscription closed by its owner or its log.
	done chan struct{}
	err  error
}

// Events returns the events as they are recorded. It is closed when the
// subscription ends; Err then says why.
func (s *Subscription) Events() <-chan Event { return s.events }

// Err returns why the subscription ended, once Events is closed:
// ErrFellBehind, or nil.
func (s *Subscription) Err() error {
	<-s.done
	return s.err
}

// Close ends the subscription.
func (s *Subscription) Close() {
	s.log.mu.Lock()
	defer s.log.mu.Unlock()

	s.end(nil)
}

// send passes e on, or ends the subscription if its reader has fallen too far
// behind: the log never waits on a reader. The caller must hold the log's
// lock.
func (s *Subscription) send(e Event) {
	select {
	case s.events <- e:
	default:
		s.end(ErrFellBehind)
	}
}

// end ends the subscription for err, once. The caller must hold the log's
// lock.
func (s *Subscription) end(err error) {
	if _, ok := s.log.subscriptions[s]; !ok {
		return
	}

	delete(s.log.subscriptions, s)
	s.err = err
	close(s.events)
	close(s.done)
}
