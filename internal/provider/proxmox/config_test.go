// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package proxmox

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	yaml "gopkg.in/yaml.v3"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/provider"
)

// node parses YAML into a configuration node.
func node(t *testing.T, doc string) *provider.Node {
	t.Helper()

	var n yaml.Node
	if err := yaml.Unmarshal([]byte(doc), &n); err != nil {
		t.Fatal(err)
	}

	return n.Content[0]
}

func TestConfigureFillsInDefaults(t *testing.T) {
	cfg, err := Type{}.Configure(node(t, `
url: https://PVE.example.com
token_id: rungar@pve!rungar
token_secret: s3cret
nodes: [pve1, pve2]
`))
	if err != nil {
		t.Fatalf("Configure() = %v", err)
	}

	c, ok := cfg.(*Config)
	if !ok {
		t.Fatalf("Configure() = %T, want a *Config", cfg)
	}
	if c.Timeout != 10*time.Second {
		t.Errorf("Timeout = %v, want the default 10s", c.Timeout)
	}
	if got := c.Endpoint(); got != "proxmox:https://pve.example.com:8006" {
		t.Errorf("Endpoint() = %q, want the URL in lower case, with its port", got)
	}
	if !slices.Equal(c.Nodes, []string{"pve1", "pve2"}) {
		t.Errorf("Nodes = %v", c.Nodes)
	}
}

func TestConfigureRefusesUnknownKeys(t *testing.T) {
	_, err := Type{}.Configure(node(t, `
url: https://pve.example.com:8006
token_id: rungar@pve!rungar
token_secret: s3cret
tokn: typo
`))
	if !errors.Is(err, errdefs.ErrInvalidArgument) {
		t.Errorf("Configure() = %v, want invalid", err)
	}
}

func TestValidateRefusesAnInvalidEntry(t *testing.T) {
	valid := func() Config {
		return Config{
			URL: "https://pve.example.com:8006", TokenID: "rungar@pve!rungar", TokenSecret: "s", Timeout: time.Second,
		}
	}

	tests := []struct {
		name   string
		change func(c *Config)
		ok     bool
	}{
		{name: "valid", change: func(*Config) {}, ok: true},
		{name: "secret from a file", change: func(c *Config) { c.TokenSecret, c.TokenSecretPath = "", "/etc/s" }, ok: true},
		{name: "a CA", change: func(c *Config) { c.TLS = &TLS{CAFile: "/ca.pem"} }, ok: true},
		{name: "insecure", change: func(c *Config) { c.TLS = &TLS{InsecureSkipVerify: true} }, ok: true},
		{name: "no url", change: func(c *Config) { c.URL = "" }},
		{name: "http", change: func(c *Config) { c.URL = "http://pve.example.com:8006" }},
		{name: "a path", change: func(c *Config) { c.URL = "https://pve.example.com:8006/api2/json" }},
		{name: "no token", change: func(c *Config) { c.TokenID = "" }},
		{name: "a token without a name", change: func(c *Config) { c.TokenID = "rungar@pve" }},
		{name: "a token without a realm", change: func(c *Config) { c.TokenID = "rungar!rungar" }},
		{name: "no secret", change: func(c *Config) { c.TokenSecret = "" }},
		{name: "both secrets", change: func(c *Config) { c.TokenSecretPath = "/etc/s" }},
		{name: "a CA and insecure", change: func(c *Config) {
			c.TLS = &TLS{CAFile: "/ca.pem", InsecureSkipVerify: true}
		}},
		{name: "empty tls", change: func(c *Config) { c.TLS = &TLS{} }},
		{name: "an empty node", change: func(c *Config) { c.Nodes = []string{""} }},
		{name: "a negative timeout", change: func(c *Config) { c.Timeout = -1 }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := valid()
			tt.change(&c)

			err := c.Validate()
			switch {
			case tt.ok && err != nil:
				t.Errorf("Validate() = %v, want nil", err)
			case !tt.ok && !errors.Is(err, errdefs.ErrInvalidArgument):
				t.Errorf("Validate() = %v, want invalid", err)
			}
		})
	}
}

func TestTheSecretIsReadFromItsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token")
	if err := os.WriteFile(path, []byte("s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	c := &Config{URL: "https://pve:8006", TokenID: "rungar@pve!rungar", TokenSecretPath: path, Timeout: time.Second}

	if err := c.CheckFiles(); err != nil {
		t.Errorf("CheckFiles() = %v", err)
	}
	if got := c.SecretFiles(); !slices.Equal(got, []string{path}) {
		t.Errorf("SecretFiles() = %v, want %s", got, path)
	}

	c.TokenSecretPath = filepath.Join(dir, "missing")
	if err := c.CheckFiles(); !errors.Is(err, errdefs.ErrInvalidArgument) {
		t.Errorf("CheckFiles() of a missing file = %v, want invalid", err)
	}
	if _, err := c.Open(t.Context(), nil); err == nil {
		t.Error("Open() with a missing secret = nil error")
	}

	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c.TokenSecretPath = empty
	if err := c.CheckFiles(); !errors.Is(err, errdefs.ErrInvalidArgument) {
		t.Errorf("CheckFiles() of an empty file = %v, want invalid", err)
	}
}
