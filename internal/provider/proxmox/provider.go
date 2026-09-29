// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

// Package proxmox implements the proxmox provider type: runners as QEMU
// virtual machines on a Proxmox VE cluster, cloned from a template. The
// provider chooses the node each VM goes on, and hands the runner its
// registration through the QEMU guest agent.
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
	"github.com/konradasb/rungar/internal/provider"
	"github.com/konradasb/rungar/internal/types"
)

// Provider is a connected Proxmox VE cluster.
type Provider struct {
	api *client

	// nodes are the nodes runners may go on; empty is every node.
	nodes []string

	// poll is how often a task or the guest agent is asked again.
	poll time.Duration

	logger *slog.Logger
	close  context.CancelFunc
}

var _ provider.Provider = (*Provider)(nil)

const (
	// defaultPoll is how often a task or the guest agent is asked again.
	defaultPoll = time.Second

	// cloneAttempts bounds the clones tried when another made a VM with the
	// VMID the cluster offered first.
	cloneAttempts = 3
)

// noCapacityMessages are what Proxmox VE's failures say when a node is out of
// something a runner needs now.
var noCapacityMessages = []string{
	"no space left",
	"not enough space",
	"out of memory",
	"cannot allocate memory",
	"not enough memory",
}

// List returns the VMs carrying the selector's labels. Their tags narrow the
// VMs down, and the labels in their descriptions, read for those alone,
// decide.
func (p *Provider) List(ctx context.Context, selector map[string]string) ([]types.Machine, error) {
	vms, err := p.resources(ctx, "vm")
	if err != nil {
		return nil, err
	}

	want := tagsOf(selector)

	var out []types.Machine
	for _, vm := range vms {
		if vm.Template != 0 || !vm.hasTags(want) {
			continue
		}

		var cfg vmConfig
		if err := p.api.get(ctx, vmPath(vm.Node, vm.VMID)+"/config", nil, &cfg); err != nil {
			if isGone(err) {
				continue
			}

			return nil, err
		}

		labels, created, ok := parseDescription(cfg.Description)
		if !ok || !types.Matches(labels, selector) {
			continue
		}

		out = append(out, machineOf(vm, labels, created))
	}

	return out, nil
}

// Create clones the runner's template onto the node with the most memory free,
// sizes and labels the clone, starts it, and once its guest agent answers
// writes the registration to jit_path. It is bounded by ctx alone.
func (p *Provider) Create(ctx context.Context, spec types.MachineSpec) error {
	runner, ok := spec.Runner.(RunnerSpec)
	if !ok {
		return errdefs.InvalidArgument("runner %q: not a proxmox runner (%T)", spec.Name, spec.Runner)
	}

	vms, err := p.resources(ctx, "vm")
	if err != nil {
		return err
	}
	tmpl, err := template(vms, runner.Template)
	if err != nil {
		return err
	}

	nodes, err := p.resources(ctx, "node")
	if err != nil {
		return err
	}
	node, err := p.chooseNode(nodes, runner)
	if err != nil {
		return err
	}

	vmid, err := p.clone(ctx, tmpl, node, spec.Name, runner)
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
		"description": {describeVM(spec.Labels, time.Now())},
	}
	if err := p.api.put(ctx, path+"/config", config); err != nil {
		return fmt.Errorf("configure VM %d: %w", vmid, err)
	}

	if err := p.task(ctx, path+"/status/start", nil); err != nil {
		return fmt.Errorf("start VM %d: %w", vmid, err)
	}

	if err := p.awaitAgent(ctx, path); err != nil {
		return fmt.Errorf("VM %d: %w", vmid, err)
	}

	write := url.Values{"file": {runner.JITPath}, "content": {spec.JITConfig}}
	if err := p.api.post(ctx, path+"/agent/file-write", write, nil); err != nil {
		return fmt.Errorf("write the registration to VM %d: %w", vmid, err)
	}

	return nil
}

// clone clones the template onto node as a VM called name, and returns its
// VMID. The VMID the cluster offers may be taken by another clone meanwhile,
// so a clone refused for that is tried again with the next.
func (p *Provider) clone(ctx context.Context, tmpl resource, node, name string, runner RunnerSpec) (int, error) {
	form := url.Values{"name": {name}, "full": {"0"}}
	if runner.FullClone {
		form.Set("full", "1")
	}
	if node != tmpl.Node {
		form.Set("target", node)
	}
	if runner.Storage != "" {
		form.Set("storage", runner.Storage)
	}
	if runner.Pool != "" {
		form.Set("pool", runner.Pool)
	}

	for attempt := 1; ; attempt++ {
		vmid, err := p.nextID(ctx)
		if err != nil {
			return 0, err
		}
		form.Set("newid", strconv.Itoa(vmid))

		err = p.task(ctx, vmPath(tmpl.Node, tmpl.VMID)+"/clone", form)
		switch {
		case err == nil:
			return vmid, nil
		case strings.Contains(errorMessage(err), "already exists") && attempt < cloneAttempts:
			continue
		case strings.Contains(errorMessage(err), "local storage"):
			// A linked clone, or any clone of a template on storage the
			// target node cannot reach, can only go on the template's node.
			return 0, errdefs.InvalidArgument("clone template %d onto %s: %s; put the template on storage "+
				"every node shares, or limit nodes to %s", tmpl.VMID, node, errorMessage(err), tmpl.Node)
		default:
			return 0, fmt.Errorf("clone template %d onto %s: %w", tmpl.VMID, node, err)
		}
	}
}

