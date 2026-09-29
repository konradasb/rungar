// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

// Package config reads and validates rungar's configuration file.
//
// Load fills in the defaults and checks the whole configuration, each
// provider's part as its type reads it; CheckFiles also reads the files it
// names. Neither contacts anything.
package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/events"
	"github.com/konradasb/rungar/internal/provider"
	"github.com/konradasb/rungar/internal/types"
)

const (
	// DefaultPath is where the configuration is read from.
	DefaultPath = "/etc/rungar/config.yaml"

	// DefaultSocket is where the daemon serves the command line.
	DefaultSocket = "/run/rungar/rungar.sock"

	// MetricsPath is the path the metrics are served on.
	MetricsPath = "/metrics"
)

// Defaults Load fills in.
const (
	// defaultMetricsListen is loopback, on the port after node_exporter's
	// 9100 and dicerd's 9101.
	defaultMetricsListen     = "127.0.0.1:9102"
	defaultReconcileInterval = 30 * time.Second
	defaultStartTimeout      = 5 * time.Minute
	defaultLogLevel          = "info"
)

// minReconcileInterval is the shortest reconcile_interval: any shorter and the
// daemon would do little but ask the providers and GitHub.
const minReconcileInterval = 5 * time.Second

// Config is the daemon's configuration.
//
//	github:
//	  url: https://github.com/my-org
//	  app_client_id: Iv1.abc123
//	  app_installation_id: 12345678
//	  app_private_key_path: /etc/rungar/app.pem
//	providers:
//	  - name: compute1
//	    type: dicer
//	    address: 10.10.0.101:7443
//	    runner:
//	      image: ghcr.io/actions/actions-runner:latest
//	scale_sets:
//	  - name: rungar-c2-m4
//	    max_runners: 8
//	    providers:
//	      - name: compute1
//	        runner: {vcpus: 2, memory: 4GiB}
type Config struct {
	// GitHub is where the scale sets live and how Rungar authenticates.
	GitHub GitHubConfig `yaml:"github"`

	// Installation names this Rungar, and is put on every runner it makes,
	// so that two installations sharing a fleet never adopt each other's
	// runners. Unset derives one from the GitHub URL, which is right unless
	// two installations serve the same GitHub.
	Installation string `yaml:"installation,omitempty"`

	// Providers are what runners are placed on: each one backend that
	// makes machines, configured as its type reads it. Providers sharing
	// settings can share them with a YAML anchor:
	//
	//	providers:
	//	  - &dicer
	//	    name: compute1
	//	    type: dicer
	//	    address: 10.10.0.101:7443
	//	    tls: {ca_file: /etc/rungar/ca.pem}
	//	  - <<: *dicer
	//	    name: compute2
	//	    address: 10.10.0.102:7443
	Providers []ProviderConfig `yaml:"providers"`

	// ScaleSets are the scale sets the daemon runs, each with its own
	// labels, ceiling and runner. Scale sets sharing a provider compete
	// only for its room. With none, the daemon only serves the command
	// line: what removing the last scale set with rungar scale-sets rm
	// takes.
	ScaleSets []types.ScaleSetSpec `yaml:"scale_sets"`

	// ReconcileInterval is how often each scale set is compared with the
	// fleet. Unset is 30s; at least 5s.
	ReconcileInterval time.Duration `yaml:"reconcile_interval,omitempty"`

	// Metrics configures the Prometheus endpoint, which is off unless
	// enabled.
	Metrics MetricsConfig `yaml:"metrics"`

	// Events configures the event log, which rungar events shows: runners
	// made, adopted, removed and lost, and why, and what providers refused.
	Events EventsConfig `yaml:"events"`

	// Socket is the Unix socket the daemon serves the command line on, used
	// by every command but rungar serve, rungar validate and rungar config.
	// It is readable and writable by the daemon's user and group alone, and whoever can use it
	// can remove runners and disable providers. Unset is
	// /run/rungar/rungar.sock.
	Socket string `yaml:"socket,omitempty"`

	// LogLevel is debug, info, warn or error. Unset is info.
	LogLevel string `yaml:"log_level,omitempty"`

	// path is the file the configuration was read from, if it was.
	path string
}

