// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package proxmox

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/docker/go-units"

	"github.com/konradasb/rungar/internal/types"
)

// resource is an entry of /cluster/resources: a VM, or a node.
type resource struct {
	Type     string  `json:"type"`
	Node     string  `json:"node"`
	Status   string  `json:"status"`
	VMID     int     `json:"vmid"`
	Name     string  `json:"name"`
	Template int     `json:"template"`
	Tags     string  `json:"tags"`
	Lock     string  `json:"lock"`
	MaxCPU   float64 `json:"maxcpu"`
	MaxMem   int64   `json:"maxmem"`
	Mem      int64   `json:"mem"`
}

// freeMem returns a node's memory not in use.
func (r resource) freeMem() int64 {
	return max(r.MaxMem-r.Mem, 0)
}

// tags returns a VM's tags. Proxmox VE separates them with semicolons, and
// accepts commas and spaces.
func (r resource) tags() []string {
	return strings.FieldsFunc(r.Tags, func(c rune) bool { return c == ';' || c == ',' || c == ' ' })
}

// hasTags reports whether a VM carries every tag in want.
func (r resource) hasTags(want []string) bool {
	have := r.tags()
	for _, tag := range want {
		if !slices.Contains(have, tag) {
			return false
		}
	}

	return true
}

// vmConfig is the part of a VM's configuration the provider reads.
type vmConfig struct {
	Description string `json:"description"`
}

// annotation is what the provider keeps in a VM's description: Rungar's labels,
// exactly, which its tags only stand for, and when it made the VM.
type annotation struct {
	Rungar struct {
		Labels  map[string]string `json:"labels"`
		Created time.Time         `json:"created"`
	} `json:"rungar"`
}

// describeVM returns the description of a VM carrying labels, made at created.
func describeVM(labels map[string]string, created time.Time) string {
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

// tagPrefix begins every tag the provider sets.
const tagPrefix = "rungar-"

// tagsOf returns the tags that stand for labels: one per label, a hash of it,
// as Proxmox VE allows few characters in a tag. They are sorted, so that a VM
// is tagged the same whatever the order of its labels.
func tagsOf(labels map[string]string) []string {
	tags := make([]string, 0, len(labels))
	for k, v := range labels {
		tags = append(tags, labelTag(k, v))
	}
	slices.Sort(tags)

	return tags
}

// labelTag returns the tag that stands for one label.
func labelTag(key, value string) string {
	sum := sha256.Sum256([]byte(key + "=" + value))

	return tagPrefix + hex.EncodeToString(sum[:8])
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
	if vm.MaxCPU > 0 && vm.MaxMem > 0 {
		m.Size = describe(int(vm.MaxCPU), vm.MaxMem)
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

// formatBytes returns a size for a person: "8 GiB".
func formatBytes(b int64) string {
	return units.CustomSize("%.4g %s", float64(b), 1024, []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"})
}
