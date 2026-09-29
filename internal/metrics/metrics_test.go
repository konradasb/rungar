// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package metrics

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	dto "github.com/prometheus/client_model/go"

	"github.com/konradasb/rungar/internal/types"
	"github.com/konradasb/rungar/internal/version"
)

const (
	gib           = 1 << 30
	testSet       = "rungar-vm"
	otherProvider = "compute2"
)

// scrape returns the metrics as the endpoint serves them.
func scrape(t *testing.T, m *Metrics) string {
	t.Helper()

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil)
	m.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("metrics endpoint returned %d", rec.Code)
	}

	return rec.Body.String()
}

func TestCountersRecord(t *testing.T) {
	m := New()

	m.CountRunnerCreated(testSet, "compute1")
	m.CountRunnerCreated(testSet, "compute1")
	m.CountRunnerCreated(testSet, otherProvider)
	m.CountRunnerRemoved(testSet, "compute1", types.RemovalJobCompleted)
	m.CountScaleUpFailed(testSet, "no_capacity")
	m.CountJobStarted(testSet)
	m.CountJobCompleted(testSet, "succeeded")
	m.CountJobCompleted(testSet, "failed")

	tests := []struct {
		metric string
		labels map[string]string
		want   float64
	}{
		{"rungar_runners_created_total", map[string]string{"scale_set": testSet, "provider": "compute1"}, 2},
		{"rungar_runners_created_total", map[string]string{"scale_set": testSet, "provider": otherProvider}, 1},
		{"rungar_runners_removed_total", map[string]string{"scale_set": testSet, "provider": "compute1", "reason": "job_completed"}, 1},
		{"rungar_scale_up_failures_total", map[string]string{"scale_set": testSet, "reason": "no_capacity"}, 1},
		{"rungar_jobs_started_total", map[string]string{"scale_set": testSet}, 1},
		{"rungar_jobs_completed_total", map[string]string{"scale_set": testSet, "result": "succeeded"}, 1},
		{"rungar_jobs_completed_total", map[string]string{"scale_set": testSet, "result": "failed"}, 1},
	}

	for _, tt := range tests {
		t.Run(tt.metric+labelSuffix(tt.labels), func(t *testing.T) {
			if got := value(t, m, tt.metric, tt.labels); got != tt.want {
				t.Errorf("%s = %v, want %v", tt.metric, got, tt.want)
			}
		})
	}
}

func TestDesiredRunners(t *testing.T) {
	m := New()

	m.SetDesiredRunners(testSet, 3)
	m.SetDesiredRunners(testSet, 7)

	// A gauge, so the last value wins rather than accumulating.
	if got := value(t, m, "rungar_runners_desired", map[string]string{"scale_set": testSet}); got != 7 {
		t.Errorf("rungar_runners_desired = %v, want 7", got)
	}
}

func TestSetRunnersCountsByStateAndProvider(t *testing.T) {
	m := New()

	m.SetRunners(testSet, []types.Runner{
		{Name: "r1", Provider: "compute1", State: types.RunnerBusy},
		{Name: "r2", Provider: "compute1", State: types.RunnerIdle},
		{Name: "r3", Provider: otherProvider, State: types.RunnerIdle},
		{Name: "r4", Provider: otherProvider, State: types.RunnerStarting},
	})

	byState := map[types.RunnerState]float64{
		types.RunnerBusy:     1,
		types.RunnerIdle:     2,
		types.RunnerStarting: 1,
	}
	for state, want := range byState {
		labels := map[string]string{"scale_set": testSet, "state": string(state)}
		if got := value(t, m, "rungar_runners_current", labels); got != want {
			t.Errorf("runners in state %s = %v, want %v", state, got, want)
		}
	}

	byProvider := map[string]float64{"compute1": 2, otherProvider: 2}
	for provider, want := range byProvider {
		labels := map[string]string{"scale_set": testSet, "provider": provider}
		if got := value(t, m, "rungar_runners_by_provider", labels); got != want {
			t.Errorf("runners on %s = %v, want %v", provider, got, want)
		}
	}
}

