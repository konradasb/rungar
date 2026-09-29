// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package gcp

import (
	"context"
	"time"
)

// defaultRetryDelay is the first pause before a request is sent again.
const defaultRetryDelay = 500 * time.Millisecond

// maxRetryDelay is the longest pause before a request is sent again.
const maxRetryDelay = 10 * time.Second

// backoff is the pauses between the sends of a request that went
// unanswered: short at first, doubling each time up to maxRetryDelay.
type backoff struct {
	next time.Duration
}

// backoff returns the pauses between the sends of a request, the first of the
// provider's retryDelay.
func (p *Provider) backoff() backoff {
	return backoff{next: p.retryDelay}
}

// wait pauses for the next delay, and reports whether ctx lasted it.
func (b *backoff) wait(ctx context.Context) bool {
	timer := time.NewTimer(b.next)
	defer timer.Stop()

	b.next = min(2*b.next, maxRetryDelay)

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
