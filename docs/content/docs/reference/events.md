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
{"time":"2026-09-28T10:21:13Z","kind":"runner","name":"rungar-c2-m4-x7k2p","action":"removed","scale_set":"rungar-c2-m4","provider":"compute1","message":"Runner removed from compute1: its job completed","attributes":{"reason":"job_completed"}}
```

## Runners

| Event | `action` | Attributes | What it means |
|---|---|---|---|
| Created | `EVENT_ACTION_CREATED` | | The daemon made the runner: registered it with GitHub and started its machine. |
| Adopted | `EVENT_ACTION_ADOPTED` | | The daemon took over a runner found on the fleet: when it starts, one an earlier daemon left; later, one it did not know of. |
| Removed | `EVENT_ACTION_REMOVED` | `reason` | The daemon removed the runner's machine. `reason` is `job_completed`, when its job completed; `scaled_down`, when its scale set needed fewer runners; `stopped`, when its machine had stopped; `never_connected`, when it did not connect to GitHub within `start_timeout`; `unregistered`, when GitHub no longer had its registration; `disconnected`, when it was disconnected from GitHub for longer than `start_timeout`; `stuck`, when it was running a job while disconnected for longer than `start_timeout`; `outdated`, when it was made from another revision of its runner block; `expired`, when it was older than `max_idle_age` or `max_age`; or `requested`, when it was removed with `rungar runners rm` or with its scale set. The same as `rungar_runners_removed_total`'s. |
| Lost | `EVENT_ACTION_LOST` | `reason` | The daemon forgot the runner without removing its machine. `reason` is `gone`, when its machine was no longer on its provider; or `unreachable`, when its provider had not answered for five minutes, and the scale set replaces it elsewhere. |

## Providers

| Event | `action` | Attributes | What it means |
|---|---|---|---|
| Full | `EVENT_ACTION_FULL` | `runner`, `error`, `for` | The provider was full for a runner, which was tried on the scale set's next provider; the scale set skips this one for a while. `runner` is the runner refused; `error` the provider's reason; and `for` how long the scale set skips it, as a Go duration: 15 seconds, doubling each time in a row, up to 2 minutes. |
| Failing | `EVENT_ACTION_FAILING` | `runner`, `error`, `for`, `failures` | The provider failed to make a runner's machine, which was tried on the scale set's next provider; the scale set skips this one for a while. `runner` is the runner; `error` the provider's error; `for` how long the scale set skips it, as for Full; and `failures` how many of the scale set's runners in a row it has refused, this one included. |

## Scale sets

| Event | `action` | Attributes | What it means |
|---|---|---|---|
| Paused | `EVENT_ACTION_PAUSED` | | The scale set was paused with `rungar scale-sets pause`: it takes no jobs and makes no runners, and its idle runners are removed. One paused by the configuration records none. |
| Resumed | `EVENT_ACTION_RESUMED` | | The scale set was resumed with `rungar scale-sets resume`, and takes jobs again. |
