// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package types_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/types"
)

func TestRunnerNameIsShortenedToFitALabel(t *testing.T) {
	tests := []struct {
		name     string
		scaleSet string
		suffix   string
		want     string
	}{
		{"short name is kept", "rungar-vm", "abcd1234", "rungar-vm-abcd1234"},
		{
			"long name is shortened to fit a label",
			strings.Repeat("a", 80), "abcd1234",
			strings.Repeat("a", 54) + "-abcd1234",
		},
		{
			"hyphens left at the cut are trimmed",
			strings.Repeat("a", 53) + "--" + strings.Repeat("b", 20), "abcd1234",
			strings.Repeat("a", 53) + "-abcd1234",
		},
		{
			"dots left at the cut are trimmed",
			strings.Repeat("a", 53) + ".b" + strings.Repeat("c", 20), "abcd1234",
			strings.Repeat("a", 53) + "-abcd1234",
		},
		{"dotted name is kept", "ci.example", "abcd1234", "ci.example-abcd1234"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := types.RunnerName(tt.scaleSet, tt.suffix)
			if err != nil {
				t.Fatalf("RunnerName() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("RunnerName() = %q, want %q", got, tt.want)
			}
			if !types.ValidRunnerName(got) {
				t.Errorf("ValidRunnerName(%q) = false, want true", got)
			}
		})
	}
}

func TestRunnerNameRefusesInvalidScaleSet(t *testing.T) {
	tests := []struct {
		name     string
		scaleSet string
	}{
		{"underscore", "rungar_probe"},
		{"space", "rungar vm"},
		{"leading hyphen", "-rungar"},
		{"empty label", "rungar..vm"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := types.RunnerName(tt.scaleSet, "abcd1234")
			if !errors.Is(err, errdefs.ErrInvalidArgument) {
				t.Errorf("RunnerName() error = %v, want an ErrInvalidArgument", err)
			}
		})
	}
}

func TestValidRunnerNameIsADNSSubdomain(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"simple", "rungar-vm-abcd1234", true},
		{"upper case", "Rungar-VM", true},
		{"dotted", "ci.example.com", true},
		{"single character", "a", true},
		{"label of 63", strings.Repeat("a", 63), true},
		{"label of 64", strings.Repeat("a", 64), false},
		{"subdomain of 253", strings.Repeat(strings.Repeat("a", 63)+".", 3) + strings.Repeat("a", 61), true},
		{"subdomain of 254", strings.Repeat(strings.Repeat("a", 63)+".", 3) + strings.Repeat("a", 62), false},
		{"empty", "", false},
		{"leading hyphen", "-a", false},
		{"trailing hyphen", "a-", false},
		{"trailing dot", "a.", false},
		{"underscore", "a_b", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := types.ValidRunnerName(tt.input); got != tt.want {
				t.Errorf("ValidRunnerName(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
