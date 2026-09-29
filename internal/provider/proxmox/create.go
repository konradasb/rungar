// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package proxmox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/types"
)

// cloneAttempts bounds the clones tried when another created a VM with the
// VMID the cluster offered first.
const cloneAttempts = 3

// Create clones the runner's template onto the node with the most memory free,
// sizes and labels the clone, starts it, and once its guest agent answers
// writes the registration to jit_path. It is bounded by ctx alone.
func (p *Provider) Create(ctx context.Context, spec types.MachineSpec) error {
	runner, ok := spec.Runner.(RunnerSpec)
	if !ok {
		return errdefs.InvalidArgument("runner %q: not a proxmox runner (%T)", spec.Name, spec.Runner)
	}

	vms, err := p.vms(ctx)
	if err != nil {
		return fmt.Errorf("list VMs: %w", err)
	}
	template, err := templateByVMID(vms, runner.Template)
	if err != nil {
		return err
	}

	nodes, err := p.clusterNodes(ctx)
	if err != nil {
		return fmt.Errorf("list nodes: %w", err)
	}
	node, err := p.chooseNode(nodes, runner)
	if err != nil {
		return err
	}

	vmid, err := p.clone(ctx, template, node, spec.Name, runner)
	if err != nil {
		return createError(err)
	}

	if err := p.boot(ctx, node, vmid, spec, runner); err != nil {
		return createError(err)
	}

	return nil
}

// boot configures a cloned VM, starts it, and writes its registration once
// its guest agent answers.
func (p *Provider) boot(ctx context.Context, node string, vmid int, spec types.MachineSpec, runner RunnerSpec) error {
	path := vmPath(node, vmid)

	config := url.Values{
		"cores":       {strconv.Itoa(runner.Cores)},
		"memory":      {strconv.FormatInt(runner.memoryMiB(), 10)},
		"agent":       {"1"},
		"tags":        {strings.Join(tagsOf(spec.Labels), ";")},
		"description": {vmDescription(spec.Labels, time.Now())},
	}
	if err := p.client.put(ctx, path+"/config", config); err != nil {
		return fmt.Errorf("configure VM %d: %w", vmid, err)
	}

	if err := p.runTask(ctx, path+"/status/start", nil); err != nil {
		return fmt.Errorf("start VM %d: %w", vmid, err)
	}

	if err := p.awaitAgent(ctx, path); err != nil {
		return fmt.Errorf("VM %d: %w", vmid, err)
	}

	write := url.Values{"file": {runner.JITPath}, "content": {spec.JITConfig}}
	if err := p.client.post(ctx, path+"/agent/file-write", write, nil); err != nil {
		return fmt.Errorf("write the registration to VM %d: %w", vmid, err)
	}

	return nil
}

// clone clones the template onto node as a VM called name, and returns its
// VMID. The VMID the cluster offers may be taken by another clone meanwhile,
// so a clone refused for that is tried again with the next.
func (p *Provider) clone(ctx context.Context, template resource, node, name string, runner RunnerSpec) (int, error) {
	form := url.Values{"name": {name}, "full": {"0"}}
	if runner.FullClone {
		form.Set("full", "1")
	}
	if node != template.Node {
		form.Set("target", node)
	}
	if runner.Storage != "" {
		form.Set("storage", runner.Storage)
	}
	if runner.Pool != "" {
		form.Set("pool", runner.Pool)
	}

	for attempt := 1; ; attempt++ {
		vmid, err := p.nextVMID(ctx)
		if err != nil {
			return 0, err
		}
		form.Set("newid", strconv.Itoa(vmid))

		err = p.runTask(ctx, vmPath(template.Node, template.VMID)+"/clone", form)
		switch {
		case err == nil:
			return vmid, nil
		case errors.Is(err, errVMIDTaken) && attempt < cloneAttempts:
			p.logger.Debug("VMID taken by another VM meanwhile; trying the next",
				slog.String("runner", name), slog.Int("vm_id", vmid))
			continue
		case errors.Is(err, errLocalStorage):
			// A linked clone, or any clone of a template on storage the
			// target node cannot reach, can only go on the template's node.
			return 0, errdefs.InvalidArgument("clone template %d onto %s: %w; put the template on storage "+
				"every node shares, or limit nodes to %s", template.VMID, node, err, template.Node)
		default:
			return 0, fmt.Errorf("clone template %d onto %s: %w", template.VMID, node, err)
		}
	}
}

