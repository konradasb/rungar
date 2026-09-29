// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konradasb/rungar/internal/config"
)

// TestNoSocketSaysRungarIsNotRunning checks a command with no daemon to ask
// says so, and how to point it at another socket.
func TestNoSocketSaysRungarIsNotRunning(t *testing.T) {
	// Short, as newHarness's is: a longer path than a socket's can be is
	// refused as invalid rather than missing.
	dir, err := os.MkdirTemp("", "rungar")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	cmd := NewCommand()
	cmd.SetArgs([]string{"status", "--socket", filepath.Join(dir, "rungar.sock")})
	cmd.SetOut(io.Discard)

	err = cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "is rungar running?") || !strings.Contains(err.Error(), socketEnv) {
		t.Errorf("rungar status = %v, want it to say rungar is not running, and how to say where it is", err)
	}
}

// TestSocketPathPrefersFlagThenEnvironment checks --socket wins over
// $RUNGAR_SOCKET, which wins over the default.
func TestSocketPathPrefersFlagThenEnvironment(t *testing.T) {
	cmd := NewCommand()

	t.Setenv(socketEnv, "")
	if got := socketPath(cmd); got != config.DefaultSocket {
		t.Errorf("socketPath() = %q, want the default", got)
	}

	t.Setenv(socketEnv, "/tmp/from-env.sock")
	if got := socketPath(cmd); got != "/tmp/from-env.sock" {
		t.Errorf("socketPath() = %q, want $%s", got, socketEnv)
	}

	if err := cmd.ParseFlags([]string{"--socket", "/tmp/from-flag.sock"}); err != nil {
		t.Fatal(err)
	}
	if got := socketPath(cmd); got != "/tmp/from-flag.sock" {
		t.Errorf("socketPath() = %q, want --socket over the environment", got)
	}
}
