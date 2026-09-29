// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package fleet

import (
	"fmt"
	"strconv"
	"time"

	"github.com/konradasb/rungar/internal/events"
)

// refusalEvent returns the event for a provider that refused to create a scale
// set's runner, which the scale set skips for wait. Its action is
// events.ActionFailing.
func refusalEvent(scaleSet, provider, runner string, wait time.Duration, refusals int, err error) events.Event {
	message := fmt.Sprintf("Failed to create runner %s: %v; %s tries the next provider, and this one again in %s",
		runner, err, scaleSet, wait)
	if refusals > 1 {
		message += fmt.Sprintf(" (%d refusals in a row)", refusals)
	}

	return providerEvent(events.ActionFailing, scaleSet, provider, message, map[string]string{
		"runner": runner, "skipped_for": wait.String(), "refusals": strconv.Itoa(refusals), "error": err.Error(),
	})
}

// fullEvent returns the event for a provider a scale set found full, and skips
// for wait.
func fullEvent(scaleSet, provider, runner string, wait time.Duration, err error) events.Event {
	message := fmt.Sprintf("Full for runner %s: %v; %s tries the next provider, and this one again in %s",
		runner, err, scaleSet, wait)

	return providerEvent(events.ActionFull, scaleSet, provider, message, map[string]string{
		"runner": runner, "skipped_for": wait.String(), "error": err.Error(),
	})
}

// providerEvent returns an event of action about provider, for scaleSet.
func providerEvent(action events.Action, scaleSet, provider, message string, attrs map[string]string) events.Event {
	return events.Event{
		Kind:       events.KindProvider,
		Name:       provider,
		Action:     action,
		ScaleSet:   scaleSet,
		Provider:   provider,
		Message:    message,
		Attributes: attrs,
	}
}
