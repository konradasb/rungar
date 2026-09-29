// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

// Package metrics is the daemon's Prometheus metrics. They are always
// recorded, and served only when the configuration enables the endpoint.
package metrics

import (
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/konradasb/rungar/internal/types"
	"github.com/konradasb/rungar/internal/version"
)

// Group is a section of the reference a metric is listed in.
type Group string

// The groups metrics are listed in.
const (
	GroupDaemon    Group = "daemon"
	GroupGitHub    Group = "github"
	GroupScaleSets Group = "scale_sets"
	GroupProviders Group = "providers"
)

// Type is a kind of Prometheus metric, as the reference names it.
type Type string

// The types of metric Rungar serves.
const (
	TypeCounter   Type = "counter"
	TypeGauge     Type = "gauge"
	TypeHistogram Type = "histogram"
)

// Description describes one of Rungar's metrics. Every metric is registered
// through one, so the generated reference lists exactly what is served.
type Description struct {
	// Name is the metric's full name.
	Name string

	// Type is the metric's type.
	Type Type

	// Labels are the metric's label names.
	Labels []string

	// Help is the metric's HELP: one plain sentence.
	Help string

	// Doc is what the reference adds to Help, in Markdown.
	Doc string

	// Group is the section of the reference the metric is listed in.
	Group Group

	// Buckets are a histogram's upper bounds, in seconds.
	Buckets []float64
}

// waitBuckets bound the waits a person notices: a job waiting for a runner, a
// runner booting. They reach past the longest start_timeout worth setting.
var waitBuckets = []float64{5, 10, 15, 20, 30, 45, 60, 90, 120, 180, 300, 600, 900, 1800}

// callBuckets bound a call to a backend or a pass of reconciliation, from a
// quick answer to a slow machine clone.
var callBuckets = []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300}

// Metrics holds the daemon's metrics, in a registry of their own with the Go
// and process collectors.
type Metrics struct {
	registry *prometheus.Registry

	// reference is every metric of Rungar's own, in the order registered.
	reference []Description

	githubRequests *prometheus.CounterVec

	paused          *prometheus.GaugeVec
	minRunners      *prometheus.GaugeVec
	desiredRunners  *prometheus.GaugeVec
	currentRunners  *prometheus.GaugeVec
	providerRunners *prometheus.GaugeVec

	runnersCreated  *prometheus.CounterVec
	runnersRemoved  *prometheus.CounterVec
	runnersLost     *prometheus.CounterVec
	scaleUpFailures *prometheus.CounterVec

	jobsStarted   *prometheus.CounterVec
	jobsCompleted *prometheus.CounterVec
	jobsAssigned  *prometheus.GaugeVec
	jobWait       *prometheus.HistogramVec
	jobRunnerWait *prometheus.HistogramVec

	runnerCreateDuration *prometheus.HistogramVec
	runnerBootDuration   *prometheus.HistogramVec

	lastPoll          *prometheus.GaugeVec
	lastReconcile     *prometheus.GaugeVec
	reconcileDuration *prometheus.HistogramVec

	providerReachable    *prometheus.GaugeVec
	providerCalls        *prometheus.CounterVec
	providerCallDuration *prometheus.HistogramVec

	// mu guards the providers last reported to the gauges set whole, so
	// that those no longer reported can be deleted after the others are
	// set, rather than every series reset first and missing from a scrape
	// in between.
	mu sync.Mutex
	// providerRunnersReported holds, by scale set, the providers last given
	// rungar_provider_runners.
	providerRunnersReported map[string]map[string]bool
	// providerReachableReported holds the providers last given
	// rungar_provider_reachable.
	providerReachableReported map[string]bool
}

