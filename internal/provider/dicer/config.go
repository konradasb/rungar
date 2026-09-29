// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package dicer

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"time"

	dicerclient "github.com/konradasb/dicer"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/provider"
	"github.com/konradasb/rungar/internal/types"
)

// Type is the dicer provider type.
type Type struct{}

var _ provider.Type = Type{}

// Config is a dicer provider's entry, less the keys every provider has. Its
// runner blocks are RunnerSpecs.
//
//	providers:
//	  - name: compute1
//	    type: dicer
//	    address: 10.10.0.101:7443
//	    tls:
//	      ca_file: /etc/rungar/ca.pem
type Config struct {
	// Address is where the Dicer daemon is, as a gRPC target:
	// "10.0.0.1:7443" for its TCP listener, or
	// "unix:///run/dicer/dicer.sock" for its socket on this machine.
	Address string `yaml:"address"`

	// TLS is how the daemon is verified and how Rungar identifies itself to
	// it. Without it, a TCP connection is plaintext and unauthenticated. It
	// is refused on a unix:// address, which the socket's permissions
	// protect instead.
	TLS *TLS `yaml:"tls,omitempty"`

	// Timeout bounds one call to the daemon, except pulling a runner's image
	// onto the host, which can take minutes and is bounded by the scale
	// set's start_timeout instead. Unset is 10s.
	Timeout time.Duration `yaml:"timeout,omitempty"`
}

var _ provider.Config = (*Config)(nil)

const (
	// defaultTimeout bounds a call to a daemon whose configuration sets no
	// timeout.
	defaultTimeout = 10 * time.Second

	// socketScheme prefixes the address of a daemon's socket.
	socketScheme = "unix://"
)

// Configure parses and validates a dicer provider's entry.
func (Type) Configure(node *provider.Node) (provider.Config, error) {
	c := &Config{}
	if err := provider.Decode(node, c); err != nil {
		return nil, err
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
// command and disk.
func (c *Config) ParseRunner(node *provider.Node) (types.RunnerSpec, error) {
	var spec RunnerSpec
	if err := provider.Decode(node, &spec); err != nil {
		return nil, err
	}

	spec = spec.withDefaults()
	if err := spec.Validate(); err != nil {
		return nil, err
	}

	return spec, nil
}

// Open returns the provider. It connects lazily, leaving a daemon that is down
// to be reported by the first call.
func (c *Config) Open(_ context.Context, logger *slog.Logger) (provider.Provider, error) {
	opts, err := c.clientOptions()
	if err != nil {
		return nil, err
	}

	dicerClient, err := dicerclient.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("create the Dicer client: %w", err)
	}

	return c.open(logger, dicerClient), nil
}

// open returns the provider over a Dicer client.
func (c *Config) open(logger *slog.Logger, client dicerAPI) *Provider {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	return &Provider{config: c, client: client, logger: logger}
}

// Endpoint returns the daemon's address, normalised: a TCP address without its
// gRPC scheme and in lower case, a socket's path cleaned. It is
// dicer:10.10.0.101:7443, or dicer:unix:///run/dicer/dicer.sock.
func (c *Config) Endpoint() string {
	if path, ok := strings.CutPrefix(c.Address, socketScheme); ok {
		return "dicer:" + socketScheme + filepath.Clean(path)
	}

	return "dicer:" + strings.ToLower(strings.TrimPrefix(c.Address, "dns:///"))
}

// reconnectParams caps the wait between reconnection attempts at 10s, rather
// than gRPC's two minutes, so that a daemon that comes back is used again
// within a reconcile interval.
var reconnectParams = grpc.ConnectParams{
	Backoff: backoff.Config{
		BaseDelay:  time.Second,
		Multiplier: 1.6,
		Jitter:     0.2,
		MaxDelay:   10 * time.Second,
	},
	MinConnectTimeout: 5 * time.Second,
}

// clientOptions returns the Dicer client's options.
func (c *Config) clientOptions() ([]dicerclient.Option, error) {
	opts := []dicerclient.Option{
		dicerclient.WithAddress(c.Address),
		dicerclient.WithDialOptions(grpc.WithConnectParams(reconnectParams)),
	}

	if c.TLS != nil {
		cfg, err := c.TLS.Load()
		if err != nil {
			return nil, err
		}
		opts = append(opts, dicerclient.WithTLS(cfg))
	}

	return opts, nil
}

// CheckFiles loads the TLS files the configuration names.
func (c *Config) CheckFiles() error {
	_, err := c.TLS.Load()

	return err
}

// SecretFiles returns the file holding the TLS private key, if one is
// configured.
func (c *Config) SecretFiles() []string {
	if c.TLS == nil || c.TLS.KeyFile == "" {
		return nil
	}

	return []string{c.TLS.KeyFile}
}

// Validate checks the address is one Dicer's client can dial, and that TLS is
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

	if c.Address == "" {
		return errdefs.InvalidArgument("address is required: where the Dicer daemon is, as HOST:PORT or %sPATH",
			socketScheme)
	}
	if _, _, err := net.SplitHostPort(strings.TrimPrefix(c.Address, "dns:///")); err != nil {
		return errdefs.InvalidArgument("invalid address %q: want %sPATH or HOST:PORT", c.Address, socketScheme)
	}

	return c.TLS.Validate()
}
