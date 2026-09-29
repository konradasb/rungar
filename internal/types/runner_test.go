// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package types_test

import (
	"testing"
	"time"

	"github.com/konradasb/rungar/internal/types"
)

// TestAnAdoptedRunnerHasNoBootDuration checks a runner the daemon did not
// create is not measured, since when its machine was created says nothing of
// its boot.
func TestAnAdoptedRunnerHasNoBootDuration(t *testing.T) {
	r := types.Runner{Adopted: true, CreatedAt: time.Now().Add(-time.Hour), ConnectedAt: time.Now()}

	if boot, ok := r.BootDuration(); ok {
		t.Errorf("BootDuration() = %v, true, want none for an adopted runner", boot)
	}
}
