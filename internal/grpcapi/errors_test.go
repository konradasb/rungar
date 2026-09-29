// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package grpcapi

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/konradasb/rungar/internal/errdefs"
)

// TestToStatus checks each class of error reaches the command line as its
// code, with its message as it was.
func TestToStatus(t *testing.T) {
	tests := []struct {
		err  error
		code codes.Code
	}{
		{errdefs.NotFound("no runner %q", "r1"), codes.NotFound},
		{errdefs.InvalidArgument("bad"), codes.InvalidArgument},
		{errdefs.Busy("running a job"), codes.FailedPrecondition},
		{errdefs.NoCapacity("full"), codes.ResourceExhausted},
		{errdefs.Unavailable("starting"), codes.Unavailable},
		{fmt.Errorf("wrapped: %w", errdefs.NotFound("gone")), codes.NotFound},
		{context.Canceled, codes.Canceled},
		{errors.New("something else"), codes.Internal},
	}

	for _, tt := range tests {
		t.Run(tt.err.Error(), func(t *testing.T) {
			s, ok := status.FromError(toStatusError(tt.err))
			if !ok {
				t.Fatal("not a status")
			}
			if s.Code() != tt.code || s.Message() != tt.err.Error() {
				t.Errorf("toStatus() = %v %q, want %v %q", s.Code(), s.Message(), tt.code, tt.err.Error())
			}
		})
	}

	if toStatusError(nil) != nil {
		t.Error("toStatus(nil) is not nil")
	}
}
