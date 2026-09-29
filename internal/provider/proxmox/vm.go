// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package proxmox

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/konradasb/rungar/internal/types"
)

// vmConfig is the part of a VM's configuration the provider reads.
type vmConfig struct {
	Description string `json:"description"`
}

// annotation is what the provider keeps in a VM's description: Rungar's labels,
// exactly, which its tags only stand for, and when it created the VM.
type annotation struct {
	Rungar struct {
		Labels  map[string]string `json:"labels"`
		Created time.Time         `json:"created"`
	} `json:"rungar"`
}

// vmDescription returns the description of a VM carrying labels, created at
// created.
func vmDescription(labels map[string]string, created time.Time) string {
	var a annotation
	a.Rungar.Labels = labels
	a.Rungar.Created = created.UTC().Truncate(time.Second)

	b, _ := json.Marshal(a) //nolint:errchkjson // a map of strings and a time always marshal

	return string(b)
}

// parseDescription returns the labels and creation time a VM's description
// carries, or false if it is not one the provider wrote.
func parseDescription(description string) (map[string]string, time.Time, bool) {
	var a annotation
	if err := json.Unmarshal([]byte(strings.TrimSpace(description)), &a); err != nil || a.Rungar.Labels == nil {
		return nil, time.Time{}, false
	}

	return a.Rungar.Labels, a.Rungar.Created, true
}

// machineOf returns a VM as a machine, with the labels and creation time its
// description carries.
func machineOf(vm resource, labels map[string]string, created time.Time) types.Machine {
	m := types.Machine{
		Name:      vm.Name,
		Labels:    labels,
		State:     machineState(vm),
		CreatedAt: created,
	}
	if vm.MaxCPU > 0 && vm.MaxMemory > 0 {
		m.Size = types.Resources{VCPUs: int(vm.MaxCPU), Memory: types.Size(vm.MaxMemory)}.String()
	}

	return m
}

// machineState returns a VM's state as a machine's. A VM being cloned or
// created is starting; a paused one still holds its runner, so it is running.
func machineState(vm resource) types.MachineState {
	switch {
	case vm.Lock == "clone" || vm.Lock == "create":
		return types.MachineStarting
	case vm.Status == "running" || vm.Status == "paused":
		return types.MachineRunning
	default:
		return types.MachineStopped
	}
}