// Default returns a configuration of defaults alone.
func Default() Config {
	var c Config
	c.applyDefaults()

	return c
}

// Load reads the configuration file at path, fills in its defaults and checks
// it.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, errdefs.InvalidArgument("no configuration file at %s; Rungar needs one to know "+
				"which GitHub to talk to and which providers to place runners on", path)
		}

		return nil, fmt.Errorf("read configuration: %w", err)
	}

	decoder := yaml.NewDecoder(bytes.NewReader(b))
	decoder.KnownFields(true)

	var c Config

	// An empty file decodes to nothing, for Validate to say what is missing.
	if err := decoder.Decode(&c); err != nil && !errors.Is(err, io.EOF) {
		return nil, errdefs.InvalidArgument("invalid configuration in %s: %s", path, err)
	}

	c.applyDefaults()

	if err := c.Validate(); err != nil {
		return nil, err
	}
	if err := c.resolve(); err != nil {
		return nil, err
	}

	c.path = path

	return &c, nil
}

// applyDefaults fills in what the configuration leaves unset.
func (c *Config) applyDefaults() {
	if c.Installation == "" {
		c.Installation = installationOf(c.GitHub.URL)
	}
	if c.ReconcileInterval == 0 {
		c.ReconcileInterval = defaultReconcileInterval
	}
	if c.Metrics.Listen == "" {
		c.Metrics.Listen = defaultMetricsListen
	}
	if c.Socket == "" {
		c.Socket = DefaultSocket
	}
	if c.Events.File == "" {
		c.Events.File = defaultEventsFile
	}
	if c.Events.MaxCount == 0 {
		c.Events.MaxCount = events.DefaultMaxCount
	}
	if c.LogLevel == "" {
		c.LogLevel = defaultLogLevel
	}

	for i := range c.Providers {
		if c.Providers[i].Weight == 0 {
			c.Providers[i].Weight = 1
		}
	}

	for i := range c.ScaleSets {
		set := &c.ScaleSets[i]
		if set.RunnerGroup == "" {
			set.RunnerGroup = types.DefaultRunnerGroup
		}
		if set.Placement == "" {
			set.Placement = types.PlacementSpread
		}
		if set.StartTimeout == 0 {
			set.StartTimeout = defaultStartTimeout
		}
	}
}

// installationOf derives an installation from a GitHub URL. It is a hash of the
// URL, ignoring case and trailing slashes, as a URL is not a valid label value
// on every backend.
func installationOf(url string) string {
	sum := sha256.Sum256([]byte(strings.TrimRight(strings.ToLower(strings.TrimSpace(url)), "/")))

	return "gh-" + hex.EncodeToString(sum[:6])
}

