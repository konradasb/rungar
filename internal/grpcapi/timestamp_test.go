// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package grpcapi

import (
	"testing"
	"time"
)

func TestTimestampIsNilForTheZeroTime(t *testing.T) {
	if timestamp(time.Time{}) != nil {
		t.Error("a time not known is not nil")
	}

	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	if !timestamp(at).AsTime().Equal(at) {
		t.Errorf("timestamp(%v) = %v", at, timestamp(at).AsTime())
	}
}
