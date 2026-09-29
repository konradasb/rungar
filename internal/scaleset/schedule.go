// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import "log/slog"

// updateMinRunners returns the min_runners in force now, as the scale set's
// schedule sets it, and records it. A change from the last is logged and
// recorded as an event, so that runners created or removed by the schedule are
// explained. s.decisionLock must be held, so that changes are recorded in
// order.
func (s *scaleSet) updateMinRunners() int {
	n, window := s.spec.MinRunners, ""
	if i, ok := s.spec.Schedule.WindowAt(s.now()); ok {
		w := s.spec.Schedule.Windows[i]
		n, window = w.MinRunners, w.String()
	}

	s.mu.Lock()
	previous := s.minRunners
	s.minRunners = n
	s.mu.Unlock()
	s.metrics.SetMinRunners(s.spec.Name, n)

	// The first decision has nothing to change from.
	if previous < 0 || previous == n {
		return n
	}

	attrs := []any{slog.Int("from", previous), slog.Int("to", n)}
	if window != "" {
		attrs = append(attrs, slog.String("window", window))
	}
	s.logger.Info("min_runners changed", attrs...)
	s.events.Record(minRunnersChangedEvent(s.spec, previous, n, window))

	return n
}
