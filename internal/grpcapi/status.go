// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package grpcapi

import (
	"context"

	"github.com/konradasb/rungar/internal/types"
	"github.com/konradasb/rungar/internal/version"
	rungarv1 "github.com/konradasb/rungar/proto/rungar/v1"
)

// GetStatus returns the daemon, its scale sets, providers and runners.
func (s *Server) GetStatus(ctx context.Context, _ *rungarv1.GetStatusRequest) (*rungarv1.DaemonStatus, error) {
	providers, runners, err := s.scaleSets.ProvidersAndRunners(ctx)
	if err != nil {
		return nil, err
	}

	out := &rungarv1.DaemonStatus{
		Daemon:               daemonToProto(s.info),
		Runners:              runnersToProto(runners.Items),
		UnreachableProviders: errorsToProto(runners.Unreachable),
	}
	out.Daemon.Github.CredentialsError = errString(runners.GitHubErr)
	for _, set := range s.scaleSets.Configured() {
		out.ScaleSets = append(out.ScaleSets, scaleSetToProto(set))
	}
	for _, p := range providers {
		out.Providers = append(out.Providers, providerToProto(p))
	}

	return out, nil
}

// daemonToProto converts the daemon's description, without the credentials'
// error, which GetStatus finds out.
func daemonToProto(info types.DaemonInfo) *rungarv1.Daemon {
	return &rungarv1.Daemon{
		Version:      version.Version,
		Commit:       version.Commit,
		StartTime:    timestamp(info.StartTime),
		ConfigFile:   info.ConfigFile,
		Installation: info.Installation,
		Github: &rungarv1.GitHub{
			Url:         info.GitHubURL,
			Scope:       info.GitHubScope,
			Credentials: info.GitHubCredentials,
		},
	}
}
