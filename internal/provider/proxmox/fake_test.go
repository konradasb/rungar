// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package proxmox

import (
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/konradasb/rungar/internal/types"
)

const (
	testTokenID = "rungar@pve!rungar"
	testSecret  = "s3cret"
	gib         = int64(1) << 30
)

// fakePVE is a Proxmox VE cluster's API, as far as the provider uses it.
type fakePVE struct {
	t      *testing.T
	server *httptest.Server

	mu    sync.Mutex
	nodes []resource
	vms   map[int]*fakeVM

	// ids are the VMIDs /cluster/nextid offers, in turn; after them, one
	// more than the highest VM.
	ids []int

	// exits are the exit statuses of the tasks of each kind -- qmclone,
	// qmstart, qmstop, qmdestroy -- unless OK.
	exits map[string]string

	// polls is how many times a task's status says it is still running.
	polls int

	// agentAfter is how many pings a VM's guest agent fails before it
	// answers.
	agentAfter int

	tasks    map[string]fakeTask
	pings    map[int]int
	requests []string
}

// fakeVM is a VM of the fake cluster.
type fakeVM struct {
	resource
	description string
	cores       int
	memoryMiB   int
	agent       string
	files       map[string]string
}

// fakeTask is a task of the fake cluster.
type fakeTask struct {
	exit  string
	polls int
}

// newFakePVE returns a fake cluster of two online nodes and a template, 9000,
// on pve1.
func newFakePVE(t *testing.T) *fakePVE {
	t.Helper()

	f := &fakePVE{
		t: t,
		nodes: []resource{
			{Type: "node", Node: "pve1", Status: "online", MaxCPU: 16, MaxMemory: 64 * gib, Memory: 40 * gib},
			{Type: "node", Node: "pve2", Status: "online", MaxCPU: 8, MaxMemory: 32 * gib, Memory: 4 * gib},
		},
		vms: map[int]*fakeVM{
			9000: {resource: resource{Type: "qemu", Node: "pve1", VMID: 9000, Name: "runner-template",
				Template: 1, Status: "stopped", Tags: "template"}},
		},
		exits: map[string]string{},
		tasks: map[string]fakeTask{},
		pings: map[int]int{},
	}

	f.server = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)

	return f
}

// open returns a provider of the fake cluster, which asks again every
// millisecond.
func (f *fakePVE) open(t *testing.T, nodes ...string) *Provider {
	t.Helper()

	c := &Config{
		URL:         f.server.URL,
		TokenID:     testTokenID,
		TokenSecret: testSecret,
		TLS:         &TLS{CAFile: f.caFile(t)},
		Nodes:       nodes,
		Timeout:     5 * time.Second,
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}

	p, err := c.Open(t.Context(), nil)
	if err != nil {
		t.Fatalf("Open() = %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })

	pp, ok := p.(*Provider)
	if !ok {
		t.Fatalf("Open() = %T, want a *Provider", p)
	}
	pp.client.poll = time.Millisecond

	return pp
}

// caFile writes the fake's certificate to a file, to verify it by.
func (f *fakePVE) caFile(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "ca.pem")
	block := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.server.Certificate().Raw})
	if err := os.WriteFile(path, block, 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

// addVM adds a VM to the cluster, as another client of it would.
func (f *fakePVE) addVM(vm *fakeVM) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.vms[vm.VMID] = vm
}

// vm returns the VM called name, or nil.
func (f *fakePVE) vm(name string) *fakeVM {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, vm := range f.vms {
		if vm.Name == name && vm.Template == 0 {
			return vm
		}
	}

	return nil
}

// count returns how many requests were made whose line starts with prefix.
func (f *fakePVE) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()

	n := 0
	for _, r := range f.requests {
		if strings.HasPrefix(r, prefix) {
			n++
		}
	}

	return n
}

