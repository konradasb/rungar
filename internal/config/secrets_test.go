// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package config

import (
	"slices"
	"testing"
)

func TestSecrets(t *testing.T) {
	tests := []struct {
		name   string
		github string
		tls    string
		want   func(config string) []string
	}{
		{
			name:   "a token in a file",
			github: "  url: https://github.com/org\n  token_path: /etc/rungar/token\n",
			want:   func(string) []string { return []string{"/etc/rungar/token"} },
		},
		{
			name: "an app's key in a file",
			github: "  url: https://github.com/org\n  app_client_id: Iv1.abc\n" +
				"  app_installation_id: 42\n  app_private_key_path: /etc/rungar/app.pem\n",
			want: func(string) []string { return []string{"/etc/rungar/app.pem"} },
		},
		{
			name:   "a token inline",
			github: "  url: https://github.com/org\n  token: ghp_secret\n",
			want:   func(config string) []string { return []string{config} },
		},
		{
			name: "an app's key inline",
			github: "  url: https://github.com/org\n  app_client_id: Iv1.abc\n" +
				"  app_installation_id: 42\n  app_private_key: x\n",
			want: func(config string) []string { return []string{config} },
		},
		{
			name:   "a provider's TLS key, and not its certificates",
			github: "  url: https://github.com/org\n  token_path: /etc/rungar/token\n",
			tls: "    tls:\n      ca_file: /etc/rungar/ca.pem\n" +
				"      cert_file: /etc/rungar/cert.pem\n      key_file: /etc/rungar/key.pem\n",
			want: func(string) []string { return []string{"/etc/rungar/token", "/etc/rungar/key.pem"} },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := "github:\n" + tt.github + `providers:
  - name: compute1
    type: dicer
    address: 10.10.0.101:7443
` + tt.tls + `scale_sets:
  - name: rungar-vm
    max_runners: 1
    providers:
      - name: compute1
        runner: {image: "ghcr.io/actions/actions-runner:latest", vcpus: 2, memory: 4GiB}
`
			path := writeConfig(t, body)

			config, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}

			if got, want := config.SecretFiles(), tt.want(path); !slices.Equal(got, want) {
				t.Errorf("Secrets() = %v, want %v", got, want)
			}
		})
	}
}
