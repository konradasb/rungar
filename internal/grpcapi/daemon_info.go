// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package grpcapi

import (
	"time"

	"github.com/konradasb/rungar/internal/github"
)

// DaemonInfo describes the running daemon.
type DaemonInfo struct {
	StartedAt  time.Time
	ConfigFile string

	// Installation is the installation's name, which every runner carries.
	Installation string

	// GitHubURL is the enterprise, organisation or repository served, and
	// GitHubScope which of those it is. GitHubCredentials describes the
	// credential without its secret.
	GitHubURL         string
	GitHubScope       github.Scope
	GitHubCredentials string
}