func (f *fakePVE) serve(w http.ResponseWriter, r *http.Request) {
	if got := r.Header.Get("Authorization"); got != "PVEAPIToken="+testTokenID+"="+testSecret {
		f.fail(w, http.StatusUnauthorized, "authentication failure")
		return
	}
	if err := r.ParseForm(); err != nil {
		f.fail(w, http.StatusBadRequest, "bad form")
		return
	}

	path := strings.TrimPrefix(r.URL.Path, apiPath+"/")
	parts := strings.Split(path, "/")

	f.mu.Lock()
	f.requests = append(f.requests, r.Method+" "+path)
	f.mu.Unlock()

	switch {
	case r.Method == http.MethodGet && path == "cluster/resources":
		f.clusterResources(w, r.Form.Get("type"))
	case r.Method == http.MethodGet && path == "cluster/nextid":
		f.nextVMID(w)
	case len(parts) == 5 && parts[0] == "nodes" && parts[2] == "tasks" && parts[4] == "status":
		f.taskStatus(w, parts[3])
	case len(parts) >= 4 && parts[0] == "nodes" && parts[2] == "qemu":
		f.qemu(w, r, parts[1], parts[3], strings.Join(parts[4:], "/"))
	default:
		f.fail(w, http.StatusNotImplemented, "Method '"+r.Method+" /"+path+"' not implemented")
	}
}

func (f *fakePVE) clusterResources(w http.ResponseWriter, resourceType string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []resource
	switch resourceType {
	case "node":
		out = slices.Clone(f.nodes)
	case "vm":
		for _, vm := range f.vms {
			r := vm.resource
			r.MaxCPU, r.MaxMemory = float64(vm.cores), int64(vm.memoryMiB)<<20
			out = append(out, r)
		}
		// A container, which the provider leaves alone.
		out = append(out, resource{Type: "lxc", Node: "pve1", VMID: 200, Name: "rungar-c2-m4-00000000"})
	}

	f.ok(w, out)
}

func (f *fakePVE) nextVMID(w http.ResponseWriter) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if len(f.ids) > 0 {
		id := f.ids[0]
		f.ids = f.ids[1:]
		f.ok(w, strconv.Itoa(id))

		return
	}

	next := 100
	for id := range f.vms {
		next = max(next, id+1)
	}

	// As Proxmox VE does: the VMID as a string.
	f.ok(w, strconv.Itoa(next))
}

func (f *fakePVE) taskStatus(w http.ResponseWriter, upid string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	task, ok := f.tasks[upid]
	if !ok {
		f.fail(w, http.StatusInternalServerError, "no such task")
		return
	}

	if task.polls > 0 {
		task.polls--
		f.tasks[upid] = task
		f.ok(w, map[string]string{"status": "running"})

		return
	}

	f.ok(w, map[string]string{"status": "stopped", "exitstatus": task.exit})
}

func (f *fakePVE) qemu(w http.ResponseWriter, r *http.Request, node, id, action string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	vmid, _ := strconv.Atoi(id)
	vm, ok := f.vms[vmid]
	if !ok || vm.Node != node {
		f.fail(w, http.StatusInternalServerError,
			fmt.Sprintf("Configuration file 'nodes/%s/qemu-server/%s.conf' does not exist", node, id))
		return
	}

	switch r.Method + " " + action {
	case "POST clone":
		f.clone(w, r, vm)
	case "PUT config":
		vm.cores, _ = strconv.Atoi(r.Form.Get("cores"))
		vm.memoryMiB, _ = strconv.Atoi(r.Form.Get("memory"))
		vm.Tags = r.Form.Get("tags")
		vm.description = r.Form.Get("description")
		vm.agent = r.Form.Get("agent")
		f.ok(w, nil)
	case "GET config":
		f.ok(w, map[string]any{"description": vm.description, "cores": vm.cores})
	case "POST status/start":
		if f.exits["qmstart"] == "" {
			vm.Status = "running"
		}
		f.task(w, node, "qmstart", vmid)
	case "POST status/stop":
		vm.Status = "stopped"
		f.task(w, node, "qmstop", vmid)
	case "POST agent/ping":
		if vm.Status != "running" || f.pings[vmid] < f.agentAfter {
			f.pings[vmid]++
			f.fail(w, http.StatusInternalServerError, "QEMU guest agent is not running")

			return
		}
		f.ok(w, map[string]any{})
	case "POST agent/file-write":
		if vm.files == nil {
			vm.files = map[string]string{}
		}
		vm.files[r.Form.Get("file")] = r.Form.Get("content")
		f.ok(w, nil)
	case "DELETE ":
		if vm.Status == "running" {
			f.fail(w, http.StatusInternalServerError, "VM "+id+" is running - destroy failed")
			return
		}
		if r.Form.Get("purge") != "1" || r.Form.Get("destroy-unreferenced-disks") != "1" {
			f.t.Errorf("DELETE %s without purge and destroy-unreferenced-disks: %v", id, r.Form)
		}
		delete(f.vms, vmid)
		f.task(w, node, "qmdestroy", vmid)
	default:
		f.fail(w, http.StatusNotImplemented, "not implemented: "+r.Method+" "+action)
	}
}

