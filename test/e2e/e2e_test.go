// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// Environment variables that configure the tests.
const (
	hostEnv   = "RUNGAR_E2E_HOST"
	userEnv   = "RUNGAR_E2E_USER"
	keyEnv    = "RUNGAR_E2E_KEY"
	configEnv = "RUNGAR_E2E_CONFIG"
	keepEnv   = "RUNGAR_E2E_KEEP"

	defaultUser   = "root"
	defaultConfig = "/etc/rungar/config.yaml"
)

const (
	// scaleSetName and installation are distinctive, so a real Rungar on the
	// same fleet and GitHub neither sees nor is seen by the tests.
	scaleSetName = "rungar-e2e"
	installation = "rungar-e2e"

	// minRunners is how many idle runners the scale set keeps. No job is
	// run, so these are the runners the tests look at.
	minRunners = 1
)

// paths locates the daemon under test on the host.
type paths struct {
	root   string
	rungar string
	config string
	runDir string
	socket string
	events string
	unit   string

	// metrics is the metrics endpoint's address, on loopback so nothing is
	// exposed.
	metrics string
}

// newPaths returns the daemon's paths under /opt/rungar-e2e.
func newPaths() paths {
	const root = "/opt/rungar-e2e"

	return paths{
		root:    root,
		rungar:  root + "/rungar",
		config:  root + "/config.yaml",
		runDir:  "/run/rungar-e2e",
		socket:  "/run/rungar-e2e/rungar.sock",
		events:  root + "/events.jsonl",
		unit:    "rungar-e2e",
		metrics: "127.0.0.1:9112",
	}
}

// environment is the host with the daemon under test running on it. TestMain
// builds one for every test to share, since runners take minutes to boot.
type environment struct {
	host  *host
	paths paths
	keep  bool

	// baseConfigPath is the host's rungar configuration, whose GitHub
	// credential and providers the daemon under test uses.
	baseConfigPath string

	// providers are the providers' names, in configuration order.
	providers []string
}

// env is the shared environment: there is one host under test.
var env *environment

func TestMain(m *testing.M) {
	addr := os.Getenv(hostEnv)
	if addr == "" {
		fmt.Fprintf(os.Stderr, "%s is not set; skipping the end-to-end tests\n", hostEnv)
		os.Exit(0)
	}

	env = &environment{
		host: &host{
			addr: addr,
			user: envOr(userEnv, defaultUser),
			key:  os.Getenv(keyEnv),
		},
		paths:          newPaths(),
		keep:           os.Getenv(keepEnv) != "",
		baseConfigPath: envOr(configEnv, defaultConfig),
	}

	code, err := run(m)
	if err != nil {
		fmt.Fprintf(os.Stderr, "end-to-end setup failed: %v\n", err)
		os.Exit(1)
	}

	os.Exit(code)
}

// run sets the host up, runs the tests and tears the host down. It is apart
// from TestMain so the teardown can be deferred, which os.Exit would skip.
func run(m *testing.M) (code int, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()

	if err := env.deploy(ctx); err != nil {
		return 0, err
	}
	defer env.teardown(ctx)

	return m.Run(), nil
}

// deploy builds rungar, installs it on the host and starts the daemon.
func (e *environment) deploy(ctx context.Context) error {
	arch, err := e.host.arch(ctx)
	if err != nil {
		return err
	}

	binary, err := build(ctx, arch)
	if err != nil {
		return err
	}

	// A killed previous run may still be serving. This one adopts its
	// runners, and removes them at teardown.
	e.stopUnit(ctx)

	if _, err := e.host.run(ctx, "mkdir", "-p", e.paths.root, e.paths.runDir); err != nil {
		return fmt.Errorf("create %s: %w", e.paths.root, err)
	}

	if err := e.host.uploadExecutable(ctx, binary, e.paths.rungar); err != nil {
		return err
	}

	if err := e.writeConfig(ctx, true); err != nil {
		return err
	}

	return e.startDaemon(ctx)
}

// writeConfig writes the daemon's configuration; see testConfig. Teardown
// writes it without the scale set, so that it can be removed.
func (e *environment) writeConfig(ctx context.Context, withScaleSet bool) error {
	base, err := e.host.run(ctx, "cat", e.baseConfigPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", e.baseConfigPath, err)
	}

	config, providers, err := testConfig(base, e.paths, withScaleSet)
	if err != nil {
		return fmt.Errorf("%s: %w", e.baseConfigPath, err)
	}
	e.providers = providers

	// A heredoc avoids quoting the content for two shells.
	script := fmt.Sprintf("cat > %s <<'RUNGAR_E2E_EOF'\n%sRUNGAR_E2E_EOF\n", e.paths.config, config)
	if _, err := e.host.runShell(ctx, script); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	return nil
}

