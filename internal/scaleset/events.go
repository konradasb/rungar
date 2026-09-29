// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"fmt"

	"github.com/konradasb/rungar/internal/types"
)

// createdEvent returns the event of a runner made.
func createdEvent(r types.Runner) types.Event {
	message := "Runner created on " + r.Provider
	if r.Size != "" {
		message += ": " + r.Size
	}

	return runnerEvent(r.ScaleSet, r.Name, r.Provider, types.ActionCreated, message, nil)
}

// adoptedEvent returns the event of a runner adopted; how says how it was
// found.
func adoptedEvent(scaleSet, name, provider, how string) types.Event {
	return runnerEvent(scaleSet, name, provider, types.ActionAdopted,
		fmt.Sprintf("Runner adopted on %s, %s", provider, how), nil)
}

// removedEvent returns the event of a runner removed for reason.
func removedEvent(scaleSet, name, provider string, reason types.RemovalReason) types.Event {
	return runnerEvent(scaleSet, name, provider, types.ActionRemoved,
		fmt.Sprintf("Runner removed from %s: %s", provider, removalWhy(reason)),
		map[string]string{"reason": string(reason)})
}

// lostEvent returns the event of a runner forgotten for reason.
func lostEvent(scaleSet, name, provider string, reason types.LossReason) types.Event {
	var why string
	switch reason {
	case types.LossGone:
		why = "its VM on " + provider + " is gone"
	case types.LossUnreachable:
		why = fmt.Sprintf("provider %s has not answered for %s; it is replaced elsewhere", provider, unreachableGrace)
	default:
		why = string(reason)
	}

	return runnerEvent(scaleSet, name, provider, types.ActionLost, "Runner lost: "+why,
		map[string]string{"reason": string(reason)})
}

// runnerEvent returns an event of a runner.
func runnerEvent(scaleSet, name, provider string, action types.EventAction, message string,
	attrs map[string]string,
) types.Event {
	return types.Event{
		Kind:       types.KindRunner,
		Name:       name,
		Action:     action,
		ScaleSet:   scaleSet,
		Provider:   provider,
		Message:    message,
		Attributes: attrs,
	}
}

// scaleSetEvent returns an event of a scale set.
func scaleSetEvent(name string, action types.EventAction, message string) types.Event {
	return types.Event{
		Kind:     types.KindScaleSet,
		Name:     name,
		Action:   action,
		ScaleSet: name,
		Message:  message,
	}
}

// removalWhy says why a runner was removed, for people.
func removalWhy(reason types.RemovalReason) string {
	switch reason {
	case types.RemovalJobCompleted:
		return "its job completed"
	case types.RemovalScaledDown:
		return "the scale set needed fewer runners"
	case types.RemovalStopped:
		return "its VM had stopped"
	case types.RemovalNeverConnected:
		return "it did not connect to GitHub within the start timeout"
	case types.RemovalUnregistered:
		return "GitHub no longer had its registration"
	case types.RemovalDisconnected:
		return "it was disconnected from GitHub for longer than the start timeout"
	case types.RemovalStuck:
		return "it was running a job while disconnected from GitHub for longer than the start timeout"
	case types.RemovalOutdated:
		return "it was made from another revision of the runner spec"
	case types.RemovalExpired:
		return "it was older than max_idle_age or max_age"
	case types.RemovalRequested:
		return "it was asked to be removed"
	default:
		return string(reason)
	}
}
