// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package gcp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/option"

	"github.com/konradasb/rungar/internal/provider"
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

	// failures are how each zone fails inserts, by zone, or of one machine
	// type, by zone/type; listFailures are how each zone fails lists.
	failures     map[string]failure
	listFailures map[string]int

	// waitFailures are the HTTP statuses each zone's next waits for an
	// operation fail with, in turn; runningWaits are how many of its next
	// waits answer before the operation is done.
	waitFailures map[string][]int
	runningWaits map[string]int

	// hangingInserts are the zones whose inserts never answer.
	hangingInserts map[string]bool

	// lostAnswers are how many inserts each zone carries out but answers
	// with a server error, as if the answer were lost on the way.
	lostAnswers map[string]int

	// insertOperations are the operations inserts started, by request ID, as
	// Compute Engine remembers them to answer a repeat with.
	insertOperations map[string]string

	// notOffered are the machine types each zone lacks, as zone/type, and
	// machineTypeFailure how asking about one fails, as an HTTP status.
	notOffered         map[string]bool
	machineTypeFailure int

	// disks are by zone, then name: those of instances created from templates.
	// diskLabelFailure is how setting their labels fails, as an HTTP status,
	// and diskDelay how long each request about one takes to answer.
	disks            map[string]map[string]*compute.Disk
	diskLabelFailure int
	diskDelay        time.Duration

	// instanceTemplates are the instance templates, by path from projects/, and
	// sources the template each instance was created from, by name.
	instanceTemplates map[string]*compute.InstanceTemplate
	sources           map[string]string

	// vanishing are the instances that go while being deleted, as a Spot
	// instance Compute Engine takes back does: the deletion's operation
	// finds them gone.
	vanishing map[string]bool

	// operationErrors are the errors each pending operation finishes with.
	operationErrors map[string][]*compute.OperationErrorErrors
	operations      int

	// requests are "METHOD path", and filters the list filters, in order.
	requests []string
	filters  []string
}

func newFakeCompute() *fakeCompute {
	return &fakeCompute{
		instances:         map[string]map[string]*compute.Instance{},
		failures:          map[string]failure{},
		listFailures:      map[string]int{},
		waitFailures:      map[string][]int{},
		runningWaits:      map[string]int{},
		hangingInserts:    map[string]bool{},
		lostAnswers:       map[string]int{},
		insertOperations:  map[string]string{},
		notOffered:        map[string]bool{},
		disks:             map[string]map[string]*compute.Disk{},
		instanceTemplates: map[string]*compute.InstanceTemplate{},
		sources:           map[string]string{},
		vanishing:         map[string]bool{},
		operationErrors:   map[string][]*compute.OperationErrorErrors{},
	}
}

var (
	instancesPath        = regexp.MustCompile(`^/projects/[^/]+/zones/([^/]+)/instances$`)
	instancePath         = regexp.MustCompile(`^/projects/[^/]+/zones/([^/]+)/instances/([^/]+)$`)
	waitPath             = regexp.MustCompile(`^/projects/[^/]+/zones/([^/]+)/operations/([^/]+)/wait$`)
	machineTypePath      = regexp.MustCompile(`^/projects/[^/]+/zones/([^/]+)/machineTypes/([^/]+)$`)
	diskPath             = regexp.MustCompile(`^/projects/[^/]+/zones/([^/]+)/disks/([^/]+)(/setLabels)?$`)
	instanceTemplatePath = regexp.MustCompile(`^/(projects/[^/]+/(?:global|regions/[^/]+)/instanceTemplates/[^/]+)$`)
	filterTermRegexp     = regexp.MustCompile(`\((\S+) = "([^"]*)"\)`)
)

