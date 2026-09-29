// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package events

import "time"

// Kind is what an event is about.
type Kind string

// Event kinds.
const (
	KindRunner   Kind = "runner"
	KindProvider Kind = "provider"
	KindScaleSet Kind = "scale_set"
)

// Action is what happened.
type Action string

// Runner actions.
const (
	// ActionCreated is a runner created by this daemon.
	ActionCreated Action = "created"

	// ActionAdopted is a runner found on the fleet: left by an earlier
	// daemon, or created by this one and missed.
	ActionAdopted Action = "adopted"

	// ActionRemoved is a runner the daemon removed, deleting its machine if
	// it had one. Its reason attribute is a types.RemovalReason.
	ActionRemoved Action = "removed"

	// ActionLost is a runner that ended without the daemon removing it or
	// its job completing: its machine ended on its own, or its provider
	// became unreachable. Its reason attribute is a types.LossReason.
	ActionLost Action = "lost"

	// ActionConnected is a runner created by this daemon that connected to
	// GitHub. Its boot_duration attribute is how long that took.
	ActionConnected Action = "connected"

	// ActionJobStarted is a runner that took a job.
	ActionJobStarted Action = "job_started"
)

// Provider actions.
const (
	// ActionFull is a provider a scale set found full, and skips for a while.
	ActionFull Action = "full"

	// ActionFailing is a provider that failed to create a scale set's runner,
	// which the scale set skips for a while.
	ActionFailing Action = "failing"
)

// Scale set actions.
const (
	// ActionPaused is a scale set paused: taking no jobs, and creating no
	// runners.
	ActionPaused Action = "paused"

	// ActionResumed is a paused scale set taking jobs again.
	ActionResumed Action = "resumed"

	// ActionMinRunnersChanged is a scale set whose schedule changed the
	// min_runners in force. Its from and to attributes are the numbers, and
	// its window attribute the window now in force, absent outside every
	// window.
	ActionMinRunnersChanged Action = "min_runners_changed"
)

// Event is something the daemon did to, or found of, a runner, provider or
// scale set.
type Event struct {
	Time time.Time `json:"time"`
	Kind Kind      `json:"kind"`

	// Name is the runner's, the provider's or the scale set's.
	Name string `json:"name"`

	Action Action `json:"action"`

	// ScaleSet is the scale set it happened for, and Provider the provider
	// it happened on: the runner's, or the provider itself.
	ScaleSet string `json:"scale_set,omitempty"`
	Provider string `json:"provider,omitempty"`

	// Message says what happened, in a line for a person.
	Message string `json:"message,omitempty"`

	// Attributes say it for a program: reason, runner, refusals,
	// skipped_for and the like.
	Attributes map[string]string `json:"attributes,omitempty"`
}

// Filter selects events. The zero Filter selects every event.
type Filter struct {
	ScaleSet string
	Provider string

	// Runner selects the events about the runner of this name.
	Runner string

	// Since selects the events recorded at or after it.
	Since time.Time
}

// Matches reports whether f selects e.
func (f Filter) Matches(e Event) bool {
	return (f.ScaleSet == "" || e.ScaleSet == f.ScaleSet) &&
		(f.Provider == "" || e.Provider == f.Provider) &&
		(f.Runner == "" || e.Kind == KindRunner && e.Name == f.Runner) &&
		!e.Time.Before(f.Since)
}
