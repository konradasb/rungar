// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"log/slog"
)

// component returns a logger that tags its records with the part of the daemon
// they come from: component=scaleset.
func component(logger *slog.Logger, name string) *slog.Logger {
	return logger.With(slog.String("component", name))
}

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

func (h demotingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, demote(level))
}

func (h demotingHandler) Handle(ctx context.Context, r slog.Record) error {
	r.Level = demote(r.Level)
	return h.next.Handle(ctx, r)
}

func (h demotingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return demotingHandler{h.next.WithAttrs(attrs)}
}

func (h demotingHandler) WithGroup(name string) slog.Handler {
	return demotingHandler{h.next.WithGroup(name)}
}

func demote(level slog.Level) slog.Level {
	if level == slog.LevelInfo {
		return slog.LevelDebug
	}

	return level
}
