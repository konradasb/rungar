// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package fleet

import (
	"fmt"
	"strconv"
	"time"

	"github.com/konradasb/rungar/internal/types"
)

// failingEvent records a provider that failed to make a scale set's runner,
// which the scale set skips for wait.
func failingEvent(scaleSet, provider, runner string, wait time.Duration, failures int, err error) types.Event {
	message := fmt.Sprintf("Failed to make runner %s: %v; %s tries the next provider, and this one again in %s",
		runner, err, scaleSet, wait)
	if failures > 1 {
		message += fmt.Sprintf(" (%d failures in a row)", failures)
	}

	return providerEvent(types.ActionFailing, scaleSet, provider, message, map[string]string{
		"runner": runner, "for": wait.String(), "failures": strconv.Itoa(failures), "error": err.Error(),
	})
}

// fullEvent records a provider a scale set found full, and skips for wait.
func fullEvent(scaleSet, provider, runner string, wait time.Duration, err error) types.Event {
	message := fmt.Sprintf("Full for runner %s: %v; %s tries the next provider, and this one again in %s",
		runner, err, scaleSet, wait)

	return providerEvent(types.ActionFull, scaleSet, provider, message, map[string]string{
		"runner": runner, "for": wait.String(), "error": err.Error(),
	})
}

func providerEvent(action types.EventAction, scaleSet, provider, message string, attrs map[string]string) types.Event {
	return types.Event{
		Kind:       types.KindProvider,
		Name:       provider,
		Action:     action,
		ScaleSet:   scaleSet,
		Provider:   provider,
		Message:    message,
		Attributes: attrs,
	}
}
