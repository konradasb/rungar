// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package grpcapi

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	ghscaleset "github.com/actions/scaleset"

	"github.com/konradasb/rungar/internal/fleet"
	"github.com/konradasb/rungar/internal/github"
	"github.com/konradasb/rungar/internal/scaleset"
	"github.com/konradasb/rungar/internal/types"
	rungarv1 "github.com/konradasb/rungar/proto/rungar/v1"
)

// fakeProvider is an in-memory provider.
type fakeProvider struct {
	mu sync.Mutex

	machines []types.Machine

	// listErr is what List answers with, if anything.
	listErr error
}

func (p *fakeProvider) List(_ context.Context, selector map[string]string) ([]types.Machine, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.listErr != nil {
		return nil, p.listErr
	}

	var out []types.Machine
	for _, m := range p.machines {
		if types.HasLabels(m.Labels, selector) {
			out = append(out, m)
		}
	}

	return out, nil
}

// setListErr sets what List answers with; nil lists the machines.
func (p *fakeProvider) setListErr(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.listErr = err
}

func (p *fakeProvider) Create(context.Context, types.MachineSpec) error { return nil }
func (p *fakeProvider) Close() error                                    { return nil }

func (p *fakeProvider) Delete(_ context.Context, name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.machines = slices.DeleteFunc(p.machines, func(m types.Machine) bool { return m.Name == name })

	return nil
}

// fakeGitHub is an in-memory GitHub, serving both the scale set API and the
// REST API.
type fakeGitHub struct {
	mu sync.Mutex

	sets    map[string]*ghscaleset.RunnerScaleSet
	runners []github.Runner

	// listErr is what listing the runners answers with, if anything.
	listErr error
}

func (g *fakeGitHub) GetRunnerGroupByName(_ context.Context, name string) (*ghscaleset.RunnerGroup, error) {
	return &ghscaleset.RunnerGroup{ID: 1, Name: name}, nil
}

func (g *fakeGitHub) GetRunnerScaleSet(_ context.Context, _ int, name string) (*ghscaleset.RunnerScaleSet, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	return g.sets[name], nil
}

func (g *fakeGitHub) CreateRunnerScaleSet(context.Context, *ghscaleset.RunnerScaleSet) (*ghscaleset.RunnerScaleSet,
	error,
) {
	return nil, errors.New("not in these tests")
}

func (g *fakeGitHub) DeleteRunnerScaleSet(_ context.Context, id int) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	for name, set := range g.sets {
		if set.ID == id {
			delete(g.sets, name)
		}
	}

	return nil
}

func (g *fakeGitHub) GenerateJitRunnerConfig(context.Context, *ghscaleset.RunnerScaleSetJitRunnerSetting, int,
) (*ghscaleset.RunnerScaleSetJitRunnerConfig, error) {
	return nil, errors.New("not in these tests")
}

func (g *fakeGitHub) GetRunnerByName(_ context.Context, name string) (*ghscaleset.RunnerReference, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	for _, r := range g.runners {
		if r.Name == name {
			return &ghscaleset.RunnerReference{ID: int(r.ID), Name: r.Name}, nil
		}
	}

	return nil, nil //nolint:nilnil // as the scale set client answers for a runner GitHub does not have
}

// RemoveRunner refuses a runner that is running a job, as GitHub does.
func (g *fakeGitHub) RemoveRunner(_ context.Context, id int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	i := slices.IndexFunc(g.runners, func(r github.Runner) bool { return r.ID == id })
	switch {
	case i < 0:
		return nil
	case g.runners[i].Busy:
		return ghscaleset.JobStillRunningError
	}

	g.runners = slices.Delete(g.runners, i, i+1)

	return nil
}

func (g *fakeGitHub) Runners(context.Context) ([]github.Runner, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.listErr != nil {
		return nil, g.listErr
	}

	return slices.Clone(g.runners), nil
}

// setListErr sets what listing the runners answers with; nil lists them.
func (g *fakeGitHub) setListErr(err error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.listErr = err
}

// endToEnd is a Server over a real scale set manager that has not been run,
// served over an in-memory connection with the daemon's interceptors: scale
// set rungar-vm on compute1, which holds the runners given, and GitHub
// holding rungar-vm. It is for what the service says of the manager's answers
// that its own tests do not cover; handler tests use a fixture.
type endToEnd struct {
	client  rungarv1.RungarServiceClient
	compute *fakeProvider
	github  *fakeGitHub
}

