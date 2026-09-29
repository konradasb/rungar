// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package docker

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/konradasb/rungar/internal/errdefs"
)

func TestConfigureDefaults(t *testing.T) {
	c := configure(t, "")

	if c.Address != "unix:///var/run/docker.sock" {
		t.Errorf("Address = %q, want the default socket", c.Address)
	}
	if c.Timeout != 10*time.Second {
		t.Errorf("Timeout = %v, want 10s", c.Timeout)
	}
}

func TestConfigureRefuses(t *testing.T) {
	tests := []struct {
		name, doc, want string
	}{
		{"relative socket", "address: unix://docker.sock", "must be absolute"},
		{"tls on a socket", "address: unix:///var/run/docker.sock\ntls: {ca_file: /ca.pem}", "cannot have tls"},
		{"no scheme", "address: 10.0.0.1:2376", "want unix://PATH or tcp://HOST:PORT"},
		{"no port", "address: tcp://10.0.0.1", "want tcp://HOST:PORT"},
		{"empty tls", "address: tcp://10.0.0.1:2376\ntls: {}", "nothing is configured"},
		{"half a pair", "address: tcp://10.0.0.1:2376\ntls: {cert_file: /c.pem}", "without key_file"},
		{"negative timeout", "timeout: -1s", "timeout must be positive"},
		{"unknown key", "host: tcp://10.0.0.1:2376", "field host not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Type{}.Configure("build1", node(t, tt.doc))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Configure() = %v, want an error saying %q", err, tt.want)
			}
			if !errors.Is(err, errdefs.ErrInvalidArgument) {
				t.Errorf("Configure() = %v, want invalid argument", err)
			}
		})
	}
}

func TestEndpoint(t *testing.T) {
	tests := []struct{ address, want string }{
		{"unix:///var/run//docker.sock", "unix:///var/run/docker.sock"},
		{"tcp://Build1.Example:2376", "tcp://build1.example:2376"},
	}

	for _, tt := range tests {
		if got := configure(t, "address: "+tt.address).Endpoint(); got != tt.want {
			t.Errorf("Endpoint() of %s = %q, want %q", tt.address, got, tt.want)
		}
	}
}

func TestSecretFiles(t *testing.T) {
	c := configure(t, "address: tcp://10.0.0.1:2376\ntls: {cert_file: /c.pem, key_file: /k.pem}")
	if got := c.SecretFiles(); len(got) != 1 || got[0] != "/k.pem" {
		t.Errorf("SecretFiles() = %v, want the key file", got)
	}

	if got := configure(t, "").SecretFiles(); got != nil {
		t.Errorf("SecretFiles() = %v, want none without tls", got)
	}
}

func TestCheckFilesReportsAMissingFile(t *testing.T) {
	c := configure(t, "address: tcp://10.0.0.1:2376\ntls: {ca_file: /nonexistent/ca.pem}")

	if err := c.CheckFiles(); err == nil || !strings.Contains(err.Error(), "ca.pem") {
		t.Errorf("CheckFiles() = %v, want the missing file named", err)
	}
}
