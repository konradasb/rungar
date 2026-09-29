// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"log/slog"
)

// demoted returns a logger that writes INFO records at DEBUG. The listener
// logs at INFO on every poll, which would otherwise be most of a quiet
// daemon's log.
func demoted(logger *slog.Logger) *slog.Logger {
	return slog.New(demotingHandler{logger.Handler()})
}

// demotingHandler passes records to next, INFO ones at DEBUG.
type demotingHandler struct {
	next slog.Handler
}

// Enabled reports whether next handles records of level, once demoted.
func (h demotingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, demote(level))
}

// Handle passes r to next, demoted.
func (h demotingHandler) Handle(ctx context.Context, r slog.Record) error {
	r.Level = demote(r.Level)
	return h.next.Handle(ctx, r)
}

// WithAttrs returns a demotingHandler over next with attrs.
func (h demotingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return demotingHandler{h.next.WithAttrs(attrs)}
}

// WithGroup returns a demotingHandler over next with the group name.
func (h demotingHandler) WithGroup(name string) slog.Handler {
	return demotingHandler{h.next.WithGroup(name)}
}

// demote returns DEBUG for INFO, and any other level as it is.
func demote(level slog.Level) slog.Level {
	if level == slog.LevelInfo {
		return slog.LevelDebug
	}

	return level
}
