// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"errors"
	"testing"
)

// TestALeaseTermLastsFromGrantToRevoke checks a term is waited for until
// granted, and ends when revoked, after which the next is waited for.
func TestALeaseTermLastsFromGrantToRevoke(t *testing.T) {
	l := newLease("lead")
	ctx := context.Background()

	// A cancelled context makes wait return as soon as it would block.
	cancelled, cancel := context.WithCancel(ctx)
	cancel()

	if _, err := l.wait(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("wait() before a term was granted = %v, want it to wait until its context ends", err)
	}

	got := make(chan (<-chan struct{}), 1)
	go func() {
		ended, _ := l.wait(ctx)
		got <- ended
	}()

	l.grant()
	ended := <-got
	if isClosed(ended) {
		t.Fatal("the term ended as soon as it was granted")
	}

	l.revoke()
	if !isClosed(ended) {
		t.Error("the term did not end when revoked")
	}

	if _, err := l.wait(cancelled); !errors.Is(err, context.Canceled) {
		t.Errorf("wait() between terms = %v, want it to wait until its context ends", err)
	}
}
