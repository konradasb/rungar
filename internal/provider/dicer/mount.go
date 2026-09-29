// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package dicer

import (
	"path/filepath"

	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"

	"github.com/konradasb/rungar/internal/errdefs"
)

// MountType is the kind of thing a Mount attaches.
type MountType string

const (
	// MountVolume is a named Dicer volume on the host the runner lands on:
	// a build cache, say. Only a volume every instance mounts read-only
	// can be shared by several runners at once.
	MountVolume MountType = "volume"

	// MountFile is a copy of a file on the host the runner lands on, read
	// when the runner starts: a CA bundle, say.
	MountFile MountType = "file"

	// MountTmpfs is an empty in-memory filesystem, gone with the runner.
	MountTmpfs MountType = "tmpfs"
)

// Mount is something attached to a runner's VM at Target:
//
//	mounts:
//	  - type: volume
//	    source: build-cache
//	    target: /home/runner/.cache
//	  - type: file
//	    source: /etc/ssl/certs/ca-certificates.crt
//	    target: /etc/ssl/certs/ca-certificates.crt
//
// Source is named as it is on the host, so it must exist on every host the
// runner may land on.
type Mount struct {
	// Type is volume, file or tmpfs.
	Type MountType `yaml:"type"`

	// Source is the volume's name, or the host file's absolute path. A
	// tmpfs has none.
	Source string `yaml:"source,omitempty"`

	// Target is the absolute path the mount appears at in the VM.
	Target string `yaml:"target"`

	// ReadOnly mounts it read-only. A volume several runners mount at once
	// must be mounted read-only by every one of them.
	ReadOnly bool `yaml:"read_only,omitempty"`
}

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
	case MountFile:
		if !filepath.IsAbs(m.Source) {
			return errdefs.InvalidArgument("file mount at %s needs an absolute source path, not %q",
				m.Target, m.Source)
		}
	case MountTmpfs:
		if m.Source != "" {
			return errdefs.InvalidArgument("tmpfs mount at %s takes no source", m.Target)
		}
	default:
		return errdefs.InvalidArgument("mount at %s: unknown type %q; want volume, file or tmpfs",
			m.Target, m.Type)
	}

	return nil
}

// Proto returns the mount as the Dicer API takes it.
func (m Mount) Proto() *dicerdv1.Mount {
	t := dicerdv1.MountType_MOUNT_TYPE_UNSPECIFIED

	switch m.Type {
	case MountVolume:
		t = dicerdv1.MountType_MOUNT_TYPE_VOLUME
	case MountFile:
		t = dicerdv1.MountType_MOUNT_TYPE_FILE
	case MountTmpfs:
		t = dicerdv1.MountType_MOUNT_TYPE_TMPFS
	}

	return &dicerdv1.Mount{Type: t, Source: m.Source, Target: m.Target, ReadOnly: m.ReadOnly}
}
