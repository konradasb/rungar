// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package dicer_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/provider/dicer"
	"github.com/konradasb/rungar/internal/types"
)

func TestRunnerSpecResources(t *testing.T) {
	spec := dicer.RunnerSpec{VCPUs: 4, Memory: 8 * gib}

	want := types.Resources{VCPUs: 4, MemoryBytes: 8 * gib}
	if got := spec.Resources(); got != want {
		t.Errorf("Resources() = %v, want %v", got, want)
	}
	if got := spec.Describe(); got != "4 vCPU, 8 GiB" {
		t.Errorf("Describe() = %q, want 4 vCPU, 8 GiB", got)
	}
}

func TestRunnerSpecValidate(t *testing.T) {
	ok := func() dicer.RunnerSpec {
		return dicer.RunnerSpec{
			ImageRef: "ghcr.io/actions/actions-runner:latest",
			VCPUs:    2,
			Memory:   4 * gib,
			Disk:     20 * gib,
		}
	}

	tests := []struct {
		name string
		spec dicer.RunnerSpec
		says string
	}{
		{
			name: "no image",
			spec: func() dicer.RunnerSpec { s := ok(); s.ImageRef = ""; return s }(),
			says: "image",
		},
		{
			name: "no vCPUs",
			spec: func() dicer.RunnerSpec { s := ok(); s.VCPUs = 0; return s }(),
			says: "vCPU",
		},
		{
			name: "no memory",
			spec: func() dicer.RunnerSpec { s := ok(); s.Memory = 0; return s }(),
			says: "memory",
		},
		{
			name: "memory written without a unit, which is bytes",
			spec: func() dicer.RunnerSpec { s := ok(); s.Memory = 64; return s }(),
			says: "at least 128MiB",
		},
		{
			name: "memory the hypervisor cannot align",
			spec: func() dicer.RunnerSpec { s := ok(); s.Memory = 4*gib + 1; return s }(),
			says: "whole number of MiB",
		},
		{
			name: "a negative disk",
			spec: func() dicer.RunnerSpec { s := ok(); s.Disk = -1; return s }(),
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
	base := dicer.RunnerSpec{ImageRef: "runner:latest", VCPUs: 2, Memory: 4 * gib, Disk: 20 * gib}

	for name, ref := range map[string]string{
		"a digest that is not one":   "runner:latest@not-a-digest",
		"a digest of the wrong size": "runner@sha256:abc",
		"a digest that is not hex":   "runner@sha256:" + strings.Repeat("z", 64),
	} {
		t.Run(name, func(t *testing.T) {
			s := base
			s.ImageRef = ref

			err := s.Validate()
			if !errors.Is(err, errdefs.ErrInvalidArgument) || !strings.Contains(err.Error(), "invalid image digest") {
				t.Errorf("Validate() = %v, want an invalid image digest", err)
			}
		})
	}

	// A tag with a digest beside it is fine -- the tag says where the image
	// came from, the digest what it is -- as is no digest at all.
	for _, ref := range []string{"runner:latest@" + pinned, "runner@" + pinned, "runner:latest"} {
		s := base
		s.ImageRef = ref
		if err := s.Validate(); err != nil {
			t.Errorf("Validate() = %v for %s", err, ref)
		}
	}
}
