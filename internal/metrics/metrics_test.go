// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package metrics

import (
	"maps"
	"net/http"
	"net/http/httptest"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"

	"github.com/konradasb/rungar/internal/types"
	"github.com/konradasb/rungar/internal/version"
)

const (
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

// TestCountersCountEachEvent checks each counter adds one for each event
// counted, by its labels.
func TestCountersCountEachEvent(t *testing.T) {
	m := New()

	m.CountRunnerCreated(testSet, "compute1")
	m.CountRunnerCreated(testSet, "compute1")
	m.CountRunnerCreated(testSet, otherProvider)
	m.CountRunnerRemoved(testSet, "compute1", types.RemovalJobCompleted)
	m.CountRunnerLost(testSet, "compute1", types.LossEnded)
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
		{"rungar_runners_lost_total", map[string]string{"scale_set": testSet, "provider": "compute1", "reason": "ended"}, 1},
		{"rungar_scale_up_failures_total", map[string]string{"scale_set": testSet, "reason": "no_capacity"}, 1},
		{"rungar_jobs_started_total", map[string]string{"scale_set": testSet}, 1},
		{"rungar_jobs_completed_total", map[string]string{"scale_set": testSet, "result": "succeeded"}, 1},
		{"rungar_jobs_completed_total", map[string]string{"scale_set": testSet, "result": "failed"}, 1},
	}

	for _, tt := range tests {
		t.Run(tt.metric+labelSuffix(tt.labels), func(t *testing.T) {
			checkValue(t, m, tt.metric, tt.labels, tt.want)
		})
	}
}

// TestDesiredRunnersTakesTheLastValue checks rungar_runners_desired is a
// gauge: the last value set wins rather than accumulating.
func TestDesiredRunnersTakesTheLastValue(t *testing.T) {
	m := New()

	m.SetDesiredRunners(testSet, 3)
	m.SetDesiredRunners(testSet, 7)

	checkValue(t, m, "rungar_runners_desired", map[string]string{"scale_set": testSet}, 7)
}

// TestSetRunnersCountsByStateAndProvider checks a scale set's runners are
// counted by state and by provider.
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
		checkValue(t, m, "rungar_runners_current", map[string]string{"scale_set": testSet, "state": string(state)}, want)
	}

	byProvider := map[string]float64{"compute1": 2, otherProvider: 2}
	for provider, want := range byProvider {
		checkValue(t, m, "rungar_provider_runners", map[string]string{"scale_set": testSet, "provider": provider}, want)
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
		checkValue(t, m, "rungar_runners_current", map[string]string{"scale_set": testSet, "state": string(state)}, 0)
	}
}

// TestSetRunnersForgetsEmptyProviders checks a provider with no runners left
// stops being reported rather than keeping its last count.
func TestSetRunnersForgetsEmptyProviders(t *testing.T) {
	m := New()

	m.SetRunners(testSet, []types.Runner{{Name: "r1", Provider: "compute1", State: types.RunnerIdle}})
	m.SetRunners(testSet, []types.Runner{{Name: "r2", Provider: otherProvider, State: types.RunnerIdle}})

	if got, ok := value(t, m, "rungar_provider_runners",
		map[string]string{"scale_set": testSet, "provider": "compute1"}); ok {
		t.Errorf("compute1 is still reported, at %v, although it has no runners left", got)
	}
	checkValue(t, m, "rungar_provider_runners", map[string]string{"scale_set": testSet, "provider": otherProvider}, 1)
}

// TestSetRunnersKeepsOtherScaleSetsProviders checks that setting one scale
// set's runners leaves another's providers reported.
func TestSetRunnersKeepsOtherScaleSetsProviders(t *testing.T) {
	m := New()

	m.SetRunners(testSet, []types.Runner{{Name: "r1", Provider: "compute1", State: types.RunnerIdle}})
	m.SetRunners("other", []types.Runner{{Name: "r2", Provider: otherProvider, State: types.RunnerIdle}})
	m.SetRunners("other", nil)

	checkValue(t, m, "rungar_provider_runners", map[string]string{"scale_set": testSet, "provider": "compute1"}, 1)
	if got, ok := value(t, m, "rungar_provider_runners",
		map[string]string{"scale_set": "other", "provider": otherProvider}); ok {
		t.Errorf("the other scale set's %s is still reported, at %v, with no runners", otherProvider, got)
	}
}

