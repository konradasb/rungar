// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package proxmox

import (
	"errors"

	"github.com/konradasb/rungar/internal/errdefs"
)

// The errors of Proxmox VE the provider acts on. The API's *apiError and
// *taskError unwrap to them, as their messages say; see messagePhrases.
var (
	// errVMNotFound is a VM that does not exist.
	errVMNotFound = errors.New("VM not found")

	// errVMIDTaken is a VMID another VM has.
	errVMIDTaken = errors.New("VMID taken")

	// errNotRunning is a VM, or its guest agent, that is not running.
	errNotRunning = errors.New("not running")

	// errLocalStorage is a clone onto another node of a template whose disks
	// are on storage local to its own.
	errLocalStorage = errors.New("template on local storage")

	// errOutOfCapacity is a node out of memory or disk space.
	errOutOfCapacity = errors.New("out of capacity")
)

// createError puts a refusal to create a runner's machine in its class; see
// provider.Provider.Create. A node out of memory or disk is full.
func createError(err error) error {
	if errors.Is(err, errOutOfCapacity) && !errors.Is(err, errdefs.ErrNoCapacity) {
		return errdefs.NoCapacity("%w", err)
	}

	return err
}