// New returns a set of metrics, all at zero.
func New() *Metrics {
	m := &Metrics{
		registry:                  prometheus.NewRegistry(),
		providerRunnersReported:   map[string]map[string]bool{},
		providerReachableReported: map[string]bool{},
	}

	m.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	m.gauge(Description{
		Name:   "rungar_build_info",
		Labels: []string{"version", "commit", "go_version"},
		Help:   "Build information for the running daemon. Always 1.",
		Doc:    "The build is in its labels.",
		Group:  GroupDaemon,
	}).WithLabelValues(version.Version, version.Commit, runtime.Version()).Set(1)

	startTime := float64(time.Now().Unix())
	m.gaugeFunc(Description{
		Name:  "rungar_start_time_seconds",
		Help:  "When the daemon started, in seconds since the Unix epoch.",
		Doc:   "Uptime is `time() - rungar_start_time_seconds`.",
		Group: GroupDaemon,
	}, func() float64 { return startTime })

	m.githubRequests = m.counter(Description{
		Name:   "rungar_github_requests_total",
		Labels: []string{"code"},
		Help:   "Requests made to GitHub, by the HTTP status it answered with.",
		Doc: "`code` is the status, such as `200`, `401` or `503`, or `error` when no answer came: a " +
			"refused connection, a timeout, a TLS failure. Every attempt is counted, retries too. Each " +
			"scale set's message session polls GitHub, so a working daemon always has a `2xx` rate " +
			"above zero.",
		Group: GroupGitHub,
	})

	m.minRunners = m.gauge(Description{
		Name:   "rungar_runners_min",
		Labels: []string{"scale_set"},
		Help:   "Runners the scale set keeps idle and ready: the min_runners in force.",
		Doc: "Its `min_runners`, or that of its `schedule`'s window in force, as of its latest scaling " +
			"decision.",
		Group: GroupScaleSets,
	})

	m.desiredRunners = m.gauge(Description{
		Name:   "rungar_runners_desired",
		Labels: []string{"scale_set"},
		Help:   "Runners the scale set should have, as of GitHub's latest statistics.",
		Doc: "Its assigned jobs plus the `min_runners` in force, at most `max_runners`; 0 while it is " +
			"paused.",
		Group: GroupScaleSets,
	})

	m.paused = m.gauge(Description{
		Name:   "rungar_scale_set_paused",
		Labels: []string{"scale_set"},
		Help:   "Whether the scale set is paused: 1 if it is, 0 if it is taking jobs.",
		Doc: "Set by `paused` in the configuration, or by `rungar scale-sets pause` and `resume` " +
			"until the daemon restarts. A paused scale set's jobs wait on GitHub.",
		Group: GroupScaleSets,
	})

	m.currentRunners = m.gauge(Description{
		Name:   "rungar_runners_current",
		Labels: []string{"scale_set", "state"},
		Help:   "Runners currently alive, by state.",
		Doc:    "`state` is `starting`, `idle` or `busy`. Every state is present, at 0 if none.",
		Group:  GroupScaleSets,
	})

	m.runnersCreated = m.counter(Description{
		Name:   "rungar_runners_created_total",
		Labels: []string{"scale_set", "provider"},
		Help:   "Runners created.",
		Group:  GroupScaleSets,
	})

	m.runnersRemoved = m.counter(Description{
		Name:   "rungar_runners_removed_total",
		Labels: []string{"scale_set", "provider", "reason"},
		Help:   "Runners removed, by scale set, provider and reason.",
		Doc: "`reason` is `job_completed`, when its job completed; `scaled_down`, when the scale set no " +
			"longer needed it; `never_connected`, when it did not connect to GitHub in time; " +
			"`unregistered`, when GitHub no longer had its registration; " +
			"`disconnected`, when it was disconnected from GitHub for too long; `stuck`, when it was " +
			"running a job while disconnected for too long; `outdated`, when it was created from another " +
			"revision of its runner block; `expired`, when it was older than `max_idle_age` or `max_age`; `requested`, " +
			"when it was removed with `rungar runners rm` or with its scale set; or `orphaned`, when GitHub " +
			"had it disconnected with no machine for longer than `start_timeout`, and only its registration " +
			"was removed, with `provider` empty. Runners created and removed as " +
			"`never_connected` in a loop point to a broken image.",
		Group: GroupScaleSets,
	})

	m.runnersLost = m.counter(Description{
		Name:   "rungar_runners_lost_total",
		Labels: []string{"scale_set", "provider", "reason"},
		Help:   "Runners lost, by scale set, provider and reason.",
		Doc: "A runner is lost when it ends without the daemon removing it or its job completing. " +
			"`reason` is `ended`, when its machine ended without its job completing -- it crashed, " +
			"never connected, or was taken back, as a Spot machine can be -- and its machine was " +
			"deleted; or `unreachable`, when its provider had been unreachable for five minutes, and the " +
			"scale set replaced it elsewhere. A rise in `ended` points to runners crashing or Spot " +
			"machines being taken back.",
		Group: GroupScaleSets,
	})

	m.scaleUpFailures = m.counter(Description{
		Name:   "rungar_scale_up_failures_total",
		Labels: []string{"scale_set", "reason"},
		Help:   "Times a runner was wanted and could not be created, by reason.",
		Doc: "`reason` is `no_capacity`, when no provider took the runner: each was full, refused it or " +
			"could not be tried. Or it is `error`, when GitHub refused to register the runner, creating " +
			"it timed out or was cancelled, or a provider that refused it left a machine that could not " +
			"be deleted. A provider that keeps refusing runners counts as `no_capacity` here; " +
			"`rungar_provider_calls_total{call=\"create\",result=\"error\"}` shows which.",
		Group: GroupScaleSets,
	})

	m.jobsStarted = m.counter(Description{
		Name:   "rungar_jobs_started_total",
		Labels: []string{"scale_set"},
		Help:   "Workflow jobs started on this fleet's runners.",
		Group:  GroupScaleSets,
	})

	m.jobsCompleted = m.counter(Description{
		Name:   "rungar_jobs_completed_total",
		Labels: []string{"scale_set", "result"},
		Help:   "Workflow jobs completed, by result.",
		Doc:    "`result` is as GitHub gives it: `succeeded`, `failed`, `canceled`.",
		Group:  GroupScaleSets,
	})

	m.jobsAssigned = m.gauge(Description{
		Name:   "rungar_jobs_assigned",
		Labels: []string{"scale_set"},
		Help:   "Jobs GitHub has assigned the scale set and not completed, running or waiting.",
		Doc: "As of GitHub's latest statistics. Those waiting for a runner are this less the busy " +
			"runners, `rungar_runners_current{state=\"busy\"}`.",
		Group: GroupScaleSets,
	})

	m.jobWait = m.histogram(Description{
		Name:   "rungar_job_wait_seconds",
		Labels: []string{"scale_set"},
		Help:   "How long each job waited, from being queued on GitHub to a runner taking it.",
		Doc: "What the job's author waits. It includes GitHub's routing of the job to the scale set, " +
			"which `rungar_job_runner_wait_seconds` leaves out. Measured by GitHub's clock.",
		Group:   GroupScaleSets,
		Buckets: waitBuckets,
	})

	m.jobRunnerWait = m.histogram(Description{
		Name:   "rungar_job_runner_wait_seconds",
		Labels: []string{"scale_set"},
		Help: "How long each job waited for a runner, from GitHub assigning it to the scale set to a " +
			"runner taking it.",
		Doc: "The part of the wait Rungar answers for: creating a runner and its booting, or none with " +
			"one idle. The number a service level is set on. Measured by GitHub's clock.",
		Group:   GroupScaleSets,
		Buckets: waitBuckets,
	})

	m.runnerCreateDuration = m.histogram(Description{
		Name:   "rungar_runner_create_duration_seconds",
		Labels: []string{"scale_set", "provider"},
		Help: "How long creating each runner took, from registering it with GitHub to its provider " +
			"having created its machine.",
		Doc: "`provider` is the one that created it, and the time includes the providers tried before " +
			"it. Runners that could not be created are not counted.",
		Group:   GroupScaleSets,
		Buckets: callBuckets,
	})

	m.runnerBootDuration = m.histogram(Description{
		Name:   "rungar_runner_boot_duration_seconds",
		Labels: []string{"scale_set", "provider"},
		Help:   "How long each runner took to connect to GitHub once its machine was created.",
		Doc: "The image's and the network's part of a job's wait. A runner that takes a waiting job " +
			"is seen connecting to within a second or two; one that does not, to within 20 seconds. " +
			"Adopted runners, and runners that never connect, are not counted.",
		Group:   GroupScaleSets,
		Buckets: waitBuckets,
	})

	m.lastPoll = m.gauge(Description{
		Name:   "rungar_scale_set_last_poll_timestamp_seconds",
		Labels: []string{"scale_set"},
		Help:   "When GitHub last answered the scale set's poll for jobs, in seconds since the Unix epoch.",
		Doc: "GitHub answers at least every minute or so, with jobs or without. One falling behind " +
			"is a scale set not hearing of its jobs, while others may be.",
		Group: GroupScaleSets,
	})

	m.lastReconcile = m.gauge(Description{
		Name:   "rungar_scale_set_last_reconcile_timestamp_seconds",
		Labels: []string{"scale_set"},
		Help:   "When the scale set last reconciled with the fleet, in seconds since the Unix epoch.",
		Doc: "Set when a reconciliation lists the providers, or those that are reachable. One falling behind " +
			"`reconcile_interval` is reconciliation stuck, or failing: the log says why at `debug`.",
		Group: GroupScaleSets,
	})

	m.reconcileDuration = m.histogram(Description{
		Name:    "rungar_reconcile_duration_seconds",
		Labels:  []string{"scale_set"},
		Help:    "How long each reconciliation with the fleet took.",
		Doc:     "Most of it is listing the providers and asking GitHub about the runners.",
		Group:   GroupScaleSets,
		Buckets: callBuckets,
	})

	m.providerReachable = m.gauge(Description{
		Name:   "rungar_provider_reachable",
		Labels: []string{"provider"},
		Help:   "1 if the provider's last listing succeeded, or it has not been listed yet; 0 if not.",
		Group:  GroupProviders,
	})

	m.providerRunners = m.gauge(Description{
		Name:   "rungar_provider_runners",
		Labels: []string{"scale_set", "provider"},
		Help:   "Runners currently alive on the provider, by scale set.",
		Doc:    "A scale set with none of its runners on the provider is absent.",
		Group:  GroupProviders,
	})

	m.providerCalls = m.counter(Description{
		Name:   "rungar_provider_calls_total",
		Labels: []string{"provider", "call", "result"},
		Help:   "Calls made to the provider, by call and result.",
		Doc: "`call` is `list`, `create` or `delete`. `result` is `ok`; `no_capacity`, when the " +
			"provider said it was full; or `error`. Calls the daemon itself cancelled are not counted.",
		Group: GroupProviders,
	})

	m.providerCallDuration = m.histogram(Description{
		Name:    "rungar_provider_call_duration_seconds",
		Labels:  []string{"provider", "call"},
		Help:    "How long each call to the provider took, whatever its result.",
		Doc:     "`call` is as for `rungar_provider_calls_total`.",
		Group:   GroupProviders,
		Buckets: callBuckets,
	})

	return m
}

