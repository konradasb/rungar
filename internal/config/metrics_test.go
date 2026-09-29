// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package config

import "testing"

// TestMetricsListenIsCheckedOnlyWhenEnabled checks metrics.listen is
// validated only when metrics are served.
func TestMetricsListenIsCheckedOnlyWhenEnabled(t *testing.T) {
	tests := []struct {
		name string
		body string
		ok   bool
	}{
		{
			name: "off by default, so its address is not checked",
			body: valid + "metrics:\n  listen: nonsense\n",
			ok:   true,
		},
		{
			name: "on, with a good address",
			body: valid + "metrics:\n  enable: true\n  listen: 127.0.0.1:9102\n",
			ok:   true,
		},
		{
			name: "on, with a bad address",
			body: valid + "metrics:\n  enable: true\n  listen: nonsense\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tt.body))
			if tt.ok && err != nil {
				t.Fatalf("Load() = %v, want nil", err)
			}
			if !tt.ok && err == nil {
				t.Fatal("Load() = nil, want an error")
			}
		})
	}
}
