// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package dicer

import (
	"errors"
	"strings"
	"testing"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/types"
)

func TestRunnerSpecResourcesAreItsVCPUsAndMemory(t *testing.T) {
	spec := RunnerSpec{VCPUs: 4, Memory: 8 * gib}

	want := types.Resources{VCPUs: 4, Memory: 8 * gib}
	if got := spec.resources(); got != want {
		t.Errorf("resources() = %v, want %v", got, want)
	}
	if got := spec.Describe(); got != "4 vCPU, 8 GiB" {
		t.Errorf("Describe() = %q, want 4 vCPU, 8 GiB", got)
	}
}

func TestRunnerSpecValidateRefusesWhatDicerCannotCreate(t *testing.T) {
	ok := func() RunnerSpec {
		return RunnerSpec{
			Image:  "ghcr.io/actions/actions-runner:latest",
			VCPUs:  2,
			Memory: 4 * gib,
			Disk:   20 * gib,
		}
	}

	tests := []struct {
		name string
		spec RunnerSpec
		says string
	}{
		{
			name: "no image",
			spec: func() RunnerSpec { s := ok(); s.Image = ""; return s }(),
			says: "image",
		},
		{
			name: "no vCPUs",
			spec: func() RunnerSpec { s := ok(); s.VCPUs = 0; return s }(),
			says: "vCPU",
		},
		{
			name: "no memory",
			spec: func() RunnerSpec { s := ok(); s.Memory = 0; return s }(),
			says: "memory",
		},
		{
			name: "memory written without a unit, which is bytes",
			spec: func() RunnerSpec { s := ok(); s.Memory = 64; return s }(),
			says: "at least 128MiB",
		},
		{
			name: "memory the hypervisor cannot align",
			spec: func() RunnerSpec { s := ok(); s.Memory = 4*gib + 1; return s }(),
			says: "whole number of MiB",
		},
		{
			name: "a negative disk",
			spec: func() RunnerSpec { s := ok(); s.Disk = -1; return s }(),
			says: "disk",
		},
	}

	if err := ok().Validate(); err != nil {
		t.Fatalf("a valid spec was refused: %v", err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.spec.Validate()
			if err == nil {
				t.Fatal("Validate() = nil, want an error")
			}
			if !errors.Is(err, errdefs.ErrInvalidArgument) {
				t.Errorf("Validate() = %v, want an ErrInvalidArgument", err)
			}
			if !strings.Contains(err.Error(), tt.says) {
				t.Errorf("Validate() said %q, want it to mention %q", err, tt.says)
			}
		})
	}
}

// pinned is a digest of the right shape, for tests about pinning.
const pinned = "sha256:5036480998280bb21e32ade9fe1b02b493861ac314b62ba1aea320b94f56ec97"

func TestRunnerSpecValidateRejectsBadDigest(t *testing.T) {
	base := RunnerSpec{Image: "runner:latest", VCPUs: 2, Memory: 4 * gib, Disk: 20 * gib}

	for name, ref := range map[string]string{
		"a digest that is not one":   "runner:latest@not-a-digest",
		"a digest of the wrong size": "runner@sha256:abc",
		"a digest that is not hex":   "runner@sha256:" + strings.Repeat("z", 64),
	} {
		t.Run(name, func(t *testing.T) {
			s := base
			s.Image = ref

			err := s.Validate()
			if !errors.Is(err, errdefs.ErrInvalidArgument) || !strings.Contains(err.Error(), "invalid image digest") {
				t.Errorf("Validate() = %v, want an invalid image digest", err)
			}
		})
	}

	// A tag with a digest beside it is fine -- the tag says where the image
	// came from, the digest what it is -- as is no digest at all.
	for _, ref := range []string{"runner:latest@" + pinned, "runner@" + pinned, "runner:latest"} {
		t.Run(ref, func(t *testing.T) {
			s := base
			s.Image = ref
			if err := s.Validate(); err != nil {
				t.Errorf("Validate() = %v", err)
			}
		})
	}
}

