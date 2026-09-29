// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

// Package proxmox implements the proxmox provider type: runners as QEMU
// virtual machines on a Proxmox VE cluster, cloned from a template. The
// provider chooses the node each VM goes on, and hands the runner its
// registration through the QEMU guest agent.
package proxmox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"time"

	"github.com/konradasb/rungar/internal/provider"
	"github.com/konradasb/rungar/internal/types"
)

// Provider is a connected Proxmox VE cluster. It is safe for concurrent use.
type Provider struct {
	client *client

	// nodes are the nodes runners may go on; empty is every node.
	nodes []string

	logger *slog.Logger
}

var _ provider.Provider = (*Provider)(nil)

// deleteTimeout bounds deleting a VM, which takes longer than a call: it is
// stopped first, and its disks destroyed.
const deleteTimeout = 5 * time.Minute

// List returns the VMs carrying the selector's labels. Their tags narrow the
// VMs down, and the labels in their descriptions, read for those alone,
// decide.
func (p *Provider) List(ctx context.Context, selector map[string]string) ([]types.Machine, error) {
	vms, err := p.vms(ctx)
	if err != nil {
		return nil, fmt.Errorf("list VMs: %w", err)
	}

	want := tagsOf(selector)

	var out []types.Machine
	for _, vm := range vms {
		if vm.Template != 0 || !vm.hasTags(want) {
			continue
		}

		var cfg vmConfig
		if err := p.client.get(ctx, vmPath(vm.Node, vm.VMID)+"/config", nil, &cfg); err != nil {
			if errors.Is(err, errVMNotFound) {
				continue
			}

			return nil, fmt.Errorf("read the configuration of VM %d: %w", vm.VMID, err)
		}

		labels, created, ok := parseDescription(cfg.Description)
		if !ok || !types.HasLabels(labels, selector) {
			continue
		}

		out = append(out, machineOf(vm, labels, created))
	}

	return out, nil
}

// Delete stops and destroys the VMs called name, with their disks, waiting
// up to deleteTimeout for them to go. A VM already gone is not an error.
func (p *Provider) Delete(ctx context.Context, name string) error {
	ctx, cancel := context.WithTimeout(ctx, deleteTimeout)
	defer cancel()

	vms, err := p.vms(ctx)
	if err != nil {
		return fmt.Errorf("list VMs: %w", err)
	}

	for _, vm := range vms {
		if vm.Name != name || vm.Template != 0 {
			continue
		}

		path := vmPath(vm.Node, vm.VMID)

		if vm.Status == "running" || vm.Status == "paused" {
			err := p.runTask(ctx, path+"/status/stop", nil)
			if err != nil && !errors.Is(err, errVMNotFound) && !errors.Is(err, errNotRunning) {
				return fmt.Errorf("stop VM %d: %w", vm.VMID, err)
			}
		}

		destroy := url.Values{"purge": {"1"}, "destroy-unreferenced-disks": {"1"}}

		var upid string
		if err := p.client.delete(ctx, path, destroy, &upid); err != nil {
			if errors.Is(err, errVMNotFound) {
				continue
			}

			return fmt.Errorf("destroy VM %d: %w", vm.VMID, err)
		}
		if err := p.client.wait(ctx, upid); err != nil && !errors.Is(err, errVMNotFound) {
			return fmt.Errorf("destroy VM %d: %w", vm.VMID, err)
		}
	}

	return nil
}

// Close cancels calls in flight and closes idle connections.
func (p *Provider) Close() error {
	p.client.close()

	return nil
}

// vms returns the cluster's QEMU VMs, templates included.
func (p *Provider) vms(ctx context.Context) ([]resource, error) {
	return p.clusterResources(ctx, "vm", "qemu")
}

// clusterNodes returns the cluster's nodes.
func (p *Provider) clusterNodes(ctx context.Context) ([]resource, error) {
	return p.clusterResources(ctx, "node", "node")
}

// clusterResources returns the entries of /cluster/resources the API lists
// for query, keeping those of kind: a query for VMs lists containers too.
func (p *Provider) clusterResources(ctx context.Context, query, kind string) ([]resource, error) {
	var all []resource
	if err := p.client.get(ctx, "/cluster/resources", url.Values{"type": {query}}, &all); err != nil {
		return nil, err
	}

	out := all[:0]
	for _, r := range all {
		if r.Type == kind {
			out = append(out, r)
		}
	}

	return out, nil
}

// runTask calls POST path, which starts a task, and waits for the task to
// end.
func (p *Provider) runTask(ctx context.Context, path string, form url.Values) error {
	var upid string
	if err := p.client.post(ctx, path, form, &upid); err != nil {
		return err
	}

	return p.client.wait(ctx, upid)
}

// vmPath returns the API path of a VM.
func vmPath(node string, vmid int) string {
	return "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid)
}