func (f *fakeCompute) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)

	// A hanging insert waits, without holding the lock, until its caller
	// gives up, which the server notices only once the body is read.
	if m := instancesPath.FindStringSubmatch(r.URL.Path); m != nil && r.Method == http.MethodPost &&
		f.hangingInserts[m[1]] {
		f.mu.Unlock()
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
		return
	}

	// A slow disk request is slow without holding the lock.
	if delay := f.diskDelay; delay > 0 && diskPath.MatchString(r.URL.Path) {
		f.mu.Unlock()
		time.Sleep(delay)
		f.mu.Lock()
	}
	defer f.mu.Unlock()

	switch m := instancesPath.FindStringSubmatch(r.URL.Path); {
	case m != nil && r.Method == http.MethodPost:
		f.insert(w, r, m[1])
		return
	case m != nil && r.Method == http.MethodGet:
		f.list(w, r, m[1])
		return
	}

	if m := machineTypePath.FindStringSubmatch(r.URL.Path); m != nil && r.Method == http.MethodGet {
		if f.machineTypeFailure != 0 {
			writeAPIError(w, f.machineTypeFailure, "backendError")
			return
		}
		if f.notOffered[m[1]+"/"+m[2]] {
			writeAPIError(w, http.StatusNotFound, "notFound")
			return
		}
		writeJSON(w, &compute.MachineType{Name: m[2], Zone: m[1]})

		return
	}

	if m := diskPath.FindStringSubmatch(r.URL.Path); m != nil {
		f.disk(w, r, m[1], m[2], m[3] != "")
		return
	}

	if m := instancePath.FindStringSubmatch(r.URL.Path); m != nil && r.Method == http.MethodGet {
		instance, ok := f.instances[m[1]][m[2]]
		if !ok {
			writeAPIError(w, http.StatusNotFound, "notFound")
			return
		}
		writeJSON(w, instance)

		return
	}

	if m := instancePath.FindStringSubmatch(r.URL.Path); m != nil && r.Method == http.MethodDelete {
		if _, ok := f.instances[m[1]][m[2]]; !ok {
			writeAPIError(w, http.StatusNotFound, "notFound")
			return
		}
		delete(f.instances[m[1]], m[2])

		operation := f.nextOperation()
		if f.vanishing[m[2]] {
			f.operationErrors[operation] = []*compute.OperationErrorErrors{{Code: "RESOURCE_NOT_FOUND", Message: "gone"}}
			writeJSON(w, &compute.Operation{Name: operation, Status: "RUNNING"})
			return
		}
		writeJSON(w, &compute.Operation{Name: operation, Status: operationDone})

		return
	}

	if m := instanceTemplatePath.FindStringSubmatch(r.URL.Path); m != nil && r.Method == http.MethodGet {
		instanceTemplate, ok := f.instanceTemplates[m[1]]
		if !ok {
			writeAPIError(w, http.StatusNotFound, "notFound")
			return
		}
		writeJSON(w, instanceTemplate)

		return
	}

	if m := waitPath.FindStringSubmatch(r.URL.Path); m != nil {
		f.wait(w, m[1], m[2])
		return
	}

	http.NotFound(w, r)
}

// insert creates an instance in a zone, or fails as the zone is set to. A
// request whose ID an earlier insert had is answered with that insert's
// operation, creating nothing, and one of a name the zone has is refused, as
// Compute Engine does.
func (f *fakeCompute) insert(w http.ResponseWriter, r *http.Request, zone string) {
	var instance compute.Instance
	if err := json.NewDecoder(r.Body).Decode(&instance); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid")
		return
	}

	requestID := r.URL.Query().Get("requestId")
	if _, err := uuid.Parse(requestID); requestID != "" && err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid")
		return
	}
	if operation, ok := f.insertOperations[requestID]; ok && requestID != "" {
		f.answerInsert(w, zone, operation)
		return
	}
	if _, ok := f.instances[zone][instance.Name]; ok {
		writeAPIError(w, http.StatusConflict, "alreadyExists")
		return
	}

	fail, failing := f.failures[zone+"/"+path.Base(instance.MachineType)]
	if !failing {
		fail, failing = f.failures[zone]
	}
	if failing && fail.status != 0 {
		writeAPIError(w, fail.status, fail.reason)
		return
	}

	operation := f.nextOperation()
	if requestID != "" {
		f.insertOperations[requestID] = operation
	}
	if failing {
		f.operationErrors[operation] = []*compute.OperationErrorErrors{{Code: fail.code, Message: "the zone said no"}}
		if !fail.leaves {
			writeJSON(w, &compute.Operation{Name: operation, Status: "RUNNING"})
			return
		}
		instance.Status = "TERMINATED"
	} else {
		instance.Status = "PROVISIONING"
	}

	if source := r.URL.Query().Get("sourceInstanceTemplate"); source != "" {
		f.sources[instance.Name] = source
		if instanceTemplate := f.instanceTemplates[source]; instanceTemplate != nil {
			if instance.MachineType == "" {
				instance.MachineType = "zones/" + zone + "/machineTypes/" + instanceTemplate.Properties.MachineType
			}
			f.attachInstanceTemplateDisks(&instance, zone, instanceTemplate.Properties.Disks)
		}
	}

	instance.CreationTimestamp = "2026-09-29T10:00:00.000-07:00"
	instance.MachineType = "https://www.googleapis.com/compute/v1/projects/p/" + instance.MachineType
	if f.instances[zone] == nil {
		f.instances[zone] = map[string]*compute.Instance{}
	}
	f.instances[zone][instance.Name] = &instance

	f.answerInsert(w, zone, operation)
}

// answerInsert answers an insert with its operation, unless the zone is set
// to lose the answer.
func (f *fakeCompute) answerInsert(w http.ResponseWriter, zone, operation string) {
	if f.lostAnswers[zone] > 0 {
		f.lostAnswers[zone]--
		writeAPIError(w, http.StatusServiceUnavailable, "backendError")
		return
	}

	writeJSON(w, &compute.Operation{Name: operation, Status: "RUNNING"})
}