// gauge registers a gauge vector described by d.
func (m *Metrics) gauge(d Description) *prometheus.GaugeVec {
	d.Type = TypeGauge
	g := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: d.Name, Help: d.Help}, d.Labels)
	m.register(d, g)

	return g
}

// gaugeFunc registers a gauge described by d, read from f when scraped.
func (m *Metrics) gaugeFunc(d Description, f func() float64) {
	d.Type = TypeGauge
	m.register(d, prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: d.Name, Help: d.Help}, f))
}

// histogram registers a histogram vector described by d, with its buckets.
func (m *Metrics) histogram(d Description) *prometheus.HistogramVec {
	d.Type = TypeHistogram
	h := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{Name: d.Name, Help: d.Help, Buckets: d.Buckets}, d.Labels)
	m.register(d, h)

	return h
}

// counter registers a counter vector described by d.
func (m *Metrics) counter(d Description) *prometheus.CounterVec {
	d.Type = TypeCounter
	c := prometheus.NewCounterVec(prometheus.CounterOpts{Name: d.Name, Help: d.Help}, d.Labels)
	m.register(d, c)

	return c
}

// register registers c and adds d to the reference. It panics on a
// duplicate, which is a programming error.
func (m *Metrics) register(d Description, c prometheus.Collector) {
	m.registry.MustRegister(c)
	m.reference = append(m.reference, d)
}

