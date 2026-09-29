// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package docker

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/konradasb/rungar/internal/errdefs"
)

func TestParseRunnerDefaults(t *testing.T) {
	spec := runner(t, "image: ghcr.io/actions/actions-runner:latest")

	if !slices.Equal(spec.Command, []string{"/home/runner/run.sh"}) {
		t.Errorf("Command = %v, want the runner image's start script", spec.Command)
	}
	if spec.Pull != PullMissing {
		t.Errorf("Pull = %q, want missing", spec.Pull)
	}
}

func TestParseRunnerRefuses(t *testing.T) {
	tests := []struct {
		name, doc, want string
	}{
		{"no image", "cpus: 2", "needs an image"},
		{"negative cpus", "image: r\ncpus: -1", "cannot be negative"},
		{"too few cpus", "image: r\ncpus: 0.001", "too few"},
		{"too little memory", "image: r\nmemory: 64MiB", "too little"},
		{"memory in bytes", "image: r\nmemory: 4096", "too little"},
		{"unknown pull", "image: r\npull: sometimes", "want missing or always"},
		{"bad env", "image: r\nenv: {\"A=B\": x}", "invalid variable name"},
		{"bad digest", "image: r@sha256:nothex", "invalid image digest"},
		{"privileged", "image: r\nprivileged: true", "field privileged not found"},
		{"relative target", "image: r\nmounts: [{type: tmpfs, target: tmp}]", "must be an absolute path"},
		{"unknown mount", "image: r\nmounts: [{type: file, source: /a, target: /a}]", "unknown type"},
		{"volume without source", "image: r\nmounts: [{type: volume, target: /c}]", "needs a source"},
		{"tmpfs with source", "image: r\nmounts: [{type: tmpfs, source: x, target: /t}]", "takes no source"},
		{"relative bind", "image: r\nmounts: [{type: bind, source: certs, target: /c}]", "absolute source path"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := (&Config{}).ParseRunner(node(t, tt.doc))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ParseRunner() = %v, want an error saying %q", err, tt.want)
			}
			if !errors.Is(err, errdefs.ErrInvalidArgument) {
				t.Errorf("ParseRunner() = %v, want invalid argument", err)
			}
		})
	}
}

// TestBindRefusesTheDockerSocket checks no bind can hand a job the daemon's
// socket: not the socket, not a directory holding it, not the daemon's state.
func TestBindRefusesTheDockerSocket(t *testing.T) {
	refused := []string{
		"/var/run/docker.sock", "/run/docker.sock", "/var/run", "/run", "/var", "/",
		"/var/run/docker", "/run/docker/containerd/containerd.sock", "/run/../run/docker.sock",
	}
	for _, source := range refused {
		m := Mount{Type: MountBind, Source: source, Target: "/mnt"}
		if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "Docker daemon's socket") {
			t.Errorf("bind of %s = %v, want it refused", source, err)
		}
	}

	allowed := []string{"/etc/ssl/certs", "/etc/ssl/certs/ca-certificates.crt", "/var/cache/runner", "/runner"}
	for _, source := range allowed {
		m := Mount{Type: MountBind, Source: source, Target: "/mnt"}
		if err := m.Validate(); err != nil {
			t.Errorf("bind of %s = %v, want it allowed", source, err)
		}
	}
}

func TestBindIsAlwaysReadOnly(t *testing.T) {
	m := Mount{Type: MountBind, Source: "/etc/ssl/certs", Target: "/etc/ssl/certs"}
	if !m.api().ReadOnly {
		t.Error("a bind is mounted read-write")
	}

	v := Mount{Type: MountVolume, Source: "cache", Target: "/cache"}
	if v.api().ReadOnly {
		t.Error("a volume not asked to be read-only is mounted read-only")
	}
}

func TestDescribe(t *testing.T) {
	tests := []struct{ doc, want string }{
		{"image: r\ncpus: 2\nmemory: 4GiB", "2 CPUs, 4 GiB"},
		{"image: r\ncpus: 1", "1 CPU"},
		{"image: r\ncpus: 0.5", "0.5 CPUs"},
		{"image: r\nmemory: 512MiB", "512 MiB"},
		{"image: r", "no limits"},
	}

	for _, tt := range tests {
		if got := runner(t, tt.doc).Describe(); got != tt.want {
			t.Errorf("Describe() of %q = %q, want %q", tt.doc, got, tt.want)
		}
	}
}

func TestSplitImageRef(t *testing.T) {
	tests := []struct{ ref, repo, tag string }{
		{"alpine", "alpine", "latest"},
		{"alpine:3.20", "alpine", "3.20"},
		{"ghcr.io/actions/actions-runner:2.321.0", "ghcr.io/actions/actions-runner", "2.321.0"},
		{"registry.local:5000/runner", "registry.local:5000/runner", "latest"},
		{"registry.local:5000/runner:v1", "registry.local:5000/runner", "v1"},
		{"runner:latest@sha256:abc", "runner:latest", "sha256:abc"},
	}

	for _, tt := range tests {
		if repo, tag := splitImageRef(tt.ref); repo != tt.repo || tag != tt.tag {
			t.Errorf("splitImageRef(%q) = %q, %q; want %q, %q", tt.ref, repo, tag, tt.repo, tt.tag)
		}
	}
}