// wait answers a wait for an operation in a zone, or fails as the zone is set
// to. An operation the fake never started is not found, as Compute Engine
// answers one it does not know.
func (f *fakeCompute) wait(w http.ResponseWriter, zone, name string) {
	if statuses := f.waitFailures[zone]; len(statuses) > 0 {
		f.waitFailures[zone] = statuses[1:]
		writeAPIError(w, statuses[0], "backendError")
		return
	}

	var n int
	if _, err := fmt.Sscanf(name, "op-%d", &n); err != nil || n < 1 || n > f.operations {
		writeAPIError(w, http.StatusNotFound, "notFound")
		return
	}

	if f.runningWaits[zone] > 0 {
		f.runningWaits[zone]--
		writeJSON(w, &compute.Operation{Name: name, Status: "RUNNING"})
		return
	}

	operation := &compute.Operation{Name: name, Status: operationDone}
	if errs := f.operationErrors[name]; len(errs) > 0 {
		operation.Error = &compute.OperationError{Errors: errs}
	}
	writeJSON(w, operation)
}

// attachInstanceTemplateDisks creates an instance's disks from its template's,
// with the template's labels alone, as Compute Engine does.
func (f *fakeCompute) attachInstanceTemplateDisks(instance *compute.Instance, zone string, disks []*compute.AttachedDisk) {
	if f.disks[zone] == nil {
		f.disks[zone] = map[string]*compute.Disk{}
	}

	for i, d := range disks {
		if d.Type == "SCRATCH" {
			instance.Disks = append(instance.Disks, &compute.AttachedDisk{Type: "SCRATCH"})
			continue
		}

		name := fmt.Sprintf("%s-%d", instance.Name, i)
		disk := &compute.Disk{Name: name, LabelFingerprint: "fp-0"}
		if d.InitializeParams != nil {
			disk.Labels = d.InitializeParams.Labels
		}
		f.disks[zone][name] = disk
		instance.Disks = append(instance.Disks, &compute.AttachedDisk{
			Source: "https://www.googleapis.com/compute/v1/projects/p/zones/" + zone + "/disks/" + name,
		})
	}
}

// disk returns a disk, or sets its labels if the fingerprint is its own.
func (f *fakeCompute) disk(w http.ResponseWriter, r *http.Request, zone, name string, setLabels bool) {
	disk, ok := f.disks[zone][name]
	if !ok {
		writeAPIError(w, http.StatusNotFound, "notFound")
		return
	}
	if !setLabels {
		writeJSON(w, disk)
		return
	}

	if f.diskLabelFailure != 0 {
		writeAPIError(w, f.diskLabelFailure, "forbidden")
		return
	}

	var req compute.ZoneSetLabelsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.LabelFingerprint != disk.LabelFingerprint {
		writeAPIError(w, http.StatusPreconditionFailed, "conditionNotMet")
		return
	}
	disk.Labels = req.Labels
	disk.LabelFingerprint += "+"
	writeJSON(w, &compute.Operation{Name: f.nextOperation(), Status: operationDone})
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
	for _, instance := range f.instances[zone] {
		if matchesFilter(instance, filter) {
			list.Items = append(list.Items, instance)
		}
	}
	writeJSON(w, list)
}

// matchesFilter reports whether an instance matches a filter of terms such as
// (labels.k = "v") and (name = "n").
func matchesFilter(instance *compute.Instance, filter string) bool {
	for _, term := range filterTermRegexp.FindAllStringSubmatch(filter, -1) {
		field, want := term[1], term[2]

		switch key, isLabel := strings.CutPrefix(field, "labels."); {
		case isLabel && instance.Labels[key] != want:
			return false
		case field == "name" && instance.Name != want:
			return false
		}
	}

	return true
}

func (f *fakeCompute) nextOperation() string {
	f.operations++
	return fmt.Sprintf("op-%d", f.operations)
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

// metadataValue returns an instance's metadata item, or "".
func metadataValue(instance *compute.Instance, key string) string {
	for _, item := range instance.Metadata.Items {
		if item.Key == key && item.Value != nil {
			return *item.Value
		}
	}

	return ""
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

	server := httptest.NewServer(f)
	t.Cleanup(server.Close)

	p, err := config.open(t.Context(), nil,
		option.WithEndpoint(server.URL+"/"),
		option.WithoutAuthentication(),
		option.WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("open() = %v", err)
	}
	p.retryDelay = time.Millisecond

	return p
}

// testRunner returns a runner block with its defaults.
func testRunner() RunnerSpec {
	return RunnerSpec{MachineTypes: provider.OneOrMore{"e2-standard-4"}, Image: "runner-image"}.withDefaults()
}

// testMachine returns the machine spec of a runner of scale set, as Rungar
// creates it.
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
