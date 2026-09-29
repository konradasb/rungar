// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"github.com/actions/scaleset/listener"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/events"
	"github.com/konradasb/rungar/internal/types"
)

// SetPaused pauses a configured scale set, or resumes it, until the
// daemon restarts, and returns it. What pausing does is setPaused's.
func (m *Manager) SetPaused(name string, paused bool) (types.ScaleSet, error) {
	s, ok := m.configuredScaleSet(name)
	if !ok {
		return types.ScaleSet{}, errdefs.NotFound("no scale set %q in the configuration", name)
	}

	s.setPaused(paused)

	return s.status(), nil
}

// setPaused pauses the scale set, or resumes it. Paused, it tells GitHub it
// has no room, so that no job is assigned to it, creates no runner, and removes
// its runners not running a job at once rather than at its next tick; its busy
// runners finish their jobs and go, as they always do. Resumed, it takes jobs
// again, and creates its min_runners at once.
func (s *scaleSet) setPaused(paused bool) {
	s.mu.Lock()
	if s.paused == paused {
		s.mu.Unlock()
		return
	}
	s.paused = paused
	s.syncCapacity()
	s.mu.Unlock()

	s.metrics.SetPaused(s.spec.Name, paused)

	if paused {
		s.logger.Info("paused until resumed or the daemon restarts: taking no jobs")
		s.events.Record(scaleSetEvent(s.spec.Name, events.ActionPaused,
			"Scale set paused: taking no jobs, and creating no runners"))
	} else {
		s.logger.Info("resumed until paused or the daemon restarts: taking jobs")
		s.events.Record(scaleSetEvent(s.spec.Name, events.ActionResumed, "Scale set resumed: taking jobs again"))
	}

	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// isPaused reports whether the scale set is paused.
func (s *scaleSet) isPaused() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.paused
}

// setListener records the running listener, or nil once it has stopped, and
// gives it the capacity the scale set has now.
func (s *scaleSet) setListener(l *listener.Listener) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.listener = l
	s.syncCapacity()
}

// syncCapacity gives the listener, if one is running, the capacity it reports
// to GitHub with its next poll: max_runners, or none while paused. s.mu must be
// held.
func (s *scaleSet) syncCapacity() {
	if s.listener == nil {
		return
	}

	capacity := s.spec.MaxRunners
	if s.paused {
		capacity = 0
	}
	s.listener.SetMaxRunners(capacity)
}
