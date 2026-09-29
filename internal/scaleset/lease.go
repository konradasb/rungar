// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"sync"
)

// lease is a daemon's hold on the message session of its lead scale set, the
// first configured. The other scale sets serve only while it is held, so that
// two daemons of one configuration never split the scale sets between them:
// the one standing by waits on the lead, and asks for no other session.
//
// Each time the lead's session is held is a term. Its methods are safe for
// concurrent use.
type lease struct {
	// lead is the lead scale set's name.
	lead string

	// mu guards the fields below. ended is closed when the current term
	// ends, or is nil between terms; granted is closed when the next term
	// starts.
	mu      sync.Mutex
	ended   chan struct{}
	granted chan struct{}
}

func newLease(lead string) *lease {
	return &lease{lead: lead, granted: make(chan struct{})}
}

// grant starts a term, once the lead holds its session.
func (l *lease) grant() {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.ended = make(chan struct{})
	close(l.granted)
}

// revoke ends the current term, once the lead no longer holds its session. It
// does nothing between terms.
func (l *lease) revoke() {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.ended == nil {
		return
	}

	close(l.ended)
	l.ended = nil
	l.granted = make(chan struct{})
}

// wait returns a channel closed when the current term ends, waiting for a term
// to start if none has, or ctx's error if it ends first.
func (l *lease) wait(ctx context.Context) (<-chan struct{}, error) {
	for {
		l.mu.Lock()
		ended, granted := l.ended, l.granted
		l.mu.Unlock()

		if ended != nil {
			return ended, nil
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-granted:
		}
	}
}