// TestSetRunnersZeroesEmptyStates checks a state with no runners left reads
// zero rather than its last value.
func TestSetRunnersZeroesEmptyStates(t *testing.T) {
	m := New()

	m.SetRunners(testSet, []types.Runner{
		{Name: "r1", Provider: "compute1", State: types.RunnerBusy},
	})
	m.SetRunners(testSet, nil)

	for _, state := range []types.RunnerState{types.RunnerBusy, types.RunnerIdle, types.RunnerStarting} {
		labels := map[string]string{"scale_set": testSet, "state": string(state)}
		if got := value(t, m, "rungar_runners_current", labels); got != 0 {
			t.Errorf("runners in state %s = %v, want 0 once the fleet is empty", state, got)
		}
	}
}

// TestSetRunnersForgetsEmptyProviders checks a provider with no runners left
// stops being reported rather than keeping its last count.
func TestSetRunnersForgetsEmptyProviders(t *testing.T) {
	m := New()

	m.SetRunners(testSet, []types.Runner{{Name: "r1", Provider: "compute1", State: types.RunnerIdle}})
	m.SetRunners(testSet, []types.Runner{{Name: "r2", Provider: otherProvider, State: types.RunnerIdle}})

	body := scrape(t, m)
	if strings.Contains(body, `rungar_runners_by_provider{provider="compute1"`) {
		t.Error("compute1 is still reported although it has no runners left")
	}
	if !strings.Contains(body, otherProvider) {
		t.Errorf("%s is not reported although it has a runner", otherProvider)
	}
}

func TestSetProviders(t *testing.T) {
	m := New()

	m.SetProviders([]types.ProviderSnapshot{
		{Name: "compute1", Reachable: true},
		{Name: otherProvider, Err: "connection refused"},
	})

	at := func(name string) map[string]string { return map[string]string{"provider": name} }

	if got := value(t, m, "rungar_provider_reachable", at("compute1")); got != 1 {
		t.Errorf("compute1 reachable = %v, want 1", got)
	}
	if got := value(t, m, "rungar_provider_reachable", at(otherProvider)); got != 0 {
		t.Errorf("%s reachable = %v, want 0", otherProvider, got)
	}
	if body := scrape(t, m); strings.Contains(body, "rungar_provider_passed_over") {
		t.Error("a provider passed over by every scale set is still reported, although none is")
	}

	// What a provider has spare is its own to describe, and not a metric.
	if body := scrape(t, m); strings.Contains(body, "rungar_provider_available") {
		t.Error("a provider's spare room is still reported as a metric")
	}
}

// TestSetProvidersForgetsRemovedOnes checks a provider no longer configured
// is not kept at its last value.
func TestSetProvidersForgetsRemovedOnes(t *testing.T) {
	m := New()

	m.SetProviders([]types.ProviderSnapshot{{Name: "compute1", Reachable: true}, {Name: "gone", Reachable: true}})
	m.SetProviders([]types.ProviderSnapshot{{Name: "compute1", Reachable: true}})

	if strings.Contains(scrape(t, m), `provider="gone"`) {
		t.Error("a provider no longer configured is still in the metrics")
	}
}

// TestHandlerServesRungarAndGoMetrics checks the endpoint serves Rungar's
// metrics and the Go collector's.
func TestHandlerServesRungarAndGoMetrics(t *testing.T) {
	m := New()
	m.CountJobStarted(testSet)

	body := scrape(t, m)

	for _, want := range []string{
		"rungar_jobs_started_total",
		"go_goroutines", // the Go collector is registered deliberately
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the endpoint does not serve %s", want)
		}
	}
}

// TestSeparateRegistries checks two Metrics do not share state, as they would
// through the default registry.
func TestSeparateRegistries(t *testing.T) {
	a, b := New(), New()

	a.CountJobStarted(testSet)

	if got := value(t, b, "rungar_jobs_started_total", map[string]string{"scale_set": testSet}); got != 0 {
		t.Errorf("a second Metrics saw %v jobs, want 0", got)
	}
}

