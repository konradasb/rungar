// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package gcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"

	"google.golang.org/api/compute/v1"
	"google.golang.org/api/impersonate"
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
	// Project is the Google Cloud project the runners' instances are created
	// in. It is required.
	Project string `yaml:"project"`

	// Zones are where the instances go, all in one region. Each of a scale
	// set's runners starts at the zone after its last runner's, so they
	// spread across them, and a zone out of stock, failing, or without the
	// machine type sends it on to the next. At least one is required.
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

	// CredentialsFile is the credential Rungar authenticates with, as JSON: a
	// service account key, a workload identity federation configuration
	// (external_account), with no key to leak, or a configuration
	// impersonating a service account. Unset uses Application Default
	// Credentials: the attached service account on Google Cloud, Workload
	// Identity on GKE, or GOOGLE_APPLICATION_CREDENTIALS.
	CredentialsFile string `yaml:"credentials_file,omitempty"`

	// ImpersonateServiceAccount is the email of a service account Rungar
	// acts as, with short-lived tokens its credentials ask for: the
	// credentials need roles/iam.serviceAccountTokenCreator on the account,
	// and the account needs the roles Rungar does. One identity can then
	// serve several projects, each granting only its own account. Unset acts
	// as the credentials themselves.
	ImpersonateServiceAccount string `yaml:"impersonate_service_account,omitempty"`

	// Timeout bounds one call to Compute Engine. Creating a runner, which
	// waits for its instance, is bounded by the scale set's start_timeout
	// instead, and deleting one by 5 minutes. Unset is 30s.
	Timeout time.Duration `yaml:"timeout,omitempty"`
}

var _ provider.Config = (*Config)(nil)

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

	// serviceAccountPattern matches a service account's email.
	serviceAccountPattern = regexp.MustCompile(`^[a-z0-9-]+@[a-z0-9.-]+\.gserviceaccount\.com$`)

	// resourceNamePattern matches the name of a Compute Engine resource: an
	// instance's, or an instance template's.
	resourceNamePattern = regexp.MustCompile(`^[a-z]([-a-z0-9]{0,61}[a-z0-9])?$`)
)

