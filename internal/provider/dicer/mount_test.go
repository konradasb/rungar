// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package dicer

import (
	"errors"
	"testing"

	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"

	"github.com/konradasb/rungar/internal/errdefs"
)

func TestMountValidateChecksTypeSourceAndTarget(t *testing.T) {
	tests := []struct {
		name  string
		mount Mount
		ok    bool
	}{
		{name: "volume", mount: Mount{Type: MountVolume, Source: "cache", Target: "/cache"}, ok: true},
		{name: "file", mount: Mount{Type: MountFile, Source: "/etc/ca.pem", Target: "/etc/ca.pem"}, ok: true},
		{name: "tmpfs", mount: Mount{Type: MountTmpfs, Target: "/tmp"}, ok: true},
		{name: "relative target", mount: Mount{Type: MountTmpfs, Target: "tmp"}},
		{name: "volume without a name", mount: Mount{Type: MountVolume, Target: "/cache"}},
		{name: "file with a relative source", mount: Mount{Type: MountFile, Source: "ca.pem", Target: "/ca.pem"}},
		{name: "tmpfs with a source", mount: Mount{Type: MountTmpfs, Source: "x", Target: "/tmp"}},
		{name: "no type", mount: Mount{Source: "cache", Target: "/cache"}},
		{name: "unknown type", mount: Mount{Type: "bind", Source: "/x", Target: "/x"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.mount.Validate()
			if tt.ok && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
			if !tt.ok && !errors.Is(err, errdefs.ErrInvalidArgument) {
				t.Fatalf("Validate() = %v, want an ErrInvalidArgument", err)
			}
		})
	}
}

func TestMountProtoCarriesEverySetting(t *testing.T) {
	tests := []struct {
		mount Mount
		want  dicerdv1.MountType
	}{
		{Mount{Type: MountVolume}, dicerdv1.MountType_MOUNT_TYPE_VOLUME},
		{Mount{Type: MountFile}, dicerdv1.MountType_MOUNT_TYPE_FILE},
		{Mount{Type: MountTmpfs}, dicerdv1.MountType_MOUNT_TYPE_TMPFS},
	}

	for _, tt := range tests {
		t.Run(string(tt.mount.Type), func(t *testing.T) {
			if got := tt.mount.proto().GetType(); got != tt.want {
				t.Errorf("%s mount became %s, want %s", tt.mount.Type, got, tt.want)
			}
		})
	}

	m := Mount{Type: MountVolume, Source: "cache", Target: "/cache", ReadOnly: true}.proto()
	if m.GetSource() != "cache" || m.GetTarget() != "/cache" || !m.GetReadOnly() {
		t.Errorf("proto() = %v, want source, target and read-only carried over", m)
	}
}
