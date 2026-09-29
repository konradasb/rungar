// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// commandTimeout bounds one command. rungar scale-sets rm --wait, which waits
// for jobs, is the slowest.
const commandTimeout = 5 * time.Minute

// rungar runs a rungar command on the host and returns its output, failing
// the test on error. The tests use the command line, not the API, to exercise
// the whole stack as a person does.
func (e *environment) rungar(t *testing.T, args ...string) string {
	t.Helper()

	out, err := e.tryRungar(t, args...)
	if err != nil {
		t.Fatalf("rungar %s: %v", strings.Join(args, " "), err)
	}

	return out
}

// tryRungar is rungar, returning the error instead of failing the test.
func (e *environment) tryRungar(t *testing.T, args ...string) (string, error) {
	t.Helper()

	ctx, cancel := commandContext(t)
	defer cancel()

	return e.runRungar(ctx, args...)
}

// runRungar runs a rungar command against the daemon under test.
func (e *environment) runRungar(ctx context.Context, args ...string) (string, error) {
	argv := append([]string{e.paths.rungar}, args...)
	argv = append(argv, "--socket", e.paths.socket)

	return e.host.run(ctx, argv...)
}

// commandContext returns the test's context, bounded by commandTimeout.
func commandContext(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()

	return context.WithTimeout(t.Context(), commandTimeout)
}

// decode decodes a command's --json output. what names it in a failure.
func decode[T any](t *testing.T, out, what string) T {
	t.Helper()

	var decoded T
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("decode %s: %v\noutput:\n%s", what, err, out)
	}

	return decoded
}

// eventually runs check every five seconds until it returns "", failing the
// test with its last result after timeout.
func (e *environment) eventually(t *testing.T, timeout time.Duration, what string, check func() string) {
	t.Helper()

	var last string
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); time.Sleep(5 * time.Second) {
		if last = check(); last == "" {
			return
		}
	}

	ctx, cancel := commandContext(t)
	defer cancel()

	t.Fatalf("%s: not within %s: %s\n%s", what, timeout, last, e.journal(ctx))
}

// journal returns the daemon's recent log, for failure messages.
func (e *environment) journal(ctx context.Context) string {
	out, err := e.host.run(ctx, "journalctl", "-u", e.paths.unit, "--no-pager", "-n", "100")
	if err != nil {
		return fmt.Sprintf("(could not read the daemon log: %v)", err)
	}

	return out
}
