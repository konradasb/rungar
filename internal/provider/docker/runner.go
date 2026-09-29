// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package docker

import (
	"strconv"
	"strings"

	"github.com/docker/go-units"
	"github.com/opencontainers/go-digest"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/types"
)

// RunnerSpec is a docker runner block: the container a runner gets on a Docker
// host. The runner's name, labels and registration are Rungar's, not the
// block's. The container is never privileged.
type RunnerSpec struct {
	// ImageRef is the image the container runs, which must carry the
	// Actions runner, as ghcr.io/actions/actions-runner does. Pinning it to
	// a digest, runner:latest@sha256:..., has every runner run exactly that
	// image. It is required.
	ImageRef string `yaml:"image"`

	// Command is what the container runs. Unset is the runner image's start
	// script, /home/runner/run.sh.
	Command []string `yaml:"command,omitempty"`

	// CPUs is how many of the host's CPUs the container may use: 2, or 0.5.
	// Unset is no limit.
	CPUs float64 `yaml:"cpus,omitempty"`

	// Memory is the most memory the container may use, written as a size:
	// 4GiB. A plain number is bytes. It is at least 128MiB. Unset is no
	// limit.
	Memory types.Size `yaml:"memory,omitempty"`

	// Network is the Docker network the container joins. Unset is the
	// daemon's default bridge. A network named must exist on every host the
	// runner may land on.
	Network string `yaml:"network,omitempty"`

	// Env is added to the runner's environment. The variable that registers
	// the runner with GitHub overrides anything set here.
	Env map[string]string `yaml:"env,omitempty"`

	// Mounts are attached to the container: a named volume, a tmpfs, or a
	// host file or directory, read-only.
	Mounts []Mount `yaml:"mounts,omitempty"`

	// Pull is when the image is pulled: missing, the default, pulls it only
	// if the host does not have it; always pulls it for every runner, so a
	// moved tag is followed.
	Pull PullPolicy `yaml:"pull,omitempty"`
}

// PullPolicy is when a runner's image is pulled.
type PullPolicy string

const (
	// PullMissing pulls an image only if the host does not have it.
	PullMissing PullPolicy = "missing"

	// PullAlways pulls the image for every runner.
	PullAlways PullPolicy = "always"
)

const (
	// defaultCommand is the start script of the official runner image.
	defaultCommand = "/home/runner/run.sh"

	// minMemory is the least memory limit a runner is given.
	minMemory types.Size = 128 << 20

	// minCPUs is the smallest CPU limit Docker takes.
	minCPUs = 0.01
)

// binarySizeUnits are the units memory is described in.
var binarySizeUnits = []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}

// Describe returns the runner's limits: "2 CPUs, 4 GiB", or "no limits".
func (s RunnerSpec) Describe() string {
	var parts []string
	if s.CPUs > 0 {
		parts = append(parts, describeCPUs(s.CPUs))
	}
	if s.Memory > 0 {
		parts = append(parts, describeMemory(s.Memory.Bytes()))
	}

	if len(parts) == 0 {
		return "no limits"
	}

	return strings.Join(parts, ", ")
}

// describeCPUs returns a number of CPUs: "1 CPU", "2 CPUs", "0.5 CPUs".
func describeCPUs(n float64) string {
	if n == 1 {
		return "1 CPU"
	}

	return strconv.FormatFloat(n, 'g', -1, 64) + " CPUs"
}

// describeMemory returns a number of bytes: "4 GiB".
func describeMemory(bytes int64) string {
	return units.CustomSize("%.4g %s", float64(bytes), 1024, binarySizeUnits)
}

// Validate checks the spec describes a container Docker can run.
func (s RunnerSpec) Validate() error {
	switch {
	case s.ImageRef == "":
		return errdefs.InvalidArgument("a runner needs an image carrying the Actions runner, " +
			"such as ghcr.io/actions/actions-runner")
	case s.CPUs < 0:
		return errdefs.InvalidArgument("cpus cannot be negative; leave it unset for no limit")
	case s.CPUs > 0 && s.CPUs < minCPUs:
		return errdefs.InvalidArgument("cpus %g is too few: the least is %g", s.CPUs, minCPUs)
	case s.Memory > 0 && s.Memory < minMemory:
		return errdefs.InvalidArgument("memory %s is too little: a runner needs at least %s; "+
			"a plain number is bytes, so write a unit, e.g. memory: 4GiB", s.Memory, minMemory)
	}

	switch s.Pull {
	case PullMissing, PullAlways:
	default:
		return errdefs.InvalidArgument("pull %q: want %s or %s", s.Pull, PullMissing, PullAlways)
	}

	for key := range s.Env {
		if key == "" || strings.Contains(key, "=") {
			return errdefs.InvalidArgument("env: invalid variable name %q", key)
		}
	}

	for _, m := range s.Mounts {
		if err := m.Validate(); err != nil {
			return err
		}
	}

	return validateDigest(s.ImageRef)
}

// validateDigest checks that the digest an image reference is pinned to, if
// any, parses.
func validateDigest(ref string) error {
	at := strings.LastIndex(ref, "@")
	if at < 0 {
		return nil
	}

	if _, err := digest.Parse(ref[at+1:]); err != nil {
		return errdefs.InvalidArgument("invalid image digest %q: want \"sha256:\" and a hex hash", ref[at+1:])
	}

	return nil
}

// splitImageRef splits an image reference into what the Engine API's pull
// takes: the repository, and the tag or digest. A reference with neither is
// the latest tag, since a pull without one would pull every tag.
func splitImageRef(ref string) (repo, tag string) {
	if at := strings.LastIndex(ref, "@"); at >= 0 {
		return ref[:at], ref[at+1:]
	}

	// A colon after the last slash is a tag; one before it is a registry's
	// port.
	if colon := strings.LastIndex(ref, ":"); colon > strings.LastIndex(ref, "/") {
		return ref[:colon], ref[colon+1:]
	}

	return ref, "latest"
}
