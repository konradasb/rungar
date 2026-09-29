// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package grpcapi

import (
	"context"
	"errors"
	"testing"

	"github.com/konradasb/rungar/internal/version"
	rungarv1 "github.com/konradasb/rungar/proto/rungar/v1"
)

// TestGetStatus checks the status holds the daemon, its configured scale sets
// alone, every provider, and every runner.
func TestGetStatus(t *testing.T) {
	f := newFixture(t, "rungar-vm/rungar-vm-1", "gone-vm/gone-vm-1")

	st, err := f.client.GetStatus(context.Background(), &rungarv1.GetStatusRequest{})
	if err != nil {
		t.Fatal(err)
	}

	d := st.GetDaemon()
	switch {
	case d.GetVersion() != version.Version || d.GetCommit() != version.Commit:
		t.Errorf("version = %s %s, want this build's", d.GetVersion(), d.GetCommit())
	case !d.GetStartTime().AsTime().Equal(f.info.StartTime):
		t.Errorf("start time = %v, want %v", d.GetStartTime().AsTime(), f.info.StartTime)
	case d.GetConfigFile() != f.info.ConfigFile || d.GetInstallation() != testInstallation:
		t.Errorf("config file %q, installation %q; want the daemon's", d.GetConfigFile(), d.GetInstallation())
	case d.GetGithub().GetUrl() != f.info.GitHubURL || d.GetGithub().GetScope() != f.info.GitHubScope ||
		d.GetGithub().GetCredentials() != f.info.GitHubCredentials:
		t.Errorf("github = %v, want the daemon's", d.GetGithub())
	}

	if len(st.GetScaleSets()) != 1 || st.GetScaleSets()[0].GetName() != "rungar-vm" {
		t.Errorf("scale sets = %v, want the configured rungar-vm alone", st.GetScaleSets())
	}
	if len(st.GetProviders()) != 1 || st.GetProviders()[0].GetRunnerCount() != 2 {
		t.Errorf("providers = %v, want compute1 with 2 runners", st.GetProviders())
	}
	if len(st.GetRunners()) != 2 {
		t.Errorf("got %d runners, want every one on the fleet: 2", len(st.GetRunners()))
	}
}

// TestGetStatusSaysWhatIsMissing checks a provider that cannot be listed is
// said rather than failing the status.
func TestGetStatusSaysWhatIsMissing(t *testing.T) {
	f := newFixture(t, "rungar-vm/rungar-vm-1")
	f.compute.listErr = errors.New("connection refused")

	st, err := f.client.GetStatus(context.Background(), &rungarv1.GetStatusRequest{})
	if err != nil {
		t.Fatal(err)
	}

	if st.GetUnreachableProviders()["compute1"] != "connection refused" {
		t.Errorf("unreachable = %v, want compute1's error", st.GetUnreachableProviders())
	}
}

// TestGetStatusChecksTheCredentials checks GitHub is asked even with no
// runners to ask about, so that the status says whether it takes the
// credentials.
func TestGetStatusChecksTheCredentials(t *testing.T) {
	f := newFixture(t)

	st, err := f.client.GetStatus(context.Background(), &rungarv1.GetStatusRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if got := st.GetDaemon().GetGithub().GetCredentialsError(); got != "" {
		t.Errorf("credentials error = %q, want none", got)
	}

	f = newFixture(t)
	f.github.listErr = errors.New("401 Unauthorized: Bad credentials")

	st, err = f.client.GetStatus(context.Background(), &rungarv1.GetStatusRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if got := st.GetDaemon().GetGithub().GetCredentialsError(); got != "401 Unauthorized: Bad credentials" {
		t.Errorf("credentials error = %q, want GitHub's refusal", got)
	}
}
