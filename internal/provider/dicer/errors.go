// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package dicer

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/konradasb/rungar/internal/errdefs"
)

// createError classifies the daemon's refusal of a runner's instance, or of
// its image, as provider.Provider.Create describes: a full host is an
// errdefs.ErrNoCapacity error, a runner the host can never take an
// errdefs.ErrInvalidArgument error. Any other error is returned as it is.
func createError(err error) error {
	switch status.Code(err) {
	case codes.ResourceExhausted, codes.FailedPrecondition:
		// Out of CPU, memory or disk; or a volume or port the runner needs
		// is another instance's, until that one has gone.
		return errdefs.NoCapacity("%w", shortError{err})
	case codes.InvalidArgument, codes.NotFound:
		// More than the host has, a kernel, network or volume it lacks, or
		// an image no registry has.
		return errdefs.InvalidArgument("%w", shortError{err})
	default:
		return err
	}
}

// shortError is an error from the daemon that reads as its message alone,
// without its gRPC code, and still unwraps to it.
type shortError struct {
	err error
}

func (e shortError) Error() string { return status.Convert(e.err).Message() }

func (e shortError) Unwrap() error { return e.err }
