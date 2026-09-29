// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package aws

import (
	"context"
	"log/slog"
	"os"
	"regexp"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/provider"
	"github.com/konradasb/rungar/internal/types"
)

// Type is the aws provider type.
type Type struct{}

var _ provider.Type = Type{}

// Config is an aws provider's entry, less the keys every provider has. Its
// runner blocks are RunnerSpecs.
//
//	providers:
//	  - name: aws
//	    type: aws
//	    region: eu-west-1
//	    subnets: [subnet-0a1b2c3d4e5f60718, subnet-0f1e2d3c4b5a69788]
type Config struct {
	// Region is the AWS region the runners' instances are made in. It is
	// required.
	Region string `yaml:"region"`

	// Subnets are the VPC subnets the instances go in, each in one
	// Availability Zone, tried in order: a runner goes to the first that has
	// the instance type, and a subnet whose zone is out of capacity sends it
	// to the next. At least one is required.
	Subnets []string `yaml:"subnets"`

	// SecurityGroups are the IDs of the security groups the instances are
	// in. Unset is the VPC's default security group.
	SecurityGroups []string `yaml:"security_groups,omitempty"`

	// PublicIP gives each instance a public IPv4 address, which is how it
	// reaches GitHub unless the subnet routes through a NAT gateway. Unset
	// is the subnet's own setting.
	PublicIP *bool `yaml:"public_ip,omitempty"`

	// InstanceProfile is the IAM instance profile the instances run as, by
	// name or ARN. Unset runs them as none.
	InstanceProfile string `yaml:"instance_profile,omitempty"`

	// Profile is the named profile, in the shared configuration and
	// credentials files, that Rungar authenticates as. Unset uses the SDK's
	// default chain: the environment, the default profile, then the role of
	// the ECS task or EC2 instance Rungar runs on.
	Profile string `yaml:"profile,omitempty"`

	// CredentialsFile is a shared credentials file to read in place of
	// ~/.aws/credentials.
	CredentialsFile string `yaml:"credentials_file,omitempty"`

	// Timeout bounds one call to EC2. Unset is 30s.
	Timeout time.Duration `yaml:"timeout,omitempty"`
}

var (
	_ provider.Config          = (*Config)(nil)
	_ provider.FileChecker     = (*Config)(nil)
	_ provider.SecretFileNamer = (*Config)(nil)
)

// defaultTimeout bounds a call to EC2 when the configuration sets no timeout.
const defaultTimeout = 30 * time.Second

var (
	// regionPattern matches a region: eu-west-1, us-gov-west-1.
	regionPattern = regexp.MustCompile(`^[a-z]{2}(-[a-z]+)+-[0-9]+$`)

	// subnetPattern and securityGroupPattern match a subnet's and a
	// security group's IDs.
	subnetPattern        = regexp.MustCompile(`^subnet-[0-9a-f]{8,17}$`)
	securityGroupPattern = regexp.MustCompile(`^sg-[0-9a-f]{8,17}$`)
)

// Configure parses and validates an aws provider's entry.
func (Type) Configure(_ string, node *provider.Node) (provider.Config, error) {
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
// disk and user data.
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

// Open returns the provider. Credentials are found now, from the files and
// environment; EC2 is first called by the provider's first call.
func (c *Config) Open(logger *slog.Logger) (provider.Provider, error) {
	opts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(c.Region)}
	if c.Profile != "" {
		opts = append(opts, awsconfig.WithSharedConfigProfile(c.Profile))
	}
	if c.CredentialsFile != "" {
		opts = append(opts, awsconfig.WithSharedCredentialsFiles([]string{c.CredentialsFile}))
	}

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(), opts...)
	if err != nil {
		return nil, err
	}

	return c.open(logger, ec2.NewFromConfig(cfg)), nil
}

// open returns the provider over an EC2 client.
func (c *Config) open(logger *slog.Logger, client ec2API) *Provider {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	return &Provider{config: c, ec2: client, logger: logger, rootDevices: map[string]string{}}
}

// Endpoint returns the region the provider makes instances in, and the
// profile it makes them as, which stands for the account.
func (c *Config) Endpoint() string {
	if c.Profile == "" {
		return "aws:" + c.Region
	}

	return "aws:" + c.Profile + "/" + c.Region
}

// CheckFiles reads the credentials file, if one is configured.
func (c *Config) CheckFiles() error {
	if c.CredentialsFile == "" {
		return nil
	}

	if _, err := os.ReadFile(c.CredentialsFile); err != nil {
		return errdefs.InvalidArgument("credentials_file: %s", err)
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

// Validate checks the region, subnets and security groups.
func (c *Config) Validate() error {
	switch {
	case c.Region == "":
		return errdefs.InvalidArgument("region is required: the AWS region to make instances in, such as eu-west-1")
	case !regionPattern.MatchString(c.Region):
		return errdefs.InvalidArgument("invalid region %q: want a region, such as eu-west-1", c.Region)
	case len(c.Subnets) == 0:
		return errdefs.InvalidArgument("subnets is required: the IDs of the subnets to make instances in, " +
			"such as [subnet-0a1b2c3d4e5f60718]")
	case c.Timeout <= 0:
		return errdefs.InvalidArgument("timeout must be positive")
	}

	seen := map[string]bool{}
	for _, subnet := range c.Subnets {
		switch {
		case !subnetPattern.MatchString(subnet):
			return errdefs.InvalidArgument("invalid subnet %q: want a subnet ID, such as subnet-0a1b2c3d4e5f60718",
				subnet)
		case seen[subnet]:
			return errdefs.InvalidArgument("subnet %q is listed twice", subnet)
		}
		seen[subnet] = true
	}

	for _, group := range c.SecurityGroups {
		if !securityGroupPattern.MatchString(group) {
			return errdefs.InvalidArgument("invalid security group %q: want a security group ID, "+
				"such as sg-0a1b2c3d4e5f60718", group)
		}
	}

	return nil
}