// newEndToEnd makes an endToEnd over runners written scaleSet/name, each
// online and idle on GitHub.
func newEndToEnd(t *testing.T, runners ...string) *endToEnd {
	t.Helper()

	f := &endToEnd{
		compute: &fakeProvider{},
		github: &fakeGitHub{sets: map[string]*ghscaleset.RunnerScaleSet{
			"rungar-vm": {ID: 7, Name: "rungar-vm", RunnerGroupName: types.DefaultRunnerGroup,
				Labels:     []ghscaleset.Label{{Name: "rungar-vm"}, {Name: "linux"}},
				Statistics: &ghscaleset.RunnerScaleSetStatistic{TotalAssignedJobs: 3, TotalRunningJobs: 2}},
		}},
	}

	for i, r := range runners {
		scaleSet, name, _ := strings.Cut(r, "/")
		f.compute.machines = append(f.compute.machines, types.Machine{
			Name:      name,
			Labels:    types.RunnerLabels(testInstallation, scaleSet, name, ""),
			State:     types.MachineRunning,
			Size:      "2 vCPU, 4 GiB",
			CreatedAt: time.Now().Add(-time.Minute),
		})
		f.github.runners = append(f.github.runners, github.Runner{ID: int64(i + 1), Name: name, Status: "online"})
	}

	eventLog := newEventLog(t)

	fl := fleet.New([]fleet.ProviderConfig{{
		Name: "compute1", Type: "dicer", Weight: 1, Endpoint: "10.0.0.1:7443", Backend: f.compute,
	}}, fleet.Config{Events: eventLog})

	spec := types.ScaleSetSpec{
		Name:         "rungar-vm",
		Labels:       []string{"linux"},
		RunnerGroup:  types.DefaultRunnerGroup,
		MinRunners:   1,
		MaxRunners:   4,
		Priority:     2,
		Providers:    []types.ProviderRef{{Name: "compute1"}},
		Placement:    types.PlacementSpread,
		StartTimeout: time.Minute,
		RunnerSpecs:  map[string]types.RunnerSpec{"compute1": runnerSize{VCPUs: 2, Memory: 4 * gib}},
	}

	sets, err := scaleset.New(scaleset.Config{
		ScaleSets:         []types.ScaleSetSpec{spec},
		Installation:      testInstallation,
		ReconcileInterval: time.Minute,
		Fleet:             fl,
		GitHub:            f.github,
		NewScaleSetClient: func() (*ghscaleset.Client, error) {
			return ghscaleset.NewClientWithPersonalAccessToken(ghscaleset.NewClientWithPersonalAccessTokenConfig{
				GitHubConfigURL: "https://github.com/my-org", PersonalAccessToken: "test",
			})
		},
		GitHubRunners: f.github,
		Events:        eventLog,
		Logger:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}

	f.client, _ = serve(t, Config{ScaleSets: sets, Events: eventLog})

	return f
}

// TestListRunnersSaysWhatIsMissing checks a listing says which providers
// could not be listed, and that GitHub could not be asked, rather than
// failing.
func TestListRunnersSaysWhatIsMissing(t *testing.T) {
	f := newEndToEnd(t, "rungar-vm/rungar-vm-1")
	f.compute.setListErr(errors.New("connection refused"))
	f.github.setListErr(errors.New("rate limited"))

	resp, err := f.client.ListRunners(context.Background(), &rungarv1.ListRunnersRequest{})
	if err != nil {
		t.Fatal(err)
	}

	if got := resp.GetUnreachableProviders()["compute1"]; got != "connection refused" {
		t.Errorf("unreachable[compute1] = %q, want the provider's error", got)
	}
	if len(resp.GetRunners()) != 0 {
		t.Errorf("got %d runners from a provider that cannot be listed", len(resp.GetRunners()))
	}

	f.compute.setListErr(nil)

	resp, err = f.client.ListRunners(context.Background(), &rungarv1.ListRunnersRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetGithubError() != "rate limited" {
		t.Errorf("github error = %q, want GitHub's", resp.GetGithubError())
	}
}

// TestGetStatusSaysWhatIsMissing checks a provider that cannot be listed is
// said rather than failing the status.
func TestGetStatusSaysWhatIsMissing(t *testing.T) {
	f := newEndToEnd(t, "rungar-vm/rungar-vm-1")
	f.compute.setListErr(errors.New("connection refused"))

	status, err := f.client.GetStatus(context.Background(), &rungarv1.GetStatusRequest{})
	if err != nil {
		t.Fatal(err)
	}

	if status.GetUnreachableProviders()["compute1"] != "connection refused" {
		t.Errorf("unreachable = %v, want compute1's error", status.GetUnreachableProviders())
	}
}

// TestGetStatusChecksTheCredentials checks GitHub is asked even with no
// runners to ask about, so that the status says whether it takes the
// credentials.
func TestGetStatusChecksTheCredentials(t *testing.T) {
	f := newEndToEnd(t)

	status, err := f.client.GetStatus(context.Background(), &rungarv1.GetStatusRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if got := status.GetDaemon().GetGithub().GetError(); got != "" {
		t.Errorf("credentials error = %q, want none", got)
	}

	f = newEndToEnd(t)
	f.github.setListErr(errors.New("401 Unauthorized: Bad credentials"))

	status, err = f.client.GetStatus(context.Background(), &rungarv1.GetStatusRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if got := status.GetDaemon().GetGithub().GetError(); got != "401 Unauthorized: Bad credentials" {
		t.Errorf("credentials error = %q, want GitHub's refusal", got)
	}
}
