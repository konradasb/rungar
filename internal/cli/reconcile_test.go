// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"testing"
)

func TestReconcile(t *testing.T) {
	h := newHarness(t)

	if err := h.run("reconcile", "rungar-vm"); err != nil {
		t.Fatal(err)
	}

	if len(h.daemon.reconciled) != 1 || h.daemon.reconciled[0] != "rungar-vm" {
		t.Errorf("reconciled = %v, want rungar-vm", h.daemon.reconciled)
	}
	h.says(t, "rungar-vm: 1 runner, 2 desired")
}
