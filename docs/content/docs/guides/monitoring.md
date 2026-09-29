---
title: Monitoring
weight: 6
description: "What the metrics say about the fleet and the scale sets, and alerts worth setting."
icon: chart-bar
related_title: Next steps
related:
  - /docs/reference/metrics
  - /docs/guides/troubleshooting
  - /docs/guides/running-the-daemon
---

Rungar tells you what it is doing in three ways: Prometheus metrics, for
dashboards and alerts; `rungar status` and the `ls` commands, for a look at
the fleet now; and the daemon's log, for what happened and why.

## Metrics

The daemon serves Prometheus metrics once enabled in its
[configuration](../../reference/configuration#metrics):

```yaml {filename="/etc/rungar/config.yaml"}
metrics:
  enable: true
  listen: 127.0.0.1:9102
```

A running daemon takes it up when restarted:

```console
$ sudo systemctl restart rungar
$ curl -s 127.0.0.1:9102/metrics | grep '^rungar_runners_current'
rungar_runners_current{scale_set="rungar-c2-m4",state="busy"} 3
rungar_runners_current{scale_set="rungar-c2-m4",state="idle"} 1
rungar_runners_current{scale_set="rungar-c2-m4",state="starting"} 0
```

The endpoint, `/metrics`, has no authentication. It says which scale sets
there are, how busy they are and which providers Rungar uses, but nothing
about the jobs or the credentials. It listens on loopback unless told
otherwise: serve it on an address only your Prometheus can reach, and scrape
it:

```yaml {filename="prometheus.yml"}
scrape_configs:
  - job_name: rungar
    static_configs:
      - targets: ["rungar1.example.com:9102"]
```

Every metric is listed in the [metrics reference](../../reference/metrics).

### What to watch

#### How long jobs wait

`rungar_job_runner_wait_seconds` is how long each job waited for a runner,
from GitHub giving it to the scale set to a runner taking it: the part of the
wait Rungar answers for, and the number to set a service level on. The share
of jobs that got a runner within two minutes, over a day:

```promql
sum by (scale_set) (rate(rungar_job_runner_wait_seconds_bucket{le="120"}[1d]))
/
sum by (scale_set) (rate(rungar_job_runner_wait_seconds_count[1d]))
```

and the wait 95 jobs in 100 were within, over the last hour:

```promql
histogram_quantile(0.95, sum by (scale_set, le) (rate(rungar_job_runner_wait_seconds_bucket[1h])))
```

A service level is set on a bucket's bound -- 5, 10, 15, 20, 30, 45, 60, 90,
120, 180, 300 seconds and on -- so that it is counted exactly.
`rungar_job_wait_seconds` is the whole wait, since the job was queued, which
adds GitHub's own routing of it; both are measured by GitHub's clock.

A wait is a runner being created and booting, which
`rungar_runner_create_duration_seconds` and
`rungar_runner_boot_duration_seconds` split by provider: the first is the
provider's, the second the image's and the network's. A job that finds a
runner idle waits for neither. See
[Troubleshooting](../troubleshooting#jobs-wait-too-long-for-a-runner) for
a runner whose wait was long.

#### The fleet and the daemon

**Whether the scale sets keep up.** `rungar_runners_desired` is how many
runners a scale set should have, and `rungar_runners_current` how many it has.
Behind for a moment is normal -- a runner takes a while to boot -- but behind
for long means its jobs are waiting:

```promql
rungar_runners_desired - on (scale_set) sum by (scale_set) (rungar_runners_current)
```

A [paused](../managing-the-fleet#pausing-a-scale-set) scale set should
have none, so its jobs waiting do not show here: `rungar_scale_set_paused` is
1 while it is.

`rungar_scale_up_failures_total` says why: `no_capacity` when no provider
took the runner, and `error` when something else stopped it -- GitHub
refused to register it or timed out, or a provider's refusal left a machine
behind that could not be deleted -- which is something to fix. `no_capacity`
is the providers being full, which is the fleet being too small or a size too
large for any provider, or the providers failing: every one that was tried
refused, for whatever reason. Which it was is in
`rungar_provider_calls_total`, below.

**Whether the runners start.** A runner stays `starting` until it connects
to GitHub, and one that never does is removed after `start_timeout` and
replaced, with a warning in the log saying so, and counted in
`rungar_runners_removed_total` with `reason="never_connected"`:

```promql
sum by (scale_set, provider) (increase(rungar_runners_removed_total{reason="never_connected"}[30m]))
```

Runner after runner of a scale set removed that way is an image without the
runner in it, or a network that cannot reach GitHub; see
[Troubleshooting](../troubleshooting#runners-that-never-connect).

**Whether each scale set is listening.** A scale set hears of its jobs by
polling GitHub, which answers at least every minute or so, jobs or none.
`rungar_scale_set_last_poll_timestamp_seconds` is when it last did; one that
falls behind is a scale set deaf to its jobs, which the daemon-wide GitHub
metrics do not show while the others are fine:

```promql
time() - rungar_scale_set_last_poll_timestamp_seconds > 180
```

`rungar_scale_set_last_reconcile_timestamp_seconds` is the same for
reconciliation, which should never be more than a `reconcile_interval` or
two behind, and `rungar_reconcile_duration_seconds` how long it takes.

**The providers.** `rungar_provider_reachable` is 0 for one that did not
answer; its runners are left alone for five minutes, and then replaced
elsewhere. A provider that refuses a runner, full or failing, is skipped
by the scale set that found it so, which tries its next provider and says
so in the events. Every call to a provider is counted by result in
`rungar_provider_calls_total`, refusals included: `call="create"` with
`result="no_capacity"` is a provider full, and with `result="error"` one
failing, which is something to fix. Calls are also timed in
`rungar_provider_call_duration_seconds`: a provider slow to list slows every
reconciliation, and one slow to create slows every runner created on it.

**GitHub.** `rungar_github_requests_total` counts every request to GitHub
by the status it was answered with, or `error` when none came. A credential
that expired or was revoked is a rate of `401`s; one missing a permission,
`403`s or `404`s; an outage, `5xx`s and `error`s. Each scale set's message
session polls GitHub all the time, so a working daemon always has a rate of
`2xx`s, and one with none is not hearing from GitHub.

**The daemon.** `rungar_build_info` carries the version and commit running,
for a dashboard to show or to join on, and `rungar_start_time_seconds` when
it started: a value that keeps changing is a daemon restarting.

**The jobs.** `rungar_jobs_completed_total` by `result` is how the jobs on the
fleet end. A rise in `failed` that is not the code's own is often the
runners': out of disk, out of memory, or an image missing a tool.

### Alerts worth having

```yaml {filename="rungar-alerts.yml"}
groups:
  - name: rungar
    rules:
      - alert: RungarDown
        expr: up{job="rungar"} == 0
        for: 5m

      - alert: RungarRestarting
        expr: changes(rungar_start_time_seconds[1h]) > 3
        annotations:
          summary: "Rungar has restarted more than 3 times in an hour: see the log"

      - alert: RungarGitHubRefusing
        expr: sum(rate(rungar_github_requests_total{code=~"401|403"}[5m])) > 0
        for: 5m
        annotations:
          summary: "GitHub is refusing Rungar's credential"

      - alert: RungarGitHubUnreachable
        expr: sum(rate(rungar_github_requests_total{code=~"2.."}[5m])) == 0
        for: 5m
        annotations:
          summary: "Rungar has had no successful answer from GitHub for 5 minutes"

      - alert: RungarProviderUnreachable
        expr: rungar_provider_reachable == 0
        for: 5m
        annotations:
          summary: "Provider {{ $labels.provider }} has been unreachable for 5 minutes"

      - alert: RungarScaleSetBehind
        expr: >
          rungar_runners_desired
          - on (scale_set) sum by (scale_set) (rungar_runners_current) > 0
        for: 15m
        annotations:
          summary: "{{ $labels.scale_set }} has had fewer runners than its jobs need for 15 minutes"

      - alert: RungarCannotScaleUp
        expr: increase(rungar_scale_up_failures_total{reason="error"}[15m]) > 0
        annotations:
          summary: "{{ $labels.scale_set }} could not create runners: see the log"

      - alert: RungarProviderCannotCreate
        expr: increase(rungar_provider_calls_total{call="create",result="error"}[15m]) > 0
        annotations:
          summary: "Provider {{ $labels.provider }} failed to create runners: see the events"

      - alert: RungarRunnersNeverConnect
        expr: >
          sum by (scale_set, provider)
          (increase(rungar_runners_removed_total{reason="never_connected"}[30m])) >= 3
        annotations:
          summary: "{{ $labels.scale_set }}'s runners on {{ $labels.provider }} keep failing to connect to GitHub"

      - alert: RungarJobsWaitTooLong
        expr: >
          histogram_quantile(0.95,
            sum by (scale_set, le) (rate(rungar_job_runner_wait_seconds_bucket[30m]))) > 300
        for: 15m
        annotations:
          summary: "{{ $labels.scale_set }}'s jobs wait more than 5 minutes for a runner, 1 in 20 of them"

      - alert: RungarScaleSetNotPolling
        expr: time() - rungar_scale_set_last_poll_timestamp_seconds > 300
        annotations:
          summary: "{{ $labels.scale_set }} has not heard from GitHub for 5 minutes: it is not getting its jobs"

      - alert: RungarReconcileStalled
        expr: time() - rungar_scale_set_last_reconcile_timestamp_seconds > 600
        annotations:
          summary: "{{ $labels.scale_set }} has not reconciled with the fleet for 10 minutes: see the log at debug"

      - alert: RungarProviderFailing
        expr: >
          sum by (provider) (rate(rungar_provider_calls_total{result="error"}[15m]))
          / sum by (provider) (rate(rungar_provider_calls_total[15m])) > 0.5
        for: 15m
        annotations:
          summary: "More than half the calls to provider {{ $labels.provider }} fail"

      - alert: RungarScaleSetPaused
        expr: rungar_scale_set_paused == 1
        for: 4h
        annotations:
          summary: "{{ $labels.scale_set }} has been paused for 4 hours: its jobs are waiting"
```

`RungarJobsWaitTooLong` and `RungarScaleSetBehind` are the ones that matter
most: jobs waiting, whatever the cause. The others say which cause. Set the
first's five minutes to the service level you promise, on a bucket's bound.
`RungarProviderCannotCreate` catches a provider failing even while the scale
sets place their runners on the others and no job waits.
`RungarScaleSetPaused` is for the cause the first two cannot see, a pause
forgotten; drop it for a scale set the configuration pauses on purpose.

Metrics count runners, but do not name them. To see which runners a scale
set has, and what each is doing:

```console
$ sudo -u rungar rungar runners ls --scale-set rungar-c2-m4
```

## Beyond the metrics

For a look at the fleet now, `rungar status` and the `ls` and `inspect`
commands name every scale set, provider and runner; see
[Draining, pausing and removing](../managing-the-fleet#scale-sets-and-runners).
For what happened and why, the daemon's log and its events; see
[Running the daemon](../running-the-daemon#the-service). A runner's events
are its timeline -- created, connected, its job started, removed -- with how
long each step took:

```console
$ sudo -u rungar rungar events --runner rungar-c2-m4-1ff1015a
```
