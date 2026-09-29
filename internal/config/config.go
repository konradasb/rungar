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

	// maxSocketPath is the longest Unix socket path: macOS's limit, which is
	// shorter than Linux's, so a configuration valid on one is on the other.
	maxSocketPath = 103
)

// Defaults Load fills in.
const (
	// defaultMetricsListen is loopback, on the port after node_exporter's
	// 9100 and dicerd's 9101.
	defaultMetricsListen     = "127.0.0.1:9102"
	defaultReconcileInterval = 30 * time.Second
	defaultStartTimeout      = 5 * time.Minute
	defaultLogLevel          = "info"
	defaultLogFormat         = "text"
)

// minReconcileInterval is the shortest reconcile_interval: any shorter and the
// daemon would do little but ask the providers and GitHub.
const minReconcileInterval = 5 * time.Second

// Config is the daemon's configuration.
//
//	version: 1
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
	// Version is the version of the configuration the file is written for:
	// 1. A later Rungar reads the file as that version meant, warning of
	// anything deprecated in it, and rungar config migrate rewrites it for
	// the version that Rungar writes. Unset is 1, with a warning.
	Version int `yaml:"version"`

	// GitHub is where the scale sets live and how Rungar authenticates.
	GitHub GitHub `yaml:"github"`

	// Installation names this Rungar, and is put on every runner it creates,
	// so that two installations sharing a fleet never adopt each other's
	// runners. Unset derives one from the GitHub URL, which is right unless
	// two installations serve the same GitHub.
	Installation string `yaml:"installation,omitempty"`

	// Providers are what runners are placed on: each one backend that
	// creates machines, configured as its type reads it. Providers sharing
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
	Providers []Provider `yaml:"providers"`

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
	Metrics Metrics `yaml:"metrics"`

	// Events configures the event log, which rungar events shows: runners
	// created, adopted, removed and lost, and why, and what providers refused.
	Events Events `yaml:"events"`

	// Socket is the Unix socket the daemon serves the command line on, used
	// by every command but rungar serve, rungar validate and rungar config.
	// It is readable and writable by the daemon's user and group alone, and
	// whoever can use it can remove runners and disable providers. It is an
	// absolute path of at most 103 characters. Unset is
	// /run/rungar/rungar.sock.
	Socket string `yaml:"socket,omitempty"`

	// LogLevel is debug, info, warn or error. Unset is info.
	LogLevel string `yaml:"log_level,omitempty"`

	// LogFormat is text, a line of key=value pairs a record, or json, a JSON
	// object a line, for a log collector to parse. Unset is text.
	LogFormat string `yaml:"log_format,omitempty"`

	// path is the file the configuration was read from, if it was, and
	// deprecations what is deprecated in it.
	path         string
	deprecations []Deprecation
}

// Load reads the configuration file at path, fills in its defaults and checks
// it. A file for an older version, or with a deprecated key, is read as
// Migrate would rewrite it, and what is deprecated is in Deprecations.
func Load(path string) (*Config, error) {
	b, err := readFile(path)
	if err != nil {
		return nil, err
	}

	return load(b, path)
}

// readFile reads a configuration file, saying what it is for when there is
// none.
func readFile(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err == nil {
		return b, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errdefs.InvalidArgument("no configuration file at %s; Rungar needs one to know "+
			"which GitHub to talk to and which providers to place runners on: write one there, "+
			"or give the path of yours with --config", path)
	}

	return nil, fmt.Errorf("read configuration: %w", err)
}

// load reads a configuration from b, the contents of the file at path.
func load(b []byte, path string) (*Config, error) {
	f, err := parseFile(b, path)
	if err != nil {
		return nil, err
	}

	return f.decode()
}

// A parsedFile is a configuration file parsed and migrated for Version, not
// yet decoded.
type parsedFile struct {
	path string
	src  []byte

	// doc is src parsed and migrated, deprecations what migrating found, and
	// rewritten whether it changed anything but the version.
	doc          yaml.Node
	deprecations []Deprecation
	rewritten    bool
}

// parseFile parses and migrates b, the contents of the file at path.
func parseFile(b []byte, path string) (*parsedFile, error) {
	// Decoded into a yaml.Node, a document is not checked for an anchor
	// that contains an alias to itself, on which provider.Resolve would
	// recurse without end, or for aliases that expand without bound. Decoded
	// into a value, it is.
	var value any
	if err := yaml.Unmarshal(b, &value); err != nil {
		return nil, errdefs.InvalidArgument("invalid configuration in %s: %w", path, err)
	}

	f := &parsedFile{path: path, src: b}
	if err := yaml.Unmarshal(b, &f.doc); err != nil {
		return nil, errdefs.InvalidArgument("invalid configuration in %s: %w", path, err)
	}

	var err error
	f.deprecations, f.rewritten, err = migrate(&f.doc)
	if err != nil {
		return nil, errdefs.InvalidArgument("invalid configuration in %s: %w", path, err)
	}

	return f, nil
}

