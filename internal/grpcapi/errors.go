// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package grpcapi

import (
	"cmp"
	"context"
	"errors"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/konradasb/rungar/internal/errdefs"
)

// classes maps each errdefs class to the status code it is sent as.
var classes = []struct {
	class error
	code  codes.Code
}{
	{errdefs.ErrNotFound, codes.NotFound},
	{errdefs.ErrInvalidArgument, codes.InvalidArgument},
	{errdefs.ErrBusy, codes.FailedPrecondition},
	{errdefs.ErrNoCapacity, codes.ResourceExhausted},
	{errdefs.ErrUnavailable, codes.Unavailable},
	{context.Canceled, codes.Canceled},
	{context.DeadlineExceeded, codes.DeadlineExceeded},
}

// toStatusError converts err to a status error with err's message and the
// code of its class: that of a status it already carries, or Internal.
func toStatusError(err error) error {
	if err == nil {
		return nil
	}

	for _, c := range classes {
		if errors.Is(err, c.class) {
			return status.Error(c.code, err.Error())
		}
	}
	if s, ok := status.FromError(err); ok {
		return s.Err()
	}

	return status.Error(codes.Internal, err.Error())
}

// UnaryErrorCodeInterceptor converts a unary handler's error to a status.
func UnaryErrorCodeInterceptor(
	ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler,
) (any, error) {
	resp, err := handler(ctx, req)
	return resp, toStatusError(err)
}

// StreamErrorCodeInterceptor converts a streaming handler's error to a status.
func StreamErrorCodeInterceptor(
	srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler,
) error {
	return toStatusError(handler(srv, ss))
}

// errorsToProto converts errors by name to their messages, or nil if there
// are none.
func errorsToProto(errs map[string]error) map[string]string {
	if len(errs) == 0 {
		return nil
	}

	out := make(map[string]string, len(errs))
	for name, err := range errs {
		out[name] = cmp.Or(errString(err), "unknown error")
	}

	return out
}

// errString is err's message, or empty for nil.
func errString(err error) string {
	if err == nil {
		return ""
	}

	return err.Error()
}
