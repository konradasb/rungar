// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package proxmox

import (
	"errors"
	"testing"

	"github.com/konradasb/rungar/internal/errdefs"
)

func TestCreateErrorMarksOutOfCapacityAsNoCapacity(t *testing.T) {
	full := &taskError{ExitStatus: "No space left on device", sentinels: sentinelsOf("No space left on device")}
	if err := createError(full); !errors.Is(err, errdefs.ErrNoCapacity) || err.Error() != full.Error() {
		t.Errorf("createError() = %v, want %v, unchanged in what it says", err, full)
	}

	other := &taskError{ExitStatus: "storage is locked"}
	if err := createError(other); errors.Is(err, errdefs.ErrNoCapacity) || !errors.Is(err, other) {
		t.Errorf("createError() = %v, want %v, unmarked", err, other)
	}
}