// TestSetProviderReachabilityRecordsReachability checks
// rungar_provider_reachable is 1 for a reachable provider and 0 for an
// unreachable one.
func TestSetProviderReachabilityRecordsReachability(t *testing.T) {
	m := New()

	m.SetProviderReachability([]types.ProviderSnapshot{
		{Name: "compute1", Reachable: true},
		{Name: otherProvider, Error: "connection refused"},
	})

	checkValue(t, m, "rungar_provider_reachable", map[string]string{"provider": "compute1"}, 1)
	checkValue(t, m, "rungar_provider_reachable", map[string]string{"provider": otherProvider}, 0)
}

// TestSetProviderReachabilityForgetsRemovedOnes checks a provider no longer
// configured is not kept at its last value.
func TestSetProviderReachabilityForgetsRemovedOnes(t *testing.T) {
	m := New()

	m.SetProviderReachability([]types.ProviderSnapshot{{Name: "compute1", Reachable: true}, {Name: "gone", Reachable: true}})
	m.SetProviderReachability([]types.ProviderSnapshot{{Name: "compute1", Reachable: true}})

	if got, ok := value(t, m, "rungar_provider_reachable", map[string]string{"provider": "gone"}); ok {
		t.Errorf("a provider no longer configured is still in the metrics, at %v", got)
	}
	checkValue(t, m, "rungar_provider_reachable", map[string]string{"provider": "compute1"}, 1)
}

// TestReplacedSeriesAreNeverMissingFromAScrape checks a scrape while the
// runners on each provider and the providers' reachability are set again
// always finds the series that stay, rather than catching them reset.
func TestReplacedSeriesAreNeverMissingFromAScrape(t *testing.T) {
	m := New()
	runners := []types.Runner{{Name: "r1", Provider: "compute1", State: types.RunnerIdle}}
	snapshots := []types.ProviderSnapshot{{Name: "compute1", Reachable: true}}
	m.SetRunners(testSet, runners)
	m.SetProviderReachability(snapshots)

	done := make(chan struct{})
	go func() {
		defer close(done)

		for range 500 {
			m.SetRunners(testSet, runners)
			m.SetProviderReachability(snapshots)
		}
	}()

	for range 200 {
		if _, ok := value(t, m, "rungar_provider_runners",
			map[string]string{"scale_set": testSet, "provider": "compute1"}); !ok {
			t.Fatal("a scrape found compute1's runners missing while they were set again")
		}
		if _, ok := value(t, m, "rungar_provider_reachable", map[string]string{"provider": "compute1"}); !ok {
			t.Fatal("a scrape found compute1's reachability missing while it was set again")
		}
	}
	<-done
}

// TestHandlerServesRungarAndGoMetrics checks the endpoint serves Rungar's
// metrics and the Go collector's.
func TestHandlerServesRungarAndGoMetrics(t *testing.T) {
	m := New()
	m.CountJobStarted(testSet)

	served := map[string]bool{}
	for line := range strings.Lines(scrape(t, m)) {
		if strings.HasPrefix(line, "#") {
			continue
		}
		name, _, _ := strings.Cut(strings.TrimSpace(line), " ")
		name, _, _ = strings.Cut(name, "{")
		served[name] = true
	}

	for _, want := range []string{
		"rungar_jobs_started_total",
		"go_goroutines", // the Go collector is registered deliberately
	} {
		if !served[want] {
			t.Errorf("the endpoint serves no sample of %s", want)
		}
	}
}

