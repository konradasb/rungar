// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

// Package scaleset runs the configured scale sets: each one's message session
// with GitHub, its runners on the fleet, and the scaling decisions between
// them. It also answers for scale sets no longer configured whose runners are
// still on the fleet.
//
// Runners outlive the daemon: stopping leaves them where they are, and
// starting adopts them, so a restart does not disturb a running job.
package scaleset
