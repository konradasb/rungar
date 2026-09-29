// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package types

import "time"

// EventKind is what an event is about.
type EventKind string

// Event kinds.
const (
	KindRunner   EventKind = "runner"
	KindProvider EventKind = "provider"
	KindScaleSet EventKind = "scale_set"
)

// EventAction is what happened.
type EventAction string

// Runner actions.
const (
	// ActionCreated is a runner made by this daemon.
	ActionCreated EventAction = "created"

	// ActionAdopted is a runner found on the fleet: left by an earlier
	// daemon, or made by this one and missed.
	ActionAdopted EventAction = "adopted"

	// ActionRemoved is a runner whose machine was removed. Its reason
	// attribute is a RemovalReason.
	ActionRemoved EventAction = "removed"

	// ActionLost is a runner forgotten without its machine being removed:
	// the machine went, or its provider stopped answering. Its reason
	// attribute is a LossReason.
	ActionLost EventAction = "lost"
)

// Provider actions.
const (
	// ActionFull is a provider a scale set found full, and skips for a while.
	ActionFull EventAction = "full"

	// ActionFailing is a provider that failed to make a scale set's runner,
	// which the scale set skips for a while.
	ActionFailing EventAction = "failing"
)

// Scale set actions.
const (
	// ActionPaused is a scale set paused: taking no jobs, and making no
	// runners.
	ActionPaused EventAction = "paused"

	// ActionResumed is a paused scale set taking jobs again.
	ActionResumed EventAction = "resumed"
)

// LossReason is why a runner was lost.
type LossReason string

// Loss reasons.
const (
	// LossGone is a runner whose machine was no longer on its provider.
	LossGone LossReason = "gone"

	// LossUnreachable is a runner whose provider did not answer for too
	// long, which is replaced elsewhere.
	LossUnreachable LossReason = "unreachable"
)

// Event is something the daemon did to, or found of, a runner, provider or
// scale set.
type Event struct {
	Time time.Time `json:"time"`
	Kind EventKind `json:"kind"`

	// Name is the runner's, the provider's or the scale set's.
	Name string `json:"name"`

	Action EventAction `json:"action"`

	// ScaleSet is the scale set it happened for, and Provider the provider
	// it happened on: the runner's, or the provider itself.
	ScaleSet string `json:"scale_set,omitempty"`
	Provider string `json:"provider,omitempty"`

	// Message says what happened, in a line for a person.
	Message string `json:"message,omitempty"`

	// Attributes say it for a program: reason, runner, failures, for.
	Attributes map[string]string `json:"attributes,omitempty"`
}

// EventFilter selects events. The zero EventFilter selects every event.
type EventFilter struct {
	ScaleSet string
	Provider string

	// Runner selects the events about the runner of this name.
	Runner string

	// Since selects the events recorded at or after it.
	Since time.Time
}

// Matches reports whether f selects e.
func (f EventFilter) Matches(e Event) bool {
	return (f.ScaleSet == "" || e.ScaleSet == f.ScaleSet) &&
		(f.Provider == "" || e.Provider == f.Provider) &&
		(f.Runner == "" || e.Kind == KindRunner && e.Name == f.Runner) &&
		!e.Time.Before(f.Since)
}