// testConfig returns the daemon's configuration and the providers' names. It
// keeps base's github and providers as they are, anchors and all, and
// replaces everything else with the tests' own settings.
func testConfig(base string, p paths, withScaleSet bool) (string, []string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(base), &doc); err != nil {
		return "", nil, err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return "", nil, errors.New("not a mapping")
	}

	var decoded struct {
		Providers []struct {
			Name string `yaml:"name"`
		} `yaml:"providers"`
	}
	if err := doc.Decode(&decoded); err != nil {
		return "", nil, err
	}

	names := make([]string, 0, len(decoded.Providers))
	for _, provider := range decoded.Providers {
		names = append(names, provider.Name)
	}
	if len(names) == 0 {
		return "", nil, errors.New("no providers")
	}

	root := doc.Content[0]
	kept := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if key := root.Content[i].Value; key == "github" || key == "providers" {
			kept.Content = append(kept.Content, root.Content[i], root.Content[i+1])
		}
	}

	own := fmt.Sprintf(`installation: %s
socket: %s
reconcile_interval: 15s
log_level: debug
events:
  file: %s
metrics:
  enable: true
  listen: %s
`, installation, p.socket, p.events, p.metrics)

	scaleSets := "scale_sets: []\n"
	if withScaleSet {
		var refs strings.Builder
		for _, name := range names {
			fmt.Fprintf(&refs, "      - name: %s\n        runner: {vcpus: 2, memory: 4GiB}\n", name)
		}
		scaleSets = fmt.Sprintf(`scale_sets:
  - name: %s
    min_runners: %d
    max_runners: 2
    providers:
%s`, scaleSetName, minRunners, refs.String())
	}

	var rest yaml.Node
	if err := yaml.Unmarshal([]byte(own+scaleSets), &rest); err != nil {
		return "", nil, err
	}
	kept.Content = append(kept.Content, rest.Content[0].Content...)

	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{kept}}); err != nil {
		return "", nil, err
	}

	return out.String(), names, nil
}

// startDaemon runs rungar as a transient systemd unit, so its log is in the
// journal for failing tests to quote, and waits for its socket.
func (e *environment) startDaemon(ctx context.Context) error {
	if _, err := e.host.run(ctx,
		"systemd-run", "--unit", e.paths.unit, "--collect",
		"--description", "Rungar end-to-end test daemon",
		e.paths.rungar, "serve", "--config", e.paths.config,
	); err != nil {
		return fmt.Errorf("start the daemon: %w", err)
	}

	return e.waitForDaemon(ctx)
}

// waitForDaemon waits for the daemon's socket to appear.
func (e *environment) waitForDaemon(ctx context.Context) error {
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		if _, err := e.host.run(ctx, "test", "-S", e.paths.socket); err == nil {
			return nil
		}
		time.Sleep(time.Second)
	}

	return fmt.Errorf("the daemon did not open %s within 30s:\n%s", e.paths.socket, e.journal(ctx))
}

// restartDaemon stops the daemon and starts it again, leaving its runners
// for the new one to adopt.
func (e *environment) restartDaemon(t *testing.T) {
	t.Helper()

	ctx, cancel := commandContext(t)
	defer cancel()

	e.stopUnit(ctx)

	// The new daemon must not find the old one's socket.
	if _, err := e.host.run(ctx, "rm", "-f", e.paths.socket); err != nil {
		t.Fatalf("remove the old socket: %v", err)
	}

	if err := e.startDaemon(ctx); err != nil {
		t.Fatalf("start the daemon again: %v", err)
	}
}

// stopUnit stops the daemon's unit and clears a failed one, either of which
// would make the next systemd-run fail with "unit already exists".
func (e *environment) stopUnit(ctx context.Context) {
	if _, err := e.host.run(ctx, "systemctl", "stop", e.paths.unit); err != nil {
		// The unit does not exist before the first run.
		_, _ = e.host.run(ctx, "systemctl", "reset-failed", e.paths.unit)
	}
}

// teardown removes the scale set and its runners with rungar scale-sets rm,
// run by a daemon that no longer configures it, then the daemon and its
// files. It reports failures rather than returning them, so they do not hide
// the tests' own result.
func (e *environment) teardown(ctx context.Context) {
	if e.keep {
		fmt.Fprintf(os.Stderr, "%s is set; leaving the daemon and its runners on %s\n", keepEnv, e.host.addr)
		return
	}

	e.stopUnit(ctx)
	_, _ = e.host.run(ctx, "rm", "-f", e.paths.socket)

	if err := e.writeConfig(ctx, false); err != nil {
		fmt.Fprintf(os.Stderr, "teardown: %v\n", err)
	} else if err := e.startDaemon(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "teardown: %v\n", err)
	} else if out, err := e.runRungar(ctx, "scale-sets", "rm", scaleSetName, "--wait"); err != nil {
		fmt.Fprintf(os.Stderr, "teardown: remove the scale set: %v\n%s", err, out)
	}

	e.stopUnit(ctx)

	if _, err := e.host.run(ctx, "rm", "-rf", e.paths.root, e.paths.runDir); err != nil {
		fmt.Fprintf(os.Stderr, "teardown: remove the daemon's files: %v\n", err)
	}
}

// build compiles rungar for linux/arch and returns the binary's path.
func build(ctx context.Context, arch string) (string, error) {
	root, err := repoRoot()
	if err != nil {
		return "", err
	}

	cmd := exec.CommandContext(ctx, "make", "build", "GOOS=linux", "GOARCH="+arch)
	cmd.Dir = root

	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build for linux/%s: %w\n%s", arch, err, out)
	}

	return filepath.Join(root, "bin", "rungar"), nil
}

// repoRoot returns the repository root, found from this file's path.
func repoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("cannot locate the test source")
	}

	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..")), nil
}

// envOr returns the environment variable name, or fallback if it is unset.
func envOr(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}

	return fallback
}
