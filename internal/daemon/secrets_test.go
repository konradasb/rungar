// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package daemon

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWarnExposed(t *testing.T) {
	tests := []struct {
		mode os.FileMode
		warn bool
	}{
		{mode: 0o600},
		{mode: 0o640},
		{mode: 0o660},
		{mode: 0o644, warn: true},
		{mode: 0o604, warn: true},
		{mode: 0o666, warn: true},
	}

	for _, tt := range tests {
		t.Run(tt.mode.String(), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "token")
			if err := os.WriteFile(path, []byte("ghp_secret"), 0o600); err != nil {
				t.Fatal(err)
			}
			// Set apart from the write, which the umask narrows.
			if err := os.Chmod(path, tt.mode); err != nil {
				t.Fatal(err)
			}

			var buf bytes.Buffer
			warnExposed(slog.New(slog.NewTextHandler(&buf, nil)), []string{path})

			if got := strings.Contains(buf.String(), "readable by anyone"); got != tt.warn {
				t.Errorf("warned = %v, want %v; logged %q", got, tt.warn, buf.String())
			}
			if tt.warn && !strings.Contains(buf.String(), path) {
				t.Errorf("the warning does not name the file: %q", buf.String())
			}
		})
	}
}

// TestWarnExposedMissingFile checks a file that is not there is left to
// loading to report, rather than warned of.
func TestWarnExposedMissingFile(t *testing.T) {
	var buf bytes.Buffer
	warnExposed(slog.New(slog.NewTextHandler(&buf, nil)), []string{filepath.Join(t.TempDir(), "nowhere")})

	if buf.Len() != 0 {
		t.Errorf("logged %q, want nothing", buf.String())
	}
}
