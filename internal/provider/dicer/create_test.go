// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package dicer

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/types"
)

// TestCreatePullsTheImageFirst checks that an image the host lacks is pulled
// before the instance is created, and one it holds is not pulled again.
func TestCreatePullsTheImageFirst(t *testing.T) {
	for _, tt := range []struct {
		name       string
		held       bool
		wantPulled []string
	}{
		{name: "an image the host lacks", wantPulled: []string{testRunner().Image}},
		{name: "an image the host holds", held: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, d := newTestProvider(t)
			if tt.held {
				d.held = map[string]bool{testRunner().Image: true}
			}

			if err := p.Create(t.Context(), testMachine("set-abcd1234", "set")); err != nil {
				t.Fatalf("Create() = %v", err)
			}

			if got := d.pullRequests(); !slices.Equal(got, tt.wantPulled) {
				t.Errorf("pulled %q, want %q", got, tt.wantPulled)
			}
			d.lastCreate(t)
		})
	}
}

// TestCreateBoundsTheInstanceButNotThePull checks that a pull may take longer
// than the provider's timeout, as a cold one does, but creating the instance
// may not: a daemon that does not answer is given up on while the runner can
// still be tried elsewhere.
func TestCreateBoundsTheInstanceButNotThePull(t *testing.T) {
	t.Run("a slow pull", func(t *testing.T) {
		p, d := newTestProvider(t)
		p.config.Timeout = 50 * time.Millisecond
		d.pullDelay = 200 * time.Millisecond

		if err := p.Create(t.Context(), testMachine("set-abcd1234", "set")); err != nil {
			t.Errorf("Create() = %v; a pull longer than the timeout was cut short", err)
		}
	})

	t.Run("a create that does not answer", func(t *testing.T) {
		p, d := newTestProvider(t)
		p.config.Timeout = 50 * time.Millisecond
		d.held = map[string]bool{testRunner().Image: true}
		d.createDelay = 5 * time.Second

		start := time.Now()
		err := p.Create(t.Context(), testMachine("set-abcd1234", "set"))
		if status.Code(err) != codes.DeadlineExceeded {
			t.Errorf("Create() = %v, want DeadlineExceeded", err)
		}
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Errorf("took %v; the timeout did not bound the create", elapsed)
		}
	})
}

// TestCreateSaysWhyThePullFailed checks that a failed pull is classed as a
// refused instance is, and names the image.
func TestCreateSaysWhyThePullFailed(t *testing.T) {
	for _, tt := range []struct {
		code  codes.Code
		class error
	}{
		{codes.NotFound, errdefs.ErrInvalidArgument},
		{codes.ResourceExhausted, errdefs.ErrNoCapacity},
	} {
		t.Run(tt.code.String(), func(t *testing.T) {
			p, d := newTestProvider(t)
			d.pullErr = status.Error(tt.code, "the daemon's reason")

			err := p.Create(t.Context(), testMachine("set-abcd1234", "set"))
			if !errors.Is(err, tt.class) {
				t.Errorf("Create() = %v, want it in %v", err, tt.class)
			}
			if err == nil || !strings.Contains(err.Error(), testRunner().Image) {
				t.Errorf("Create() = %v, want it to name the image", err)
			}
			if d.createCount() != 0 {
				t.Error("an instance was created though its image could not be pulled")
			}
		})
	}

	t.Run(codes.Unavailable.String(), func(t *testing.T) {
		p, d := newTestProvider(t)
		d.pullErr = status.Error(codes.Unavailable, "rate limited")

		err := p.Create(t.Context(), testMachine("set-abcd1234", "set"))
		if status.Code(err) != codes.Unavailable {
			t.Errorf("Create() = %v, want the daemon's status passed through", err)
		}
	})
}

// TestCreateLetsTheConfigurationTurnOffRoot checks that a spec can turn off a
// default Rungar sets for convenience, unlike the registration, which it
// cannot.
func TestCreateLetsTheConfigurationTurnOffRoot(t *testing.T) {
	p, d := newTestProvider(t)

	runner := testRunner()
	runner.Env = map[string]string{allowRootEnv: "0", types.JITConfigEnv: "somebody else's"}

	spec := testMachine("set-abcd1234", "set")
	spec.Runner = runner
	if err := p.Create(t.Context(), spec); err != nil {
		t.Fatalf("Create() = %v", err)
	}

	env := d.lastCreate(t).GetEnv()
	if env[allowRootEnv] != "0" {
		t.Errorf("%s = %q, want the configured value", allowRootEnv, env[allowRootEnv])
	}
	if env[types.JITConfigEnv] != "encoded-jit-config" {
		t.Errorf("%s = %q; a configuration must not break registration", types.JITConfigEnv, env[types.JITConfigEnv])
	}
}

func TestCreateRefusesAnotherTypesRunner(t *testing.T) {
	p, _ := newTestProvider(t)

	spec := testMachine("set-abcd1234", "set")
	spec.Runner = otherRunner{}
	if err := p.Create(t.Context(), spec); !errors.Is(err, errdefs.ErrInvalidArgument) {
		t.Errorf("Create() = %v, want an ErrInvalidArgument", err)
	}
}

// TestCreateSaysWhyItRefused checks that each refusal is put in its class: a
// full daemon is marked so, for the priority hold.
func TestCreateSaysWhyItRefused(t *testing.T) {
	for _, tt := range []struct {
		code  codes.Code
		class error
	}{
		{codes.ResourceExhausted, errdefs.ErrNoCapacity},
		{codes.FailedPrecondition, errdefs.ErrNoCapacity},
		{codes.InvalidArgument, errdefs.ErrInvalidArgument},
		{codes.NotFound, errdefs.ErrInvalidArgument},
	} {
		t.Run(tt.code.String(), func(t *testing.T) {
			p, d := newTestProvider(t)
			d.held = map[string]bool{testRunner().Image: true}
			d.createErr = status.Error(tt.code, "the daemon's reason")

			err := p.Create(t.Context(), testMachine("set-abcd1234", "set"))
			if !errors.Is(err, tt.class) {
				t.Errorf("Create() = %v, want it in %v", err, tt.class)
			}
			if err == nil || err.Error() != "the daemon's reason" {
				t.Errorf("Create() = %v, want the daemon's message alone", err)
			}
			if got := status.Code(err); got != tt.code {
				t.Errorf("Create() has code %v, want the daemon's %v kept", got, tt.code)
			}
		})
	}

	for _, code := range []codes.Code{codes.Unavailable, codes.Internal, codes.DeadlineExceeded} {
		t.Run(code.String(), func(t *testing.T) {
			p, d := newTestProvider(t)
			d.held = map[string]bool{testRunner().Image: true}
			d.createErr = status.Error(code, "down")

			err := p.Create(t.Context(), testMachine("set-abcd1234", "set"))
			if err == nil || errors.Is(err, errdefs.ErrNoCapacity) || errors.Is(err, errdefs.ErrInvalidArgument) {
				t.Fatalf("Create() = %v, want a failure in neither class", err)
			}
			if status.Code(err) != code {
				t.Errorf("Create() = %v, want the daemon's status passed through", err)
			}
		})
	}
}