// nextID returns a VMID the cluster has free.
func (p *Provider) nextID(ctx context.Context) (int, error) {
	var raw json.RawMessage
	if err := p.api.get(ctx, "/cluster/nextid", nil, &raw); err != nil {
		return 0, fmt.Errorf("find a free VMID: %w", err)
	}

	id, err := strconv.Atoi(strings.Trim(string(raw), `"`))
	if err != nil {
		return 0, fmt.Errorf("find a free VMID: %q is not one", raw)
	}

	return id, nil
}

// awaitAgent waits for the guest agent of the VM at path to answer, asking
// every poll until ctx is done.
func (p *Provider) awaitAgent(ctx context.Context, path string) error {
	for {
		err := p.api.post(ctx, path+"/agent/ping", nil, nil)
		if err == nil {
			return nil
		}

		select {
		case <-time.After(p.poll):
		case <-ctx.Done():
			return fmt.Errorf("the guest agent did not answer: %w; last: %w", ctx.Err(), err)
		}
	}
}

// Delete stops and destroys the VMs called name, with their disks. A VM
// already gone is not an error.
func (p *Provider) Delete(ctx context.Context, name string) error {
	vms, err := p.resources(ctx, "vm")
	if err != nil {
		return err
	}

	for _, vm := range vms {
		if vm.Name != name || vm.Template != 0 {
			continue
		}

		path := vmPath(vm.Node, vm.VMID)

		if vm.Status == "running" || vm.Status == "paused" {
			if err := p.task(ctx, path+"/status/stop", nil); err != nil && !isGone(err) && !isNotRunning(err) {
				return fmt.Errorf("stop VM %d: %w", vm.VMID, err)
			}
		}

		destroy := url.Values{"purge": {"1"}, "destroy-unreferenced-disks": {"1"}}

		var upid string
		if err := p.api.delete(ctx, path, destroy, &upid); err != nil {
			if isGone(err) {
				continue
			}

			return fmt.Errorf("destroy VM %d: %w", vm.VMID, err)
		}
		if err := p.api.wait(ctx, upid, p.poll); err != nil && !isGone(err) {
			return fmt.Errorf("destroy VM %d: %w", vm.VMID, err)
		}
	}

	return nil
}

// Close cancels calls in flight and closes idle connections.
func (p *Provider) Close() error {
	p.close()
	p.api.http.CloseIdleConnections()

	return nil
}

// resources returns the cluster's QEMU VMs, templates included, or its nodes.
func (p *Provider) resources(ctx context.Context, typ string) ([]resource, error) {
	var all []resource
	if err := p.api.get(ctx, "/cluster/resources", url.Values{"type": {typ}}, &all); err != nil {
		return nil, err
	}

	want := "node"
	if typ == "vm" {
		want = "qemu"
	}

	out := all[:0]
	for _, r := range all {
		if r.Type == want {
			out = append(out, r)
		}
	}

	return out, nil
}

// allowed returns the nodes runners may go on.
func (p *Provider) allowed(nodes []resource) []resource {
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
	allowed := p.allowed(nodes)
	if len(allowed) == 0 {
		if len(p.nodes) > 0 {
			return "", errdefs.InvalidArgument("none of the nodes %s is in the cluster",
				strings.Join(p.nodes, ", "))
		}

		return "", errors.New("proxmox: the cluster lists no nodes")
	}

	memory := runner.Memory.Bytes()

	var (
		best     *resource
		online   []string
		everFits bool
		known    bool
	)
	for i, n := range allowed {
		if n.MaxCPU > 0 && n.MaxMem > 0 {
			known = true
			if int(n.MaxCPU) >= runner.Cores && n.MaxMem >= memory {
				everFits = true
			}
		}

		if n.Status != "online" {
			continue
		}
		online = append(online, fmt.Sprintf("%s has %s", n.Node, formatBytes(n.freeMem())))

		if int(n.MaxCPU) < runner.Cores || n.freeMem() < memory {
			continue
		}
		if best == nil || n.freeMem() > best.freeMem() {
			best = &allowed[i]
		}
	}

	switch {
	case known && !everFits:
		return "", errdefs.InvalidArgument("no node can take %s: every node has fewer cores or less memory",
			runner.Describe())
	case len(online) == 0:
		return "", errors.New("proxmox: no node runners may go on is online")
	case best == nil:
		return "", errdefs.NoCapacity("no node has %s free for %s: %s", formatBytes(memory), runner.Describe(),
			strings.Join(online, ", "))
	}

	return best.Node, nil
}

// task calls POST path, which starts a task, and waits for the task to end.
func (p *Provider) task(ctx context.Context, path string, form url.Values) error {
	var upid string
	if err := p.api.post(ctx, path, form, &upid); err != nil {
		return err
	}

	return p.api.wait(ctx, upid, p.poll)
}

// template returns the template VM of this VMID. It returns an
// errdefs.ErrInvalidArgument error if there is none.
func template(vms []resource, vmid int) (resource, error) {
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

// createError puts a failure to make a runner in its class; see
// provider.Provider.Create. A node out of memory or disk is full.
func createError(err error) error {
	if errors.Is(err, errdefs.ErrInvalidArgument) || errors.Is(err, errdefs.ErrNoCapacity) {
		return err
	}

	message := errorMessage(err)
	for _, m := range noCapacityMessages {
		if strings.Contains(message, m) {
			return errdefs.NoCapacity("%s", err)
		}
	}

	return err
}

// isGone reports whether err says the VM does not exist.
func isGone(err error) bool {
	message := errorMessage(err)

	return strings.Contains(message, "does not exist") || strings.Contains(message, "no such vm")
}

// isNotRunning reports whether err says the VM is already stopped.
func isNotRunning(err error) bool {
	return strings.Contains(errorMessage(err), "not running")
}

// vmPath returns the API path of a VM.
func vmPath(node string, vmid int) string {
	return "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid)
}
