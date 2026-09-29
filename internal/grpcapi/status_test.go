// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package grpcapi

import (
	"context"
	"errors"
	"testing"

	"github.com/konradasb/rungar/internal/types"
	"github.com/konradasb/rungar/internal/version"
	rungarv1 "github.com/konradasb/rungar/proto/rungar/v1"
)

// TestGetStatusHoldsEverythingTheDaemonRuns checks the status holds the daemon,
// its configured scale sets alone, every provider, and every runner.
func TestGetStatusHoldsEverythingTheDaemonRuns(t *testing.T) {
	f := newFixture(t)
	f.scaleSets.configured = []types.ScaleSet{{
		Spec: types.ScaleSetSpec{Name: "rungar-vm"}, Status: types.ScaleSetStatus{Configured: true},
	}}
	f.scaleSets.providers = []types.Provider{{Snapshot: types.ProviderSnapshot{Name: "compute1", RunnerCount: 2}}}
	f.scaleSets.runners = types.RunnerList{
		Items:     []types.Runner{{Name: "gone-vm-1"}, {Name: "rungar-vm-1"}},
		GitHubErr: errors.New("401 Unauthorized: Bad credentials"),
	}

	status, err := f.client.GetStatus(context.Background(), &rungarv1.GetStatusRequest{})
	if err != nil {
		t.Fatal(err)
	}

	d := status.GetDaemon()
	switch {
	case d.GetVersion() != version.Version || d.GetCommit() != version.Commit:
		t.Errorf("version = %s %s, want this build's", d.GetVersion(), d.GetCommit())
	case !d.GetStartTime().AsTime().Equal(f.info.StartedAt):
		t.Errorf("start time = %v, want %v", d.GetStartTime().AsTime(), f.info.StartedAt)
	case d.GetConfigFile() != f.info.ConfigFile || d.GetInstallation() != testInstallation:
		t.Errorf("config file %q, installation %q; want the daemon's", d.GetConfigFile(), d.GetInstallation())
	case d.GetGithub().GetUrl() != f.info.GitHubURL || d.GetGithub().GetScope() != string(f.info.GitHubScope) ||
		d.GetGithub().GetCredentials() != f.info.GitHubCredentials:
		t.Errorf("github = %v, want the daemon's", d.GetGithub())
	case d.GetGithub().GetError() != "401 Unauthorized: Bad credentials":
		t.Errorf("credentials error = %q, want GitHub's refusal", d.GetGithub().GetError())
	}

	if len(status.GetScaleSets()) != 1 || status.GetScaleSets()[0].GetName() != "rungar-vm" {
		t.Errorf("scale sets = %v, want the configured rungar-vm alone", status.GetScaleSets())
	}
	if len(status.GetProviders()) != 1 || status.GetProviders()[0].GetRunnerCount() != 2 {
		t.Errorf("providers = %v, want compute1 with 2 runners", status.GetProviders())
	}
	if len(status.GetRunners()) != 2 {
		t.Errorf("got %d runners, want every one: 2", len(status.GetRunners()))
	}
}
