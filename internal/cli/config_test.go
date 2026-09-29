// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestConfigMigratePrints checks migrate prints the file rewritten, and what
// it changed apart from it, leaving the file as it was.
func TestConfigMigratePrints(t *testing.T) {
	path := writeConfigFile(t, unversioned, 0o600)

	stdout, stderr, err := runLocal(t, "config", "migrate", "-f", path)
	if err != nil {
		t.Fatalf("rungar config migrate = %v", err)
	}

	if want := "version: 1\n\n# Rungar.\ngithub:\n"; !strings.HasPrefix(stdout, want) {
		t.Errorf("stdout =\n%s\nwant it to start\n%s", stdout, want)
	}
	if !strings.Contains(stderr, "Changed: "+path+": version is not set") {
		t.Errorf("stderr = %q, want the change", stderr)
	}

	if b, _ := os.ReadFile(path); string(b) != unversioned {
		t.Errorf("the file was changed without --write:\n%s", b)
	}
}

// TestConfigMigrateWrites checks --write replaces the file, keeping its mode,
// and that a second run finds nothing to do.
func TestConfigMigrateWrites(t *testing.T) {
	path := writeConfigFile(t, unversioned, 0o640)

	stdout, _, err := runLocal(t, "config", "migrate", "-f", path, "--write")
	if err != nil {
		t.Fatalf("rungar config migrate --write = %v", err)
	}
	if !strings.Contains(stdout, "is migrated to version 1: 1 change") {
		t.Errorf("stdout = %q, want the file migrated", stdout)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want the file's, 0640", info.Mode().Perm())
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "version: 1\n") {
		t.Errorf("the file was not rewritten:\n%s", b)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".config.yaml.*")); len(left) != 0 {
		t.Errorf("left behind %v", left)
	}

	stdout, stderr, err := runLocal(t, "config", "migrate", "-f", path, "--write")
	if err != nil || !strings.Contains(stdout, "is up to date") || stderr != "" {
		t.Errorf("rungar config migrate --write again = %v, %q, %q; want nothing to change", err, stdout, stderr)
	}
}

// TestConfigMigrateFollowsALink checks --write replaces the file a symbolic
// link names, not the link.
func TestConfigMigrateFollowsALink(t *testing.T) {
	target := writeConfigFile(t, unversioned, 0o600)
	link := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if _, _, err := runLocal(t, "config", "migrate", "-f", link, "--write"); err != nil {
		t.Fatalf("rungar config migrate --write = %v", err)
	}

	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the link was replaced: %v, %v", info, err)
	}
	if b, _ := os.ReadFile(target); !strings.Contains(string(b), "version: 1\n") {
		t.Errorf("the file the link names was not rewritten:\n%s", b)
	}
}