// installationRe matches an installation every backend can keep as a label
// value.
var installationRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._-]{0,61}[A-Za-z0-9])?$`)

// Validate reports whether the daemon can run from the configuration, once its
// defaults are filled in.
func (c *Config) Validate() error {
	if err := c.GitHub.validate(); err != nil {
		return err
	}
	if !installationRe.MatchString(c.Installation) {
		return errdefs.InvalidArgument("invalid installation %q: use letters, digits, '.', '_' and '-', "+
			"at most 63 of them, starting and ending with a letter or digit", c.Installation)
	}
	if err := c.validateProviders(); err != nil {
		return err
	}
	if err := c.validateScaleSets(); err != nil {
		return err
	}
	if c.ReconcileInterval < minReconcileInterval {
		return errdefs.InvalidArgument("reconcile_interval must be at least %s", minReconcileInterval)
	}
	if !filepath.IsAbs(c.Socket) {
		return errdefs.InvalidArgument("socket %q: want an absolute path", c.Socket)
	}
	if _, err := c.Level(); err != nil {
		return err
	}
	if err := c.Events.validate(); err != nil {
		return err
	}

	return c.Metrics.validate()
}

// validateScaleSets checks each scale set, that their names are unique, and
// that their providers exist. Names are compared ignoring case, as GitHub
// does.
func (c *Config) validateScaleSets() error {
	seen := make(map[string]string, len(c.ScaleSets))
	for _, set := range c.ScaleSets {
		if err := set.Validate(); err != nil {
			return err
		}
		if other, ok := seen[strings.ToLower(set.Name)]; ok {
			if other == set.Name {
				return errdefs.InvalidArgument("two scale sets are both named %q", set.Name)
			}

			return errdefs.InvalidArgument("scale sets %q and %q differ only in case, which GitHub takes "+
				"for one scale set", other, set.Name)
		}
		seen[strings.ToLower(set.Name)] = set.Name

		for _, name := range set.ProviderNames() {
			if _, ok := c.Provider(name); !ok {
				return errdefs.InvalidArgument("scale set %q: no provider named %q", set.Name, name)
			}
		}
	}

	return nil
}

// resolve has each provider's type read its settings, and each scale set's
// runner for each of its providers: the provider's block, with the scale set's
// block for that provider over it.
func (c *Config) resolve() error {
	reaches := map[string]string{}

	for i := range c.Providers {
		p := &c.Providers[i]

		settings, err := providerTypes[p.Type].Configure(p.Name, &p.raw)
		if err != nil {
			return errdefs.InvalidArgument("provider %q: %s", p.Name, err)
		}
		p.settings = settings

		// Two providers of one backend would place runners on it apart, each
		// counting only its own against its limit.
		endpoint := settings.Endpoint()
		if other, ok := reaches[endpoint]; ok {
			return errdefs.InvalidArgument("providers %q and %q both reach %s", other, p.Name, endpoint)
		}
		reaches[endpoint] = p.Name
	}

	for i := range c.ScaleSets {
		set := &c.ScaleSets[i]
		set.RunnerSpecs = make(map[string]types.RunnerSpec, len(set.Providers))
		set.RunnerRevisions = make(map[string]string, len(set.Providers))

		for _, ref := range set.Providers {
			p, _ := c.Provider(ref.Name)

			block := provider.Merge(&p.RunnerBlock, &ref.RunnerBlock)

			spec, err := p.settings.ParseRunner(block)
			if err != nil {
				return errdefs.InvalidArgument("scale set %q: runner on provider %q: %s", set.Name, ref.Name, err)
			}
			revision, err := types.RunnerRevision(spec)
			if err != nil {
				return fmt.Errorf("scale set %q: runner on provider %q: %w", set.Name, ref.Name, err)
			}
			set.RunnerSpecs[ref.Name] = spec
			set.RunnerRevisions[ref.Name] = revision
		}
	}

	return nil
}

// CheckFiles reads the files the configuration names, the GitHub credential and
// each provider's own, without contacting anything.
func (c *Config) CheckFiles() error {
	if err := c.GitHub.CheckFiles(); err != nil {
		return err
	}

	for _, p := range c.Providers {
		if checker, ok := p.settings.(provider.FileChecker); ok {
			if err := checker.CheckFiles(); err != nil {
				return errdefs.InvalidArgument("provider %q: %s", p.Name, err)
			}
		}
	}

	return nil
}

// SecretFiles returns the files holding a secret: the GitHub credential, each
// provider's keys, and the configuration file itself when a credential is
// inline.
func (c *Config) SecretFiles() []string {
	var files []string
	for _, f := range []string{c.GitHub.TokenPath, c.GitHub.AppPrivateKeyPath} {
		if f != "" {
			files = append(files, f)
		}
	}

	if c.path != "" && (c.GitHub.Token != "" || c.GitHub.AppPrivateKey != "") {
		files = append(files, c.path)
	}

	for _, p := range c.Providers {
		if namer, ok := p.settings.(provider.SecretFileNamer); ok {
			files = append(files, namer.SecretFiles()...)
		}
	}

	return files
}

// Level returns the configured log level.
func (c *Config) Level() (slog.Level, error) {
	switch strings.ToLower(c.LogLevel) {
	case "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, errdefs.InvalidArgument("unknown log level %q: want debug, info, warn or error", c.LogLevel)
	}
}
