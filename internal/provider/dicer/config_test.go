// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package dicer

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	yaml "gopkg.in/yaml.v3"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/provider"
)

func TestConfigValidateChecksAddressAndTLS(t *testing.T) {
	tests := []struct {
		name   string
		config Config
		ok     bool
	}{
		{
			name:   "socket",
			config: Config{Address: "unix:///run/dicer/dicer.sock"},
			ok:     true,
		},
		{
			name:   "tcp",
			config: Config{Address: "10.10.0.101:7443"},
			ok:     true,
		},
		{
			name:   "tcp with tls",
			config: Config{Address: "10.10.0.101:7443", TLS: &TLS{CAFile: "/etc/rungar/ca.pem"}},
			ok:     true,
		},
		{name: "no address", config: Config{}},
		{name: "tcp without a port", config: Config{Address: "10.10.0.101"}},
		{name: "a relative socket path", config: Config{Address: "unix://run/dicer/dicer.sock"}},
		{
			name:   "tls on a socket",
			config: Config{Address: "unix:///run/dicer/dicer.sock", TLS: &TLS{CAFile: "/ca.pem"}},
		},
		{
			name:   "a certificate without its key",
			config: Config{Address: "10.10.0.101:7443", TLS: &TLS{CertFile: "/c.pem"}},
		},
		{name: "empty tls", config: Config{Address: "10.10.0.101:7443", TLS: &TLS{}}},
		{name: "a negative timeout", config: Config{Address: "10.10.0.101:7443", Timeout: -1}},
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

// TestEndpointIsTheSameForOneDaemon checks two addresses naming one daemon
// compare equal, which is what lets the daemon refuse two providers of the
// same Dicer host.
func TestEndpointIsTheSameForOneDaemon(t *testing.T) {
	for _, pair := range [][2]string{
		{"dns:///Compute1:7443", "compute1:7443"},
		{"unix:///run/dicer/dicer.sock", "unix:///run/dicer/../dicer/dicer.sock"},
	} {
		t.Run(pair[0], func(t *testing.T) {
			a, b := Config{Address: pair[0]}, Config{Address: pair[1]}
			if a.Endpoint() != b.Endpoint() {
				t.Errorf("%s and %s are one daemon, but %q != %q", pair[0], pair[1], a.Endpoint(), b.Endpoint())
			}
		})
	}
}

// TestOpenRefusesTLSThatWillNotLoad checks that TLS material that is not
// there is reported when the provider is opened, rather than on the first
// call to the daemon.
func TestOpenRefusesTLSThatWillNotLoad(t *testing.T) {
	config := configure(t, `
address: 10.0.0.1:7443
tls:
  ca_file: `+filepath.Join(t.TempDir(), "missing.pem")+`
`)

	if _, err := config.Open(t.Context(), nil); !errors.Is(err, errdefs.ErrInvalidArgument) {
		t.Errorf("Open() = %v, want an ErrInvalidArgument for a CA file that is not there", err)
	}
}

// configure reads a Dicer provider's configuration, written as it is in the
// configuration file.
func configure(t *testing.T, body string) provider.Config {
	t.Helper()

	config, err := Type{}.Configure(node(t, body))
	if err != nil {
		t.Fatalf("Configure() = %v", err)
	}

	return config
}

// node parses YAML into the node a provider type is handed.
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

func TestConfigureReadsAddressAndTLS(t *testing.T) {
	configured := configure(t, `
address: 10.10.0.101:7443
tls:
  ca_file: /etc/rungar/ca.pem
timeout: 30s
`)

	config, ok := configured.(*Config)
	if !ok {
		t.Fatalf("Configure() = %T, want a *Config", configured)
	}
	if config.Address != "10.10.0.101:7443" || config.TLS == nil || config.TLS.CAFile != "/etc/rungar/ca.pem" {
		t.Errorf("config = %+v, want the address and TLS", config)
	}
	if config.Endpoint() != "dicer:10.10.0.101:7443" {
		t.Errorf("Endpoint() = %q", config.Endpoint())
	}
}

func TestConfigureRefuses(t *testing.T) {
	for name, body := range map[string]string{
		"nothing at all":         "",
		"an address that is not": "address: 10.0.0.1\n",
		"a misspelt setting":     "addr: 10.0.0.1:7443\n",
		"tls with nothing in it": "address: 10.0.0.1:7443\ntls: {}\n",
		// Rungar's, which never reaches the type.
		"a runner block": "address: 10.0.0.1:7443\nrunner: {image: runner}\n",
		// A provider is one daemon: several are several providers.
		"several hosts": "hosts:\n  - {name: a, address: 10.0.0.1:7443}\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Type{}.Configure(node(t, body))
			if !errors.Is(err, errdefs.ErrInvalidArgument) {
				t.Errorf("Configure() = %v, want an ErrInvalidArgument", err)
			}
		})
	}
}

// TestCheckFilesNamesTheTLSSetting checks a TLS file that does not load is an
// ErrInvalidArgument naming the setting once.
func TestCheckFilesNamesTheTLSSetting(t *testing.T) {
	c := &Config{Address: "10.0.0.1:7443", TLS: &TLS{CAFile: filepath.Join(t.TempDir(), "missing.pem")}}

	err := c.CheckFiles()
	if !errors.Is(err, errdefs.ErrInvalidArgument) || !strings.HasPrefix(err.Error(), "tls: ca_file: ") ||
		strings.Count(err.Error(), "tls:") != 1 {
		t.Errorf("CheckFiles() = %v, want an ErrInvalidArgument starting tls: ca_file:", err)
	}
}
