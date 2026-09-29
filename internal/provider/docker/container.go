// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package docker

import (
	"maps"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/konradasb/rungar/internal/types"
)

// jitConfigEnv is the variable the Actions runner reads its just-in-time
// configuration from.
const jitConfigEnv = "ACTIONS_RUNNER_INPUT_JITCONFIG"

// containerConfig is the body of a container create, as much of it as a
// runner needs.
type containerConfig struct {
	Image      string            `json:"Image"`
	Cmd        []string          `json:"Cmd,omitempty"`
	Env        []string          `json:"Env,omitempty"`
	Labels     map[string]string `json:"Labels,omitempty"`
	HostConfig hostConfig        `json:"HostConfig"`
}

// hostConfig is a container's host configuration. It has no Privileged: a
// runner's container is never privileged.
type hostConfig struct {
	AutoRemove  bool       `json:"AutoRemove"`
	Init        bool       `json:"Init"`
	NanoCPUs    int64      `json:"NanoCpus,omitempty"`
	Memory      int64      `json:"Memory,omitempty"`
	NetworkMode string     `json:"NetworkMode,omitempty"`
	Mounts      []apiMount `json:"Mounts,omitempty"`
}

// containerSummary is a container as the Engine API lists it.
type containerSummary struct {
	Names   []string          `json:"Names"`
	Labels  map[string]string `json:"Labels"`
	State   string            `json:"State"`
	Created int64             `json:"Created"`
}

// containerFor returns the container a runner gets. It is removed when it
// stops, since its registration is good for one use, so that a runner whose
// job ends while Rungar is down leaves nothing behind. An init process reaps
// what the job leaves running.
func containerFor(spec types.MachineSpec, runner RunnerSpec) containerConfig {
	env := make(map[string]string, len(runner.Env)+1)
	maps.Copy(env, runner.Env)

	// The registration cannot be overridden.
	env[jitConfigEnv] = spec.JITConfig

	vars := make([]string, 0, len(env))
	for _, key := range slices.Sorted(maps.Keys(env)) {
		vars = append(vars, key+"="+env[key])
	}

	mounts := make([]apiMount, 0, len(runner.Mounts))
	for _, m := range runner.Mounts {
		mounts = append(mounts, m.api())
	}

	return containerConfig{
		Image:  runner.ImageRef,
		Cmd:    runner.Command,
		Env:    vars,
		Labels: maps.Clone(spec.Labels),
		HostConfig: hostConfig{
			AutoRemove:  true,
			Init:        true,
			NanoCPUs:    int64(math.Round(runner.CPUs * 1e9)),
			Memory:      runner.Memory.Bytes(),
			NetworkMode: runner.Network,
			Mounts:      mounts,
		},
	}
}

// machineOf converts a listed container to a machine. The list does not say
// what limits a container has, so its size is left unsaid.
func machineOf(c containerSummary) types.Machine {
	m := types.Machine{Labels: c.Labels, State: machineState(c.State)}

	// Names are listed with a leading slash: "/rungar-c2-m4-1ff1015a".
	if len(c.Names) > 0 {
		m.Name = strings.TrimPrefix(c.Names[0], "/")
	}
	if c.Created > 0 {
		m.CreatedAt = time.Unix(c.Created, 0)
	}

	return m
}

// machineState converts a container's state to a machine's. A paused
// container still holds its runner, so it counts as running.
func machineState(s string) types.MachineState {
	switch s {
	case "created", "restarting":
		return types.MachineStarting
	case "running", "paused":
		return types.MachineRunning
	default:
		return types.MachineStopped
	}
}
