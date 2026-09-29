// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package grpcapi

import (
	"cmp"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
)

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

// timestamp converts t, or returns nil for the zero time.
func timestamp(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}

	return timestamppb.New(t)
}