// TestTwoMetricsShareNothing checks two Metrics do not share state, as they
// would through the default registry.
func TestTwoMetricsShareNothing(t *testing.T) {
	a, b := New(), New()

	a.CountJobStarted(testSet)

	if got, ok := value(t, b, "rungar_jobs_started_total", map[string]string{"scale_set": testSet}); ok {
		t.Errorf("a second Metrics saw %v jobs, want none", got)
	}
}

// TestDurationsAreObservedInSeconds checks every histogram of a duration
// records it in seconds.
func TestDurationsAreObservedInSeconds(t *testing.T) {
	m := New()

	m.ObserveJobWait(testSet, 90*time.Second, 30*time.Second)
	m.ObserveJobWait(testSet, 10*time.Second, 5*time.Second)
	m.ObserveRunnerCreateDuration(testSet, "compute1", 1500*time.Millisecond)
	m.ObserveRunnerBootDuration(testSet, "compute1", 40*time.Second)
	m.ObserveReconcile(testSet, time.Now(), 2*time.Second, true)
	m.ObserveProviderCall("compute1", "list", "ok", 250*time.Millisecond)

	set := map[string]string{"scale_set": testSet}
	onProvider := map[string]string{"scale_set": testSet, "provider": "compute1"}

	tests := []struct {
		metric string
		labels map[string]string
		want   float64
	}{
		{"rungar_job_wait_seconds", set, 100},
		{"rungar_job_runner_wait_seconds", set, 35},
		{"rungar_runner_create_duration_seconds", onProvider, 1.5},
		{"rungar_runner_boot_duration_seconds", onProvider, 40},
		{"rungar_reconcile_duration_seconds", set, 2},
		{"rungar_provider_call_duration_seconds", map[string]string{"provider": "compute1", "call": "list"}, 0.25},
	}

	for _, tt := range tests {
		t.Run(tt.metric, func(t *testing.T) {
			checkValue(t, m, tt.metric, tt.labels, tt.want)
		})
	}
}

// TestProviderCallsAreCountedByResult checks provider calls are counted by
// their result, and a result never seen is not served.
func TestProviderCallsAreCountedByResult(t *testing.T) {
	m := New()

	m.ObserveProviderCall("compute1", "create", "ok", time.Second)
	m.ObserveProviderCall("compute1", "create", "no_capacity", time.Second)
	m.ObserveProviderCall("compute1", "create", "no_capacity", time.Second)

	at := func(result string) map[string]string {
		return map[string]string{"provider": "compute1", "call": "create", "result": result}
	}
	checkValue(t, m, "rungar_provider_calls_total", at("ok"), 1)
	checkValue(t, m, "rungar_provider_calls_total", at("no_capacity"), 2)
	if got, ok := value(t, m, "rungar_provider_calls_total", at("error")); ok {
		t.Errorf("calls with result error = %v, want none served", got)
	}
}

// TestLastReconcileMovesOnlyWhenTheFleetWasListed checks a reconciliation that
// could not list the fleet is timed, but does not count as the last one.
func TestLastReconcileMovesOnlyWhenTheFleetWasListed(t *testing.T) {
	m := New()
	at := time.Unix(1_800_000_000, 0)
	set := map[string]string{"scale_set": testSet}

	m.ObserveReconcile(testSet, at, time.Second, true)
	m.ObserveReconcile(testSet, at.Add(time.Minute), time.Second, false)

	// The last reconcile is the one that listed the fleet; the time is both
	// reconciliations'.
	checkValue(t, m, "rungar_scale_set_last_reconcile_timestamp_seconds", set, 1_800_000_000)
	checkValue(t, m, "rungar_reconcile_duration_seconds", set, 2)
}

// TestBuildInfoIsOneLabelledWithTheBuild checks rungar_build_info is 1, with
// the build in its labels.
func TestBuildInfoIsOneLabelledWithTheBuild(t *testing.T) {
	m := New()

	labels := map[string]string{"version": version.Version, "commit": version.Commit, "go_version": runtime.Version()}
	checkValue(t, m, "rungar_build_info", labels, 1)
}

