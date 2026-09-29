// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package gcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"google.golang.org/api/compute/v1"
	"google.golang.org/api/option"

	"github.com/konradasb/rungar/internal/types"
)

// failure is how the fake fails a zone's inserts: as the request, with an HTTP
// status and reason, or as the operation, with an error code.
type failure struct {
	status int
	reason string
	code   string

	// leaves has a failed operation leave a terminated instance behind.
	leaves bool
}

// fakeCompute is the part of the Compute Engine API the provider calls.
type fakeCompute struct {
	mu sync.Mutex

	// instances are by zone, then name.
	instances map[string]map[string]*compute.Instance

	// failures are how each zone fails inserts, and listFailures lists.
	failures     map[string]failure
	listFailures map[string]int

	// opErrors are the errors each pending operation finishes with.
	opErrors map[string][]*compute.OperationErrorErrors
	ops      int

	// requests are "METHOD path", and filters the list filters, in order.
	requests []string
	filters  []string
}

func newFakeCompute() *fakeCompute {
	return &fakeCompute{
		instances:    map[string]map[string]*compute.Instance{},
		failures:     map[string]failure{},
		listFailures: map[string]int{},
		opErrors:     map[string][]*compute.OperationErrorErrors{},
	}
}

var (
	instancesPath    = regexp.MustCompile(`^/projects/[^/]+/zones/([^/]+)/instances$`)
	instancePath     = regexp.MustCompile(`^/projects/[^/]+/zones/([^/]+)/instances/([^/]+)$`)
	waitPath         = regexp.MustCompile(`^/projects/[^/]+/zones/[^/]+/operations/([^/]+)/wait$`)
	filterTermRegexp = regexp.MustCompile(`\((\S+) = "([^"]*)"\)`)
)

func (f *fakeCompute) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.requests = append(f.requests, r.Method+" "+r.URL.Path)

	switch m := instancesPath.FindStringSubmatch(r.URL.Path); {
	case m != nil && r.Method == http.MethodPost:
		f.insert(w, r, m[1])
		return
	case m != nil && r.Method == http.MethodGet:
		f.list(w, r, m[1])
		return
	}

	if m := instancePath.FindStringSubmatch(r.URL.Path); m != nil && r.Method == http.MethodDelete {
		if _, ok := f.instances[m[1]][m[2]]; !ok {
			writeAPIError(w, http.StatusNotFound, "notFound")
			return
		}
		delete(f.instances[m[1]], m[2])
		writeJSON(w, &compute.Operation{Name: f.nextOp(), Status: operationDone})

		return
	}

	if m := waitPath.FindStringSubmatch(r.URL.Path); m != nil {
		op := &compute.Operation{Name: m[1], Status: operationDone}
		if errs := f.opErrors[m[1]]; len(errs) > 0 {
			op.Error = &compute.OperationError{Errors: errs}
		}
		writeJSON(w, op)

		return
	}

	http.NotFound(w, r)
}

// insert makes an instance in a zone, or fails as the zone is set to.
func (f *fakeCompute) insert(w http.ResponseWriter, r *http.Request, zone string) {
	var inst compute.Instance
	if err := json.NewDecoder(r.Body).Decode(&inst); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid")
		return
	}

	fail, failing := f.failures[zone]
	if failing && fail.status != 0 {
		writeAPIError(w, fail.status, fail.reason)
		return
	}

	op := f.nextOp()
	if failing {
		f.opErrors[op] = []*compute.OperationErrorErrors{{Code: fail.code, Message: "the zone said no"}}
		if !fail.leaves {
			writeJSON(w, &compute.Operation{Name: op, Status: "RUNNING"})
			return
		}
		inst.Status = "TERMINATED"
	} else {
		inst.Status = "PROVISIONING"
	}

	inst.CreationTimestamp = "2026-09-29T10:00:00.000-07:00"
	inst.MachineType = "https://www.googleapis.com/compute/v1/projects/p/" + inst.MachineType
	if f.instances[zone] == nil {
		f.instances[zone] = map[string]*compute.Instance{}
	}
	f.instances[zone][inst.Name] = &inst

	writeJSON(w, &compute.Operation{Name: op, Status: "RUNNING"})
}

// list returns a zone's instances matching the request's filter.
func (f *fakeCompute) list(w http.ResponseWriter, r *http.Request, zone string) {
	if status := f.listFailures[zone]; status != 0 {
		writeAPIError(w, status, "backendError")
		return
	}

	filter := r.URL.Query().Get("filter")
	f.filters = append(f.filters, filter)

	list := &compute.InstanceList{}
	for _, inst := range f.instances[zone] {
		if matchesFilter(inst, filter) {
			list.Items = append(list.Items, inst)
		}
	}
	writeJSON(w, list)
}

// matchesFilter reports whether an instance matches a filter of terms such as
// (labels.k = "v") and (name = "n").
func matchesFilter(inst *compute.Instance, filter string) bool {
	for _, term := range filterTermRegexp.FindAllStringSubmatch(filter, -1) {
		field, want := term[1], term[2]

		switch key, isLabel := strings.CutPrefix(field, "labels."); {
		case isLabel && inst.Labels[key] != want:
			return false
		case field == "name" && inst.Name != want:
			return false
		}
	}

	return true
}

func (f *fakeCompute) nextOp() string {
	f.ops++
	return fmt.Sprintf("op-%d", f.ops)
}

// instance returns the instance of this name in a zone, or nil.
func (f *fakeCompute) instance(zone, name string) *compute.Instance {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.instances[zone][name]
}

// count returns how many requests were "METHOD path" with path matching re.
func (f *fakeCompute) count(method string, re *regexp.Regexp) int {
	f.mu.Lock()
	defer f.mu.Unlock()

	n := 0
	for _, req := range f.requests {
		m, path, _ := strings.Cut(req, " ")
		if m == method && re.MatchString(path) {
			n++
		}
	}

	return n
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// writeAPIError writes an error as Google's APIs do.
func writeAPIError(w http.ResponseWriter, status int, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
		"code":    status,
		"message": "the request said no: " + reason,
		"errors":  []map[string]string{{"reason": reason, "message": "the request said no"}},
	}})
}

// testConfig returns a configuration of three zones in europe-west1.
func testConfig() *Config {
	return &Config{
		Project: "my-ci-project",
		Zones:   []string{"europe-west1-b", "europe-west1-c", "europe-west1-d"},
		Timeout: defaultTimeout,
	}
}

// newTestProvider returns a provider of config over the fake.
func newTestProvider(t *testing.T, config *Config, f *fakeCompute) *Provider {
	t.Helper()

	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)

	p, err := config.open(nil,
		option.WithEndpoint(srv.URL+"/"),
		option.WithoutAuthentication(),
		option.WithHTTPClient(srv.Client()))
	if err != nil {
		t.Fatalf("open() = %v", err)
	}

	return p
}

// testRunner returns a runner block with its defaults.
func testRunner() RunnerSpec {
	return RunnerSpec{MachineType: "e2-standard-4", Image: "runner-image"}.withDefaults()
}

// testMachine returns the machine spec of a runner of scale set, as Rungar
// makes it.
func testMachine(name, scaleSet string) types.MachineSpec {
	return types.MachineSpec{
		Name:      name,
		Labels:    types.RunnerLabels("gh-4e3651f01be5", scaleSet, name, "a1b2c3"),
		JITConfig: "jit-" + name,
		Runner:    testRunner(),
	}
}

// otherRunner is a runner block of another provider type.
type otherRunner struct{}

func (otherRunner) Describe() string { return "other" }