// Configure parses and validates a gcp provider's entry.
func (Type) Configure(node *provider.Node) (provider.Config, error) {
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
// disk and startup script, and checks a regional instance template is in the
// provider's region.
func (c *Config) ParseRunner(node *provider.Node) (types.RunnerSpec, error) {
	var spec RunnerSpec
	if err := provider.Decode(node, &spec); err != nil {
		return nil, err
	}

	spec = spec.withDefaults()
	if err := spec.Validate(); err != nil {
		return nil, err
	}

	if ref, _ := parseInstanceTemplateRef(spec.InstanceTemplate, c.Project); ref.region != "" && ref.region != c.region() {
		return nil, errdefs.InvalidArgument("instance_template %s is in %s, not %s: a regional template "+
			"creates instances in its own region alone", spec.InstanceTemplate, ref.region, c.region())
	}

	return spec, nil
}

// Open returns the provider. Credentials are found now; Compute Engine, and
// the IAM Credentials API when impersonating, are first called by the
// provider's first call.
func (c *Config) Open(ctx context.Context, logger *slog.Logger) (provider.Provider, error) {
	var opts []option.ClientOption
	if c.CredentialsFile != "" {
		kind, err := c.credentialsType()
		if err != nil {
			return nil, err
		}

		opts = append(opts, option.WithAuthCredentialsFile(kind, c.CredentialsFile))
	}

	// The token source and the client keep their context for the calls they
	// make later, which Open's ending must not cancel.
	ctx = context.WithoutCancel(ctx)

	if c.ImpersonateServiceAccount != "" {
		tokenSource, err := impersonate.CredentialsTokenSource(ctx, impersonate.CredentialsConfig{
			TargetPrincipal: c.ImpersonateServiceAccount,
			Scopes:          []string{cloudPlatformScope},
		}, opts...)
		if err != nil {
			return nil, fmt.Errorf("impersonate %s: %w", c.ImpersonateServiceAccount, err)
		}
		opts = []option.ClientOption{option.WithTokenSource(tokenSource)}
	}

	return c.open(ctx, logger, opts...)
}

// open returns the provider, its client made with opts.
func (c *Config) open(ctx context.Context, logger *slog.Logger, opts ...option.ClientOption) (*Provider, error) {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	service, err := compute.NewService(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("create the Compute Engine client: %w", err)
	}

	return &Provider{config: c, compute: service, logger: logger, retryDelay: defaultRetryDelay}, nil
}

// Endpoint returns the project and region the provider creates instances in.
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

// hasExternalIP reports whether instances get an external address.
func (c *Config) hasExternalIP() bool {
	return c.ExternalIP == nil || *c.ExternalIP
}

// credentialTypes are the credentials credentials_file takes, by the type
// their JSON says they are.
var credentialTypes = map[string]option.CredentialsType{
	"service_account":              option.ServiceAccount,
	"external_account":             option.ExternalAccount,
	"impersonated_service_account": option.ImpersonatedServiceAccount,
}

// CheckFiles reads the credentials file, if one is configured.
func (c *Config) CheckFiles() error {
	if c.CredentialsFile == "" {
		return nil
	}

	_, err := c.credentialsType()
	return err
}

// credentialsType reads the credentials file and returns its type.
func (c *Config) credentialsType() (option.CredentialsType, error) {
	b, err := os.ReadFile(c.CredentialsFile)
	if err != nil {
		return "", errdefs.InvalidArgument("credentials_file: %w", err)
	}

	var credential struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(b, &credential); err != nil {
		return "", errdefs.InvalidArgument("credentials_file %s: not a JSON credential: %w", c.CredentialsFile, err)
	}

	kind, ok := credentialTypes[credential.Type]
	if !ok {
		return "", errdefs.InvalidArgument("credentials_file %s: a %q credential, want a service account key, "+
			"or an external_account or impersonated_service_account configuration (for your own "+
			"gcloud login, leave it unset: Application Default Credentials find it)",
			c.CredentialsFile, credential.Type)
	}

	return kind, nil
}

// CheckRunnerName checks a runner's name can be its instance's: Compute Engine
// allows lower case letters, digits and hyphens alone, starting with a letter.
func (c *Config) CheckRunnerName(name string) error {
	if resourceNamePattern.MatchString(name) {
		return nil
	}

	return errdefs.InvalidArgument("its runners, such as %s, cannot be Compute Engine instances, "+
		"whose names are lower case letters, digits and hyphens, starting with a letter: rename the scale set",
		name)
}

// SecretFiles returns the credentials file, if one is configured.
func (c *Config) SecretFiles() []string {
	if c.CredentialsFile == "" {
		return nil
	}

	return []string{c.CredentialsFile}
}

// Validate checks the project, the zones and that they share a region, and
// the service account to impersonate.
func (c *Config) Validate() error {
	switch {
	case c.Project == "":
		return errdefs.InvalidArgument("project is required: the Google Cloud project to create instances in")
	case !projectPattern.MatchString(c.Project):
		return errdefs.InvalidArgument("invalid project %q: want a project ID, such as my-ci-123456", c.Project)
	case len(c.Zones) == 0:
		return errdefs.InvalidArgument("zones is required: the zones to create instances in, " +
			"such as [europe-west1-b, europe-west1-c]")
	case c.Timeout <= 0:
		return errdefs.InvalidArgument("timeout must be positive")
	case len(c.Scopes) > 0 && c.ServiceAccount == "":
		return errdefs.InvalidArgument("scopes need a service_account to be given to")
	case c.ImpersonateServiceAccount != "" && !serviceAccountPattern.MatchString(c.ImpersonateServiceAccount):
		return errdefs.InvalidArgument("invalid impersonate_service_account %q: want a service account's "+
			"email, such as rungar@my-ci-123456.iam.gserviceaccount.com", c.ImpersonateServiceAccount)
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