// decode decodes the file as migrated, fills in its defaults and checks it.
func (f *parsedFile) decode() (*Config, error) {
	b := f.src
	if f.rewritten {
		var err error
		if b, err = yaml.Marshal(&f.doc); err != nil {
			return nil, fmt.Errorf("migrate configuration: %w", err)
		}
	}

	decoder := yaml.NewDecoder(bytes.NewReader(b))
	decoder.KnownFields(true)

	var c Config

	// An empty file decodes to nothing, for validate to say what is missing.
	if err := decoder.Decode(&c); err != nil && !errors.Is(err, io.EOF) {
		return nil, errdefs.InvalidArgument("invalid configuration in %s: %w", f.path, err)
	}

	c.applyDefaults()

	if err := c.validate(); err != nil {
		return nil, err
	}
	if err := c.resolve(); err != nil {
		return nil, err
	}

	c.path = f.path
	c.deprecations = f.deprecations

	return &c, nil
}

// Deprecations returns what is deprecated in the file the configuration was
// read from, which this Rungar reads but a later one may not.
func (c *Config) Deprecations() []Deprecation {
	return c.deprecations
}

// applyDefaults fills in what the configuration leaves unset.
func (c *Config) applyDefaults() {
	if c.Version == 0 {
		c.Version = Version
	}
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
	if c.LogFormat == "" {
		c.LogFormat = defaultLogFormat
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

// installationPattern matches an installation every backend can keep as a label
// value.
var installationPattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._-]{0,61}[A-Za-z0-9])?$`)

// validate returns an error if the daemon cannot run from the configuration,
// once its defaults are filled in.
func (c *Config) validate() error {
	if err := c.GitHub.validate(); err != nil {
		return err
	}
	if !installationPattern.MatchString(c.Installation) {
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
	if len(c.Socket) > maxSocketPath {
		return errdefs.InvalidArgument("socket %q: %d characters, longer than a Unix socket's path can be: "+
			"use one of at most %d", c.Socket, len(c.Socket), maxSocketPath)
	}
	if _, err := c.LogHandler(io.Discard); err != nil {
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
	endpoints := map[string]string{}

	for i := range c.Providers {
		p := &c.Providers[i]

		cfg, err := providerTypes[p.Type].Configure(&p.raw)
		if err != nil {
			return errdefs.InvalidArgument("provider %q: %w", p.Name, err)
		}
		p.config = cfg

		// Two providers of one backend would place runners on it apart, each
		// counting only its own against its limit.
		endpoint := cfg.Endpoint()
		if other, ok := endpoints[endpoint]; ok {
			return errdefs.InvalidArgument("providers %q and %q both reach %s", other, p.Name, endpoint)
		}
		endpoints[endpoint] = p.Name
	}

	for i := range c.ScaleSets {
		set := &c.ScaleSets[i]
		set.RunnerSpecs = make(map[string]types.RunnerSpec, len(set.Providers))
		set.RunnerRevisions = make(map[string]string, len(set.Providers))

		for _, ref := range set.Providers {
			p, _ := c.Provider(ref.Name)

			block := provider.Merge(&p.RunnerBlock, &ref.RunnerBlock)

			spec, err := p.config.ParseRunner(block)
			if err != nil {
				return errdefs.InvalidArgument("scale set %q: runner on provider %q: %w", set.Name, ref.Name, err)
			}
			if err := checkRunnerName(p.config, set.Name); err != nil {
				return errdefs.InvalidArgument("scale set %q: provider %q: %w", set.Name, ref.Name, err)
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

// runnerNameChecker is a provider.Config whose backend accepts fewer machine
// names than Rungar gives runners, for the configuration to refuse a scale set
// whose runners it could never create.
type runnerNameChecker interface {
	// CheckRunnerName checks name, a runner's name with its random suffix
	// all zeroes, can be a machine's name on the backend.
	CheckRunnerName(name string) error
}

// checkRunnerName checks a provider can create the machines of a scale set's
// runners, if its type is particular about their names.
func checkRunnerName(cfg provider.Config, scaleSet string) error {
	checker, ok := cfg.(runnerNameChecker)
	if !ok {
		return nil
	}

	name, err := types.RunnerName(scaleSet, "00000000")
	if err != nil {
		return err
	}

	return checker.CheckRunnerName(name)
}

// filesChecker is a provider.Config that can check the files it names without
// contacting anything, for rungar validate.
type filesChecker interface {
	CheckFiles() error
}

// CheckFiles reads the files the configuration names, the GitHub credential and
// each provider's own, without contacting anything.
func (c *Config) CheckFiles() error {
	if err := c.GitHub.CheckFiles(); err != nil {
		return err
	}

	for _, p := range c.Providers {
		if checker, ok := p.config.(filesChecker); ok {
			if err := checker.CheckFiles(); err != nil {
				return errdefs.InvalidArgument("provider %q: %w", p.Name, err)
			}
		}
	}

	return nil
}

// LogHandler returns a handler that writes to w in the configured format, at
// the configured level.
func (c *Config) LogHandler(w io.Writer) (slog.Handler, error) {
	level, err := c.logLevel()
	if err != nil {
		return nil, err
	}

	opts := &slog.HandlerOptions{Level: level}

	switch strings.ToLower(c.LogFormat) {
	case "text":
		return slog.NewTextHandler(w, opts), nil
	case "json":
		return slog.NewJSONHandler(w, opts), nil
	default:
		return nil, errdefs.InvalidArgument("unknown log format %q: want text or json", c.LogFormat)
	}
}

// logLevel returns the configured log level.
func (c *Config) logLevel() (slog.Level, error) {
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
