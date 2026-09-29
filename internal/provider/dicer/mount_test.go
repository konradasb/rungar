// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package dicer_test

import (
	"errors"
	"testing"

	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/provider/dicer"
)

func TestMountValidate(t *testing.T) {
	tests := []struct {
		name  string
		mount dicer.Mount
		ok    bool
	}{
		{name: "volume", mount: dicer.Mount{Type: dicer.MountVolume, Source: "cache", Target: "/cache"}, ok: true},
		{name: "file", mount: dicer.Mount{Type: dicer.MountFile, Source: "/etc/ca.pem", Target: "/etc/ca.pem"}, ok: true},
		{name: "tmpfs", mount: dicer.Mount{Type: dicer.MountTmpfs, Target: "/tmp"}, ok: true},
		{name: "relative target", mount: dicer.Mount{Type: dicer.MountTmpfs, Target: "tmp"}},
		{name: "volume without a name", mount: dicer.Mount{Type: dicer.MountVolume, Target: "/cache"}},
		{name: "file with a relative source", mount: dicer.Mount{Type: dicer.MountFile, Source: "ca.pem", Target: "/ca.pem"}},
		{name: "tmpfs with a source", mount: dicer.Mount{Type: dicer.MountTmpfs, Source: "x", Target: "/tmp"}},
		{name: "no type", mount: dicer.Mount{Source: "cache", Target: "/cache"}},
		{name: "unknown type", mount: dicer.Mount{Type: "bind", Source: "/x", Target: "/x"}},
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

func TestMountProto(t *testing.T) {
	tests := []struct {
		mount dicer.Mount
		want  dicerdv1.MountType
	}{
		{dicer.Mount{Type: dicer.MountVolume}, dicerdv1.MountType_MOUNT_TYPE_VOLUME},
		{dicer.Mount{Type: dicer.MountFile}, dicerdv1.MountType_MOUNT_TYPE_FILE},
		{dicer.Mount{Type: dicer.MountTmpfs}, dicerdv1.MountType_MOUNT_TYPE_TMPFS},
	}

	for _, tt := range tests {
		if got := tt.mount.Proto().GetType(); got != tt.want {
			t.Errorf("%s mount became %s, want %s", tt.mount.Type, got, tt.want)
		}
	}

	m := dicer.Mount{Type: dicer.MountVolume, Source: "cache", Target: "/cache", ReadOnly: true}.Proto()
	if m.GetSource() != "cache" || m.GetTarget() != "/cache" || !m.GetReadOnly() {
		t.Errorf("Proto() = %v, want source, target and read-only carried over", m)
	}
}

func TestRunnerSpecValidatesMounts(t *testing.T) {
	spec := dicer.RunnerSpec{
		ImageRef: "runner",
		VCPUs:    2,
		Memory:   4 * gib,
		Mounts:   []dicer.Mount{{Type: dicer.MountVolume, Target: "/cache"}},
	}

	if err := spec.Validate(); !errors.Is(err, errdefs.ErrInvalidArgument) {
		t.Errorf("Validate() = %v, want the bad mount refused", err)
	}
}