// TestGitHubRequestsAreCountedByCode checks GitHub requests are counted by
// the status they were answered with.
func TestGitHubRequestsAreCountedByCode(t *testing.T) {
	m := New()

	m.CountGitHubRequest("200")
	m.CountGitHubRequest("200")
	m.CountGitHubRequest("401")
	m.CountGitHubRequest("error")

	for code, want := range map[string]float64{"200": 2, "401": 1, "error": 1} {
		checkValue(t, m, "rungar_github_requests_total", map[string]string{"code": code}, want)
	}
}

// TestPausedIsOneOrZero checks rungar_scale_set_paused is 1 while a scale set
// is paused and 0 once it is resumed.
func TestPausedIsOneOrZero(t *testing.T) {
	m := New()

	m.SetPaused(testSet, true)
	checkValue(t, m, "rungar_scale_set_paused", map[string]string{"scale_set": testSet}, 1)

	m.SetPaused(testSet, false)
	checkValue(t, m, "rungar_scale_set_paused", map[string]string{"scale_set": testSet}, 0)
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
	m.CountRunnerLost(testSet, "compute1", types.LossEnded)
	m.CountScaleUpFailed(testSet, "error")
	m.CountJobStarted(testSet)
	m.CountJobCompleted(testSet, "succeeded")
	m.SetJobsAssigned(testSet, 1)
	m.SetMinRunners(testSet, 1)
	m.ObserveJobWait(testSet, time.Minute, time.Second)
	m.ObserveRunnerCreateDuration(testSet, "compute1", time.Second)
	m.ObserveRunnerBootDuration(testSet, "compute1", time.Minute)
	m.SetLastPoll(testSet, time.Now())
	m.ObserveReconcile(testSet, time.Now(), time.Second, true)
	m.SetProviderReachability([]types.ProviderSnapshot{{Name: "compute1", Reachable: true}})
	m.ObserveProviderCall("compute1", "create", "ok", time.Second)

	listed := map[string]Description{}
	for _, d := range m.Reference() {
		listed[d.Name] = d

		if d.Help == "" || d.Group == "" || d.Type == "" {
			t.Errorf("%s: help, group and type are all required: %+v", d.Name, d)
		}
		if (d.Type == TypeHistogram) != (len(d.Buckets) > 0) {
			t.Errorf("%s: a histogram, and only a histogram, has buckets: %+v", d.Name, d)
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
		if want := Type(strings.ToLower(f.GetType().String())); d.Type != want {
			t.Errorf("%s is a %s in the reference, and served as a %s", d.Name, d.Type, want)
		}
	}

	if served != len(listed) {
		t.Errorf("%d metrics served, %d in the reference: this test records into too few", served, len(listed))
	}
}

// value reads one metric with the given labels out of m: a histogram's sum.
// It reports false if m serves no such metric.
func value(t *testing.T, m *Metrics, name string, labels map[string]string) (float64, bool) {
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
				return c.GetValue(), true
			}
			if g := metric.GetGauge(); g != nil {
				return g.GetValue(), true
			}
			if h := metric.GetHistogram(); h != nil {
				return h.GetSampleSum(), true
			}
		}
	}

	return 0, false
}

// checkValue checks that m serves the metric with the given labels, at want.
func checkValue(t *testing.T, m *Metrics, name string, labels map[string]string, want float64) {
	t.Helper()

	got, ok := value(t, m, name, labels)
	if !ok {
		t.Errorf("%s%s is not served, want %v", name, labelSuffix(labels), want)
		return
	}
	if got != want {
		t.Errorf("%s%s = %v, want %v", name, labelSuffix(labels), got, want)
	}
}

// hasLabels reports whether pairs include every label in want.
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

// labelSuffix returns labels as a test name's suffix, sorted by name.
func labelSuffix(labels map[string]string) string {
	var b strings.Builder
	for _, k := range slices.Sorted(maps.Keys(labels)) {
		b.WriteString("/" + k + "=" + labels[k])
	}

	return b.String()
}
