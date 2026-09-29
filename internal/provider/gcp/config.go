// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package gcp

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"

	"google.golang.org/api/compute/v1"
	"google.golang.org/api/option"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/provider"
	"github.com/konradasb/rungar/internal/types"
)

// Type is the gcp provider type.
type Type struct{}

var _ provider.Type = Type{}

// Config is a gcp provider's entry, less the keys every provider has. Its
// runner blocks are RunnerSpecs.
//
//	providers:
//	  - name: gcp
//	    type: gcp
//	    project: my-ci
//	    zones: [europe-west1-b, europe-west1-c]
type Config struct {
	// Project is the Google Cloud project the runners' instances are made
	// in. It is required.
	Project string `yaml:"project"`

	// Zones are where the instances go, all in one region, tried in order:
	// a runner goes to the first that has the machine, and a zone out of
	// stock or out of quota sends it to the next. At least one is required.
	Zones []string `yaml:"zones"`

	// Network and subnetwork are the VPC network and subnetwork the
	// instances are on, by name or URL. Unset is the project's default
	// network.
	Network    string `yaml:"network,omitempty"`
	Subnetwork string `yaml:"subnetwork,omitempty"`

	// ExternalIP gives each instance an external address, which is how it
	// reaches GitHub unless the network has Cloud NAT. Unset is true.
	ExternalIP *bool `yaml:"external_ip,omitempty"`

	// ServiceAccount is the email of the service account the instances run
	// as, and scopes its OAuth scopes. Unset runs them as none. A service
	// account with no scopes gets cloud-platform, which leaves access to the
	// account's IAM roles.
	ServiceAccount string   `yaml:"service_account,omitempty"`
	Scopes         []string `yaml:"scopes,omitempty"`

	// CredentialsFile is a service account key, as JSON, that Rungar
	// authenticates with. Unset uses Application Default Credentials: the
	// attached service account on Google Cloud, or
	// GOOGLE_APPLICATION_CREDENTIALS.
	CredentialsFile string `yaml:"credentials_file,omitempty"`

	// Timeout bounds one call to Compute Engine, other than making or
	// removing an instance, which take as long as Compute Engine does.
	// Unset is 30s.
	Timeout time.Duration `yaml:"timeout,omitempty"`
}

var (
	_ provider.Config          = (*Config)(nil)
	_ provider.FileChecker     = (*Config)(nil)
	_ provider.SecretFileNamer = (*Config)(nil)
)

// defaultTimeout bounds a call to Compute Engine when the configuration sets
// no timeout.
const defaultTimeout = 30 * time.Second

// cloudPlatformScope is the scope an instance's service account gets unless
// the configuration names others.
const cloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"

var (
	// projectPattern matches a project ID.
	projectPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)

	// zonePattern matches a zone: europe-west1-b.
	zonePattern = regexp.MustCompile(`^[a-z]+-[a-z]+[0-9]+-[a-z]$`)
)

// Configure parses and validates a gcp provider's entry.
func (Type) Configure(_ string, node *provider.Node) (provider.Config, error) {
	c := &Config{}
	if err := provider.Decode(node, c); err != nil {
		return nil, err
	}

	if c.Timeout == 0 {
		c.Timeout = defaultTimeout
	}
	if c.ServiceAccount != "" && len(c.Scopes) == 0 {
		c.Scopes = []string{cloudPlatformScope}
	}

	if err := c.Validate(); err != nil {
		return nil, err
	}

	return c, nil
}

// ParseRunner parses and validates a runner block, filling in the default
// disk and startup script.
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

// Open returns the provider. Credentials are found now; Compute Engine is
// first called by the provider's first call.
func (c *Config) Open(logger *slog.Logger) (provider.Provider, error) {
	var opts []option.ClientOption
	if c.CredentialsFile != "" {
		opts = append(opts, option.WithAuthCredentialsFile(option.ServiceAccount, c.CredentialsFile))
	}

	return c.open(logger, opts...)
}

// open returns the provider, its client made with opts.
func (c *Config) open(logger *slog.Logger, opts ...option.ClientOption) (*Provider, error) {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	svc, err := compute.NewService(context.Background(), opts...)
	if err != nil {
		return nil, err
	}

	return &Provider{config: c, compute: svc, logger: logger}, nil
}

// Endpoint returns the project and region the provider makes instances in.
func (c *Config) Endpoint() string {
	return "gcp:" + c.Project + "/" + c.region()
}

// region returns the region of the provider's zones.
func (c *Config) region() string {
	return regionOf(c.Zones[0])
}

// regionOf returns a zone's region: europe-west1-b is in europe-west1.
func regionOf(zone string) string {
	return zone[:strings.LastIndex(zone, "-")]
}

// externalIP reports whether instances get an external address.
func (c *Config) externalIP() bool {
	return c.ExternalIP == nil || *c.ExternalIP
}

// CheckFiles reads the credentials file, if one is configured.
func (c *Config) CheckFiles() error {
	if c.CredentialsFile == "" {
		return nil
	}

	b, err := os.ReadFile(c.CredentialsFile)
	if err != nil {
		return errdefs.InvalidArgument("credentials_file: %s", err)
	}

	var key struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(b, &key); err != nil {
		return errdefs.InvalidArgument("credentials_file %s: not a JSON key: %s", c.CredentialsFile, err)
	}
	if key.Type != "service_account" {
		return errdefs.InvalidArgument("credentials_file %s: a %q credential, want a service account key",
			c.CredentialsFile, key.Type)
	}

	return nil
}

// SecretFiles returns the credentials file, if one is configured.
func (c *Config) SecretFiles() []string {
	if c.CredentialsFile == "" {
		return nil
	}

	return []string{c.CredentialsFile}
}

// Validate checks the project and zones, and that the zones share a region.
func (c *Config) Validate() error {
	switch {
	case c.Project == "":
		return errdefs.InvalidArgument("project is required: the Google Cloud project to make instances in")
	case !projectPattern.MatchString(c.Project):
		return errdefs.InvalidArgument("invalid project %q: want a project ID, such as my-ci-123456", c.Project)
	case len(c.Zones) == 0:
		return errdefs.InvalidArgument("zones is required: the zones to make instances in, " +
			"such as [europe-west1-b, europe-west1-c]")
	case c.Timeout <= 0:
		return errdefs.InvalidArgument("timeout must be positive")
	case len(c.Scopes) > 0 && c.ServiceAccount == "":
		return errdefs.InvalidArgument("scopes need a service_account to be given to")
	}

	seen := map[string]bool{}
	for _, zone := range c.Zones {
		switch {
		case !zonePattern.MatchString(zone):
			return errdefs.InvalidArgument("invalid zone %q: want a zone, such as europe-west1-b", zone)
		case seen[zone]:
			return errdefs.InvalidArgument("zone %q is listed twice", zone)
		case regionOf(zone) != c.region():
			return errdefs.InvalidArgument("zones %s and %s are in different regions: "+
				"a provider's zones share one, and another region is another provider",
				c.Zones[0], zone)
		}
		seen[zone] = true
	}

	return nil
}
