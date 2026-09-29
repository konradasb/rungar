// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

// Package types holds the data shared across Rungar's packages: scale sets,
// runners and their machines, providers, labels, and how calls to providers
// end.
//
// Besides describing that data, it decodes and validates it from YAML,
// computes the min_runners a schedule keeps in force, derives runner names
// and hashes runner specs into revisions. It imports nothing of Rungar's but
// errdefs, so every other package may import it. It knows nothing of any
// backend: a runner's machine is described by the RunnerSpec its provider
// parsed.
package types