// Reference returns every metric of Rungar's own, in the order registered.
func (m *Metrics) Reference() []Description {
	return append([]Description(nil), m.reference...)
}

// Handler serves the metrics.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// CountGitHubRequest counts a request made to GitHub, by the status it was
// answered with, or "error" if no answer came.
func (m *Metrics) CountGitHubRequest(code string) {
	m.githubRequests.WithLabelValues(code).Inc()
}

// SetMinRunners records the min_runners in force for a scale set.
func (m *Metrics) SetMinRunners(scaleSet string, n int) {
	m.minRunners.WithLabelValues(scaleSet).Set(float64(n))
}

// SetDesiredRunners records how many runners a scale set should have.
func (m *Metrics) SetDesiredRunners(scaleSet string, n int) {
	m.desiredRunners.WithLabelValues(scaleSet).Set(float64(n))
}

// SetPaused records whether a scale set is paused.
func (m *Metrics) SetPaused(scaleSet string, paused bool) {
	m.paused.WithLabelValues(scaleSet).Set(boolValue(paused))
}

// CountRunnerCreated counts a runner created on a provider.
func (m *Metrics) CountRunnerCreated(scaleSet, provider string) {
	m.runnersCreated.WithLabelValues(scaleSet, provider).Inc()
}

// CountRunnerRemoved counts a runner removed, by reason.
func (m *Metrics) CountRunnerRemoved(scaleSet, provider string, reason types.RemovalReason) {
	m.runnersRemoved.WithLabelValues(scaleSet, provider, string(reason)).Inc()
}

