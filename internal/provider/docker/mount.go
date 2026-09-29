// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package docker

import (
	"path/filepath"
	"strings"

	"github.com/konradasb/rungar/internal/errdefs"
)

// MountType is the kind of thing a Mount attaches.
type MountType string

const (
	// MountVolume is a named Docker volume on the host the runner lands on:
	// a build cache, say.
	MountVolume MountType = "volume"

	// MountTmpfs is an empty in-memory filesystem, gone with the runner.
	MountTmpfs MountType = "tmpfs"

	// MountBind is a file or directory of the host the runner lands on,
	// always read-only: a CA bundle, say.
	MountBind MountType = "bind"
)

// Mount is something attached to a runner's container at Target:
//
//	mounts:
//	  - type: volume
//	    source: build-cache
//	    target: /home/runner/.cache
//	  - type: bind
//	    source: /etc/ssl/certs/ca-certificates.crt
//	    target: /etc/ssl/certs/ca-certificates.crt
//
// Source is named as it is on the host, so it must exist on every host the
// runner may land on. Nothing that holds the Docker daemon's socket can be
// bound, since a job with the socket has root on the host.
type Mount struct {
	// Type is volume, tmpfs or bind.
	Type MountType `yaml:"type"`

	// Source is the volume's name, or the host path's absolute path. A
	// tmpfs has none.
	Source string `yaml:"source,omitempty"`

	// Target is the absolute path the mount appears at in the container.
	Target string `yaml:"target"`

	// ReadOnly mounts a volume read-only. A bind is always read-only.
	ReadOnly bool `yaml:"read_only,omitempty"`
}

// dockerRuns are where a Docker daemon keeps its socket on a host: the socket
// is <dir>/docker.sock, and its state is under <dir>/docker.
var dockerRuns = []string{"/var/run", "/run"}

// Validate checks the mount as far as can be done without asking a host.
func (m Mount) Validate() error {
	if !filepath.IsAbs(m.Target) {
		return errdefs.InvalidArgument("mount target %q must be an absolute path", m.Target)
	}

	switch m.Type {
	case MountVolume:
		if m.Source == "" {
			return errdefs.InvalidArgument("volume mount at %s needs a source: the volume's name", m.Target)
		}
	case MountTmpfs:
		if m.Source != "" {
			return errdefs.InvalidArgument("tmpfs mount at %s takes no source", m.Target)
		}
	case MountBind:
		return m.validateBind()
	default:
		return errdefs.InvalidArgument("mount at %s: unknown type %q; want volume, tmpfs or bind",
			m.Target, m.Type)
	}

	return nil
}

// validateBind checks a bind's source is an absolute path that is neither the
// Docker daemon's socket nor holds it, nor is under its state.
func (m Mount) validateBind() error {
	if !filepath.IsAbs(m.Source) {
		return errdefs.InvalidArgument("bind mount at %s needs an absolute source path, not %q",
			m.Target, m.Source)
	}

	source := filepath.Clean(m.Source)
	for _, dir := range dockerRuns {
		if within(dir+"/docker.sock", source) || strings.HasPrefix(source, dir+"/docker") {
			return errdefs.InvalidArgument("bind mount at %s: %s gives the Docker daemon's socket, "+
				"and with it root on the host, to every job", m.Target, m.Source)
		}
	}

	return nil
}

// within reports whether path is dir or under it.
func within(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, strings.TrimSuffix(dir, "/")+"/")
}

// apiMount is a mount as the Engine API takes it.
type apiMount struct {
	Type     string `json:"Type"`
	Source   string `json:"Source,omitempty"`
	Target   string `json:"Target"`
	ReadOnly bool   `json:"ReadOnly,omitempty"`
}

// api returns the mount as the Engine API takes it.
func (m Mount) api() apiMount {
	return apiMount{
		Type:     string(m.Type),
		Source:   m.Source,
		Target:   m.Target,
		ReadOnly: m.ReadOnly || m.Type == MountBind,
	}
}