// clone clones template as Proxmox VE does, creating the VM before its task
// ends: a failed clone leaves one behind. It is called with f.mu held.
func (f *fakePVE) clone(w http.ResponseWriter, r *http.Request, template *fakeVM) {
	newID, _ := strconv.Atoi(r.Form.Get("newid"))
	if _, taken := f.vms[newID]; taken {
		f.fail(w, http.StatusInternalServerError, fmt.Sprintf("unable to create VM %d: config file already exists", newID))
		return
	}

	node := template.Node
	if target := r.Form.Get("target"); target != "" {
		node = target
	}

	f.vms[newID] = &fakeVM{resource: resource{
		Type: "qemu", Node: node, VMID: newID, Name: r.Form.Get("name"), Status: "stopped", Tags: template.Tags,
	}}

	f.task(w, template.Node, "qmclone", template.VMID)
}

// task starts a task of kind on node, and answers with its UPID. It is called
// with f.mu held.
func (f *fakePVE) task(w http.ResponseWriter, node, kind string, vmid int) {
	upid := fmt.Sprintf("UPID:%s:0000%04X:00000001:%08X:%s:%d:%s:", node, len(f.tasks), len(f.tasks), kind, vmid, testTokenID)

	exit := f.exits[kind]
	if exit == "" {
		exit = "OK"
	}
	f.tasks[upid] = fakeTask{exit: exit, polls: f.polls}

	f.ok(w, upid)
}

// ok answers with data, as Proxmox VE does.
func (f *fakePVE) ok(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"data": data}); err != nil {
		f.t.Error(err)
	}
}

// fail answers with an error as Proxmox VE does: its message in the status
// line, which net/http cannot write, so the connection is taken over. It runs
// on the server's goroutine, so it reports a failure with Error, not Fatal.
func (f *fakePVE) fail(w http.ResponseWriter, status int, message string) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		f.t.Error("the fake's connection cannot be taken over")
		return
	}

	conn, buf, err := hijacker.Hijack()
	if err != nil {
		f.t.Error(err)
		return
	}
	defer func() { _ = conn.Close() }()

	body := `{"data":null}`
	_, _ = fmt.Fprintf(buf, "HTTP/1.1 %d %s\r\nContent-Type: application/json\r\nContent-Length: %d\r\n"+
		"Connection: close\r\n\r\n%s", status, message, len(body), body)
	_ = buf.Flush()
}

// testRunner returns a runner of 2 cores and 4 GiB cloned from template 9000.
func testRunner() RunnerSpec {
	return RunnerSpec{Template: 9000, Cores: 2, Memory: types.Size(4 * gib), JITPath: defaultJITPath}
}

// testMachine returns the machine spec of a runner of scale set.
func testMachine(name, scaleSet string) types.MachineSpec {
	return types.MachineSpec{
		Name:      name,
		Labels:    types.RunnerLabels("gh-test", scaleSet, name, "abc123"),
		JITConfig: "jit-" + name,
		Runner:    testRunner(),
	}
}

// otherRunner is a runner spec of another provider type.
type otherRunner struct{}

func (otherRunner) Describe() string { return "other" }
