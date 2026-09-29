// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package types

import "time"

// ProviderSnapshot is a provider as placement sees it: whether it may be tried,
// and how many runners it has against its limit. Placement never knows what a
// provider has spare; a full provider refuses.
type ProviderSnapshot struct {
	Name   string
	Type   string
	Weight float64

	// Disabled keeps the provider out of placement, leaving the runners on
	// it to finish their jobs.
	Disabled bool

	// Reachable reports whether the provider answered when last asked, and
	// Error why it did not.
	Reachable bool
	Error     string

	// MaxRunners is the most runners the provider may have, of every scale
	// set, or zero for no limit.
	MaxRunners int

	// RunnerCount is how many of Rungar's runners are on the provider, those
	// being created included.
	RunnerCount int

	// Hold is the scale set keeping lower priorities off the provider, if
	// any.
	Hold *Hold
}

// Hold is a scale set that found a provider full, keeping lower priorities off
// it so that the room it is waiting for goes to it.
type Hold struct {
	ScaleSet string
	Priority int

	// Remaining is how long the hold lasts unless the scale set renews it.
	Remaining time.Duration
}

// Provider is a provider as configured and as placement sees it.
type Provider struct {
	Snapshot ProviderSnapshot
	Endpoint string

	// ConfiguredDisabled is what the configuration says; Snapshot.Disabled
	// differs from it when changed while the daemon runs.
	ConfiguredDisabled bool

	// ScaleSets are the configured scale sets placed on the provider.
	ScaleSets []ProviderScaleSet
}

// ProviderScaleSet is how a provider stands for one scale set placed on it.
type ProviderScaleSet struct {
	Name string

	// BackoffFor is how much longer the scale set skips the provider after
	// it refused the scale set's runner, Refusals how many times in a row it
	// has, and Refusal why it last did. Full is whether that refusal said
	// the provider was full.
	BackoffFor time.Duration
	Refusals   int
	Refusal    string
	Full       bool
}
