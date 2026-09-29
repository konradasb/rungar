// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

// Package metrics is the daemon's Prometheus metrics. They are always
// recorded, and served only when the configuration enables the endpoint.
package metrics

import (
	"net/http"
	"runtime"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/konradasb/rungar/internal/types"
	"github.com/konradasb/rungar/internal/version"
)

// Metric groups: the sections of the reference.
const (
	GroupDaemon    = "daemon"
	GroupGitHub    = "github"
	GroupScaleSets = "scale_sets"
	GroupProviders = "providers"
)

// Description describes one of Rungar's metrics. Every metric is registered
// through one, so the generated reference lists exactly what is served.
type Description struct {
	// Name is the metric's full name.
	Name string

	// Type is counter or gauge.
	Type string

	// Labels are the metric's label names.
	Labels []string

	// Help is the metric's HELP: one plain sentence.
	Help string

	// Doc is what the reference adds to Help, in Markdown.
	Doc string

	// Group is the section of the reference the metric is listed in.
	Group string
}

// Metrics holds the daemon's metrics, in a registry of their own with the Go
// and process collectors.
type Metrics struct {
	registry *prometheus.Registry

	// reference is every metric of Rungar's own, in the order registered.
	reference []Description

	githubRequests *prometheus.CounterVec

	paused            *prometheus.GaugeVec
	desiredRunners    *prometheus.GaugeVec
	currentRunners    *prometheus.GaugeVec
	runnersByProvider *prometheus.GaugeVec

	runnersCreated  *prometheus.CounterVec
	runnersRemoved  *prometheus.CounterVec
	scaleUpFailures *prometheus.CounterVec

	jobsStarted   *prometheus.CounterVec
	jobsCompleted *prometheus.CounterVec

	providerReachable *prometheus.GaugeVec
}

// New returns a set of metrics, all at zero.
func New() *Metrics {
	m := &Metrics{registry: prometheus.NewRegistry()}

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

	m.desiredRunners = m.gauge(Description{
		Name:   "rungar_runners_desired",
		Labels: []string{"scale_set"},
		Help:   "Runners the scale set should have, as of GitHub's latest statistics.",
		Doc:    "Its assigned jobs plus `min_runners`, at most `max_runners`; 0 while it is paused.",
		Group:  GroupScaleSets,
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

	m.runnersByProvider = m.gauge(Description{
		Name:   "rungar_runners_by_provider",
		Labels: []string{"scale_set", "provider"},
		Help:   "Runners currently alive, by provider.",
		Doc:    "A provider with none of the scale set's runners is absent.",
		Group:  GroupScaleSets,
	})

	m.runnersCreated = m.counter(Description{
		Name:   "rungar_runners_created_total",
		Labels: []string{"scale_set", "provider"},
		Help:   "Runner VMs created.",
		Group:  GroupScaleSets,
	})

	m.runnersRemoved = m.counter(Description{
		Name:   "rungar_runners_removed_total",
		Labels: []string{"scale_set", "provider", "reason"},
		Help:   "Runners removed, by scale set, provider and reason.",
		Doc: "`reason` is `job_completed`, when its job completed; `scaled_down`, when the scale set no " +
			"longer needed it; `stopped`, when its VM stopped; `never_connected`, when it did not connect " +
			"to GitHub in time; `unregistered`, when GitHub no longer had its registration; " +
			"`disconnected`, when it was disconnected from GitHub for too long; `stuck`, when it was " +
			"running a job while disconnected for too long; `outdated`, when it was made from an older " +
			"runner spec; `expired`, when it was older than `max_idle_age` or `max_age`; or `requested`, " +
			"when it was removed with `rungar runners rm` or with its scale set. Runners made and removed as " +
			"`never_connected` in a loop point to a broken image.",
		Group: GroupScaleSets,
	})

	m.scaleUpFailures = m.counter(Description{
		Name:   "rungar_scale_up_failures_total",
		Labels: []string{"scale_set", "reason"},
		Help:   "Times a runner was wanted and could not be created, by reason.",
		Doc: "`reason` is `no_capacity`, when no provider had room, or `error`, when a provider or " +
			"GitHub refused.",
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

	m.providerReachable = m.gauge(Description{
		Name:   "rungar_provider_reachable",
		Labels: []string{"provider"},
		Help:   "1 if the provider answered the last call made of it, 0 if not.",
		Group:  GroupProviders,
	})

	return m
}

// gauge registers a gauge vector described by d.
func (m *Metrics) gauge(d Description) *prometheus.GaugeVec {
	d.Type = "gauge"
	g := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: d.Name, Help: d.Help}, d.Labels)
	m.register(d, g)

	return g
}

// gaugeFunc registers a gauge described by d, read from f when scraped.
func (m *Metrics) gaugeFunc(d Description, f func() float64) {
	d.Type = "gauge"
	m.register(d, prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: d.Name, Help: d.Help}, f))
}

// counter registers a counter vector described by d.
func (m *Metrics) counter(d Description) *prometheus.CounterVec {
	d.Type = "counter"
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

// SetDesiredRunners records how many runners a scale set should have.
func (m *Metrics) SetDesiredRunners(scaleSet string, n int) {
	m.desiredRunners.WithLabelValues(scaleSet).Set(float64(n))
}

// SetPaused records whether a scale set is paused.
func (m *Metrics) SetPaused(scaleSet string, paused bool) {
	var v float64
	if paused {
		v = 1
	}
	m.paused.WithLabelValues(scaleSet).Set(v)
}

// CountRunnerCreated counts a runner made on a provider.
func (m *Metrics) CountRunnerCreated(scaleSet, provider string) {
	m.runnersCreated.WithLabelValues(scaleSet, provider).Inc()
}

// CountRunnerRemoved counts a runner removed, by reason.
func (m *Metrics) CountRunnerRemoved(scaleSet, provider string, reason types.RemovalReason) {
	m.runnersRemoved.WithLabelValues(scaleSet, provider, string(reason)).Inc()
}

// CountScaleUpFailed counts a runner wanted and not made, by reason.
func (m *Metrics) CountScaleUpFailed(scaleSet, reason string) {
	m.scaleUpFailures.WithLabelValues(scaleSet, reason).Inc()
}

// CountJobStarted counts a job started.
func (m *Metrics) CountJobStarted(scaleSet string) {
	m.jobsStarted.WithLabelValues(scaleSet).Inc()
}

// CountJobCompleted counts a job completed, by its result.
func (m *Metrics) CountJobCompleted(scaleSet, result string) {
	m.jobsCompleted.WithLabelValues(scaleSet, result).Inc()
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

	m.runnersByProvider.DeletePartialMatch(prometheus.Labels{"scale_set": scaleSet})
	for provider, n := range byProvider {
		m.runnersByProvider.WithLabelValues(scaleSet, provider).Set(float64(n))
	}
}

// SetProviders records whether each provider answered.
func (m *Metrics) SetProviders(states []types.ProviderSnapshot) {
	m.providerReachable.Reset()

	for _, state := range states {
		m.providerReachable.WithLabelValues(state.Name).Set(boolValue(state.Reachable))
	}
}

// boolValue returns 1 for true and 0 for false.
func boolValue(b bool) float64 {
	if b {
		return 1
	}

	return 0
}
