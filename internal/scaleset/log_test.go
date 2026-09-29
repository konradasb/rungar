// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// TestListenerLogIsDemoted checks the listener's INFO lines are written at
// DEBUG, and its warnings as they are.
func TestListenerLogIsDemoted(t *testing.T) {
	var buf bytes.Buffer
	logger := demoted(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))

	logger.Info("Getting next message")
	logger.Warn("session expired")

	if out := buf.String(); strings.Contains(out, "Getting next message") || !strings.Contains(out, "session expired") {
		t.Errorf("at INFO the log is %q, want the warning alone", out)
	}

	buf.Reset()
	logger = demoted(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	logger.With(slog.String("scale_set", "rungar-vm")).Info("Getting next message")

	if out := buf.String(); !strings.Contains(out, "level=DEBUG") || !strings.Contains(out, "scale_set=rungar-vm") {
		t.Errorf("at DEBUG the log is %q, want the line at DEBUG with its attributes", out)
	}
}