// CountRunnerLost counts a runner lost, by reason.
func (m *Metrics) CountRunnerLost(scaleSet, provider string, reason types.LossReason) {
	m.runnersLost.WithLabelValues(scaleSet, provider, string(reason)).Inc()
}

// CountScaleUpFailed counts a runner wanted and not created, by reason:
// types.CallNoCapacity or types.CallError.
func (m *Metrics) CountScaleUpFailed(scaleSet string, reason types.CallResult) {
	m.scaleUpFailures.WithLabelValues(scaleSet, string(reason)).Inc()
}

// CountJobStarted counts a job started.
func (m *Metrics) CountJobStarted(scaleSet string) {
	m.jobsStarted.WithLabelValues(scaleSet).Inc()
}

// CountJobCompleted counts a job completed, by its result.
func (m *Metrics) CountJobCompleted(scaleSet, result string) {
	m.jobsCompleted.WithLabelValues(scaleSet, result).Inc()
}

// SetJobsAssigned records how many jobs GitHub has assigned a scale set.
func (m *Metrics) SetJobsAssigned(scaleSet string, n int) {
	m.jobsAssigned.WithLabelValues(scaleSet).Set(float64(n))
}

// ObserveJobWait records how long a job waited: in all, since it was queued,
// and for a runner, since the scale set was assigned it.
func (m *Metrics) ObserveJobWait(scaleSet string, total, forRunner time.Duration) {
	m.jobWait.WithLabelValues(scaleSet).Observe(total.Seconds())
	m.jobRunnerWait.WithLabelValues(scaleSet).Observe(forRunner.Seconds())
}

