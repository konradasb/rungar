// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package dicer_test

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	yaml "gopkg.in/yaml.v3"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/provider"
	"github.com/konradasb/rungar/internal/provider/dicer"
	"github.com/konradasb/rungar/internal/types"
)

const gib = 1 << 30

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name   string
		config dicer.Config
		ok     bool
	}{
		{
			name:   "socket",
			config: dicer.Config{Address: "unix:///run/dicer/dicer.sock"},
			ok:     true,
		},
		{
			name:   "tcp",
			config: dicer.Config{Address: "10.10.0.101:7443"},
			ok:     true,
		},
		{
			name:   "tcp with tls",
			config: dicer.Config{Address: "10.10.0.101:7443", TLS: &dicer.TLS{CAFile: "/etc/rungar/ca.pem"}},
			ok:     true,
		},
		{name: "no address", config: dicer.Config{}},
		{name: "tcp without a port", config: dicer.Config{Address: "10.10.0.101"}},
		{name: "a relative socket path", config: dicer.Config{Address: "unix://run/dicer/dicer.sock"}},
		{
			name:   "tls on a socket",
			config: dicer.Config{Address: "unix:///run/dicer/dicer.sock", TLS: &dicer.TLS{CAFile: "/ca.pem"}},
		},
		{
			name:   "a certificate without its key",
			config: dicer.Config{Address: "10.10.0.101:7443", TLS: &dicer.TLS{CertFile: "/c.pem"}},
		},
		{name: "empty tls", config: dicer.Config{Address: "10.10.0.101:7443", TLS: &dicer.TLS{}}},
		{name: "a negative timeout", config: dicer.Config{Address: "10.10.0.101:7443", Timeout: -1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// As Configure leaves it.
			if tt.config.Timeout == 0 {
				tt.config.Timeout = 10 * time.Second
			}

			err := tt.config.Validate()
			if tt.ok && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
			if !tt.ok && !errors.Is(err, errdefs.ErrInvalidArgument) {
				t.Errorf("Validate() = %v, want an ErrInvalidArgument", err)
			}
		})
	}
}

// TestEndpoint checks two addresses naming one daemon compare equal, which is
// what lets the daemon refuse two providers of the same Dicer host.
func TestEndpoint(t *testing.T) {
	for _, pair := range [][2]string{
		{"dns:///Compute1:7443", "compute1:7443"},
		{"unix:///run/dicer/dicer.sock", "unix:///run/dicer/../dicer/dicer.sock"},
	} {
		a, b := dicer.Config{Address: pair[0]}, dicer.Config{Address: pair[1]}
		if a.Endpoint() != b.Endpoint() {
			t.Errorf("%s and %s are one daemon, but %q != %q", pair[0], pair[1], a.Endpoint(), b.Endpoint())
		}
	}
}

// TestConnectRefusesTLSThatWillNotLoad checks that TLS material that is not
// there is reported when the provider is connected, rather than on the first
// call to the daemon.
func TestConnectRefusesTLSThatWillNotLoad(t *testing.T) {
	config := configure(t, `
address: 10.0.0.1:7443
tls:
  ca_file: `+filepath.Join(t.TempDir(), "missing.pem")+`
`)

	if _, err := config.Open(nil); err == nil {
		t.Error("Connect() = nil, want an error for a CA file that is not there")
	}
}

// configure reads a Dicer provider's configuration, written as it is in the
// configuration file.
func configure(t *testing.T, body string) provider.Config {
	t.Helper()

	config, err := dicer.Type{}.Configure("rack1", node(t, body))
	if err != nil {
		t.Fatalf("Configure() = %v", err)
	}

	return config
}

func node(t *testing.T, body string) *provider.Node {
	t.Helper()

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(doc.Content) == 0 {
		return nil
	}

	return doc.Content[0]
}

