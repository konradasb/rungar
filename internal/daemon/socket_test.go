// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package daemon

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// socketPath returns a path for a socket in a directory of its own, short
// enough for a socket, as a test's temporary directory can be too long.
func socketPath(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp("", "rungar")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	return filepath.Join(dir, "rungar.sock")
}

// TestListenSocketReplacesAStaleSocket checks a socket nothing serves on is
// replaced by one the daemon serves, readable by its user and group alone.
func TestListenSocketReplacesAStaleSocket(t *testing.T) {
	path := socketPath(t)

	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	// Left behind, as by a daemon that was killed.
	stale.SetUnlinkOnClose(false)
	_ = stale.Close()

	listener, err := listenSocket(t.Context(), path)
	if err != nil {
		t.Fatalf("listenSocket() = %v, want the stale socket replaced", err)
	}
	defer listener.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != socketMode {
		t.Errorf("socket mode = %04o, want %04o", got, socketMode)
	}

	conn, err := (&net.Dialer{}).DialContext(t.Context(), "unix", path)
	if err != nil {
		t.Fatalf("cannot connect to the new socket: %v", err)
	}
	_ = conn.Close()
}

// TestListenSocketRefusesALiveSocket checks a socket another daemon serves on
// is left alone.
func TestListenSocketRefusesALiveSocket(t *testing.T) {
	path := socketPath(t)

	live, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()

	_, err = listenSocket(t.Context(), path)
	if err == nil || !strings.Contains(err.Error(), "already serving") {
		t.Fatalf("listenSocket() = %v, want it to say another rungar is serving", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the live socket was removed: %v", err)
	}
}

// TestListenSocketRefusesAFileThatIsNotASocket checks a file in the socket's
// place is left alone.
func TestListenSocketRefusesAFileThatIsNotASocket(t *testing.T) {
	path := socketPath(t)
	if err := os.WriteFile(path, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := listenSocket(t.Context(), path)
	if err == nil || !strings.Contains(err.Error(), "is not a socket") {
		t.Fatalf("listenSocket() = %v, want it to say the file is not a socket", err)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "keep me" {
		t.Errorf("the file was changed: %q, %v", b, err)
	}
}