// ObserveRunnerCreateDuration records how long creating a runner on a provider
// took.
func (m *Metrics) ObserveRunnerCreateDuration(scaleSet, provider string, d time.Duration) {
	m.runnerCreateDuration.WithLabelValues(scaleSet, provider).Observe(d.Seconds())
}

// ObserveRunnerBootDuration records how long a runner took to connect to
// GitHub.
func (m *Metrics) ObserveRunnerBootDuration(scaleSet, provider string, d time.Duration) {
	m.runnerBootDuration.WithLabelValues(scaleSet, provider).Observe(d.Seconds())
}

// SetLastPoll records when GitHub last answered a scale set's poll.
func (m *Metrics) SetLastPoll(scaleSet string, at time.Time) {
	m.lastPoll.WithLabelValues(scaleSet).Set(unixSeconds(at))
}

// ObserveReconcile records a reconciliation that finished at finished and
// took d; listed is whether it listed the fleet.
func (m *Metrics) ObserveReconcile(scaleSet string, finished time.Time, d time.Duration, listed bool) {
	m.reconcileDuration.WithLabelValues(scaleSet).Observe(d.Seconds())
	if listed {
		m.lastReconcile.WithLabelValues(scaleSet).Set(unixSeconds(finished))
	}
}

// ObserveProviderCall records a call to a provider, which took d and ended
// with result.
func (m *Metrics) ObserveProviderCall(provider, call string, result types.CallResult, d time.Duration) {
	m.providerCalls.WithLabelValues(provider, call, string(result)).Inc()
	m.providerCallDuration.WithLabelValues(provider, call).Observe(d.Seconds())
}

// SetRunners records a scale set's runners by state and by provider. Every
// state is reported, at zero if none; a provider with none is not.
func (m *Metrics) SetRunners(scaleSet string, runners []types.Runner) {
	byState := map[types.RunnerState]int{
		types.RunnerStarting: 0,
		types.RunnerIdle:     0,
		types.RunnerBusy:     0,
	}
	byProvider := map[string]int{}

	for _, r := range runners {
		byState[r.State]++
		byProvider[r.Provider]++
	}

	for state, n := range byState {
		m.currentRunners.WithLabelValues(scaleSet, string(state)).Set(float64(n))
	}

	reported := make(map[string]bool, len(byProvider))

	m.mu.Lock()
	defer m.mu.Unlock()

	for provider, n := range byProvider {
		m.providerRunners.WithLabelValues(scaleSet, provider).Set(float64(n))
		reported[provider] = true
	}
	for provider := range m.providerRunnersReported[scaleSet] {
		if !reported[provider] {
			m.providerRunners.DeleteLabelValues(scaleSet, provider)
		}
	}
	m.providerRunnersReported[scaleSet] = reported
}

// SetProviderReachability records whether each provider is reachable, and
// forgets providers not among snapshots.
func (m *Metrics) SetProviderReachability(snapshots []types.ProviderSnapshot) {
	reported := make(map[string]bool, len(snapshots))

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, snapshot := range snapshots {
		m.providerReachable.WithLabelValues(snapshot.Name).Set(boolValue(snapshot.Reachable))
		reported[snapshot.Name] = true
	}
	for provider := range m.providerReachableReported {
		if !reported[provider] {
			m.providerReachable.DeleteLabelValues(provider)
		}
	}
	m.providerReachableReported = reported
}

// unixSeconds returns t in seconds since the Unix epoch.
func unixSeconds(t time.Time) float64 {
	return float64(t.UnixNano()) / float64(time.Second)
}

// boolValue returns 1 for true and 0 for false.
func boolValue(b bool) float64 {
	if b {
		return 1
	}

	return 0
}
