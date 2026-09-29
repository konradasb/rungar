// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package config

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// everyType is a configuration with a provider of every type, anchors, merge
// keys and inline secrets.
const everyType = `
github:
  url: https://github.com/my-org
  token: ghp_inline_secret
providers:
  - &dicer
    name: compute1
    type: dicer
    address: 10.10.0.101:7443
    runner:
      image: ghcr.io/actions/actions-runner:latest
  - <<: *dicer
    name: compute2
    address: 10.10.0.102:7443
    max_runners: 4
  - name: build1
    type: docker
    address: unix:///var/run/docker.sock
    runner: {image: ghcr.io/actions/actions-runner:latest}
  - name: pve
    type: proxmox
    url: https://pve.example.com:8006
    token_id: rungar@pve!rungar
    token_secret: pve_inline_secret
    runner: {template: 9000}
  - name: gcp
    type: gcp
    project: my-ci-project
    zones: [europe-west1-b]
    service_account: runner@my-ci-project.iam.gserviceaccount.com
    runner: {image: runner-image}
  - name: aws
    type: aws
    region: eu-west-1
    subnets: [subnet-0a1b2c3d]
    disabled: true
    runner: {image: ami-0a1b2c3d4e5f60718}
scale_sets:
  - name: rungar-c4-m8
    max_runners: 8
    providers:
      - name: compute1
        runner: &c4-m8 {vcpus: 4, memory: 8GiB} # a comment
      - name: compute2
        runner: *c4-m8
      - build1
      - name: pve
        runner: {cores: 4, memory: 8GiB}
      - name: gcp
        runner: {machine_type: e2-custom-4-8192}
      - name: aws
        runner: {instance_type: m7i.xlarge}
`

func TestYAMLLoadsAsTheSameConfiguration(t *testing.T) {
	config, err := Load(writeConfig(t, everyType))
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}

	b, err := config.YAML()
	if err != nil {
		t.Fatalf("YAML() = %v", err)
	}
	out := string(b)

	for _, absent := range []string{"ghp_inline_secret", "pve_inline_secret", "&", "*c4-m8", "<<", "# a comment"} {
		if strings.Contains(out, absent) {
			t.Errorf("YAML() has %q:\n%s", absent, out)
		}
	}
	for _, present := range []string{
		"token: " + redacted,
		"token_secret: " + redacted,
		"installation: gh-",
		"start_timeout: 5m0s",
		"scopes:\n      - https://www.googleapis.com/auth/cloud-platform",
		"max_runners: 4",
		"disabled: true",
	} {
		if !strings.Contains(out, present) {
			t.Errorf("YAML() lacks %q:\n%s", present, out)
		}
	}

	// Printed with its secrets back, it loads as the configuration it came
	// from: every runner the same.
	out = strings.Replace(out, "token: "+redacted, "token: ghp_inline_secret", 1)
	out = strings.Replace(out, "token_secret: "+redacted, "token_secret: pve_inline_secret", 1)

	again, err := Load(writeConfig(t, out))
	if err != nil {
		t.Fatalf("Load() of YAML() = %v\n%s", err, out)
	}

	for i, set := range config.ScaleSets {
		if got := again.ScaleSets[i].RunnerRevisions; !maps.Equal(got, set.RunnerRevisions) {
			t.Errorf("scale set %s: runner revisions %v, want %v", set.Name, got, set.RunnerRevisions)
		}
	}
	for i, p := range config.Providers {
		if got, want := again.Providers[i].settings.Endpoint(), p.settings.Endpoint(); got != want {
			t.Errorf("provider %s: endpoint %q, want %q", p.Name, got, want)
		}
	}
}

func TestYAMLOfTheExample(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "example.yml"))
	if err != nil {
		t.Fatal(err)
	}

	config, err := Load(writeConfig(t, string(body)))
	if err != nil {
		t.Fatal(err)
	}

	b, err := config.YAML()
	if err != nil {
		t.Fatalf("YAML() = %v", err)
	}
	if _, err := Load(writeConfig(t, string(b))); err != nil {
		t.Errorf("Load() of YAML() = %v\n%s", err, b)
	}
}
