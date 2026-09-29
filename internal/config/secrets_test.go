// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package config

import (
	"slices"
	"testing"
)

// TestSecretFilesNameEveryCredentialFile checks SecretFiles names each file
// holding a secret, and the configuration itself when a secret is inline.
func TestSecretFilesNameEveryCredentialFile(t *testing.T) {
	const dicer = "    type: dicer\n    address: 10.10.0.101:7443\n" +
		"    runner: {image: \"ghcr.io/actions/actions-runner:latest\", vcpus: 2, memory: 4GiB}\n"

	tests := []struct {
		name     string
		github   string
		provider string
		want     func(config string) []string
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
			provider: dicer + "    tls:\n      ca_file: /etc/rungar/ca.pem\n" +
				"      cert_file: /etc/rungar/cert.pem\n      key_file: /etc/rungar/key.pem\n",
			want: func(string) []string { return []string{"/etc/rungar/token", "/etc/rungar/key.pem"} },
		},
		{
			name:   "a provider's secret in a file",
			github: "  url: https://github.com/org\n  token_path: /etc/rungar/token\n",
			provider: "    type: proxmox\n    url: https://pve.example.com:8006\n" +
				"    token_id: rungar@pve!rungar\n    token_secret_path: /etc/rungar/pve-token\n" +
				"    runner: {template: 9000, cores: 2, memory: 4GiB}\n",
			want: func(string) []string { return []string{"/etc/rungar/token", "/etc/rungar/pve-token"} },
		},
		{
			name:   "a provider's secret inline",
			github: "  url: https://github.com/org\n  token_path: /etc/rungar/token\n",
			provider: "    type: proxmox\n    url: https://pve.example.com:8006\n" +
				"    token_id: rungar@pve!rungar\n    token_secret: pve_inline_secret\n" +
				"    runner: {template: 9000, cores: 2, memory: 4GiB}\n",
			want: func(config string) []string { return []string{"/etc/rungar/token", config} },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := tt.provider
			if provider == "" {
				provider = dicer
			}
			body := "github:\n" + tt.github + "providers:\n  - name: compute1\n" + provider + `scale_sets:
  - name: rungar-vm
    max_runners: 1
    providers:
      - name: compute1
`
			path := writeConfig(t, body)

			cfg, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}

			if got, want := cfg.SecretFiles(), tt.want(path); !slices.Equal(got, want) {
				t.Errorf("SecretFiles() = %v, want %v", got, want)
			}
		})
	}
}
