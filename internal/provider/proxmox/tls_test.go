// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package proxmox

import (
	"crypto/tls"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/konradasb/rungar/internal/errdefs"
)

func TestTLSLoadTrustsWhatItIsGiven(t *testing.T) {
	f := newFakePVE(t)
	dir := t.TempDir()

	notPEM := filepath.Join(dir, "not.pem")
	if err := os.WriteFile(notPEM, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		tls      *TLS
		roots    bool
		insecure bool
		ok       bool
	}{
		{name: "unset", ok: true},
		{name: "a CA file", tls: &TLS{CAFile: f.caFile(t)}, roots: true, ok: true},
		{name: "insecure", tls: &TLS{InsecureSkipVerify: true}, insecure: true, ok: true},
		{name: "a CA file that is not PEM", tls: &TLS{CAFile: notPEM}},
		{name: "a CA file that is missing", tls: &TLS{CAFile: filepath.Join(dir, "missing.pem")}},
		{name: "nothing configured", tls: &TLS{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := tt.tls.Load()
			if !tt.ok {
				if !errors.Is(err, errdefs.ErrInvalidArgument) {
					t.Errorf("Load() = %v, want invalid", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() = %v", err)
			}

			if cfg.MinVersion != tls.VersionTLS12 {
				t.Errorf("MinVersion = %x, want TLS 1.2", cfg.MinVersion)
			}
			if got := cfg.RootCAs != nil; got != tt.roots {
				t.Errorf("RootCAs set = %v, want %v", got, tt.roots)
			}
			if cfg.InsecureSkipVerify != tt.insecure {
				t.Errorf("InsecureSkipVerify = %v, want %v", cfg.InsecureSkipVerify, tt.insecure)
			}
		})
	}
}

// TestTLSLoadKeepsTheCause checks a CA file that cannot be read says why, as
// an error a caller can match.
func TestTLSLoadKeepsTheCause(t *testing.T) {
	_, err := (&TLS{CAFile: filepath.Join(t.TempDir(), "missing.pem")}).Load()
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Load() = %v, want it to wrap fs.ErrNotExist", err)
	}
}
