// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package config

import (
	"bytes"
	"errors"
	"fmt"
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

	path := filepath.Join(t.TempDir(), "cfg.yaml")
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

// TestValidFileLoadsInFull checks a valid file loads with its scale sets and
// providers, each provider configured by its type and each runner read.
func TestValidFileLoadsInFull(t *testing.T) {
	cfg, err := Load(writeConfig(t, valid))
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}

	if len(cfg.ScaleSets) != 1 || cfg.ScaleSets[0].Name != "rungar-vm" {
		t.Errorf("scale sets = %+v, want one named rungar-vm", cfg.ScaleSets)
	}
	if len(cfg.Providers) != 1 || cfg.Providers[0].Name != "compute1" || cfg.Providers[0].Type != "dicer" {
		t.Errorf("providers = %+v, want one dicer provider named compute1", cfg.Providers)
	}

	// Loading hands each part to its provider, so a loaded configuration
	// is checked in full and its runners are read.
	p, ok := cfg.Provider("compute1")
	if !ok || p.config == nil {
		t.Fatal("the provider was not configured when the file was loaded")
	}
	runner := cfg.ScaleSets[0].RunnerSpecs["compute1"]
	if runner == nil || runner.Describe() != "2 vCPU, 4 GiB" {
		t.Errorf("runner = %v, want the scale set's, as its provider read it", runner)
	}
}

// TestMissingFileIsAnInvalidArgument checks a configuration file that is not
// there is the caller's to fix.
func TestMissingFileIsAnInvalidArgument(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nowhere.yaml"))
	if err == nil {
		t.Fatal("Load() = nil, want an error: Rungar has nothing to do without a configuration")
	}
	if !errors.Is(err, errdefs.ErrInvalidArgument) {
		t.Errorf("Load() = %v, want an ErrInvalidArgument", err)
	}
}

// TestInvalidFileIsRefused checks a file with something wrong with it is
// refused, saying what.
func TestInvalidFileIsRefused(t *testing.T) {
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
			name: "a relative socket",
			body: valid + "socket: rungar.sock\n",
			says: "want an absolute path",
		},
		{
			name: "a socket too long for a Unix socket's path",
			body: valid + "socket: /" + strings.Repeat("a", 100) + "/rungar.sock\n",
			says: "longer than a Unix socket's path can be",
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
		{
			name: "an unknown log format",
			body: valid + "log_format: xml\n",
			says: "log format",
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

// TestLogLevelIsReadInAnyCase checks each log level is accepted in any case,
// unset is info, and an unknown one is refused.
func TestLogLevelIsReadInAnyCase(t *testing.T) {
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
			cfg := Config{LogLevel: tt.given}
			cfg.applyDefaults()

			got, err := cfg.logLevel()
			if tt.ok {
				if err != nil {
					t.Fatalf("logLevel() = %v", err)
				}
				if got != tt.want {
					t.Errorf("logLevel() = %v, want %v", got, tt.want)
				}

				return
			}

			if err == nil {
				t.Fatal("logLevel() = nil, want an error")
			}
			if !errors.Is(err, errdefs.ErrInvalidArgument) {
				t.Errorf("logLevel() = %v, want an ErrInvalidArgument", err)
			}
		})
	}
}

