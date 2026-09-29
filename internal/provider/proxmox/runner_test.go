// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package proxmox

import (
	"errors"
	"testing"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/types"
)

func TestParseRunnerFillsInDefaults(t *testing.T) {
	c := &Config{}

	spec, err := c.ParseRunner(node(t, `{template: 9000, cores: 4, memory: 8GiB}`))
	if err != nil {
		t.Fatalf("ParseRunner() = %v", err)
	}

	r, ok := spec.(RunnerSpec)
	if !ok {
		t.Fatalf("ParseRunner() = %T, want a RunnerSpec", spec)
	}
	if r.JITPath != "/run/rungar/jitconfig" {
		t.Errorf("JITPath = %q, want the default", r.JITPath)
	}
	if got := spec.Describe(); got != "4 vCPU, 8 GiB" {
		t.Errorf("Describe() = %q, want 4 vCPU, 8 GiB", got)
	}
}

func TestValidateRefusesAnInvalidRunner(t *testing.T) {
	valid := func() RunnerSpec {
		return RunnerSpec{Template: 9000, Cores: 2, Memory: 4 << 30, JITPath: "/run/rungar/jitconfig"}
	}

	tests := []struct {
		name   string
		change func(s *RunnerSpec)
		ok     bool
	}{
		{name: "valid", change: func(*RunnerSpec) {}, ok: true},
		{name: "a full clone to storage", change: func(s *RunnerSpec) { s.FullClone, s.Storage = true, "ceph" }, ok: true},
		{name: "no template", change: func(s *RunnerSpec) { s.Template = 0 }},
		{name: "not a VMID", change: func(s *RunnerSpec) { s.Template = 99 }},
		{name: "no cores", change: func(s *RunnerSpec) { s.Cores = 0 }},
		{name: "no memory", change: func(s *RunnerSpec) { s.Memory = 0 }},
		{name: "too little memory", change: func(s *RunnerSpec) { s.Memory = 64 << 20 }},
		{name: "memory not in MiB", change: func(s *RunnerSpec) { s.Memory = 4<<30 + 1 }},
		{name: "storage for a linked clone", change: func(s *RunnerSpec) { s.Storage = "ceph" }},
		{name: "a relative jit_path", change: func(s *RunnerSpec) { s.JITPath = "run/jit" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := valid()
			tt.change(&s)

			err := s.Validate()
			switch {
			case tt.ok && err != nil:
				t.Errorf("Validate() = %v, want nil", err)
			case !tt.ok && !errors.Is(err, errdefs.ErrInvalidArgument):
				t.Errorf("Validate() = %v, want invalid", err)
			}
		})
	}
}

// TestRunnerRevisionIsStable checks a spec's revision, a hash of its JSON,
// does not change with field order or defaults the block did not write.
func TestRunnerRevisionIsStable(t *testing.T) {
	c := &Config{}

	a, err := c.ParseRunner(node(t, `{template: 9000, cores: 4, memory: 8GiB}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.ParseRunner(node(t, `{memory: 8GiB, cores: 4, template: 9000, jit_path: /run/rungar/jitconfig}`))
	if err != nil {
		t.Fatal(err)
	}

	ra, err := types.RunnerRevision(a)
	if err != nil {
		t.Fatal(err)
	}
	rb, err := types.RunnerRevision(b)
	if err != nil {
		t.Fatal(err)
	}
	if ra != rb {
		t.Errorf("revisions %s and %s differ for the same runner", ra, rb)
	}
}
