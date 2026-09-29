// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	ghscaleset "github.com/actions/scaleset"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/fleet"
	"github.com/konradasb/rungar/internal/github"
	"github.com/konradasb/rungar/internal/types"
)

// TestWarnLeftoversWarnsOncePerScaleSet checks each scale set no longer
// configured is warned of once, with its runners counted, and that the
// configured one is not.
func TestWarnLeftoversWarnsOncePerScaleSet(t *testing.T) {
	f := newFixture(t, "rungar-vm/rungar-vm-1", "gone-vm/gone-vm-1", "gone-vm/gone-vm-2", "old-vm/old-vm-1")

	var logs bytes.Buffer
	f.m.logger = slog.New(slog.NewTextHandler(&logs, nil))
	f.m.warnLeftovers(context.Background())

	got := logs.String()
	if n := strings.Count(got, "no longer configured"); n != 2 {
		t.Errorf("warned %d times, want once each for gone-vm and old-vm; logged:\n%s", n, got)
	}
	if !strings.Contains(got, "scale_set=gone-vm runners=2") || !strings.Contains(got, "scale_set=old-vm runners=1") {
		t.Errorf("warnings do not name the leftovers and their runners; logged:\n%s", got)
	}
	if strings.Contains(got, "scale_set=rungar-vm") {
		t.Errorf("warned of the configured scale set; logged:\n%s", got)
	}
	if len(f.compute.deleted) != 0 {
		t.Errorf("deleted %v, want the leftovers left alone", f.compute.deleted)
	}
}

// TestReconcileAdoptsANewRunnerAtOnce checks a pass asked for runs at once: a
// runner that appeared on the fleet since the last is adopted.
func TestReconcileAdoptsANewRunnerAtOnce(t *testing.T) {
	f := newFixture(t, "rungar-vm/rungar-vm-1")
	f.adopt(t)

	f.compute.mu.Lock()
	f.compute.machines = append(f.compute.machines, types.Machine{
		Name:   "rungar-vm-2",
		Labels: types.RunnerLabels(testInstallation, "rungar-vm", "rungar-vm-2", ""),
		State:  types.MachineRunning, CreatedAt: time.Now(),
	})
	f.compute.mu.Unlock()
	f.onGitHub["rungar-vm-2"] = answer{Registered: true, Online: true}

	sets, err := f.m.Reconcile(context.Background(), nil)
	if err != nil {
		t.Fatalf("Reconcile() = %v", err)
	}
	if len(sets) != 1 || len(sets[0].Status.Runners) != 2 {
		t.Errorf("sets = %+v; want rungar-vm with both its runners", sets)
	}
}

func TestReconcileRefusesUnknownAndStartingScaleSets(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if _, err := f.m.Reconcile(ctx, []string{"nowhere"}); !errors.Is(err, errdefs.ErrNotFound) {
		t.Errorf("Reconcile() of a scale set not configured = %v, want an ErrNotFound", err)
	}
	if _, err := f.m.Reconcile(ctx, nil); !errors.Is(err, errdefs.ErrUnavailable) {
		t.Errorf("Reconcile() of a scale set still starting = %v, want an ErrUnavailable", err)
	}
}

