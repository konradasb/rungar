// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package config

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/provider/dicer"
	"github.com/konradasb/rungar/internal/types"
)

// writeConfig writes a configuration file and returns its path.
func writeConfig(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

// valid is a configuration nothing is wrong with, for tests about one thing
// at a time.
const valid = `
github:
  url: https://github.com/my-org
  app_client_id: Iv1.abc123
  app_installation_id: 12345678
  app_private_key_path: /etc/rungar/app.pem
providers:
  - name: compute1
    type: dicer
    address: 10.10.0.101:7443
scale_sets:
  - name: rungar-vm
    max_runners: 10
    providers:
      - name: compute1
        runner:
          image: ghcr.io/actions/actions-runner:latest
          vcpus: 2
          memory: 4GiB
`

func TestLoadConfig(t *testing.T) {
	config, err := Load(writeConfig(t, valid))
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}

	if len(config.ScaleSets) != 1 || config.ScaleSets[0].Name != "rungar-vm" {
		t.Errorf("scale sets = %+v, want one named rungar-vm", config.ScaleSets)
	}
	if len(config.Providers) != 1 || config.Providers[0].Name != "compute1" || config.Providers[0].Type != "dicer" {
		t.Errorf("providers = %+v, want one dicer provider named compute1", config.Providers)
	}

	// Loading hands each part to its provider, so a loaded configuration
	// is checked in full and its runners are read.
	p, ok := config.Provider("compute1")
	if !ok || p.settings == nil {
		t.Fatal("the provider was not configured when the file was loaded")
	}
	runner := config.ScaleSets[0].RunnerSpecs["compute1"]
	if runner == nil || runner.Describe() != "2 vCPU, 4 GiB" {
		t.Errorf("runner = %v, want the scale set's, as its provider read it", runner)
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nowhere.yaml"))
	if err == nil {
		t.Fatal("Load() = nil, want an error: Rungar has nothing to do without a configuration")
	}
	if !errors.Is(err, errdefs.ErrInvalidArgument) {
		t.Errorf("Load() = %v, want an ErrInvalidArgument", err)
	}
}

func TestLoadConfigRejects(t *testing.T) {
	tests := []struct {
		name string
		body string
		says string
	}{
		{
			name: "a misspelled field, rather than silently ignoring it",
			body: strings.Replace(valid, "max_runners:", "maxrunners:", 1),
			says: "maxrunners",
		},
		{
			name: "no GitHub URL",
			body: strings.Replace(valid, "  url: https://github.com/my-org\n", "", 1),
			says: "github.url",
		},
		{
			name: "no credentials",
			body: strings.Replace(valid, `  app_client_id: Iv1.abc123
  app_installation_id: 12345678
  app_private_key_path: /etc/rungar/app.pem
`, "", 1),
			says: "no credentials",
		},
		{
			name: "no providers",
			body: strings.Replace(valid, `providers:
  - name: compute1
    type: dicer
    address: 10.10.0.101:7443
`, "", 1),
			says: "no providers",
		},
		{
			name: "a placement Rungar does not know",
			body: strings.Replace(valid, "    max_runners: 10\n", "    max_runners: 10\n    placement: random\n", 1),
			says: "unknown placement",
		},
		{
			name: "a weight that is not a number",
			body: strings.Replace(valid, "    type: dicer\n", "    type: dicer\n    weight: .nan\n", 1),
			says: "weight must be positive",
		},
		{
			name: "an infinite weight",
			body: strings.Replace(valid, "    type: dicer\n", "    type: dicer\n    weight: .inf\n", 1),
			says: "weight must be positive",
		},
		{
			name: "a reconcile interval too short to do anything else",
			body: "reconcile_interval: 1ns\n" + valid,
			says: "reconcile_interval must be at least 5s",
		},
		{
			name: "a negative weight",
			body: strings.Replace(valid, "    type: dicer\n", "    type: dicer\n    weight: -1\n", 1),
			says: "weight must be positive",
		},
		{
			name: "a negative max_runners on a provider",
			body: strings.Replace(valid, "    type: dicer\n", "    type: dicer\n    max_runners: -1\n", 1),
			says: "max_runners cannot be negative",
		},
		{
			name: "a placement on a provider, where it no longer goes",
			body: strings.Replace(valid, "    type: dicer\n", "    type: dicer\n    placement: pack\n", 1),
			says: `provider "compute1"`,
		},
		{
			name: "a provider of a type Rungar does not know",
			body: strings.Replace(valid, "type: dicer", "type: vsphere", 1),
			says: `unknown type "vsphere"`,
		},
		{
			name: "a provider with no type",
			body: strings.Replace(valid, "    type: dicer\n", "", 1),
			says: "type is required",
		},
		{
			name: "a scale set on a provider there is not",
			body: strings.Replace(valid, "      - name: compute1\n", "      - compute9\n      - name: compute1\n", 1),
			says: `no provider named "compute9"`,
		},
		{
			name: "a scale set on no provider",
			body: strings.Split(valid, "    providers:\n")[0],
			says: "providers is required",
		},
		{
			name: "a provider listed twice",
			body: strings.Replace(valid, "      - name: compute1\n", "      - compute1\n      - name: compute1\n", 1),
			says: "listed twice",
		},
		{
			// What a provider's own part says is its type's to check, and
			// the error says which provider it was.
			name: "a provider whose own configuration is wrong",
			body: strings.Replace(valid, "address: 10.10.0.101:7443", "addr: 10.10.0.101:7443", 1),
			says: `provider "compute1"`,
		},
		{
			name: "an installation that cannot be a label",
			body: valid + "installation: not a label\n",
			says: "installation",
		},
		{
			name: "a runner with no image",
			body: strings.Replace(valid, "          image: ghcr.io/actions/actions-runner:latest\n", "", 1),
			says: `scale set "rungar-vm": runner on provider "compute1"`,
		},
		{
			name: "an unknown log level",
			body: valid + "log_level: loud\n",
			says: "log level",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tt.body))
			if err == nil {
				t.Fatal("Load() = nil, want an error")
			}
			if !errors.Is(err, errdefs.ErrInvalidArgument) {
				t.Errorf("Load() = %v, want an ErrInvalidArgument", err)
			}
			if !strings.Contains(err.Error(), tt.says) {
				t.Errorf("Load() said %q, want it to mention %q", err, tt.says)
			}
		})
	}
}

