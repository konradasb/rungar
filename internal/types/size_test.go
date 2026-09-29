// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package types_test

import (
	"errors"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/types"
)

func TestSizeUnmarshal(t *testing.T) {
	tests := []struct {
		yaml string
		want types.Size
	}{
		// What a person writes.
		{"4GiB", 4 << 30},
		{"512MiB", 512 << 20},
		{"20GiB", 20 << 30},
		{"1TiB", 1 << 40},
		// Every unit is binary, GB as much as GiB: these are memory and
		// disk sizes, and that is the reading Dicer gives them too.
		{"1GB", 1 << 30},
		{"500MB", 500 << 20},
		// Shorthand, which go-units accepts.
		{"4g", 4 << 30},
		{"512m", 512 << 20},
		// A plain number is bytes, so nothing written that way stops working.
		{"4294967296", 4 << 30},
		{"0", 0},
	}

	for _, tt := range tests {
		t.Run(tt.yaml, func(t *testing.T) {
			var got types.Size
			if err := yaml.Unmarshal([]byte(tt.yaml), &got); err != nil {
				t.Fatalf("unmarshal %q = %v", tt.yaml, err)
			}
			if got != tt.want {
				t.Errorf("unmarshal %q = %d, want %d", tt.yaml, got, tt.want)
			}
		})
	}
}

func TestSizeUnmarshalRejects(t *testing.T) {
	tests := []string{
		"not-a-size",
		"10 parsecs",
		"-1",
		"-4GiB",
		"GiB",
	}

	for _, in := range tests {
		t.Run(in, func(t *testing.T) {
			var got types.Size

			err := yaml.Unmarshal([]byte(in), &got)
			if err == nil {
				t.Fatalf("unmarshal %q = %d, want an error", in, got)
			}
			if !errors.Is(err, errdefs.ErrInvalidArgument) {
				t.Errorf("unmarshal %q = %v, want an ErrInvalidArgument", in, err)
			}
		})
	}
}

func TestSizeString(t *testing.T) {
	tests := []struct {
		size types.Size
		want string
	}{
		{4 << 30, "4GiB"},
		{512 << 20, "512MiB"},
		{20 << 30, "20GiB"},
		{0, "0B"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.size.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSizeBytes(t *testing.T) {
	if got := (types.Size(4 << 30)).Bytes(); got != 4294967296 {
		t.Errorf("Bytes() = %d, want 4294967296", got)
	}
}