// TestRunnerRevisionIsAsBefore checks a runner's revision is what it was
// before the spec's fields were renamed, so an upgrade does not replace every
// idle runner.
func TestRunnerRevisionIsAsBefore(t *testing.T) {
	r := testRunner()
	r.Kernel, r.Network = "vmlinux-6.12", "isolated"

	got, err := types.RunnerRevision(r)
	if err != nil {
		t.Fatal(err)
	}
	if want := "dae34f7f0a68"; got != want {
		t.Errorf("revision %s, want %s", got, want)
	}
}

// TestRunnerSpecValidatesMounts checks a spec that is otherwise valid is
// refused for a bad mount.
func TestRunnerSpecValidatesMounts(t *testing.T) {
	spec := RunnerSpec{
		Image:  "runner",
		VCPUs:  2,
		Memory: 4 * gib,
		Disk:   20 * gib,
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("Validate() without mounts = %v, want nil", err)
	}

	spec.Mounts = []Mount{{Type: MountVolume, Target: "/cache"}}
	err := spec.Validate()
	if !errors.Is(err, errdefs.ErrInvalidArgument) || !strings.Contains(err.Error(), "/cache") {
		t.Errorf("Validate() = %v, want the mount at /cache refused", err)
	}
}

// addressOnly is a dicer provider's entry that sets only what it must.
const addressOnly = "address: 10.0.0.1:7443\n"

// TestParseRunnerReadsTheBlock checks each setting of a runner block is
// read, and the runner described by its size.
func TestParseRunnerReadsTheBlock(t *testing.T) {
	spec, err := configure(t, addressOnly).ParseRunner(node(t, `
image: ghcr.io/actions/actions-runner:latest
kernel: vmlinux-6.12
vcpus: 4
memory: 8GiB
disk: 30GiB
`))
	if err != nil {
		t.Fatalf("ParseRunner() = %v", err)
	}

	runner, ok := spec.(RunnerSpec)
	if !ok {
		t.Fatalf("ParseRunner() = %T, want a dicer runner", spec)
	}
	if runner.Image != "ghcr.io/actions/actions-runner:latest" || runner.Kernel != "vmlinux-6.12" {
		t.Errorf("runner = %+v, want the image and kernel", runner)
	}
	if runner.Disk != 30<<30 {
		t.Errorf("disk = %s, want 30GiB", runner.Disk)
	}
	if got := runner.resources(); got != (types.Resources{VCPUs: 4, Memory: 8 << 30}) {
		t.Errorf("resources() = %s, want 4 vCPU, 8 GiB", got)
	}
	if got := spec.Describe(); got != "4 vCPU, 8 GiB" {
		t.Errorf("Describe() = %q, want 4 vCPU, 8 GiB", got)
	}
}

func TestParseRunnerRefuses(t *testing.T) {
	config := configure(t, addressOnly)

	for name, body := range map[string]string{
		"nothing at all":         "",
		"no size":                "image: runner\n",
		"a misspelt setting":     "image: runner\nvcpus: 2\nmemory: 4GiB\nkernal: x\n",
		"a size that is not one": "image: runner\nvcpus: 2\nmemory: lots\n",
		"a digest that is not":   "image: runner@sha256:nope\nvcpus: 2\nmemory: 4GiB\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := config.ParseRunner(node(t, body)); !errors.Is(err, errdefs.ErrInvalidArgument) {
				t.Errorf("ParseRunner() = %v, want an ErrInvalidArgument", err)
			}
		})
	}
}

// TestParseRunnerFillsInDefaults checks what a runner block that says only
// what it must is given: the runner image's start script, and a 20GiB disk,
// since Dicer has no default disk and refuses an instance without one.
func TestParseRunnerFillsInDefaults(t *testing.T) {
	spec, err := configure(t, addressOnly).ParseRunner(node(t, `
image: ghcr.io/actions/actions-runner:latest
vcpus: 2
memory: 4GiB
`))
	if err != nil {
		t.Fatalf("ParseRunner() = %v", err)
	}

	runner, ok := spec.(RunnerSpec)
	if !ok {
		t.Fatalf("ParseRunner() = %T, want a dicer runner", spec)
	}
	if runner.Disk != 20<<30 || len(runner.Command) != 1 || runner.Command[0] != "/home/runner/run.sh" {
		t.Errorf("runner = %+v, want a 20GiB disk and the runner image's start script", runner)
	}
}