// TestLogHandlerWritesTheFormat checks each log format writes records as it
// says, and unset is text.
func TestLogHandlerWritesTheFormat(t *testing.T) {
	tests := []struct {
		format string
		want   string
	}{
		{format: "", want: "level=INFO msg=started runner=r1\n"},
		{format: "text", want: "level=INFO msg=started runner=r1\n"},
		{format: "JSON", want: `{"level":"INFO","msg":"started","runner":"r1"}` + "\n"},
	}

	for _, tt := range tests {
		name := tt.format
		if name == "" {
			name = "unset"
		}

		t.Run(name, func(t *testing.T) {
			cfg := Config{LogFormat: tt.format}
			cfg.applyDefaults()

			var buf bytes.Buffer
			handler, err := cfg.LogHandler(&buf)
			if err != nil {
				t.Fatalf("LogHandler() = %v", err)
			}

			// A zero time is left out, which keeps the record the same.
			record := slog.NewRecord(time.Time{}, slog.LevelInfo, "started", 0)
			record.AddAttrs(slog.String("runner", "r1"))
			if err := handler.Handle(t.Context(), record); err != nil {
				t.Fatal(err)
			}

			if got := buf.String(); got != tt.want {
				t.Errorf("wrote %q, want %q", got, tt.want)
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

// TestEachScaleSetKeepsItsOwnSettings checks several scale sets load in the
// order written, each with its own settings, and anchors reach providers.
func TestEachScaleSetKeepsItsOwnSettings(t *testing.T) {
	cfg, err := Load(writeConfig(t, multiple))
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}

	if len(cfg.ScaleSets) != 2 {
		t.Fatalf("loaded %d scale sets, want 2", len(cfg.ScaleSets))
	}

	small, large := cfg.ScaleSets[0], cfg.ScaleSets[1]

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
	compute2, _ := cfg.Provider("compute2")
	if compute2.Type != "dicer" || compute2.Weight != 2 || compute2.MaxRunners != 8 || compute2.RunnerBlock.Kind == 0 {
		t.Errorf("compute2 = %+v, want the anchor's type and runner, and its own weight and limit", compute2)
	}
	// max_runners is Rungar's, not the type's: it does not reach the dicer
	// provider's settings, which would refuse it.
	if compute1, _ := cfg.Provider("compute1"); compute1.MaxRunners != 0 {
		t.Errorf("compute1 max_runners = %d, want none: unset is no limit", compute1.MaxRunners)
	}
	if dc, ok := compute2.config.(*dicer.Config); !ok || dc.Address != "10.10.0.102:7443" {
		t.Errorf("compute2 = %+v, want its own address", compute2.config)
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
			if r := runner(set, p); r.Image != "ghcr.io/actions/actions-runner:latest" {
				t.Errorf("%s on %s: image = %q, want it inherited from the provider", set.Name, p, r.Image)
			}
		}
	}
	if runner(small, "compute1").VCPUs != 2 || runner(large, "compute1").VCPUs != 4 {
		t.Error("the sizes are not each scale set's own")
	}
	if got := runner(large, "compute1").Network; got != "default" {
		t.Errorf("rungar-c4-m8 on compute1: network = %q, want the provider's", got)
	}
	if got := runner(large, "compute2").Network; got != "isolated" {
		t.Errorf("rungar-c4-m8 on compute2: network = %q, want the scale set's block for that provider", got)
	}

	// Runners created alike share a revision; any difference tells them apart.
	if small.RunnerRevisions["compute1"] == "" || small.RunnerRevisions["compute1"] != small.RunnerRevisions["compute2"] {
		t.Errorf("rungar-c2-m4 revisions = %v, want one for its runners created alike", small.RunnerRevisions)
	}
	if large.RunnerRevisions["compute1"] == large.RunnerRevisions["compute2"] ||
		large.RunnerRevisions["compute1"] == small.RunnerRevisions["compute1"] {
		t.Errorf("revisions %v and %v, want runners created differently told apart",
			small.RunnerRevisions, large.RunnerRevisions)
	}
}

// TestNoScaleSetsIsAccepted checks a configuration may have none, which
// is how the last scale set is taken out before rungar scale-sets rm removes
// it.
func TestNoScaleSetsIsAccepted(t *testing.T) {
	cfg, err := Load(writeConfig(t, strings.Split(multiple, "scale_sets:")[0]))
	if err != nil {
		t.Fatalf("Load() = %v, want a configuration with no scale sets to load", err)
	}
	if len(cfg.ScaleSets) != 0 {
		t.Errorf("scale sets = %v, want none", cfg.ScaleSets)
	}
}

// TestInvalidScaleSetIsRefused checks a scale set with something wrong with
// it is refused, saying what.
func TestInvalidScaleSetIsRefused(t *testing.T) {
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

// TestScheduleIsReadAsWritten checks a schedule is read from the file, with
// its times unquoted as well as quoted, and printed as it was written.
func TestScheduleIsReadAsWritten(t *testing.T) {
	body := strings.Replace(valid, "    max_runners: 10\n", `    max_runners: 10
    schedule:
      timezone: Europe/Vilnius
      windows:
        - days: [mon-fri]
          from: 08:00
          to: "19:00"
          min_runners: 4
`, 1)

	cfg, err := Load(writeConfig(t, body))
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}

	schedule := cfg.ScaleSets[0].Schedule
	if schedule.TimeZone.String() != "Europe/Vilnius" || len(schedule.Windows) != 1 ||
		schedule.Windows[0].String() != "mon-fri 08:00-19:00" || schedule.Windows[0].MinRunners != 4 {
		t.Errorf("schedule = %+v, want mon-fri 08:00-19:00 in Europe/Vilnius, 4", schedule)
	}

	printed, err := cfg.YAML()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"timezone: Europe/Vilnius", "- mon-fri", `from: "08:00"`, "min_runners: 4"} {
		if !strings.Contains(string(printed), want) {
			t.Errorf("YAML() =\n%s\nwant it to contain %q", printed, want)
		}
	}
}

// TestInvalidScheduleIsRefused checks a schedule with something wrong with it
// is refused, saying what.
func TestInvalidScheduleIsRefused(t *testing.T) {
	tests := []struct {
		name     string
		schedule string
		says     string
	}{
		{
			name:     "an unknown time zone",
			schedule: "timezone: Europe/Atlantis\n      windows: [{days: mon, from: \"08:00\", to: \"19:00\"}]",
			says:     "unknown time zone",
		},
		{
			name:     "an unknown day",
			schedule: "windows: [{days: [weekdays], from: \"08:00\", to: \"19:00\"}]",
			says:     "unknown day",
		},
		{
			name:     "more runners than max_runners",
			schedule: "windows: [{days: mon, from: \"08:00\", to: \"19:00\", min_runners: 11}]",
			says:     `scale set "rungar-vm": schedule: window 1: min_runners (11) is more than max_runners (10)`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := strings.Replace(valid, "    max_runners: 10\n",
				"    max_runners: 10\n    schedule:\n      "+tt.schedule+"\n", 1)

			_, err := Load(writeConfig(t, body))
			if !errors.Is(err, errdefs.ErrInvalidArgument) {
				t.Fatalf("Load() = %v, want an ErrInvalidArgument", err)
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
		c := Config{GitHub: GitHub{URL: url}}
		c.applyDefaults()

		return c.Installation
	}

	org := of("https://github.com/my-org")
	if !installationPattern.MatchString(org) {
		t.Fatalf("installation %q is not one a backend can keep", org)
	}
	if of("https://GitHub.com/my-org/") != org {
		t.Error("the same GitHub, written differently, is a different installation")
	}
	if of("https://github.com/other-org") == org {
		t.Error("two GitHub organisations are the same installation")
	}

	configured := Config{GitHub: GitHub{URL: "https://github.com/my-org"}, Installation: "rungar-prod"}
	configured.applyDefaults()
	if configured.Installation != "rungar-prod" {
		t.Errorf("installation = %q, want the configured one", configured.Installation)
	}
}

// TestEmptyFileSaysWhatItIsMissing checks that an empty file says what it is
// missing, rather than reporting the end of the file as an error.
func TestEmptyFileSaysWhatItIsMissing(t *testing.T) {
	_, err := Load(writeConfig(t, ""))
	if err == nil || !strings.Contains(err.Error(), "github.url") {
		t.Errorf("Load(empty) = %v, want it to say github.url is required", err)
	}
}

// TestInstallationMustBeALabelValue checks an installation is accepted only
// when every backend can keep it as a label value.
func TestInstallationMustBeALabelValue(t *testing.T) {
	tests := []struct {
		installation string
		valid        bool
	}{
		{"gh-0123456789ab", true},
		{"rungar.prod_1", true},
		{"a", true},
		{"", false},
		{"-leading", false},
		{"trailing.", false},
		{"has space", false},
		{"has/slash", false},
		{"0123456789012345678901234567890123456789012345678901234567890123", false},
	}

	for _, tt := range tests {
		t.Run(tt.installation, func(t *testing.T) {
			if got := installationPattern.MatchString(tt.installation); got != tt.valid {
				t.Errorf("installation %q valid = %v, want %v", tt.installation, got, tt.valid)
			}
		})
	}
}

// TestAnchorContainingItselfIsRefused checks that a provider merging its own
// anchor is refused, rather than resolved without end.
func TestAnchorContainingItselfIsRefused(t *testing.T) {
	_, err := Load(writeConfig(t, `
github:
  url: https://github.com/my-org
  token_path: /etc/rungar/token
providers:
  - &dicer
    <<: *dicer
    name: compute1
    type: dicer
    address: 10.10.0.101:7443
`))
	if err == nil || !strings.Contains(err.Error(), "contains itself") {
		t.Errorf("Load() = %v, want the anchor refused for containing itself", err)
	}
}

// TestRunnerNamesAProviderRefusesAreRefusedOnLoad checks a scale set whose
// runners a provider could never name its machines after is refused when
// loaded, not on every attempt to create one.
func TestRunnerNamesAProviderRefusesAreRefusedOnLoad(t *testing.T) {
	const gcp = `
github:
  url: https://github.com/my-org
  token: ghp_abc
providers:
  - name: gcp
    type: gcp
    project: my-ci-123456
    zones: [europe-west4-a]
    runner: {machine_type: e2-standard-4, image: runner}
scale_sets:
  - name: %s
    max_runners: 1
    providers:
      - name: gcp
`

	tests := []struct {
		name string
		ok   bool
	}{
		{"linux-c4-m16", true},
		{"Linux-X64", false},
		{"ci.large", false},
		{"4core", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, fmt.Sprintf(gcp, tt.name)))
			switch {
			case tt.ok && err != nil:
				t.Fatalf("Load() = %v, want nil", err)
			case !tt.ok && (err == nil || !strings.Contains(err.Error(), "Compute Engine instances")):
				t.Fatalf("Load() = %v, want the scale set refused for its runners' names", err)
			}
		})
	}
}

// fuzzSeeds are configurations for the fuzz tests to start from: valid ones,
// and the layouts withVersion edits by hand.
var fuzzSeeds = []string{
	valid,
	versioned,
	multiple,
	"",
	"version: 1\n",
	"# the fleet\nlog_level: debug\n",
	"version: 1 # current\nlog_level: info\n",
	"{log_level: info}\n",
	"version: 1\r\nlog_level: info\r\n",
	"  log_level: info\n",
	"log_level: \"héllo\"\nversion: 1\n",
	"---\nversion: 1\n...\n",
	"a: &a {b: 1}\nc: *a\n",
}

// FuzzLoad checks that loading any file fails or succeeds without panicking,
// and that a configuration it loads prints as YAML that loads again and
// prints the same.
func FuzzLoad(f *testing.F) {
	for _, seed := range fuzzSeeds {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, b []byte) {
		cfg, err := load(b, "cfg.yaml")
		if err != nil {
			return
		}

		printed, err := cfg.YAML()
		if err != nil {
			t.Fatalf("YAML() = %v, for a configuration that loaded", err)
		}

		again, err := load(printed, "cfg.yaml")
		if err != nil {
			t.Fatalf("the printed configuration does not load: %v\n%s", err, printed)
		}

		reprinted, err := again.YAML()
		if err != nil {
			t.Fatalf("YAML() of the reloaded configuration = %v", err)
		}
		if !bytes.Equal(printed, reprinted) {
			t.Errorf("printing is not stable:\n%s\nthen\n%s", printed, reprinted)
		}
	})
}
