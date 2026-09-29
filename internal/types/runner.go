// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package types

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

// RunnerSpec is a runner's machine as its provider parsed the scale set's
// runner block. It is plain data, encodable as JSON.
type RunnerSpec interface {
	// Describe says how big the runner is, in its provider's terms: "4 vCPU,
	// 8 GiB", or a cloud's machine type. It is only ever shown.
	Describe() string
}

// RunnerRevision returns a short hash of spec, which tells runners made from
// different specs apart. An image named by tag has the same revision whatever
// the tag points at.
func RunnerRevision(spec RunnerSpec) (string, error) {
	b, err := json.Marshal(spec)
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256(b)

	return hex.EncodeToString(sum[:6]), nil
}

// RunnerState is what a runner is doing. A runner takes one job and is
// removed, so it is never idle again once busy.
type RunnerState string

const (
	// RunnerStarting is a runner whose machine exists but which has not yet
	// connected to GitHub.
	RunnerStarting RunnerState = "starting"

	// RunnerIdle is a runner connected to GitHub and waiting for a job.
	RunnerIdle RunnerState = "idle"

	// RunnerBusy is a runner running a job.
	RunnerBusy RunnerState = "busy"
)

// RemovalReason is why a runner was removed.
type RemovalReason string

// The reasons a runner is removed for.
const (
	// RemovalJobCompleted is a runner whose job completed.
	RemovalJobCompleted RemovalReason = "job_completed"

	// RemovalScaledDown is a runner its scale set no longer needed.
	RemovalScaledDown RemovalReason = "scaled_down"

	// RemovalStopped is a runner whose machine stopped.
	RemovalStopped RemovalReason = "stopped"

	// RemovalNeverConnected is a runner that did not connect to GitHub
	// within its start timeout.
	RemovalNeverConnected RemovalReason = "never_connected"

	// RemovalUnregistered is a runner GitHub no longer had the registration
	// of.
	RemovalUnregistered RemovalReason = "unregistered"

	// RemovalDisconnected is a runner disconnected from GitHub for longer
	// than its start timeout.
	RemovalDisconnected RemovalReason = "disconnected"

	// RemovalStuck is a runner running a job while disconnected from GitHub
	// for longer than its start timeout.
	RemovalStuck RemovalReason = "stuck"

	// RemovalOutdated is a runner made from another revision of its
	// runner spec.
	RemovalOutdated RemovalReason = "outdated"

	// RemovalExpired is a runner older than its scale set's max_idle_age
	// or max_age.
	RemovalExpired RemovalReason = "expired"

	// RemovalRequested is a runner removed on request: with rungar runners
	// rm, or with its scale set.
	RemovalRequested RemovalReason = "requested"
)

// Runner is a runner of a scale set, and what it is doing. Its name is both its
// machine's and GitHub's. State is empty for a runner listed from the fleet
// whose scale set the daemon does not run.
type Runner struct {
	Name     string
	ScaleSet string
	Provider string
	State    RunnerState

	// JobID is the job a busy runner is running, when the daemon was told.
	JobID string

	// Size describes the runner's machine, as its provider does, or is empty.
	Size string

	// Adopted reports that the runner was found on the fleet rather than
	// made by this daemon.
	Adopted bool

	// Revision is the RunnerRevision the machine was made from, or empty if
	// the machine does not say.
	Revision string

	CreatedAt time.Time

	// MachineState and GitHubStatus are set for runners listed from the
	// fleet.
	MachineState MachineState
	GitHubStatus GitHubStatus
}

// GitHubStatus is what GitHub says of a runner. Empty means unknown: GitHub was
// not asked, or could not be.
type GitHubStatus string

// What GitHub can say of a runner.
const (
	// GitHubBusy is a runner running a job.
	GitHubBusy GitHubStatus = "busy"

	// GitHubIdle is a runner connected to GitHub, waiting for a job.
	GitHubIdle GitHubStatus = "idle"

	// GitHubOffline is a runner GitHub has that is not connected.
	GitHubOffline GitHubStatus = "offline"

	// GitHubNotRegistered is a runner GitHub does not have.
	GitHubNotRegistered GitHubStatus = "not-registered"
)

// RunnerList is the runners on the fleet, and what could not be asked about
// them.
type RunnerList struct {
	Items []Runner

	// Unreachable maps each provider that could not be listed to why.
	Unreachable map[string]error

	// GitHubErr is why GitHub could not be asked about the runners. GitHub is
	// asked even when there are none, so it also says whether GitHub accepts
	// the daemon's credentials.
	GitHubErr error
}