// nextVMID returns a VMID the cluster has free.
func (p *Provider) nextVMID(ctx context.Context) (int, error) {
	var raw json.RawMessage
	if err := p.client.get(ctx, "/cluster/nextid", nil, &raw); err != nil {
		return 0, fmt.Errorf("find a free VMID: %w", err)
	}

	id, err := strconv.Atoi(strings.Trim(string(raw), `"`))
	if err != nil {
		return 0, fmt.Errorf("find a free VMID: %q is not one", raw)
	}

	return id, nil
}

// awaitAgent waits for the guest agent of the VM at path to answer, asking
// again every poll until ctx is done.
func (p *Provider) awaitAgent(ctx context.Context, path string) error {
	for {
		err := p.client.post(ctx, path+"/agent/ping", nil, nil)
		if err == nil {
			return nil
		}

		select {
		case <-time.After(p.client.poll):
		case <-ctx.Done():
			return fmt.Errorf("the guest agent did not answer: %w; last: %w", ctx.Err(), err)
		}
	}
}

// allowedNodes returns the nodes runners may go on.
func (p *Provider) allowedNodes(nodes []resource) []resource {
	if len(p.nodes) == 0 {
		return nodes
	}

	var out []resource
	for _, n := range nodes {
		if slices.Contains(p.nodes, n.Node) {
			out = append(out, n)
		}
	}

	return out
}

// chooseNode returns the online node, of those allowed, with the most memory
// free that has room for the runner. It returns an errdefs.ErrInvalidArgument
// error if no node could ever take the runner, and an errdefs.ErrNoCapacity
// error if none can now.
func (p *Provider) chooseNode(nodes []resource, runner RunnerSpec) (string, error) {
	allowed := p.allowedNodes(nodes)
	if len(allowed) == 0 {
		if len(p.nodes) > 0 {
			return "", errdefs.InvalidArgument("none of the nodes %s is in the cluster",
				strings.Join(p.nodes, ", "))
		}

		return "", errors.New("the cluster lists no nodes")
	}

	memory := runner.Memory.Bytes()

	var (
		best        *resource
		online      []string
		anyNodeFits bool
		sizesKnown  bool
	)
	for i, n := range allowed {
		if n.MaxCPU > 0 && n.MaxMemory > 0 {
			sizesKnown = true
			if int(n.MaxCPU) >= runner.Cores && n.MaxMemory >= memory {
				anyNodeFits = true
			}
		}

		if n.Status != "online" {
			continue
		}
		online = append(online, fmt.Sprintf("%s has %s", n.Node, types.Size(n.freeMemory())))

		if int(n.MaxCPU) < runner.Cores || n.freeMemory() < memory {
			continue
		}
		if best == nil || n.freeMemory() > best.freeMemory() {
			best = &allowed[i]
		}
	}

	switch {
	case sizesKnown && !anyNodeFits:
		return "", errdefs.InvalidArgument("no node can take %s: every node has fewer cores or less memory",
			runner.Describe())
	case len(online) == 0:
		return "", errors.New("no node runners may go on is online")
	case best == nil:
		return "", errdefs.NoCapacity("no node has %s free for %s: %s", runner.Memory, runner.Describe(),
			strings.Join(online, ", "))
	}

	return best.Node, nil
}

// templateByVMID returns the template VM of this VMID. It returns an
// errdefs.ErrInvalidArgument error if there is none.
func templateByVMID(vms []resource, vmid int) (resource, error) {
	for _, vm := range vms {
		if vm.VMID != vmid {
			continue
		}
		if vm.Template == 0 {
			return resource{}, errdefs.InvalidArgument("VM %d is not a template: convert it to one first", vmid)
		}

		return vm, nil
	}

	return resource{}, errdefs.InvalidArgument("template %d is not in the cluster", vmid)
}
