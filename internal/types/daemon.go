// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package types

import "time"

// DaemonInfo describes the running daemon.
type DaemonInfo struct {
	StartTime  time.Time
	ConfigFile string

	// Installation is the installation's name, which every runner carries.
	Installation string

	// GitHubURL is the enterprise, organisation or repository served, and
	// GitHubScope which of those it is. GitHubCredentials describes the
	// credential without its secret.
	GitHubURL         string
	GitHubScope       string
	GitHubCredentials string
}
