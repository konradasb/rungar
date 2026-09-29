// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"testing"
	"time"
)

// TestAgeRoundsDownToTheLargestUnit checks an age is written in its largest
// whole unit, and a negative one as "-".
func TestAgeRoundsDownToTheLargestUnit(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{90 * time.Second, "1m"},
		{45 * time.Minute, "45m"},
		{90 * time.Minute, "1h"},
		{26 * time.Hour, "1d"},
		// A clock that disagrees between hosts must not print nonsense.
		{-time.Minute, "-"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := age(tt.in); got != tt.want {
				t.Errorf("age(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestPluralCountsTheNoun checks the count is written before the noun, which
// takes an s unless there is one.
func TestPluralCountsTheNoun(t *testing.T) {
	tests := []struct {
		n    int
		noun string
		want string
	}{
		{0, "runner", "0 runners"},
		{1, "runner", "1 runner"},
		{2, "runner", "2 runners"},
		{1, "more runner", "1 more runner"},
		{3, "more runner", "3 more runners"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := plural(tt.n, tt.noun); got != tt.want {
				t.Errorf("plural(%d, %q) = %q, want %q", tt.n, tt.noun, got, tt.want)
			}
		})
	}
}
