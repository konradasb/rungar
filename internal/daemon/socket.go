// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// socketMode lets the daemon's user and group use the socket.
const socketMode = 0o660

// listenSocket listens on the Unix socket at path, replacing a stale one.
func listenSocket(ctx context.Context, path string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create the socket's directory: %w", err)
	}

	if err := removeStaleSocket(ctx, path); err != nil {
		return nil, err
	}

	// Owner-only until chmod, so nobody connects in between. The umask is
	// the process's, so a file another goroutine creates meanwhile is made
	// owner-only too: narrower than asked, never wider.
	old := syscall.Umask(0o177)
	listener, err := (&net.ListenConfig{}).Listen(ctx, "unix", path)
	syscall.Umask(old)

	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", path, err)
	}

	if err := os.Chmod(path, socketMode); err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("set the socket's permissions: %w", err)
	}

	return listener, nil
}

// removeStaleSocket removes a socket nothing answers on. It refuses a file that
// is not a socket, and a socket another daemon serves.
func removeStaleSocket(ctx context.Context, path string) error {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("check the socket: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("%s is there and is not a socket: remove it, or set socket to another path", path)
	}

	dialCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	if conn, err := (&net.Dialer{}).DialContext(dialCtx, "unix", path); err == nil {
		_ = conn.Close()
		return fmt.Errorf("another rungar is already serving on %s: stop it, or set socket to another path", path)
	}

	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove the stale socket: %w", err)
	}

	return nil
}
