// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package dicer

import (
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/konradasb/rungar/internal/errdefs"
)

// TestCreateErrorClassifiesTheDaemonsRefusal checks each gRPC code is put in
// its class, or none, and that a classified error says the daemon's message
// alone while keeping its status.
func TestCreateErrorClassifiesTheDaemonsRefusal(t *testing.T) {
	tests := []struct {
		code  codes.Code
		class error
	}{
		{codes.ResourceExhausted, errdefs.ErrNoCapacity},
		{codes.FailedPrecondition, errdefs.ErrNoCapacity},
		{codes.InvalidArgument, errdefs.ErrInvalidArgument},
		{codes.NotFound, errdefs.ErrInvalidArgument},
		{codes.Unavailable, nil},
		{codes.Internal, nil},
		{codes.DeadlineExceeded, nil},
	}

	for _, tt := range tests {
		t.Run(tt.code.String(), func(t *testing.T) {
			cause := status.Error(tt.code, "the daemon's reason")
			err := createError(cause)

			if status.Code(err) != tt.code {
				t.Errorf("createError() has code %v, want %v", status.Code(err), tt.code)
			}

			if tt.class == nil {
				if errors.Is(err, errdefs.ErrNoCapacity) || errors.Is(err, errdefs.ErrInvalidArgument) {
					t.Errorf("createError() = %v, want it in neither class", err)
				}

				return
			}

			if !errors.Is(err, tt.class) {
				t.Errorf("createError() = %v, want it in %v", err, tt.class)
			}
			if !errors.Is(err, cause) {
				t.Errorf("createError() = %v, want it to wrap the daemon's error", err)
			}
			if got := err.Error(); got != "the daemon's reason" {
				t.Errorf("createError() = %q, want the daemon's message alone", got)
			}
		})
	}
}
