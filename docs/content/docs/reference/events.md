---
title: Events
weight: 5
description: "Every event Rungar records of runners, providers and scale sets, with its attributes."
icon: clock
---

The events the daemon records of what it did to runners, providers and scale
sets, and why. [`rungar events`](../cli/rungar_events)
shows them; each event below is named as the command prints it, and by its
`action` in `--json`.

## Fields

| Field | Description |
|---|---|
| `time` | When it happened. |
| `kind` | What it is about: a runner, a provider or a scale set. |
| `name` | The runner's, the provider's or the scale set's name. |
| `action` | What happened: one of the events below. |
| `scale_set` | The scale set it happened for. |
| `provider` | The provider it happened on: the runner's, or the provider itself. |
| `message` | What happened, in a line for a person. |
| `attributes` | What happened, for a program: each event's are in its table. |

## The file

The daemon keeps the events in `events.file`, `/var/log/rungar/events.jsonl`
unless set, one JSON object a line, so that they outlive a restart. It keeps
the most recent `events.max_count`, 10,000 unless set, and, with
`events.max_age` set, none older. `rungar events` asks the running daemon
for them; with no daemon running, the file can be read as it is, with `jq`
say.

In the file, `kind` and `action` are lower case, `runner` and `removed`;
`rungar events --json` writes them as the API's names,
`EVENT_KIND_RUNNER` and `EVENT_ACTION_REMOVED`:

```json
{"time":"2026-09-28T10:21:13Z","kind":"runner","name":"rungar-c2-m4-1ff1015a","action":"removed","scale_set":"rungar-c2-m4","provider":"compute1","message":"Runner removed from compute1: its job completed","attributes":{"reason":"job_completed"}}
```

## Runners

| Event | `action` | Attributes | What it means |
|---|---|---|---|
| Created | `EVENT_ACTION_CREATED` | `create_duration` | The daemon created the runner: registered it with GitHub and created its machine. `create_duration` is how long that took, as a Go duration, the providers tried before the one that created it included. |
| Adopted | `EVENT_ACTION_ADOPTED` | | The daemon took over a runner found on the fleet: when it starts, one an earlier daemon left; later, one it did not know of. |
| Connected | `EVENT_ACTION_CONNECTED` | `boot_duration` | A runner the daemon created connected to GitHub for the first time. `boot_duration` is how long after its machine was created, as a Go duration; see `rungar_runner_boot_duration_seconds` for how closely it is measured. An adopted runner has none. |
| Job started | `EVENT_ACTION_JOB_STARTED` | `job_id`, `repository`, `workflow_run_id`, `wait`, `runner_wait` | The runner took a job. `job_id` is GitHub's ID of the job, `repository` its repository as `owner/name`, and `workflow_run_id` its workflow run's. `wait` is how long the job waited since GitHub queued it, and `runner_wait` how much of that was since GitHub assigned it to the scale set, as Go durations; both are left out when GitHub's message did not give the times. The same as `rungar_job_wait_seconds`'s and `rungar_job_runner_wait_seconds`'s. |
| Removed | `EVENT_ACTION_REMOVED` | `reason` | The daemon removed the runner, deleting its machine if it had one. `reason` is `job_completed`, when its job completed; `scaled_down`, when its scale set needed fewer runners; `never_connected`, when it did not connect to GitHub within `start_timeout`; `unregistered`, when GitHub no longer had its registration; `disconnected`, when it was disconnected from GitHub for longer than `start_timeout`; `stuck`, when it was running a job while disconnected for longer than `start_timeout`; `outdated`, when it was created from another revision of its runner block; `expired`, when it was older than `max_idle_age` or `max_age`; `requested`, when it was removed with `rungar runners rm` or with its scale set; or `orphaned`, when GitHub had it disconnected with no machine on the fleet for longer than `start_timeout`. A runner with no machine has only its registration removed, and no `provider`. The same as `rungar_runners_removed_total`'s. |
| Lost | `EVENT_ACTION_LOST` | `reason` | The runner ended without the daemon removing it or its job completing. `reason` is `ended`, when its machine ended -- stopped, or gone from its provider -- and GitHub still had its registration after `start_timeout`: it crashed, never connected, or was taken back, as a Spot machine can be; its machine is deleted. Or `unreachable`, when its provider had been unreachable for five minutes, and the scale set replaces it elsewhere; its machine is left alone. The same as `rungar_runners_lost_total`'s. |

A runner created by the daemon is Created, Connected, has its Job started and
is Removed, so `rungar events --runner NAME` is its timeline: how long its
machine took to create, to boot, and how long its job waited.

## Providers

| Event | `action` | Attributes | What it means |
|---|---|---|---|
| Full | `EVENT_ACTION_FULL` | `runner`, `error`, `skipped_for` | The provider was full for a runner, which was tried on the scale set's next provider; the scale set skips this one for a while. `runner` is the runner refused; `error` the provider's reason; and `skipped_for` how long the scale set skips it, as a Go duration: 15 seconds, doubling each time in a row, up to 2 minutes. |
| Failing | `EVENT_ACTION_FAILING` | `runner`, `error`, `skipped_for`, `refusals` | The provider failed to create a runner's machine, which was tried on the scale set's next provider; the scale set skips this one for a while. `runner` is the runner; `error` the provider's error; `skipped_for` how long the scale set skips it, as for Full; and `refusals` how many of the scale set's runners in a row it has refused, this one included. |

## Scale sets

| Event | `action` | Attributes | What it means |
|---|---|---|---|
| Paused | `EVENT_ACTION_PAUSED` | | The scale set was paused with `rungar scale-sets pause`: it takes no jobs and creates no runners, and its runners not running a job are removed. One paused by the configuration records none. |
| Resumed | `EVENT_ACTION_RESUMED` | | The scale set was resumed with `rungar scale-sets resume`, and takes jobs again. |
| Min runners changed | `EVENT_ACTION_MIN_RUNNERS_CHANGED` | `from`, `to`, `window` | The scale set's `schedule` changed the `min_runners` in force, as a window opened or closed. `from` and `to` are the numbers, and `window` the window now in force, such as `mon-fri 08:00-19:00`, absent outside every window. The daemon starting records none. |
