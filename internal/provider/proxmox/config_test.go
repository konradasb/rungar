// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package proxmox_test

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
	"github.com/konradasb/rungar/internal/provider/proxmox"
	"github.com/konradasb/rungar/internal/types"
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

func TestConfigure(t *testing.T) {
	cfg, err := proxmox.Type{}.Configure("pve", node(t, `
url: https://PVE.example.com
token_id: rungar@pve!rungar
token_secret: s3cret
nodes: [pve1, pve2]
`))
	if err != nil {
		t.Fatalf("Configure() = %v", err)
	}

	c := cfg.(*proxmox.Config) //nolint:forcetypeassert // Configure returns a *Config
	if c.Timeout != 10*time.Second {
		t.Errorf("Timeout = %v, want the default 10s", c.Timeout)
	}
	if got := c.Endpoint(); got != "https://pve.example.com:8006" {
		t.Errorf("Endpoint() = %q, want the URL in lower case, with its port", got)
	}
	if !slices.Equal(c.Nodes, []string{"pve1", "pve2"}) {
		t.Errorf("Nodes = %v", c.Nodes)
	}
}

func TestConfigureRefusesUnknownKeys(t *testing.T) {
	_, err := proxmox.Type{}.Configure("pve", node(t, `
url: https://pve.example.com:8006
token_id: rungar@pve!rungar
token_secret: s3cret
tokn: typo
`))
	if !errors.Is(err, errdefs.ErrInvalidArgument) {
		t.Errorf("Configure() = %v, want invalid", err)
	}
}

func TestConfigValidate(t *testing.T) {
	valid := func() proxmox.Config {
		return proxmox.Config{
			URL: "https://pve.example.com:8006", TokenID: "rungar@pve!rungar", TokenSecret: "s", Timeout: time.Second,
		}
	}

	tests := []struct {
		name   string
		change func(c *proxmox.Config)
		ok     bool
	}{
		{name: "valid", change: func(*proxmox.Config) {}, ok: true},
		{name: "secret from a file", change: func(c *proxmox.Config) { c.TokenSecret, c.TokenSecretPath = "", "/etc/s" }, ok: true},
		{name: "a CA", change: func(c *proxmox.Config) { c.TLS = &proxmox.TLS{CAFile: "/ca.pem"} }, ok: true},
		{name: "insecure", change: func(c *proxmox.Config) { c.TLS = &proxmox.TLS{InsecureSkipVerify: true} }, ok: true},
		{name: "no url", change: func(c *proxmox.Config) { c.URL = "" }},
		{name: "http", change: func(c *proxmox.Config) { c.URL = "http://pve.example.com:8006" }},
		{name: "a path", change: func(c *proxmox.Config) { c.URL = "https://pve.example.com:8006/api2/json" }},
		{name: "no token", change: func(c *proxmox.Config) { c.TokenID = "" }},
		{name: "a token without a name", change: func(c *proxmox.Config) { c.TokenID = "rungar@pve" }},
		{name: "a token without a realm", change: func(c *proxmox.Config) { c.TokenID = "rungar!rungar" }},
		{name: "no secret", change: func(c *proxmox.Config) { c.TokenSecret = "" }},
		{name: "both secrets", change: func(c *proxmox.Config) { c.TokenSecretPath = "/etc/s" }},
		{name: "a CA and insecure", change: func(c *proxmox.Config) {
			c.TLS = &proxmox.TLS{CAFile: "/ca.pem", InsecureSkipVerify: true}
		}},
		{name: "empty tls", change: func(c *proxmox.Config) { c.TLS = &proxmox.TLS{} }},
		{name: "an empty node", change: func(c *proxmox.Config) { c.Nodes = []string{""} }},
		{name: "a negative timeout", change: func(c *proxmox.Config) { c.Timeout = -1 }},
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

	c := &proxmox.Config{URL: "https://pve:8006", TokenID: "rungar@pve!rungar", TokenSecretPath: path, Timeout: time.Second}

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
	if _, err := c.Open(nil); err == nil {
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

func TestParseRunner(t *testing.T) {
	c := &proxmox.Config{}

	spec, err := c.ParseRunner(node(t, `{template: 9000, cores: 4, memory: 8GiB}`))
	if err != nil {
		t.Fatalf("ParseRunner() = %v", err)
	}

	r := spec.(proxmox.RunnerSpec) //nolint:forcetypeassert // ParseRunner returns a RunnerSpec
	if r.JITPath != "/run/rungar/jitconfig" {
		t.Errorf("JITPath = %q, want the default", r.JITPath)
	}
	if got := spec.Describe(); got != "4 cores, 8 GiB" {
		t.Errorf("Describe() = %q, want 4 cores, 8 GiB", got)
	}
}

func TestRunnerSpecValidate(t *testing.T) {
	valid := func() proxmox.RunnerSpec {
		return proxmox.RunnerSpec{Template: 9000, Cores: 2, Memory: 4 << 30, JITPath: "/run/rungar/jitconfig"}
	}

	tests := []struct {
		name   string
		change func(s *proxmox.RunnerSpec)
		ok     bool
	}{
		{name: "valid", change: func(*proxmox.RunnerSpec) {}, ok: true},
		{name: "a full clone to storage", change: func(s *proxmox.RunnerSpec) { s.FullClone, s.Storage = true, "ceph" }, ok: true},
		{name: "no template", change: func(s *proxmox.RunnerSpec) { s.Template = 0 }},
		{name: "not a VMID", change: func(s *proxmox.RunnerSpec) { s.Template = 99 }},
		{name: "no cores", change: func(s *proxmox.RunnerSpec) { s.Cores = 0 }},
		{name: "no memory", change: func(s *proxmox.RunnerSpec) { s.Memory = 0 }},
		{name: "too little memory", change: func(s *proxmox.RunnerSpec) { s.Memory = 64 << 20 }},
		{name: "memory not in MiB", change: func(s *proxmox.RunnerSpec) { s.Memory = 4<<30 + 1 }},
		{name: "storage for a linked clone", change: func(s *proxmox.RunnerSpec) { s.Storage = "ceph" }},
		{name: "a relative jit_path", change: func(s *proxmox.RunnerSpec) { s.JITPath = "run/jit" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := valid()
			tt.change(&s)

			err := s.Validate()
			switch {
			case tt.ok && err != nil:
				t.Errorf("Validate() = %v, want nil", err)
			case !tt.ok && !errors.Is(err, errdefs.ErrInvalidArgument):
				t.Errorf("Validate() = %v, want invalid", err)
			}
		})
	}
}

// A spec is encoded as JSON for its revision, which must not change with
// field order or defaults the block did not write.
func TestRunnerRevisionIsStable(t *testing.T) {
	c := &proxmox.Config{}

	a, err := c.ParseRunner(node(t, `{template: 9000, cores: 4, memory: 8GiB}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.ParseRunner(node(t, `{memory: 8GiB, cores: 4, template: 9000, jit_path: /run/rungar/jitconfig}`))
	if err != nil {
		t.Fatal(err)
	}

	ra, _ := types.RunnerRevision(a)
	rb, _ := types.RunnerRevision(b)
	if ra != rb {
		t.Errorf("revisions %s and %s differ for the same runner", ra, rb)
	}
}
