// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package provider

import (
	"context"
	"sync"
)

// InFlight cancels a provider's calls in flight when it closes, as Close
// promises. Its zero value is ready to use; it must not be copied after first
// use. It is safe for concurrent use.
type InFlight struct {
	initOnce, cancelOnce sync.Once

	// done is closed by Cancel.
	done chan struct{}
}

// Context returns a copy of ctx that is also cancelled by Cancel, and the
// function that releases it, which the caller must call when the call ends. A
// context returned after Cancel is cancelled already.
func (f *InFlight) Context(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	done := f.doneChannel()

	go func() {
		select {
		case <-done:
			cancel()
		case <-ctx.Done():
		}
	}()

	return ctx, cancel
}

// Cancel cancels every context Context has returned or will return. It may be
// called more than once.
func (f *InFlight) Cancel() {
	done := f.doneChannel()
	f.cancelOnce.Do(func() { close(done) })
}

// doneChannel returns done, making it on first use.
func (f *InFlight) doneChannel() chan struct{} {
	f.initOnce.Do(func() { f.done = make(chan struct{}) })

	return f.done
}
