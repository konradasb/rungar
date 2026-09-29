// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package proxmox

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/provider"
	"github.com/konradasb/rungar/internal/types"
)

// Type is the proxmox provider type.
type Type struct{}

var _ provider.Type = Type{}

// Config is a proxmox provider's entry, less the keys every provider has. Its
// runner blocks are RunnerSpecs.
//
//	providers:
//	  - name: pve
//	    type: proxmox
//	    url: https://pve.example.com:8006
//	    token_id: rungar@pve!rungar
//	    token_secret_path: /etc/rungar/pve-token
type Config struct {
	// URL is where the cluster's API is: any of its nodes,
	// https://pve.example.com:8006. Unset port is 8006. It is required.
	URL string `yaml:"url"`

	// TokenID is the API token Rungar authenticates with, as USER@REALM!NAME.
	// Its privileges are on the provider's page. It is required.
	TokenID string `yaml:"token_id"`

	// TokenSecret is the token's secret. TokenSecretPath is a file holding
	// it instead, which keeps it out of this file. Give one or the other.
	TokenSecret     string `yaml:"token_secret,omitempty"`
	TokenSecretPath string `yaml:"token_secret_path,omitempty"`

	// TLS is how the API's certificate is verified. Unset uses this
	// machine's root CAs.
	TLS *TLS `yaml:"tls,omitempty"`

	// Nodes are the cluster's nodes runners may be placed on. Unset is every
	// online node.
	Nodes []string `yaml:"nodes,omitempty"`

	// Timeout bounds one call to the API. Creating a runner, which clones and
	// boots a VM, is bounded by the scale set's start_timeout instead. Unset
	// is 10s.
	Timeout time.Duration `yaml:"timeout,omitempty"`
}

// Config has no CheckRunnerName: a VM's name must be a DNS name, and every
// runner's name already is one.
var _ provider.Config = (*Config)(nil)

const (
	// defaultTimeout bounds a call to the API when the configuration sets no
	// timeout.
	defaultTimeout = 10 * time.Second

	// defaultPort is the port Proxmox VE serves its API on.
	defaultPort = "8006"

	// apiPath is the API's root under the URL.
	apiPath = "/api2/json"
)

// Configure parses and validates a proxmox provider's entry.
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
// jit_path.
func (c *Config) ParseRunner(node *provider.Node) (types.RunnerSpec, error) {
	var spec RunnerSpec
	if err := provider.Decode(node, &spec); err != nil {
		return nil, err
	}

	if spec.JITPath == "" {
		spec.JITPath = defaultJITPath
	}

	if err := spec.Validate(); err != nil {
		return nil, err
	}

	return spec, nil
}

// Open returns the provider. It connects lazily, leaving an API that is down
// to be reported by the first call.
func (c *Config) Open(_ context.Context, logger *slog.Logger) (provider.Provider, error) {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	secret, err := c.secret()
	if err != nil {
		return nil, err
	}

	transport := http.DefaultTransport.(*http.Transport).Clone() //nolint:forcetypeassert // always an *http.Transport
	if transport.TLSClientConfig, err = c.TLS.Load(); err != nil {
		return nil, err
	}

	return &Provider{
		client: newClient(c.baseURL()+apiPath, "PVEAPIToken="+c.TokenID+"="+secret, transport, c.Timeout),
		nodes:  slices.Clone(c.Nodes),
		logger: logger,
	}, nil
}

// Endpoint returns the API's URL, normalised as baseURL does:
// proxmox:https://pve.example.com:8006.
func (c *Config) Endpoint() string {
	return "proxmox:" + c.baseURL()
}

// baseURL returns the API's URL, normalised: in lower case, with its port,
// and without a path.
func (c *Config) baseURL() string {
	u, err := url.Parse(c.URL)
	if err != nil {
		return strings.ToLower(c.URL)
	}

	host := u.Host
	if u.Port() == "" {
		host = net.JoinHostPort(u.Hostname(), defaultPort)
	}

	return strings.ToLower(u.Scheme + "://" + host)
}

// CheckFiles reads the token's secret and the TLS authorities.
func (c *Config) CheckFiles() error {
	if _, err := c.secret(); err != nil {
		return err
	}

	_, err := c.TLS.Load()

	return err
}

// SecretFiles returns the file holding the token's secret, if one is
// configured.
func (c *Config) SecretFiles() []string {
	if c.TokenSecretPath == "" {
		return nil
	}

	return []string{c.TokenSecretPath}
}

// SecretKeys returns the key that can hold the token's secret inline, which
// rungar config prints redacted.
func (c *Config) SecretKeys() []string {
	return []string{"token_secret"}
}

// Validate checks the settings, without reading the files they name.
func (c *Config) Validate() error {
	u, err := url.Parse(c.URL)
	switch {
	case c.URL == "":
		return errdefs.InvalidArgument("url is required: where the cluster's API is, " +
			"such as https://pve.example.com:8006")
	case err != nil || u.Scheme != "https" || u.Hostname() == "":
		return errdefs.InvalidArgument("invalid url %q: want https://HOST[:PORT]", c.URL)
	case u.Path != "" && u.Path != "/":
		return errdefs.InvalidArgument("invalid url %q: give the host alone, without %s", c.URL, apiPath)
	}

	switch {
	case c.TokenID == "":
		return errdefs.InvalidArgument("token_id is required: the API token, as USER@REALM!NAME")
	case !validTokenID(c.TokenID):
		return errdefs.InvalidArgument("invalid token_id %q: want USER@REALM!NAME", c.TokenID)
	case c.TokenSecret == "" && c.TokenSecretPath == "":
		return errdefs.InvalidArgument("token_secret or token_secret_path is required")
	case c.TokenSecret != "" && c.TokenSecretPath != "":
		return errdefs.InvalidArgument("give token_secret or token_secret_path, not both")
	case c.Timeout <= 0:
		return errdefs.InvalidArgument("timeout must be positive")
	}

	if slices.Contains(c.Nodes, "") {
		return errdefs.InvalidArgument("nodes: a node's name cannot be empty")
	}

	return c.TLS.Validate()
}

// validTokenID reports whether id is USER@REALM!NAME.
func validTokenID(id string) bool {
	user, name, ok := strings.Cut(id, "!")
	if !ok || name == "" {
		return false
	}

	at := strings.LastIndex(user, "@")

	return at > 0 && at < len(user)-1
}

// secret returns the token's secret, from the configuration or its file.
func (c *Config) secret() (string, error) {
	if c.TokenSecretPath == "" {
		return c.TokenSecret, nil
	}

	b, err := os.ReadFile(c.TokenSecretPath)
	if err != nil {
		return "", errdefs.InvalidArgument("token_secret_path: %w", err)
	}

	secret := strings.TrimSpace(string(b))
	if secret == "" {
		return "", errdefs.InvalidArgument("token_secret_path: %s is empty", c.TokenSecretPath)
	}

	return secret, nil
}