func TestMetricsValidation(t *testing.T) {
	tests := []struct {
		name string
		body string
		ok   bool
	}{
		{
			name: "off by default, so its address is not checked",
			body: valid + "metrics:\n  listen: nonsense\n",
			ok:   true,
		},
		{
			name: "on, with a good address",
			body: valid + "metrics:\n  enable: true\n  listen: 127.0.0.1:9102\n",
			ok:   true,
		},
		{
			name: "on, with a bad address",
			body: valid + "metrics:\n  enable: true\n  listen: nonsense\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tt.body))
			if tt.ok && err != nil {
				t.Fatalf("Load() = %v, want nil", err)
			}
			if !tt.ok && err == nil {
				t.Fatal("Load() = nil, want an error")
			}
		})
	}
}

func TestEventsDefaults(t *testing.T) {
	config, err := Load(writeConfig(t, valid))
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}

	want := EventsConfig{File: "/var/log/rungar/events.jsonl", MaxCount: 10000}
	if config.Events != want {
		t.Errorf("events = %+v, want %+v", config.Events, want)
	}
}

func TestEventsValidation(t *testing.T) {
	tests := []struct {
		name string
		body string
		says string
	}{
		{
			name: "all of it",
			body: valid + "events:\n  file: /srv/rungar/events.jsonl\n  max_count: 500\n  max_age: 168h\n",
		},
		{
			name: "a relative file",
			body: valid + "events:\n  file: events.jsonl\n",
			says: "want an absolute path",
		},
		{
			name: "a negative count",
			body: valid + "events:\n  max_count: -1\n",
			says: "events.max_count cannot be negative",
		},
		{
			name: "a negative age",
			body: valid + "events:\n  max_age: -1h\n",
			says: "events.max_age cannot be negative",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tt.body))
			switch {
			case tt.says == "" && err != nil:
				t.Fatalf("Load() = %v, want nil", err)
			case tt.says != "" && (err == nil || !strings.Contains(err.Error(), tt.says)):
				t.Fatalf("Load() = %v, want an error saying %q", err, tt.says)
			}
		})
	}
}

// TestExampleConfig checks that the example in the repository is one the
// daemon would actually accept, since it is what everyone starts from.
func TestExampleConfig(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "example.yml"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Load(writeConfig(t, string(body))); err != nil {
		t.Errorf("example.yml is not a valid configuration: %v", err)
	}
}

func TestLogLevel(t *testing.T) {
	tests := []struct {
		given string
		want  slog.Level
		ok    bool
	}{
		{given: "", want: slog.LevelInfo, ok: true},
		{given: "info", want: slog.LevelInfo, ok: true},
		{given: "INFO", want: slog.LevelInfo, ok: true},
		{given: "debug", want: slog.LevelDebug, ok: true},
		{given: "warn", want: slog.LevelWarn, ok: true},
		{given: "warning", want: slog.LevelWarn, ok: true},
		{given: "error", want: slog.LevelError, ok: true},
		{given: "loud"},
	}

	for _, tt := range tests {
		name := tt.given
		if name == "" {
			name = "unset"
		}

		t.Run(name, func(t *testing.T) {
			config := Config{LogLevel: tt.given}
			config.applyDefaults()

			got, err := config.Level()
			if tt.ok {
				if err != nil {
					t.Fatalf("Level() = %v", err)
				}
				if got != tt.want {
					t.Errorf("Level() = %v, want %v", got, tt.want)
				}

				return
			}

			if err == nil {
				t.Fatal("Level() = nil, want an error")
			}
			if !errors.Is(err, errdefs.ErrInvalidArgument) {
				t.Errorf("Level() = %v, want an ErrInvalidArgument", err)
			}
		})
	}
}

