// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"fmt"
	"strconv"
	"time"

	"github.com/konradasb/rungar/internal/events"
	"github.com/konradasb/rungar/internal/types"
)

// createdEvent returns the event of a runner created, which took d.
func createdEvent(r types.Runner, d time.Duration) events.Event {
	message := "Runner created on " + r.Provider
	if r.Size != "" {
		message += ": " + r.Size
	}

	return runnerEvent(r.ScaleSet, r.Name, r.Provider, events.ActionCreated, message,
		map[string]string{"create_duration": d.String()})
}

// connectedEvent returns the event of a runner connected to GitHub, d after
// its machine was created.
func connectedEvent(r types.Runner, d time.Duration) events.Event {
	return runnerEvent(r.ScaleSet, r.Name, r.Provider, events.ActionConnected,
		"Runner connected to GitHub, "+roundedDuration(d).String()+" after its machine was created",
		map[string]string{"boot_duration": d.String()})
}

// jobStartedEvent returns the event of a runner taking its job, which waited
// as wait says if waitKnown.
func jobStartedEvent(r types.Runner, wait jobWait, waitKnown bool) events.Event {
	job := r.Job

	name := job.DisplayName
	if name == "" {
		name = job.ID
	}
	message := "Job started: " + name
	if job.Repository != "" {
		message += " of " + job.Repository
	}

	attrs := map[string]string{"job_id": job.ID, "repository": job.Repository}
	if job.WorkflowRunID != 0 {
		attrs["workflow_run_id"] = strconv.FormatInt(job.WorkflowRunID, 10)
	}
	if waitKnown {
		message += fmt.Sprintf(", after waiting %s, %s of it for a runner",
			roundedDuration(wait.total), roundedDuration(wait.forRunner))
		attrs["wait"] = wait.total.String()
		attrs["runner_wait"] = wait.forRunner.String()
	}

	return runnerEvent(r.ScaleSet, r.Name, r.Provider, events.ActionJobStarted, message, attrs)
}

// roundedDuration returns d rounded for people: to the second, or to the
// millisecond under one.
func roundedDuration(d time.Duration) time.Duration {
	if d < time.Second {
		return d.Round(time.Millisecond)
	}

	return d.Round(time.Second)
}

// adoptedEvent returns the event of a runner adopted; how says how it was
// found.
func adoptedEvent(scaleSet, name, provider string, how adoption) events.Event {
	return runnerEvent(scaleSet, name, provider, events.ActionAdopted,
		fmt.Sprintf("Runner adopted on %s, %s", provider, how), nil)
}

// removedEvent returns the event of a runner removed for reason.
func removedEvent(scaleSet, name, provider string, reason types.RemovalReason) events.Event {
	return runnerEvent(scaleSet, name, provider, events.ActionRemoved,
		fmt.Sprintf("Runner removed from %s: %s", provider, removalDescription(reason)),
		map[string]string{"reason": string(reason)})
}

// registrationRemovedEvent returns the event of an orphan's registration
// removed for reason.
func registrationRemovedEvent(scaleSet, name string, reason types.RemovalReason) events.Event {
	return runnerEvent(scaleSet, name, "", events.ActionRemoved,
		"Runner's registration removed from GitHub, as it has no machine: "+removalDescription(reason),
		map[string]string{"reason": string(reason)})
}

// lostEvent returns the event of a runner lost for reason.
func lostEvent(scaleSet, name, provider string, reason types.LossReason) events.Event {
	var why string
	switch reason {
	case types.LossEnded:
		why = "its machine on " + provider + " ended without its job completing"
	case types.LossUnreachable:
		why = fmt.Sprintf("provider %s has been unreachable for %s; it is replaced elsewhere",
			provider, unreachableGrace)
	default:
		why = string(reason)
	}

	return runnerEvent(scaleSet, name, provider, events.ActionLost, "Runner lost: "+why,
		map[string]string{"reason": string(reason)})
}

// runnerEvent returns an event of a runner.
func runnerEvent(scaleSet, name, provider string, action events.Action, message string,
	attrs map[string]string,
) events.Event {
	return events.Event{
		Kind:       events.KindRunner,
		Name:       name,
		Action:     action,
		ScaleSet:   scaleSet,
		Provider:   provider,
		Message:    message,
		Attributes: attrs,
	}
}

// scaleSetEvent returns an event of a scale set.
func scaleSetEvent(name string, action events.Action, message string) events.Event {
	return events.Event{
		Kind:     events.KindScaleSet,
		Name:     name,
		Action:   action,
		ScaleSet: name,
		Message:  message,
	}
}

// minRunnersChangedEvent returns the event of a scale set's min_runners
// changed by its schedule, from one number to another, with the window now in
// force, or empty outside every window.
func minRunnersChangedEvent(spec types.ScaleSetSpec, from, to int, window string) events.Event {
	why := "outside the schedule's windows"
	if window != "" {
		why = "in the schedule's window " + window + " " + spec.Schedule.TimeZone.String()
	}

	e := scaleSetEvent(spec.Name, events.ActionMinRunnersChanged,
		fmt.Sprintf("min_runners changed from %d to %d, %s", from, to, why))
	e.Attributes = map[string]string{"from": strconv.Itoa(from), "to": strconv.Itoa(to)}
	if window != "" {
		e.Attributes["window"] = window
	}

	return e
}

// removalDescription says why a runner was removed, for people.
func removalDescription(reason types.RemovalReason) string {
	switch reason {
	case types.RemovalJobCompleted:
		return "its job completed"
	case types.RemovalScaledDown:
		return "the scale set needed fewer runners"
	case types.RemovalNeverConnected:
		return "it did not connect to GitHub within the start timeout"
	case types.RemovalUnregistered:
		return "GitHub no longer had its registration"
	case types.RemovalDisconnected:
		return "it was disconnected from GitHub for longer than the start timeout"
	case types.RemovalStuck:
		return "it was running a job while disconnected from GitHub for longer than the start timeout"
	case types.RemovalOutdated:
		return "it was created from another revision of the runner spec"
	case types.RemovalExpired:
		return "it was older than max_idle_age or max_age"
	case types.RemovalRequested:
		return "it was asked to be removed"
	case types.RemovalOrphaned:
		return "it had no machine for longer than the start timeout"
	default:
		return string(reason)
	}
}