// value reads one metric with the given labels out of m.
func value(t *testing.T, m *Metrics, name string, labels map[string]string) float64 {
	t.Helper()

	families, err := m.registry.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	for _, family := range families {
		if family.GetName() != name {
			continue
		}

		for _, metric := range family.GetMetric() {
			if !hasLabels(metric.GetLabel(), labels) {
				continue
			}
			if c := metric.GetCounter(); c != nil {
				return c.GetValue()
			}
			if g := metric.GetGauge(); g != nil {
				return g.GetValue()
			}
		}
	}

	return 0
}

func hasLabels(pairs []*dto.LabelPair, want map[string]string) bool {
	got := make(map[string]string, len(pairs))
	for _, p := range pairs {
		got[p.GetName()] = p.GetValue()
	}

	for k, v := range want {
		if got[k] != v {
			return false
		}
	}

	return true
}

func labelSuffix(labels map[string]string) string {
	var b strings.Builder
	for k, v := range labels {
		b.WriteString("/" + k + "=" + v)
	}

	return b.String()
}

func TestBuildInfo(t *testing.T) {
	m := New()

	labels := map[string]string{"version": version.Version, "commit": version.Commit, "go_version": runtime.Version()}
	if got := value(t, m, "rungar_build_info", labels); got != 1 {
		t.Errorf("rungar_build_info = %v, want 1", got)
	}
}

func TestGitHubRequests(t *testing.T) {
	m := New()

	m.CountGitHubRequest("200")
	m.CountGitHubRequest("200")
	m.CountGitHubRequest("401")
	m.CountGitHubRequest("error")

	for code, want := range map[string]float64{"200": 2, "401": 1, "error": 1} {
		if got := value(t, m, "rungar_github_requests_total", map[string]string{"code": code}); got != want {
			t.Errorf("rungar_github_requests_total{code=%q} = %v, want %v", code, got, want)
		}
	}
}

func TestPausedIsOneOrZero(t *testing.T) {
	m := New()

	m.SetPaused(testSet, true)
	if got := value(t, m, "rungar_scale_set_paused", map[string]string{"scale_set": testSet}); got != 1 {
		t.Errorf("paused = %v, want 1", got)
	}

	m.SetPaused(testSet, false)
	if got := value(t, m, "rungar_scale_set_paused", map[string]string{"scale_set": testSet}); got != 0 {
		t.Errorf("resumed = %v, want 0", got)
	}
}

// TestReferenceListsEverything checks every served Rungar metric is in the
// reference, with a description.
func TestReferenceListsEverything(t *testing.T) {
	m := New()

	// Every vector is empty until something is recorded, and an empty one
	// is not served.
	m.CountGitHubRequest("200")
	m.SetPaused(testSet, false)
	m.SetDesiredRunners(testSet, 1)
	m.SetRunners(testSet, []types.Runner{{Name: "r1", Provider: "compute1", State: types.RunnerIdle}})
	m.CountRunnerCreated(testSet, "compute1")
	m.CountRunnerRemoved(testSet, "compute1", types.RemovalJobCompleted)
	m.CountScaleUpFailed(testSet, "error")
	m.CountJobStarted(testSet)
	m.CountJobCompleted(testSet, "succeeded")
	m.SetProviders([]types.ProviderSnapshot{{Name: "compute1", Reachable: true}})

	listed := map[string]Description{}
	for _, d := range m.Reference() {
		listed[d.Name] = d

		if d.Help == "" || d.Group == "" || d.Type == "" {
			t.Errorf("%s: help, group and type are all required: %+v", d.Name, d)
		}
	}

	families, err := m.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}

	served := 0
	for _, f := range families {
		if !strings.HasPrefix(f.GetName(), "rungar_") {
			continue
		}
		served++

		d, ok := listed[f.GetName()]
		if !ok {
			t.Errorf("%s is served but not in the reference", f.GetName())
			continue
		}
		if want := strings.ToLower(f.GetType().String()); d.Type != want {
			t.Errorf("%s is a %s in the reference, and served as a %s", d.Name, d.Type, want)
		}
	}

	if served != len(listed) {
		t.Errorf("%d metrics served, %d in the reference: this test records into too few", served, len(listed))
	}
}