// multiple is a configuration with a family of runner sizes sharing its
// providers' defaults, which is what several scale sets are for. The second
// provider shares the first's settings with an anchor, as a fleet of Dicer
// hosts does.
const multiple = `
github:
  url: https://github.com/my-org
  token_path: /etc/rungar/token
providers:
  - &dicer
    name: compute1
    type: dicer
    address: unix:///run/dicer/dicer.sock
    runner:
      image: ghcr.io/actions/actions-runner:latest
      network: default
  - <<: *dicer
    name: compute2
    address: 10.10.0.102:7443
    weight: 2
    max_runners: 8
scale_sets:
  - name: rungar-c2-m4
    placement: pack
    max_runners: 12
    start_timeout: 3m
    providers:
      - name: compute1
        runner: {vcpus: 2, memory: 4GiB}
      - name: compute2
        runner: {vcpus: 2, memory: 4GiB}
  - name: rungar-c4-m8
    max_runners: 6
    providers:
      - name: compute1
        runner: {vcpus: 4, memory: 8GiB}
      - name: compute2
        runner: {vcpus: 4, memory: 8GiB, network: isolated}
`

func TestLoadConfigWithSeveralScaleSets(t *testing.T) {
	config, err := Load(writeConfig(t, multiple))
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}

	if len(config.ScaleSets) != 2 {
		t.Fatalf("loaded %d scale sets, want 2", len(config.ScaleSets))
	}

	small, large := config.ScaleSets[0], config.ScaleSets[1]

	if small.Name != "rungar-c2-m4" || large.Name != "rungar-c4-m8" {
		t.Errorf("names = %q, %q; want them in the order written", small.Name, large.Name)
	}
	if small.StartTimeout != 3*time.Minute || large.StartTimeout != defaultStartTimeout {
		t.Errorf("start timeouts = %v, %v; want each scale set's own", small.StartTimeout, large.StartTimeout)
	}

	if small.Placement != types.PlacementPack || large.Placement != types.PlacementSpread {
		t.Errorf("placements = %q, %q; want each scale set's own", small.Placement, large.Placement)
	}

	// The anchor's settings reach the second provider, which keeps what
	// it gives itself.
	compute2, _ := config.Provider("compute2")
	if compute2.Type != "dicer" || compute2.Weight != 2 || compute2.MaxRunners != 8 || compute2.RunnerBlock.Kind == 0 {
		t.Errorf("compute2 = %+v, want the anchor's type and runner, and its own weight and limit", compute2)
	}
	// max_runners is Rungar's, not the type's: it does not reach the dicer
	// provider's settings, which would refuse it.
	if compute1, _ := config.Provider("compute1"); compute1.MaxRunners != 0 {
		t.Errorf("compute1 max_runners = %d, want none: unset is no limit", compute1.MaxRunners)
	}
	if dc, ok := compute2.settings.(*dicer.Config); !ok || dc.Address != "10.10.0.102:7443" {
		t.Errorf("compute2 = %+v, want its own address", compute2.settings)
	}

	// What each runner is made of is its provider's to read; that it was
	// read, over the provider's defaults, is what matters here.
	runner := func(set types.ScaleSetSpec, provider string) dicer.RunnerSpec {
		t.Helper()

		r, ok := set.RunnerSpecs[provider].(dicer.RunnerSpec)
		if !ok {
			t.Fatalf("%s on %s = %T, want a dicer runner", set.Name, provider, set.RunnerSpecs[provider])
		}

		return r
	}

	for _, set := range []types.ScaleSetSpec{small, large} {
		for _, p := range []string{"compute1", "compute2"} {
			if r := runner(set, p); r.ImageRef != "ghcr.io/actions/actions-runner:latest" {
				t.Errorf("%s on %s: image = %q, want it inherited from the provider", set.Name, p, r.ImageRef)
			}
		}
	}
	if runner(small, "compute1").VCPUs != 2 || runner(large, "compute1").VCPUs != 4 {
		t.Error("the sizes are not each scale set's own")
	}
	if got := runner(large, "compute1").NetworkName; got != "default" {
		t.Errorf("rungar-c4-m8 on compute1: network = %q, want the provider's", got)
	}
	if got := runner(large, "compute2").NetworkName; got != "isolated" {
		t.Errorf("rungar-c4-m8 on compute2: network = %q, want the scale set's block for that provider", got)
	}

	// Runners made alike share a revision; any difference tells them apart.
	if small.RunnerRevisions["compute1"] == "" || small.RunnerRevisions["compute1"] != small.RunnerRevisions["compute2"] {
		t.Errorf("rungar-c2-m4 revisions = %v, want one for its runners made alike", small.RunnerRevisions)
	}
	if large.RunnerRevisions["compute1"] == large.RunnerRevisions["compute2"] ||
		large.RunnerRevisions["compute1"] == small.RunnerRevisions["compute1"] {
		t.Errorf("revisions %v and %v, want runners made differently told apart", small.RunnerRevisions, large.RunnerRevisions)
	}
}

