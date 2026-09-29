// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package docker

import (
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"time"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/provider"
	"github.com/konradasb/rungar/internal/types"
)

// Type is the docker provider type.
type Type struct{}

var _ provider.Type = Type{}

// Config is a docker provider's entry, less the keys every provider has. Its
// runner blocks are RunnerSpecs.
//
//	providers:
//	  - name: build1
//	    type: docker
//	    address: tcp://10.10.0.111:2376
//	    tls:
//	      ca_file: /etc/rungar/docker-ca.pem
//	      cert_file: /etc/rungar/docker-client.pem
//	      key_file: /etc/rungar/docker-client-key.pem
type Config struct {
	// Address is where the Docker daemon is: "unix:///var/run/docker.sock"
	// for its socket on this machine, or "tcp://HOST:PORT" for its TCP
	// listener. Unset is unix:///var/run/docker.sock.
	Address string `yaml:"address,omitempty"`

	// TLS is how the daemon is verified and how Rungar identifies itself to
	// it. Without it, a TCP connection is plaintext and unauthenticated, and
	// whoever can reach it has root on the host. It is refused on a unix://
	// address, which the socket's permissions protect instead.
	TLS *TLS `yaml:"tls,omitempty"`

	// Timeout bounds one call to the daemon, except making a runner's
	// container, which may pull its image first. Unset is 10s.
	Timeout time.Duration `yaml:"timeout,omitempty"`
}

var (
	_ provider.Config          = (*Config)(nil)
	_ provider.FileChecker     = (*Config)(nil)
	_ provider.SecretFileNamer = (*Config)(nil)
)

const (
	// defaultAddress is the Docker daemon's socket on a Linux host.
	defaultAddress = socketScheme + "/var/run/docker.sock"

	// defaultTimeout bounds a call to a daemon whose configuration sets no
	// timeout.
	defaultTimeout = 10 * time.Second

	// socketScheme and tcpScheme prefix a daemon's address.
	socketScheme = "unix://"
	tcpScheme    = "tcp://"
)

// Configure parses and validates a docker provider's entry.
func (Type) Configure(_ string, node *provider.Node) (provider.Config, error) {
	c := &Config{}
	if err := provider.Decode(node, c); err != nil {
		return nil, err
	}

	if c.Address == "" {
		c.Address = defaultAddress
	}
	if c.Timeout == 0 {
		c.Timeout = defaultTimeout
	}

	if err := c.Validate(); err != nil {
		return nil, err
	}

	return c, nil
}

// ParseRunner parses and validates a runner block, filling in the default
// command and pull policy.
func (c *Config) ParseRunner(node *provider.Node) (types.RunnerSpec, error) {
	var spec RunnerSpec
	if err := provider.Decode(node, &spec); err != nil {
		return nil, err
	}

	if len(spec.Command) == 0 {
		spec.Command = []string{defaultCommand}
	}
	if spec.Pull == "" {
		spec.Pull = PullMissing
	}

	if err := spec.Validate(); err != nil {
		return nil, err
	}

	return spec, nil
}

// Open returns the provider. It connects lazily, leaving a daemon that is down
// to be reported by the first call.
func (c *Config) Open(logger *slog.Logger) (provider.Provider, error) {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	cl, err := c.client()
	if err != nil {
		return nil, err
	}

	return &Provider{client: cl, timeout: c.Timeout, logger: logger}, nil
}

// client returns an Engine API client for the daemon's address.
func (c *Config) client() (*client, error) {
	if path, ok := strings.CutPrefix(c.Address, socketScheme); ok {
		return newSocketClient(path), nil
	}

	hostPort := strings.TrimPrefix(c.Address, tcpScheme)
	if c.TLS == nil {
		return newTCPClient(hostPort, nil), nil
	}

	cfg, err := c.TLS.Load()
	if err != nil {
		return nil, errdefs.InvalidArgument("%s", err)
	}

	return newTCPClient(hostPort, cfg), nil
}

// Endpoint returns the daemon's address, normalised: a socket's path cleaned,
// a TCP address in lower case.
func (c *Config) Endpoint() string {
	if path, ok := strings.CutPrefix(c.Address, socketScheme); ok {
		return socketScheme + filepath.Clean(path)
	}

	return strings.ToLower(c.Address)
}

// CheckFiles loads the TLS files the configuration names.
func (c *Config) CheckFiles() error {
	if c.TLS == nil {
		return nil
	}

	if _, err := c.TLS.Load(); err != nil {
		return errdefs.InvalidArgument("tls: %s", err)
	}

	return nil
}

// SecretFiles returns the file holding the TLS private key, if one is
// configured.
func (c *Config) SecretFiles() []string {
	if c.TLS == nil || c.TLS.KeyFile == "" {
		return nil
	}

	return []string{c.TLS.KeyFile}
}

// Validate checks the address is a unix:// or tcp:// one, and that TLS is
// configured only for a TCP address.
func (c *Config) Validate() error {
	if c.Timeout <= 0 {
		return errdefs.InvalidArgument("timeout must be positive")
	}

	if path, ok := strings.CutPrefix(c.Address, socketScheme); ok {
		switch {
		case !filepath.IsAbs(path):
			return errdefs.InvalidArgument("invalid address %q: the socket path must be absolute", c.Address)
		case c.TLS != nil:
			return errdefs.InvalidArgument(
				"a unix:// address cannot have tls: a socket is controlled by its file permissions")
		}

		return nil
	}

	hostPort, ok := strings.CutPrefix(c.Address, tcpScheme)
	if !ok {
		return errdefs.InvalidArgument("invalid address %q: want %sPATH or %sHOST:PORT",
			c.Address, socketScheme, tcpScheme)
	}
	if _, _, err := net.SplitHostPort(hostPort); err != nil {
		return errdefs.InvalidArgument("invalid address %q: want %sHOST:PORT", c.Address, tcpScheme)
	}

	return c.TLS.Validate()
}
