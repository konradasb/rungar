// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package errdefs_test

import (
	"errors"
	"testing"

	"github.com/konradasb/rungar/internal/errdefs"
)

// TestErrorMatchesItsClassAndLeavesItOutOfTheMessage checks each constructor:
// the class is matched with errors.Is, never read from the message.
func TestErrorMatchesItsClassAndLeavesItOutOfTheMessage(t *testing.T) {
	tests := []struct {
		name  string
		err   error
		class error
		says  string
	}{
		{
			name:  "invalid argument",
			err:   errdefs.InvalidArgument("provider %q is wrong", "a"),
			class: errdefs.ErrInvalidArgument,
			says:  `provider "a" is wrong`,
		},
		{
			name:  "not found",
			err:   errdefs.NotFound("no provider %q", "a"),
			class: errdefs.ErrNotFound,
			says:  `no provider "a"`,
		},
		{
			name:  "no capacity",
			err:   errdefs.NoCapacity("no provider has room for %d", 2),
			class: errdefs.ErrNoCapacity,
			says:  "no provider has room for 2",
		},
		{
			name:  "busy",
			err:   errdefs.Busy("runner %q is running a job", "r1"),
			class: errdefs.ErrBusy,
			says:  `runner "r1" is running a job`,
		},
		{
			name:  "unavailable",
			err:   errdefs.Unavailable("scale set %q is starting", "s1"),
			class: errdefs.ErrUnavailable,
			says:  `scale set "s1" is starting`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !errors.Is(tt.err, tt.class) {
				t.Errorf("%v is not in its class", tt.err)
			}
			if got := tt.err.Error(); got != tt.says {
				t.Errorf("message = %q, want %q", got, tt.says)
			}
		})
	}
}

// TestErrorMatchesNoOtherClass checks that no class can be mistaken for
// another.
func TestErrorMatchesNoOtherClass(t *testing.T) {
	classes := []struct {
		class     error
		construct func(string, ...any) error
	}{
		{errdefs.ErrInvalidArgument, errdefs.InvalidArgument},
		{errdefs.ErrNotFound, errdefs.NotFound},
		{errdefs.ErrNoCapacity, errdefs.NoCapacity},
		{errdefs.ErrBusy, errdefs.Busy},
		{errdefs.ErrUnavailable, errdefs.Unavailable},
	}

	for i, c := range classes {
		err := c.construct("x")
		for j, other := range classes {
			if i != j && errors.Is(err, other.class) {
				t.Errorf("an error in %q matched %q", c.class, other.class)
			}
		}
	}
}

// TestErrorKeepsTheCauseItWraps checks that a cause wrapped into the message
// with %w survives, so a caller can still match what it came from.
func TestErrorKeepsTheCauseItWraps(t *testing.T) {
	cause := errors.New("the underlying problem")
	err := errdefs.NotFound("looking for it: %w", cause)

	if !errors.Is(err, cause) {
		t.Error("the wrapped cause was lost")
	}
	if !errors.Is(err, errdefs.ErrNotFound) {
		t.Error("the class was lost")
	}
}
