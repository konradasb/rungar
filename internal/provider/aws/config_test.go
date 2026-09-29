// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package aws

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	yaml "gopkg.in/yaml.v3"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/provider"
)

// node parses YAML into the node a provider type is handed.
func node(t *testing.T, s string) *provider.Node {
	t.Helper()

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(s), &doc); err != nil {
		t.Fatalf("yaml: %v", err)
	}
	if len(doc.Content) == 0 {
		return nil
	}

	return doc.Content[0]
}

// TestConfigureFillsInDefaults checks the default timeout and public IP, and
// that the endpoint names the profile only when one is set.
func TestConfigureFillsInDefaults(t *testing.T) {
	c, err := Type{}.Configure(node(t, `
region: eu-west-1
subnets: [subnet-0a1b2c3d4e5f60718, subnet-0f1e2d3c4b5a69788]
security_groups: [sg-0a1b2c3d4e5f60718]
instance_profile: rungar-runner
`))
	if err != nil {
		t.Fatalf("Configure() = %v", err)
	}

	config, ok := c.(*Config)
	if !ok {
		t.Fatalf("Configure() = %T, want *Config", c)
	}
	if config.Timeout != defaultTimeout {
		t.Errorf("timeout = %s, want %s", config.Timeout, defaultTimeout)
	}
	if config.PublicIP != nil {
		t.Errorf("public_ip unset = %v, want nil: the subnet's setting", *config.PublicIP)
	}
	if got, want := c.Endpoint(), "aws:eu-west-1"; got != want {
		t.Errorf("Endpoint() = %q, want %q", got, want)
	}

	config.Profile = "ci"
	if got, want := c.Endpoint(), "aws:ci/eu-west-1"; got != want {
		t.Errorf("Endpoint() with a profile = %q, want %q", got, want)
	}
}

func TestConfigureRefuses(t *testing.T) {
	const subnets = "\nsubnets: [subnet-0a1b2c3d4e5f60718]"

	tests := []struct {
		name, yaml, says string
	}{
		{"no region", `subnets: [subnet-0a1b2c3d4e5f60718]`, "region is required"},
		{"bad region", "region: Ireland" + subnets, "invalid region"},
		{"zone as region", "region: eu-west-1a" + subnets, "invalid region"},
		{"no subnets", `region: eu-west-1`, "subnets is required"},
		{"bad subnet", "region: eu-west-1\nsubnets: [runners]", "invalid subnet"},
		{"subnet twice", "region: eu-west-1\nsubnets: [subnet-0a1b2c3d, subnet-0a1b2c3d]", "listed twice"},
		{"bad security group", "region: eu-west-1" + subnets + "\nsecurity_groups: [runners]", "invalid security group"},
		{"negative timeout", "region: eu-west-1" + subnets + "\ntimeout: -1s", "timeout"},
		{"unknown key", "region: eu-west-1" + subnets + "\nzones: [eu-west-1a]", "zones"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Type{}.Configure(node(t, tt.yaml))
			if !errors.Is(err, errdefs.ErrInvalidArgument) || !strings.Contains(err.Error(), tt.says) {
				t.Errorf("Configure() = %v, want an invalid argument saying %q", err, tt.says)
			}
		})
	}
}

func TestCheckFilesReadsTheCredentialsFile(t *testing.T) {
	dir := t.TempDir()
	present := filepath.Join(dir, "credentials")
	if err := os.WriteFile(present, []byte("[default]\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		file string
		ok   bool
	}{
		{"none", "", true},
		{"present", present, true},
		{"missing", filepath.Join(dir, "missing"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := testConfig()
			c.CredentialsFile = tt.file

			err := c.CheckFiles()
			if (err == nil) != tt.ok {
				t.Errorf("CheckFiles() = %v, want ok %v", err, tt.ok)
			}
			if err != nil && (!errors.Is(err, errdefs.ErrInvalidArgument) || !errors.Is(err, fs.ErrNotExist)) {
				t.Errorf("CheckFiles() = %v, want an ErrInvalidArgument wrapping why the file did not read", err)
			}

			want := []string{tt.file}
			if tt.file == "" {
				want = nil
			}
			if got := c.SecretFiles(); !slices.Equal(got, want) {
				t.Errorf("SecretFiles() = %v, want %v", got, want)
			}
		})
	}
}

func TestOpenContactsNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials")
	if err := os.WriteFile(path, []byte("[ci]\naws_access_key_id = AKIA\naws_secret_access_key = x\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	c := testConfig()
	c.Profile = "ci"
	c.CredentialsFile = path

	p, err := c.Open(t.Context(), nil)
	if err != nil {
		t.Fatalf("Open() = %v", err)
	}
	if mode := retryModeOf(t, p); mode != aws.RetryModeAdaptive {
		t.Errorf("retry mode = %q, want %q: the client slows down while EC2 throttles it", mode, aws.RetryModeAdaptive)
	}
	if err := p.Close(); err != nil {
		t.Errorf("Close() = %v", err)
	}
}

// retryModeOf returns the retry mode of the provider's EC2 client.
func retryModeOf(t *testing.T, p provider.Provider) aws.RetryMode {
	t.Helper()

	opened, ok := p.(*Provider)
	if !ok {
		t.Fatalf("Open() = %T, want *Provider", p)
	}
	client, ok := opened.ec2.(*ec2.Client)
	if !ok {
		t.Fatalf("the provider's client is %T, want *ec2.Client", opened.ec2)
	}

	return client.Options().RetryMode
}

func TestConfigureReadsTheTimeout(t *testing.T) {
	c, err := Type{}.Configure(node(t, "region: eu-west-1\nsubnets: [subnet-0a1b2c3d]\ntimeout: 1m"))
	if err != nil {
		t.Fatal(err)
	}
	if config, ok := c.(*Config); !ok || config.Timeout != time.Minute {
		t.Errorf("Configure() = %+v, want a timeout of 1m", c)
	}
}