// TestLoadConfigWithNoScaleSets checks a configuration may have none, which
// is how the last scale set is taken out before rungar scale-sets rm removes
// it.
func TestLoadConfigWithNoScaleSets(t *testing.T) {
	cfg, err := Load(writeConfig(t, strings.Split(multiple, "scale_sets:")[0]))
	if err != nil {
		t.Fatalf("Load() = %v, want a configuration with no scale sets to load", err)
	}
	if len(cfg.ScaleSets) != 0 {
		t.Errorf("scale sets = %v, want none", cfg.ScaleSets)
	}
}

func TestLoadConfigRejectsBadScaleSets(t *testing.T) {
	tests := []struct {
		name string
		body string
		says string
	}{
		{
			name: "two with the same name",
			body: strings.Replace(multiple, "rungar-c4-m8", "rungar-c2-m4", 1),
			says: "both named",
		},
		{
			name: "two whose names differ only in case, one scale set on GitHub",
			body: strings.Replace(multiple, "name: rungar-c4-m8", "name: RUNGAR-C2-M4", 1),
			says: "differ only in case",
		},
		{
			name: "one that inherits no image",
			body: strings.Replace(multiple,
				"      image: ghcr.io/actions/actions-runner:latest\n", "", 1),
			says: "image",
		},
		{
			name: "two providers reaching one daemon",
			body: strings.Replace(multiple, "scale_sets:", `  - name: again
    type: dicer
    address: unix:///run/dicer/../dicer/dicer.sock
scale_sets:`, 1),
			says: "both reach",
		},
		{
			name: "two providers with the same name",
			body: strings.Replace(multiple, "scale_sets:", `  - name: compute1
    type: dicer
    address: unix:///run/dicer/other.sock
scale_sets:`, 1),
			says: "both named",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tt.body))
			if err == nil {
				t.Fatal("Load() = nil, want an error")
			}
			if !errors.Is(err, errdefs.ErrInvalidArgument) {
				t.Errorf("Load() = %v, want an ErrInvalidArgument", err)
			}
			if !strings.Contains(err.Error(), tt.says) {
				t.Errorf("Load() said %q, want it to mention %q", err, tt.says)
			}
		})
	}
}

// TestInstallationIsDerivedFromGitHub checks what keeps two installations
// apart without anyone configuring anything: one GitHub, one installation,
// however its URL was written.
func TestInstallationIsDerivedFromGitHub(t *testing.T) {
	of := func(url string) string {
		c := Config{GitHub: GitHubConfig{URL: url}}
		c.applyDefaults()

		return c.Installation
	}

	org := of("https://github.com/my-org")
	if !installationRe.MatchString(org) {
		t.Fatalf("installation %q is not one a backend can keep", org)
	}
	if of("https://GitHub.com/my-org/") != org {
		t.Error("the same GitHub, written differently, is a different installation")
	}
	if of("https://github.com/other-org") == org {
		t.Error("two GitHub organisations are the same installation")
	}

	configured := Config{GitHub: GitHubConfig{URL: "https://github.com/my-org"}, Installation: "rungar-prod"}
	configured.applyDefaults()
	if configured.Installation != "rungar-prod" {
		t.Errorf("installation = %q, want the configured one", configured.Installation)
	}
}

// TestLoadConfigEmptyFile checks that an empty file says what it is missing,
// rather than reporting the end of the file as an error.
func TestLoadConfigEmptyFile(t *testing.T) {
	_, err := Load(writeConfig(t, ""))
	if err == nil || !strings.Contains(err.Error(), "github.url") {
		t.Errorf("Load(empty) = %v, want it to say github.url is required", err)
	}
}

func TestValidInstallation(t *testing.T) {
	for id, want := range map[string]bool{
		"gh-0123456789ab": true,
		"rungar.prod_1":   true,
		"a":               true,
		"":                false,
		"-leading":        false,
		"trailing.":       false,
		"has space":       false,
		"has/slash":       false,
		"0123456789012345678901234567890123456789012345678901234567890123": false,
	} {
		if got := installationRe.MatchString(id); got != want {
			t.Errorf("installation %q valid = %v, want %v", id, got, want)
		}
	}
}