// TestProviderLimitHoldsAcrossScaleSets checks that New has the fleet count
// every scale set's runners against a provider's max_runners: two scale sets
// sharing a provider that allows 3 get 3 between them, whatever each wants.
func TestProviderLimitHoldsAcrossScaleSets(t *testing.T) {
	compute := &fakeProvider{}
	fleetManager := fleet.New([]fleet.ProviderConfig{
		{Name: "compute1", Type: "test", Weight: 1, MaxRunners: 3, Backend: compute},
	}, fleet.Config{})

	var specs []types.ScaleSetSpec
	for _, name := range []string{"small", "large"} {
		specs = append(specs, types.ScaleSetSpec{
			Name: name, MaxRunners: 10, StartTimeout: time.Minute, RunnerGroup: types.DefaultRunnerGroup,
			Providers:   []types.ProviderRef{{Name: "compute1"}},
			RunnerSpecs: map[string]types.RunnerSpec{"compute1": runnerSize{VCPUs: 2, Memory: 4 * gib}},
		})
	}

	m, err := New(Config{
		ScaleSets:         specs,
		Installation:      testInstallation,
		ReconcileInterval: time.Minute,
		Fleet:             fleetManager,
		GitHub:            &fakeGitHub{},
		NewScaleSetClient: func() (*ghscaleset.Client, error) {
			return ghscaleset.NewClientWithPersonalAccessToken(ghscaleset.NewClientWithPersonalAccessTokenConfig{
				GitHubConfigURL: "https://github.com/my-org", PersonalAccessToken: "not-a-token",
			})
		},
		GitHubRunners: noRunners{},
		Logger:        discardLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	for _, s := range m.sets {
		s.github = &fakeGitHub{}
		s.id.Store(1)
		s.serving.Store(true)
	}
	for _, s := range m.sets {
		if _, err := s.HandleDesiredRunnerCount(ctx, 5); err != nil {
			t.Fatalf("%s: HandleDesiredRunnerCount() = %v", s.spec.Name, err)
		}
	}

	total := m.sets[0].runnerCount() + m.sets[1].runnerCount()
	if total != 3 || len(compute.machines) != 3 {
		t.Errorf("%d runners between the scale sets, %d machines on compute1; want 3 of each, its limit",
			total, len(compute.machines))
	}
}

// TestProviderLimitCountsMachinesNotYetDeleted checks a runner whose machine
// cannot be deleted keeps its place under its provider's max_runners: the
// scale set replaces it, but not on a provider whose limit its machine still
// takes, until the machine is deleted.
func TestProviderLimitCountsMachinesNotYetDeleted(t *testing.T) {
	compute := &fakeProvider{deleteErr: errors.New("the provider's API is down")}
	fleetManager := fleet.New([]fleet.ProviderConfig{
		{Name: "compute1", Type: "test", Weight: 1, MaxRunners: 1, Backend: compute},
	}, fleet.Config{})

	m, err := New(Config{
		ScaleSets: []types.ScaleSetSpec{{
			Name: "rungar-vm", MaxRunners: 10, StartTimeout: time.Minute, RunnerGroup: types.DefaultRunnerGroup,
			Providers:   []types.ProviderRef{{Name: "compute1"}},
			RunnerSpecs: map[string]types.RunnerSpec{"compute1": runnerSize{VCPUs: 2, Memory: 4 * gib}},
		}},
		Installation:      testInstallation,
		ReconcileInterval: time.Minute,
		Fleet:             fleetManager,
		GitHub:            &fakeGitHub{},
		NewScaleSetClient: func() (*ghscaleset.Client, error) {
			return ghscaleset.NewClientWithPersonalAccessToken(ghscaleset.NewClientWithPersonalAccessTokenConfig{
				GitHubConfigURL: "https://github.com/my-org", PersonalAccessToken: "not-a-token",
			})
		},
		GitHubRunners: noRunners{},
		Logger:        discardLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	s := m.sets[0]
	s.github = &fakeGitHub{}
	s.id.Store(1)
	s.serving.Store(true)

	if _, err := s.HandleDesiredRunnerCount(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if s.runnerCount() != 1 {
		t.Fatalf("%d runners, want 1", s.runnerCount())
	}
	name := s.sortedRunners()[0].Name

	// Its job completes, and its machine cannot be deleted.
	if err := s.HandleJobCompleted(ctx, &ghscaleset.JobCompleted{RunnerName: name, Result: "succeeded"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HandleDesiredRunnerCount(ctx, 1); err != nil {
		t.Fatal(err)
	}

	compute.mu.Lock()
	machines := len(compute.machines)
	compute.mu.Unlock()
	if machines != 1 || s.runnerCount() != 0 {
		t.Errorf("%d machines on compute1, %d runners; want only the 1 not yet deleted, and no runner: "+
			"its limit is 1", machines, s.runnerCount())
	}

	// Once the machine is deleted, its place is taken.
	compute.mu.Lock()
	compute.deleteErr = nil
	compute.mu.Unlock()
	s.settleLeaving(ctx, s.now())
	if _, err := s.HandleDesiredRunnerCount(ctx, 1); err != nil {
		t.Fatal(err)
	}

	if s.runnerCount() != 1 {
		t.Errorf("%d runners once the machine was deleted, want 1", s.runnerCount())
	}
}

// noRunners is GitHub's REST API with no runners registered.
type noRunners struct{}

func (noRunners) Runners(context.Context) ([]github.Runner, error) { return nil, nil }

// TestNewRefusesAConfigMissingARequiredField checks New says which field is
// missing rather than leaving the Manager to panic on it later.
func TestNewRefusesAConfigMissingARequiredField(t *testing.T) {
	complete := func() Config {
		return Config{
			ScaleSets:         []types.ScaleSetSpec{{Name: "rungar-vm"}},
			Fleet:             fleet.New(nil, fleet.Config{}),
			GitHub:            &fakeGitHub{},
			NewScaleSetClient: func() (*ghscaleset.Client, error) { return nil, nil },
			GitHubRunners:     noRunners{},
		}
	}

	tests := []struct {
		field  string
		remove func(*Config)
	}{
		{"Fleet", func(c *Config) { c.Fleet = nil }},
		{"GitHub", func(c *Config) { c.GitHub = nil }},
		{"GitHubRunners", func(c *Config) { c.GitHubRunners = nil }},
		{"NewScaleSetClient", func(c *Config) { c.NewScaleSetClient = nil }},
	}
	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			cfg := complete()
			tt.remove(&cfg)

			if _, err := New(cfg); err == nil || !strings.Contains(err.Error(), "Config."+tt.field) {
				t.Errorf("New() = %v, want an error naming Config.%s", err, tt.field)
			}
		})
	}
}

// TestListenersLogToTheirOwnLogger checks the Manager and its scale sets log
// to Logger, adding only the scale set, and the listeners to ListenerLogger.
func TestListenersLogToTheirOwnLogger(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	m, err := New(Config{
		ScaleSets: []types.ScaleSetSpec{{Name: "rungar-vm"}},
		Fleet:     fleet.New(nil, fleet.Config{}),
		GitHub:    &fakeGitHub{},
		NewScaleSetClient: func() (*ghscaleset.Client, error) {
			return ghscaleset.NewClientWithPersonalAccessToken(ghscaleset.NewClientWithPersonalAccessTokenConfig{
				GitHubConfigURL: "https://github.com/my-org", PersonalAccessToken: "not-a-token",
			})
		},
		GitHubRunners:  noRunners{},
		Logger:         logger.With(slog.String("component", "scaleset")),
		ListenerLogger: logger.With(slog.String("component", "listener")),
	})
	if err != nil {
		t.Fatal(err)
	}

	m.logger.Info("from the manager")
	m.sets[0].logger.Info("from the scale set")
	m.sets[0].listenerLogger.Warn("from the listener")

	want := []string{
		`msg="from the manager" component=scaleset`,
		`msg="from the scale set" component=scaleset scale_set=rungar-vm`,
		`msg="from the listener" component=listener scale_set=rungar-vm`,
	}
	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if len(lines) != len(want) {
		t.Fatalf("logged:\n%s\nwant %d lines", logs.String(), len(want))
	}
	for i, line := range lines {
		if !strings.HasSuffix(line, want[i]) {
			t.Errorf("line %d = %q, want it to end %q", i, line, want[i])
		}
	}
}
