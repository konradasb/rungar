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

`rungar_scale_up_failures_total` says why: `no_capacity` when the providers
are full, which is the fleet being too small or a size too large for any
provider, and `error` when a provider or GitHub refused, which is something
to fix.

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

**The providers.** `rungar_provider_reachable` is 0 for one that did not
answer; its runners are left alone for five minutes, and then replaced
elsewhere. A provider that refuses a runner, full or failing, is skipped
by the scale set that found it so, which tries its next provider and says
so in the events; the refusals are counted in
`rungar_scale_up_failures_total`.

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
          summary: "Provider {{ $labels.provider }} has not answered for 5 minutes"

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
          summary: "{{ $labels.scale_set }} could not make runners: see the log"

      - alert: RungarRunnersNeverConnect
        expr: >
          sum by (scale_set, provider)
          (increase(rungar_runners_removed_total{reason="never_connected"}[30m])) >= 3
        annotations:
          summary: "{{ $labels.scale_set }}'s runners on {{ $labels.provider }} keep failing to connect to GitHub"

      - alert: RungarScaleSetPaused
        expr: rungar_scale_set_paused == 1
        for: 4h
        annotations:
          summary: "{{ $labels.scale_set }} has been paused for 4 hours: its jobs are waiting"
```

`RungarScaleSetBehind` is the one that matters most: it is jobs waiting,
whatever the cause. The others say which cause. `RungarScaleSetPaused` is for
the one it cannot see, a pause forgotten; drop it for a scale set the
configuration pauses on purpose.

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
[Running the daemon](../running-the-daemon#the-service).
