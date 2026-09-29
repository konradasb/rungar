// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package proxmox

// resource is an entry of /cluster/resources: a VM, or a node.
type resource struct {
	Type      string  `json:"type"`
	Node      string  `json:"node"`
	Status    string  `json:"status"`
	VMID      int     `json:"vmid"`
	Name      string  `json:"name"`
	Template  int     `json:"template"`
	Tags      string  `json:"tags"`
	Lock      string  `json:"lock"`
	MaxCPU    float64 `json:"maxcpu"`
	MaxMemory int64   `json:"maxmem"`
	Memory    int64   `json:"mem"`
}

// freeMemory returns a node's memory not in use.
func (r resource) freeMemory() int64 {
	return max(r.MaxMemory-r.Memory, 0)
}
