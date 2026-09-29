// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// replaceFile replaces the file at path with b, whole or not at all: b is
// written beside it, with its mode and owner, and renamed over it. A symbolic
// link is followed, and the file it names replaced.
func replaceFile(path string, b []byte) error {
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	path = target

	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}

	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	// Gone once renamed; left behind only if something failed first.
	defer func() { _ = os.Remove(f.Name()) }()

	if err := writeLike(f, b, info); err != nil {
		_ = f.Close()
		return fmt.Errorf("replace %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}

	if err := os.Rename(f.Name(), path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}

	return nil
}

// writeLike writes b to f, and gives f the mode and owner of the file info
// describes: the daemon's user reads the configuration through its group.
func writeLike(f *os.File, b []byte, info os.FileInfo) error {
	if err := f.Chmod(info.Mode().Perm()); err != nil {
		return fmt.Errorf("keep the file's mode: %w", err)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		if err := f.Chown(int(st.Uid), int(st.Gid)); err != nil {
			return fmt.Errorf("keep the file's owner, %d:%d: %w", st.Uid, st.Gid, err)
		}
	}
	if _, err := f.Write(b); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync: %w", err)
	}

	return nil
}
