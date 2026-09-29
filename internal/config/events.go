// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package config

import (
	"path/filepath"
	"time"

	"github.com/konradasb/rungar/internal/errdefs"
)

// defaultEventsFile is in the directory systemd's LogsDirectory gives the
// service.
const defaultEventsFile = "/var/log/rungar/events.jsonl"

// EventsConfig configures the event log, which rungar events shows.
type EventsConfig struct {
	// File is where the events are kept, one JSON object a line, so that
	// they outlive the daemon. Its directory is made if it is not there.
	// Unset is /var/log/rungar/events.jsonl.
	File string `yaml:"file,omitempty"`

	// MaxCount is how many events are kept: the most recent. Unset is
	// 10000.
	MaxCount int `yaml:"max_count,omitempty"`

	// MaxAge is how long an event is kept. Unset keeps each until max_count
	// newer ones push it out.
	MaxAge time.Duration `yaml:"max_age,omitempty"`
}

func (e *EventsConfig) validate() error {
	if !filepath.IsAbs(e.File) {
		return errdefs.InvalidArgument("events.file %q: want an absolute path", e.File)
	}
	if e.MaxCount < 0 {
		return errdefs.InvalidArgument("events.max_count cannot be negative")
	}
	if e.MaxAge < 0 {
		return errdefs.InvalidArgument("events.max_age cannot be negative")
	}

	return nil
}
