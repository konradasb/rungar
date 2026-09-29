// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

// Package types holds the data shared across Rungar's packages: scale sets,
// runners and their machines, providers, labels and events.
//
// It has no behaviour beyond validating and describing that data, and imports
// nothing of Rungar's but errdefs, so every other package may import it. It
// knows nothing of any backend: a runner's machine is described by the
// RunnerSpec its provider parsed.
package types
