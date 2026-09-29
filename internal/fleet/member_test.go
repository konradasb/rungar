// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package fleet

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestBackoffDoublesUpToItsLimit checks a backoff doubles each time in a row
// up to its limit, without overflowing however many times it has.
func TestBackoffDoublesUpToItsLimit(t *testing.T) {
	const first, limit = 10 * time.Second, 40 * time.Second

	tests := []struct {
		times int
		want  time.Duration
	}{
		{1, 10 * time.Second},
		{2, 20 * time.Second},
		{3, 40 * time.Second},
		{4, 40 * time.Second},
		{maxDoublings + 1, 40 * time.Second},
		{500, 40 * time.Second},
	}
	for _, tt := range tests {
		t.Run(strconv.Itoa(tt.times), func(t *testing.T) {
			if got := doubled(first, limit, tt.times); got != tt.want {
				t.Errorf("doubled(%v, %v, %d) = %v, want %v", first, limit, tt.times, got, tt.want)
			}
		})
	}

	if got := doubled(time.Duration(1)<<62, time.Hour, maxDoublings+1); got != time.Hour {
		t.Errorf("doubled() past overflow = %v, want the limit", got)
	}
}

// TestBackoffIsForgottenOnceARunnerIsCreated checks a scale set's backoff
// doubles with each refusal in a row, whether full or failing, and starts
// again from the first once the provider creates its runner.
func TestBackoffIsForgottenOnceARunnerIsCreated(t *testing.T) {
	m, _ := testFleet(t, Config{}, ProviderConfig{Name: "a", Backend: &fake{}})
	a := m.byName["a"]

	for i, err := range []error{errFull, errFailing} {
		if wait, refusals, _ := a.recordRefusal("s", err); refusals != i+1 || wait != 10*time.Second<<i {
			t.Errorf("refusal %d skipped for %v, counted %d", i+1, wait, refusals)
		}
	}

	a.recordSuccess("s")
	if wait, refusals, _ := a.recordRefusal("s", errFailing); wait != 10*time.Second || refusals != 1 {
		t.Errorf("after a runner was created, skipped for %v after %d refusals, want the first 10s again", wait, refusals)
	}
}

// TestALimitCountsRunnersUnderTheProvidersLock checks a claim counts the scale
// sets' runners while holding the provider's lock. Counted before it, a runner
// recorded in between is missed and the provider can go over its limit.
func TestALimitCountsRunnersUnderTheProvidersLock(t *testing.T) {
	m, _ := testFleet(t, Config{}, ProviderConfig{Name: "a", MaxRunners: 2, Backend: &fake{}})
	a := m.byName["a"]

	var readLocked bool
	release, ok := a.claim(func() int {
		// Held by the claim, so not ours to take.
		if a.mu.TryLock() {
			a.mu.Unlock()
		} else {
			readLocked = true
		}

		return 1
	})
	if !ok {
		t.Fatal("claim() refused a place under the limit")
	}
	defer release()

	if !readLocked {
		t.Error("claim() read the runners on the provider without holding its lock")
	}

	// One runner on it and one being created: at its limit of 2.
	if _, ok := a.claim(func() int { return 1 }); ok {
		t.Error("claim() took a place beyond the limit")
	}
}

// TestReachabilityIsLoggedWhenItChanges checks a provider that becomes
// unreachable is warned of once, and said to be reachable again once it is,
// and that a call its caller gave up on changes nothing.
func TestReachabilityIsLoggedWhenItChanges(t *testing.T) {
	var buf bytes.Buffer
	m := New([]ProviderConfig{{Name: "compute2"}, {Name: "compute3"}},
		Config{Logger: slog.New(slog.NewTextHandler(&buf, nil))})
	compute2 := m.byName["compute2"]

	compute2.recordListResult(nil)
	compute2.recordListResult(errors.New("connection refused"))
	compute2.recordListResult(errors.New("connection refused"))
	compute2.recordListResult(context.Canceled)
	compute2.recordListResult(nil)
	compute2.recordListResult(nil)

	out := buf.String()
	if n := strings.Count(out, "is unreachable"); n != 1 {
		t.Errorf("warned %d times that it is unreachable, want once:\n%s", n, out)
	}
	if n := strings.Count(out, "is reachable again"); n != 1 {
		t.Errorf("said %d times that it is reachable again, want once:\n%s", n, out)
	}
	if !strings.Contains(out, "provider=compute2") || !strings.Contains(out, "connection refused") {
		t.Errorf("the log does not name the provider and why:\n%s", out)
	}
	if strings.Index(out, "is unreachable") > strings.Index(out, "is reachable again") {
		t.Errorf("the log is out of order:\n%s", out)
	}

	buf.Reset()
	m.byName["compute3"].recordListResult(errors.New("connection refused"))

	if !strings.Contains(buf.String(), "is unreachable") {
		t.Error("a provider down from the start is not warned of")
	}
}