func TestConfigure(t *testing.T) {
	configured := configure(t, `
address: 10.10.0.101:7443
tls:
  ca_file: /etc/rungar/ca.pem
timeout: 30s
`)

	config, ok := configured.(*dicer.Config)
	if !ok {
		t.Fatalf("Configure() = %T, want a *dicer.Config", configured)
	}
	if config.Address != "10.10.0.101:7443" || config.TLS == nil || config.TLS.CAFile != "/etc/rungar/ca.pem" {
		t.Errorf("config = %+v, want the address and TLS", config)
	}
	if config.Endpoint() != "10.10.0.101:7443" {
		t.Errorf("Endpoint() = %q", config.Endpoint())
	}
}

func TestConfigureRefuses(t *testing.T) {
	for name, body := range map[string]string{
		"nothing at all":         "",
		"an address that is not": "address: 10.0.0.1\n",
		"a misspelt setting":     "addr: 10.0.0.1:7443\n",
		"tls with nothing in it": "address: 10.0.0.1:7443\ntls: {}\n",
		// These are Rungar's, and never reach the type.
		"a runner block": "address: 10.0.0.1:7443\nrunner: {image: runner}\n",
		"hosts":          "hosts:\n  - {name: a, address: 10.0.0.1:7443}\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := dicer.Type{}.Configure("compute1", node(t, body))
			if !errors.Is(err, errdefs.ErrInvalidArgument) {
				t.Errorf("Configure() = %v, want an ErrInvalidArgument", err)
			}
		})
	}
}

const oneHost = "address: 10.0.0.1:7443\n"

func TestRunner(t *testing.T) {
	spec, err := configure(t, oneHost).ParseRunner(node(t, `
image: ghcr.io/actions/actions-runner:latest
kernel: vmlinux-6.12
vcpus: 4
memory: 8GiB
disk: 30GiB
`))
	if err != nil {
		t.Fatalf("Runner() = %v", err)
	}

	runner, ok := spec.(dicer.RunnerSpec)
	if !ok {
		t.Fatalf("Runner() = %T, want a dicer runner", spec)
	}
	if runner.ImageRef != "ghcr.io/actions/actions-runner:latest" || runner.KernelName != "vmlinux-6.12" {
		t.Errorf("runner = %+v, want the image and kernel", runner)
	}
	if runner.Disk != 30<<30 {
		t.Errorf("disk = %s, want 30GiB", runner.Disk)
	}
	if got := runner.Resources(); got != (types.Resources{VCPUs: 4, MemoryBytes: 8 << 30}) {
		t.Errorf("Resources() = %s, want 4 vCPU, 8 GiB", got)
	}
	if got := spec.Describe(); got != "4 vCPU, 8 GiB" {
		t.Errorf("Describe() = %q, want 4 vCPU, 8 GiB", got)
	}
}

func TestRunnerRefuses(t *testing.T) {
	config := configure(t, oneHost)

	for name, body := range map[string]string{
		"nothing at all":         "",
		"no size":                "image: runner\n",
		"a misspelt setting":     "image: runner\nvcpus: 2\nmemory: 4GiB\nkernal: x\n",
		"a size that is not one": "image: runner\nvcpus: 2\nmemory: lots\n",
		"a digest that is not":   "image: runner@sha256:nope\nvcpus: 2\nmemory: 4GiB\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := config.ParseRunner(node(t, body)); !errors.Is(err, errdefs.ErrInvalidArgument) {
				t.Errorf("Runner() = %v, want an ErrInvalidArgument", err)
			}
		})
	}
}

// TestRunnerDefaults checks what a runner block that says only what it must
// is given: Dicer has no default disk, and refuses an instance without one.
func TestRunnerDefaults(t *testing.T) {
	spec, err := configure(t, oneHost).ParseRunner(node(t, `
image: ghcr.io/actions/actions-runner:latest
vcpus: 2
memory: 4GiB
`))
	if err != nil {
		t.Fatalf("Runner() = %v", err)
	}

	runner, ok := spec.(dicer.RunnerSpec)
	if !ok {
		t.Fatalf("Runner() = %T, want a dicer runner", spec)
	}
	if runner.Disk != 20<<30 || len(runner.Command) != 1 || runner.Command[0] != "/home/runner/run.sh" {
		t.Errorf("runner = %+v, want a 20GiB disk and the runner image's start script", runner)
	}
}
