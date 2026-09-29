// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

// Package provider defines the interface between Rungar and the backends that
// make runners' machines. A provider is one backend -- a Dicer host, a cloud
// project -- and decides where on it a machine goes; Rungar only chooses
// between providers.
//
// Each backend is a Type, registered by name in internal/config. Rungar reads
// the keys every provider entry has (name, type, weight, max_runners,
// disabled and runner) and hands the Type the rest, and each scale set's
// runner block for the provider merged over the provider's (see Merge).
package provider

import (
	"context"
	"log/slog"

	"github.com/konradasb/rungar/internal/types"
)

// Type is a kind of backend.
type Type interface {
	// Configure parses and validates a provider's entry, less the keys every
	// provider has. It contacts nothing.
	Configure(name string, config *Node) (Config, error)
}

// Config is a configured provider, not yet connected.
type Config interface {
	// ParseRunner parses and validates a runner block, already merged over
	// the provider's, as the complete runner. A nil node is an empty block.
	ParseRunner(node *Node) (types.RunnerSpec, error)

	// Open returns the connected provider. It may connect lazily, leaving a
	// backend that is down to be reported by the first call.
	Open(logger *slog.Logger) (Provider, error)

	// Endpoint names the backend, normalised so that two providers naming
	// the same one compare equal. Providers may not share an endpoint.
	Endpoint() string
}

// FileChecker is a Config that can check the files it names without
// contacting anything, for rungar validate.
type FileChecker interface {
	CheckFiles() error
}

// SecretFileNamer is a Config that names the files holding its secrets, which
// the daemon warns of at start if anyone can read them.
type SecretFileNamer interface {
	SecretFiles() []string
}

// Provider is a connected backend. It is safe for concurrent use, and bounds
// each call by a timeout of its own as well as its context.
type Provider interface {
	// List returns the machines carrying every label in selector. An error
	// means what is on the backend is unknown, not that it is empty.
	List(ctx context.Context, selector map[string]string) ([]types.Machine, error)

	// Create makes a machine and starts it, wherever on the backend it
	// chooses. The machine is never restarted: its registration is good for
	// one use.
	//
	// Rungar does not ask whether a provider has room; it tries it. On an
	// error it tries the next provider, and the scale set skips this one for
	// a while, longer each time in a row.
	//
	// An error may be marked errdefs.NoCapacity when the backend knows it is
	// full for this runner now (CPU, memory, a quota, a zone out of stock).
	// A scale set of higher priority then keeps lower ones off the provider,
	// so that the room it is waiting for goes to it. Marking is optional: an
	// unmarked error is handled safely, only without the hold.
	//
	// Rungar removes any machine a failed Create left, with Delete, before
	// trying elsewhere.
	Create(ctx context.Context, spec types.MachineSpec) error

	// Delete removes a machine, stopping it first. A machine already gone is
	// not an error.
	Delete(ctx context.Context, name string) error

	// Close releases the provider's connections, cancelling calls in flight.
	Close() error
}
