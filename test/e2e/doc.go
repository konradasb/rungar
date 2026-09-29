// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

//go:build e2e

// Package e2e runs rungar on a real host, against real providers -- Dicer
// hosts, Compute Engine, EC2 -- and a real GitHub, and drives it through its
// command line. It covers what unit tests cannot: runners booting,
// registering and connecting to GitHub, and a restarted daemon adopting them.
//
// The tests are behind the e2e build tag. Run them with:
//
//	make test-e2e RUNGAR_E2E_HOST=10.10.0.101
//
// The host needs a rungar configuration, /etc/rungar/config.yaml unless
// RUNGAR_E2E_CONFIG says otherwise, whose GitHub credential and providers the
// tests use; RUNGAR_E2E_PROVIDERS=gcp,compute1 limits them to some of the
// providers. Everything else is the tests' own: the daemon under test has its
// own binary, configuration, events, socket and systemd unit, and its own
// installation and scale set, both named rungar-e2e, so a real Rungar on the
// same host and fleet is left alone. The scale set and its runners are removed
// at the end.
//
// environment_test.go sets up the environment, host_test.go reaches the host
// over SSH, rungar_test.go runs commands, and daemon_test.go tests the daemon
// itself: a restart, and its metrics. The other files test one command each.
package e2e
