// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package aws

import "testing"

func TestParseLaunchTemplateRefTakesAnIDOrNameAndVersion(t *testing.T) {
	tests := []struct {
		in   string
		want launchTemplateRef
		ok   bool
	}{
		{"runner-gpu", launchTemplateRef{name: "runner-gpu"}, true},
		{"runner-gpu:3", launchTemplateRef{name: "runner-gpu", version: "3"}, true},
		{"runner-gpu:$Latest", launchTemplateRef{name: "runner-gpu", version: "$Latest"}, true},
		{"ci/runner_(gpu).v2", launchTemplateRef{name: "ci/runner_(gpu).v2"}, true},
		{"lt-0a1b2c3d4e5f60718", launchTemplateRef{id: "lt-0a1b2c3d4e5f60718"}, true},
		{"lt-0a1b2c3d4e5f60718:$Default", launchTemplateRef{id: "lt-0a1b2c3d4e5f60718", version: "$Default"}, true},
		{"rg", launchTemplateRef{}, false},
		{"runner gpu", launchTemplateRef{}, false},
		{"runner-gpu:", launchTemplateRef{}, false},
		{"runner-gpu:0", launchTemplateRef{}, false},
		{"runner-gpu:latest", launchTemplateRef{}, false},
		{"runner-gpu:3:4", launchTemplateRef{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, ok := parseLaunchTemplateRef(tt.in)
			if got != tt.want || ok != tt.ok {
				t.Errorf("parseLaunchTemplateRef(%q) = %+v, %v, want %+v, %v", tt.in, got, ok, tt.want, tt.ok)
			}
		})
	}
}
