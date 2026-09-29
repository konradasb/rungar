// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package grpcapi

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// timestamp converts t, or returns nil for the zero time.
func timestamp(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}

	return timestamppb.New(t)
}
