// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package grpcapi

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/konradasb/rungar/internal/errdefs"
	rungarv1 "github.com/konradasb/rungar/proto/rungar/v1"
)

// TestToStatusErrorKeepsEachClassAsItsCode checks each class of error reaches
// the command line as its code, with its message as it was.
func TestToStatusErrorKeepsEachClassAsItsCode(t *testing.T) {
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
		{context.DeadlineExceeded, codes.DeadlineExceeded},
		{status.Error(codes.PermissionDenied, "already a status"), codes.PermissionDenied},
		{errors.New("something else"), codes.Internal},
	}

	for _, tt := range tests {
		t.Run(tt.err.Error(), func(t *testing.T) {
			s, ok := status.FromError(toStatusError(tt.err))
			if !ok {
				t.Fatal("not a status")
			}
			if s.Code() != tt.code || s.Message() != status.Convert(tt.err).Message() {
				t.Errorf("toStatusError() = %v %q, want %v %q", s.Code(), s.Message(), tt.code, tt.err.Error())
			}
		})
	}

	if toStatusError(nil) != nil {
		t.Error("toStatusError(nil) is not nil")
	}
}

func TestErrorsToProtoKeepsEachErrorsText(t *testing.T) {
	if errorsToProto(nil) != nil {
		t.Error("no errors is not nil")
	}

	out := errorsToProto(map[string]error{"compute1": errors.New("refused"), "cloud": nil})
	if out["compute1"] != "refused" || out["cloud"] != "unknown error" {
		t.Errorf("errorsToProto() = %v, want each error, and a nil one said to be unknown", out)
	}

	if errString(nil) != "" || errString(errors.New("x")) != "x" {
		t.Error("errString is not the error's text, or empty")
	}
}

// TestHandlerErrorsArriveAsTheirClass checks that a handler's error reaches the
// client as a status of its class through the interceptors, with its message.
func TestHandlerErrorsArriveAsTheirClass(t *testing.T) {
	f := newFixture(t)

	_, err := f.client.GetProvider(context.Background(), &rungarv1.GetProviderRequest{Name: "nowhere"})
	wantCode(t, err, codes.NotFound)

	if msg := status.Convert(err).Message(); !strings.Contains(msg, `no provider "nowhere"`) {
		t.Errorf("message = %q, want it to name the provider", msg)
	}
}
