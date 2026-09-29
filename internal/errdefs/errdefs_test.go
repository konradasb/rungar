// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package errdefs_test

import (
	"errors"
	"testing"

	"github.com/konradasb/rungar/internal/errdefs"
)

func TestErrorClasses(t *testing.T) {
	tests := []struct {
		name  string
		err   error
		class error
		says  string
	}{
		{
			name:  "invalid argument",
			err:   errdefs.InvalidArgument("host %q is wrong", "a"),
			class: errdefs.ErrInvalidArgument,
			says:  `host "a" is wrong`,
		},
		{
			name:  "not found",
			err:   errdefs.NotFound("no host %q", "a"),
			class: errdefs.ErrNotFound,
			says:  `no host "a"`,
		},
		{
			name:  "no capacity",
			err:   errdefs.NoCapacity("no host has room for %d", 2),
			class: errdefs.ErrNoCapacity,
			says:  "no host has room for 2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !errors.Is(tt.err, tt.class) {
				t.Errorf("%v is not in its class", tt.err)
			}

			// The class is matched, not read: its name must stay out of
			// the message.
			if got := tt.err.Error(); got != tt.says {
				t.Errorf("message = %q, want %q", got, tt.says)
			}
		})
	}

	// The classes are distinct, so one cannot be mistaken for another.
	if errors.Is(errdefs.NotFound("x"), errdefs.ErrNoCapacity) {
		t.Error("a NotFound matched ErrNoCapacity")
	}
}

// TestErrorsWrap checks that a cause wrapped into the message survives, so a
// caller can still match what it came from.
func TestErrorsWrap(t *testing.T) {
	cause := errors.New("the underlying problem")
	err := errdefs.NotFound("looking for it: %w", cause)

	if !errors.Is(err, cause) {
		t.Error("the wrapped cause was lost")
	}
	if !errors.Is(err, errdefs.ErrNotFound) {
		t.Error("the class was lost")
	}
}
