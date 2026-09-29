// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package gcp

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
	c, err := Type{}.Configure("gcp", node(t, `
project: my-ci-project
zones: [europe-west1-b, europe-west1-c]
service_account: runner@my-ci-project.iam.gserviceaccount.com
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
	if !slices.Equal(config.Scopes, []string{cloudPlatformScope}) {
		t.Errorf("scopes = %v, want cloud-platform", config.Scopes)
	}
	if !config.externalIP() {
		t.Error("external_ip unset is false, want true")
	}
	if got, want := c.Endpoint(), "gcp:my-ci-project/europe-west1"; got != want {
		t.Errorf("Endpoint() = %q, want %q", got, want)
	}
}

func TestConfigureRefuses(t *testing.T) {
	tests := []struct {
		name, yaml, says string
	}{
		{"no project", `zones: [europe-west1-b]`, "project is required"},
		{"bad project", "project: My_CI\nzones: [europe-west1-b]", "invalid project"},
		{"no zones", `project: my-ci-project`, "zones is required"},
		{"bad zone", "project: my-ci-project\nzones: [europe]", "invalid zone"},
		{"zone twice", "project: my-ci-project\nzones: [europe-west1-b, europe-west1-b]", "listed twice"},
		{"two regions", "project: my-ci-project\nzones: [europe-west1-b, us-central1-a]", "different regions"},
		{"scopes alone", "project: my-ci-project\nzones: [europe-west1-b]\nscopes: [x]", "need a service_account"},
		{"negative timeout", "project: my-ci-project\nzones: [europe-west1-b]\ntimeout: -1s", "timeout"},
		{"unknown key", "project: my-ci-project\nzones: [europe-west1-b]\nregion: europe-west1", "region"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Type{}.Configure("gcp", node(t, tt.yaml))
			if !errors.Is(err, errdefs.ErrInvalidArgument) || !strings.Contains(err.Error(), tt.says) {
				t.Errorf("Configure() = %v, want an invalid argument saying %q", err, tt.says)
			}
		})
	}
}

func TestParseRunner(t *testing.T) {
	c := testConfig()

	spec, err := c.ParseRunner(node(t, `
machine_type: e2-custom-4-8192
image: projects/my-ci-project/global/images/family/runner
`))
	if err != nil {
		t.Fatalf("ParseRunner() = %v", err)
	}

	r, ok := spec.(RunnerSpec)
	if !ok {
		t.Fatalf("ParseRunner() = %T, want RunnerSpec", spec)
	}
	if r.DiskSize != defaultDiskSize || r.DiskType != defaultDiskType || r.StartupScript != defaultStartupScript {
		t.Errorf("ParseRunner() = %+v, want the default disk and startup script", r)
	}
	if got := spec.Describe(); got != "e2-custom-4-8192" {
		t.Errorf("Describe() = %q, want the machine type", got)
	}
}

func TestParseRunnerRefuses(t *testing.T) {
	const base = "machine_type: e2-standard-4\nimage: runner\n"

	tests := []struct {
		name, yaml, says string
	}{
		{"no machine type", "image: runner", "machine_type"},
		{"bad machine type", "machine_type: E2 Standard\nimage: runner", "invalid machine_type"},
		{"no image", "machine_type: e2-standard-4", "image"},
		{"small disk", base + "disk_size: 5GiB", "too small"},
		{"partial GiB", base + "disk_size: 10.5GiB", "whole number of GiB"},
		{"bad label", base + "labels: {Team: ci}", "label Team"},
		{"rungar label", base + "labels: {rungar_team: ci}", "Rungar's"},
		{"startup script metadata", base + "metadata: {startup-script: x}", "startup_script instead"},
		{"rungar metadata", base + "metadata: {rungar-jitconfig: x}", "Rungar's"},
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

	if got := r.Describe(); got != "e2-standard-4 (spot)" {
		t.Errorf("Describe() = %q", got)
	}
}

func TestCheckFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}

	tests := []struct {
		name string
		file string
		ok   bool
	}{
		{"none", "", true},
		{"service account key", write("key.json", `{"type": "service_account"}`), true},
		{"user credentials", write("user.json", `{"type": "authorized_user"}`), false},
		{"not JSON", write("key.txt", "secret"), false},
		{"missing", filepath.Join(dir, "missing.json"), false},
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

func TestOpenWithCredentialsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key.json")
	if err := os.WriteFile(path, []byte(`{"type": "authorized_user"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	c := testConfig()
	c.CredentialsFile = path

	// A key of the wrong kind is refused when opened, before any call.
	if _, err := c.Open(nil); err == nil {
		t.Error("Open() with a user credential as a service account key = nil error")
	}
}

func TestTimeoutDefault(t *testing.T) {
	c, err := Type{}.Configure("gcp", node(t, "project: my-ci-project\nzones: [europe-west1-b]\ntimeout: 1m"))
	if err != nil {
		t.Fatal(err)
	}
	if config, ok := c.(*Config); !ok || config.Timeout != time.Minute {
		t.Errorf("Configure() = %+v, want a timeout of 1m", c)
	}
}

var _ types.RunnerSpec = RunnerSpec{}
