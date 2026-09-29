// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package aws

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	yaml "gopkg.in/yaml.v3"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/provider"
	"github.com/konradasb/rungar/internal/types"
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

func TestConfigure(t *testing.T) {
	c, err := Type{}.Configure("aws", node(t, `
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
			_, err := Type{}.Configure("aws", node(t, tt.yaml))
			if !errors.Is(err, errdefs.ErrInvalidArgument) || !strings.Contains(err.Error(), tt.says) {
				t.Errorf("Configure() = %v, want an invalid argument saying %q", err, tt.says)
			}
		})
	}
}

func TestParseRunner(t *testing.T) {
	spec, err := testConfig().ParseRunner(node(t, `
instance_type: c7g.2xlarge
image: ami-0a1b2c3d4e5f60718
`))
	if err != nil {
		t.Fatalf("ParseRunner() = %v", err)
	}

	r, ok := spec.(RunnerSpec)
	if !ok {
		t.Fatalf("ParseRunner() = %T, want RunnerSpec", spec)
	}
	if r.DiskSize != defaultDiskSize || r.DiskType != defaultDiskType || r.UserData != defaultUserData {
		t.Errorf("ParseRunner() = %+v, want the default disk and user data", r)
	}
	if got := spec.Describe(); got != "c7g.2xlarge" {
		t.Errorf("Describe() = %q, want the instance type", got)
	}
}

func TestParseRunnerRefuses(t *testing.T) {
	const base = "instance_type: m7i.xlarge\nimage: ami-0a1b2c3d4e5f60718\n"

	tests := []struct {
		name, yaml, says string
	}{
		{"no instance type", "image: ami-0a1b2c3d4e5f60718", "instance_type"},
		{"bad instance type", "instance_type: M7i XLarge\nimage: ami-0a1b2c3d4e5f60718", "invalid instance_type"},
		{"no image", "instance_type: m7i.xlarge", "image"},
		{"image by name", "instance_type: m7i.xlarge\nimage: ubuntu-24.04", "want an AMI ID"},
		{"small disk", base + "disk_size: 512MiB", "disk_size"},
		{"partial GiB", base + "disk_size: 10.5GiB", "whole number of GiB"},
		{"bad disk type", base + "disk_type: st1", "invalid disk_type"},
		{"name tag", base + "tags: {Name: x}", "runner's"},
		{"rungar tag", base + "tags: {rungar.sh/team: ci}", "Rungar's"},
		{"aws tag", base + "tags: {aws:team: ci}", "AWS's"},
		{"user data not a script", base + "user_data: '#cloud-config'", "starting with #!"},
		{"unknown key", base + "vcpus: 4", "vcpus"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := testConfig().ParseRunner(node(t, tt.yaml))
			if !errors.Is(err, errdefs.ErrInvalidArgument) || !strings.Contains(err.Error(), tt.says) {
				t.Errorf("ParseRunner() = %v, want an invalid argument saying %q", err, tt.says)
			}
		})
	}
}

func TestDescribeSpot(t *testing.T) {
	r := testRunner()
	r.Spot = true

	if got := r.Describe(); got != "m7i.xlarge (spot)" {
		t.Errorf("Describe() = %q", got)
	}
}

func TestCheckFiles(t *testing.T) {
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

			if err := c.CheckFiles(); (err == nil) != tt.ok {
				t.Errorf("CheckFiles() = %v, want ok %v", err, tt.ok)
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

	p, err := c.Open(nil)
	if err != nil {
		t.Fatalf("Open() = %v", err)
	}
	if err := p.Close(); err != nil {
		t.Errorf("Close() = %v", err)
	}
}

func TestTimeoutDefault(t *testing.T) {
	c, err := Type{}.Configure("aws", node(t, "region: eu-west-1\nsubnets: [subnet-0a1b2c3d]\ntimeout: 1m"))
	if err != nil {
		t.Fatal(err)
	}
	if config, ok := c.(*Config); !ok || config.Timeout != time.Minute {
		t.Errorf("Configure() = %+v, want a timeout of 1m", c)
	}
}

var _ types.RunnerSpec = RunnerSpec{}
