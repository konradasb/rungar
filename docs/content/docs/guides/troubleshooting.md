---
title: Troubleshooting
weight: 7
description: "Reading rungar status and the logs, and what the common failures mean."
icon: support
related:
  - /docs/guides/running-the-daemon
  - /docs/guides/monitoring
  - /docs/concepts/runner-lifecycle
---

Most problems announce themselves in one of three places: `rungar status`,
the daemon's log, or GitHub's own view of the scale set. This guide starts
from what you see and works back to why.

## Where to look

| To learn | Run |
|---|---|
| Whether the configuration loads, and its files are there | `sudo -u rungar rungar validate` |
| The configuration as the daemon runs it, defaults filled in and secrets hidden | `sudo -u rungar rungar config` |
| The configuration, every provider and every runner at once | `sudo -u rungar rungar status` |
| One scale set: on GitHub, in the configuration, and its runners | `sudo -u rungar rungar scale-sets inspect NAME` |
| One provider: whether it answers, what it is skipped for, its runners | `sudo -u rungar rungar providers inspect NAME` |
| Every runner, and what GitHub says each is doing | `sudo -u rungar rungar runners ls` |
| What happened to runners, providers and scale sets, and why, even before a restart | `sudo -u rungar rungar events` |
| What the daemon did, and why | `journalctl -u rungar` |

`rungar validate` and `rungar config` need no daemon; it is run as `rungar`
because the configuration and its credentials are readable by that group
alone. The others ask the running daemon, through its socket, which `rungar`
and root can use, and say so when there is no daemon to ask -- which leaves
`validate`, `config` and the journal. For more from the daemon, set
`log_level: debug`; a restart takes it up, and disturbs no runner.

## Reading rungar status

`rungar status` is what the running daemon knows, in four parts:

- **The daemon**: which GitHub, as whom, the installation's ID -- what
  Rungar's labels on every machine say it is -- how long it has run, and
  which configuration file it read.
- **Scale sets**: each one's status, its providers and placement, its labels,
  its runner size as the providers read it, how many runners it has and how
  many GitHub's statistics call for, and its limits. It is `LISTENING` once
  it is taking jobs; `STARTING`, `WAITING` on standby for another daemon's
  session, `UNREACHABLE` while GitHub does not answer, `HOLDING BACK` for a
  scale set of higher priority, and `PAUSED` are said more of under the
  table.
- **Providers**: as placement sees them -- whether each answered, and how
  many runners it has against its `max_runners`. `DISABLED` and `DRAINING`
  are providers taken out of placement, `until the daemon restarts` when
  that was done while it ran; `HELD` is one a scale set of higher priority
  found full, keeping lower ones off until it finds room; `UNREACHABLE` says
  why. Under the table, a line for each provider a scale set skips: full, or
  failing to create its runner and why, and for how much longer.
- **Runners**: every runner of this installation's on the fleet, what the
  daemon knows it to be doing -- `Starting`, `Idle` or `Busy` -- its
  machine's state, and what GitHub says it is doing: `Busy`, `Idle`,
  `Offline` -- not connected -- or `Not registered`.

Its last line is how many runners there are, of how many the scale sets
allow.

The Credentials line says whether GitHub answered to them: `OK`, or GitHub's
refusal, or why it could not be reached, on the line under it. GitHub is asked
each time, so a bad credential shows here without restarting the daemon.

`rungar status` is for reading: it exits non-zero only if the daemon cannot be
asked, never for what it reports.

## The daemon does not start

```console
$ systemctl status rungar
$ journalctl -u rungar -n 50
```

**A configuration that does not load** stops it at start with the reason:
an unknown key, a scale set on a provider that is not there, a runner block
the provider refuses. `rungar validate` says the same without starting
anything; see [Configuration](../../reference/configuration).

**A credential GitHub does not accept** stops it once it looks up its scale
sets. A `401` is a wrong key, an expired token, or an App not installed where
`github.url` points; a `403` or `404` is a credential without a permission it
needs. See [GitHub credentials](../github-credentials).

**Another rungar on the socket** stops it before it does anything: `another
rungar is already serving on /run/rungar/rungar.sock`. Two daemons from one
configuration would each run the same scale sets. A socket left by a daemon
that did not stop cleanly is removed without being mentioned.

systemd starts it again every five seconds after a failure, so a daemon that
cannot start shows as restarting; the reason is in the log each time.

## Jobs stay queued

A job waits on GitHub for one of five reasons, in about this order of
likelihood.

**It does not target the scale set.** A workflow's `runs-on` must name
labels the scale set has -- its name, and the `labels` configured -- and
nothing else. `self-hosted`, `linux` and `x64` are labels a scale set has
only if it is given them. A job whose labels no scale set has is never
assigned to one, and Rungar never hears of it. `rungar scale-sets inspect`
gives the line that targets the scale set, to copy, last of its fields and
before its runners:

```console
$ sudo -u rungar rungar scale-sets inspect rungar-c2-m4
...
  Workflows: runs-on: rungar-c2-m4

Runners (2)
...
```

**The scale set may not serve the repository.** A scale set of an
organisation belongs to a runner group, `Default` unless `runner_group`
says otherwise, and a group can be limited to some repositories. A job from
another repository waits.

**No runner can be created.** The log says why, each time Rungar tries:

```text
level=WARN msg="cannot scale up further" component=scaleset scale_set=rungar-c8-m16 error="create runner \"rungar-c8-m16-5d0e9b27\": no provider took the runner: compute1: not enough memory; compute2: not enough memory"
```

- *No provider took the runner*: each provider tried refused it, and says
  why. Full -- out of CPU, memory or disk, a quota -- is the fleet being too
  small for the moment: jobs start as runners finish and make room, and each
  full provider is tried again within two minutes. For good, the fleet needs
  more room, a provider's `max_runners` more, or the size less.
- *No provider can be tried*: every provider is being skipped, each with why
  -- `full`, `failing to create its runner: ...`, `at its limit of N
  runners`, `disabled`, `unreachable`, or `held for scale set ...`, which is
  a scale set of higher [priority](../../concepts/placement#priority) that
  found it full and is waiting for room. A hold lasts two
  `reconcile_interval`s, a minute by default, unless the other keeps
  trying.

Or the scale set is at its `max_runners`, every runner it may have running a
job: the next job waits for one to finish, which is what the limit is for.

**The runners never start taking jobs.** See the next section.

**The scale set is paused.** `rungar status` shows it `PAUSED`: it takes no
jobs until `rungar scale-sets resume`, or, paused by `paused: true` in the
configuration, until that is removed and the daemon restarted. See
[Pausing a scale set](../managing-the-fleet#pausing-a-scale-set).

## Jobs wait too long for a runner

`rungar_job_runner_wait_seconds` says how long jobs wait for a runner once
GitHub has given them to the scale set; see
[Monitoring](../monitoring#how-long-jobs-wait). A wait is creating a runner and
its booting, and one runner's events say which part was slow:

```console
$ sudo -u rungar rungar events --runner rungar-c2-m4-1ff1015a --json | jq -c '[.action, .attributes]'
["EVENT_ACTION_CREATED",{"create_duration":"41.2s"}]
["EVENT_ACTION_CONNECTED",{"boot_duration":"2m3s"}]
["EVENT_ACTION_JOB_STARTED",{"job_id":"…","repository":"octo/app","runner_wait":"2m45s","wait":"2m49s",…}]
```

- **Creating the runner is slow**, `create_duration` or
  `rungar_runner_create_duration_seconds`: the provider. Its calls are timed
  in `rungar_provider_call_duration_seconds{call="create"}`; a time much
  longer than the others' is a slow backend, or one refusing, which
  `rungar_provider_calls_total` counts, before another took the runner.
- **Booting is slow**, `boot_duration` or
  `rungar_runner_boot_duration_seconds`: the image and the network. A large
  image pulled at every start, a startup script installing tools, or a slow
  way out to GitHub. The machine's console says which.
- **Both are quick, and the wait is not**: no runner was being created. The
  scale set was at `max_runners`, paused, or out of room on its providers --
  `rungar_scale_up_failures_total` -- or not hearing of its jobs at all; see
  [Jobs stay queued](#jobs-stay-queued).

A standing reserve, `min_runners`, has runners booted before jobs come, and
takes the boot out of the wait for as many jobs as it has runners.

## Runners that never connect

A runner whose machine boots but that never connects to GitHub is removed
after the scale set's `start_timeout`, five minutes unless set, and another
created in its place:

```text
level=WARN msg="removing runner" component=scaleset scale_set=rungar-c2-m4 runner=rungar-c2-m4-1ff1015a provider=compute1 reason=never_connected
```

One now and then is a slow boot. Runner after runner is something wrong with
what they boot:

- **The image does not carry the runner**, or `command` starts something
  else. The official image's runner is `/home/runner/run.sh`.
- **The machine cannot reach GitHub**: no route out, no DNS, or a proxy the
  runner is not told about -- give it `HTTPS_PROXY` and `NO_PROXY` in the
  runner block's `env` on Dicer, or in the image or its startup
  script on the others. The daemon's own proxy is not passed on to runners.
- **The runner is too old.** GitHub stops accepting runners too far behind;
  a runner image not updated in a long while is refused at connect.

The machine's console says which. How to read it is the provider's: for a
Dicer host, `dicer logs NAME` on the host, with the runner's name. To keep a
runner long enough to look, raise `start_timeout` for a while.

A runner connected and then lost is removed the same way, after the same
time:

```text
level=WARN msg="removing runner" component=scaleset scale_set=rungar-c2-m4 runner=rungar-c2-m4-1ff1015a provider=compute1 reason=disconnected
```

## A provider refuses runners

The runner is tried on the scale set's next provider, and the scale set
skips this one for a while -- 15 seconds, doubling each time in a row, up
to 2 minutes. Other scale sets still try it.

**It is full**:

```text
level=INFO msg="provider is full; trying the next" component=fleet scale_set=rungar-c8-m16 provider=compute1 runner=rungar-c8-m16-5d0e9b27 error="not enough memory" skipped_for=15s refusals=1
```

**It fails**: anything else -- more vCPUs than the host has, a kernel, a
network or an image it does not have, a daemon that errs or times out. A
runner block the provider can never create fails every time: fix it, or the
host, and it is taken up on the next try, with no restart:

```text
level=WARN msg="provider failed to create a runner; trying the next" component=fleet scale_set=rungar-c8-m16 provider=compute2 runner=rungar-c8-m16-a41c7f03 error="8 vCPUs is more than the host's 4 CPUs" skipped_for=30s refusals=2
```

`rungar status` and `rungar providers ls` show, under the table, each scale
set that skips a provider: full, or failing with how many times in a row and
the last error, and for how much longer; `rungar providers inspect compute2`
shows the same for one provider. `rungar events --provider compute2` has
every refusal, with the runner refused. A provider is tried again once its
time is up, and one runner created there forgives it.

## A provider cannot be reached

`rungar status` shows it `UNREACHABLE`, with why. Nothing new is placed on
it. Its runners are left alone for five minutes -- a network problem says
nothing about whether their jobs are still running -- and then forgotten, so
that the scale set replaces them elsewhere:

```text
level=WARN msg="forgetting runner on a provider unreachable for too long" component=scaleset scale_set=rungar-c2-m4 runner=rungar-c2-m4-1ff1015a provider=compute2
```

When it is reachable again, whatever is still on it is adopted. For a Dicer host,
the usual reasons are a stopped `dicerd`, a firewall, or TLS: the error
names which certificate did not match.

## Another message session holds the scale set

```text
level=INFO msg="another message session holds the scale set; standing by until it ends" component=scaleset scale_set=rungar-c2-m4 retry_in=10s error="… RunnerScaleSetSessionConflictException …"
```

GitHub allows one message session per scale set. Another is held either by
a second Rungar serving the same scale set -- a
[standby](../running-the-daemon#running-a-standby), if one was meant -- or by
a daemon that ended without closing its session, which GitHub expires within
a few minutes. Rungar stands by: it touches no runner, and tries again every
minute at most, carrying on once it has the session. Meanwhile `rungar
status` shows the scale set `WAITING`.

## Lost the message session

```text
level=WARN msg="lost the message session; leaving the runners to whichever daemon holds it next" component=scaleset scale_set=rungar-c2-m4 retry_in=10s error="scale set \"rungar-c2-m4\": listener: message session lost: …"
```

GitHub stopped answering over the scale set's session, or ended it. Rungar
stops looking after the scale set's runners, leaving them where they are,
and waits for the session again as it does at start. If another daemon took
it meanwhile, that one now looks after them.

While GitHub cannot be reached, it keeps trying, every minute at most, and
`rungar status` shows the scale set `UNREACHABLE`:

```text
level=WARN msg="GitHub did not answer a request for the message session; trying again" component=scaleset scale_set=rungar-c2-m4 retry_in=10s error="… i/o timeout"
```

The error says why: a refused connection, a timeout, a failure to resolve
GitHub's name. Any other refusal -- credentials no longer accepted, say --
stops the daemon, as it would at start.

The lead scale set, the first configured, losing its session takes every
other scale set's with it:

```text
level=INFO msg="the lead scale set's message session was lost; standing by with it, leaving the runners to whichever daemon holds it next" component=scaleset scale_set=rungar-c4-m8 lead=rungar-c2-m4
```

Until the daemon holds the lead's session again, the others show `WAITING`.
See [Running a standby](../running-the-daemon#running-a-standby).

## Warnings worth reading

**The scale set's labels on GitHub are not the configured ones.** GitHub
keeps the labels a scale set was created with. Delete it on GitHub, or with
`rungar scale-sets rm` after taking it out of the configuration, and let
Rungar create it again with the configured ones.

**Runners of this scale set belong to another installation.** A Rungar
serving another GitHub shares the fleet, and this is expected; or
`github.url` or `installation` changed, and those runners were left behind.
See [State and
adoption](../../concepts/state-and-adoption#changing-the-configuration).

## Runners left behind

**A scale set no longer configured** shows as `LEFTOVER` in
`rungar scale-sets ls`, with its runners still on the fleet and itself still
on GitHub. `rungar scale-sets rm` removes it.

**Offline runners on GitHub** with no machine are registrations whose machine
went while no daemon was watching -- killed with the host, say. The daemon
removes those of its scale sets once they have been offline for longer than
`start_timeout` and every provider has been listed, or has been unreachable
for five minutes. `rungar runners rm` removes one at once. Those of scale sets no
longer configured go with `rungar scale-sets rm`.

**A runner that is stuck** -- `Idle` on GitHub but never given a job, or a
machine that will not go -- is removed with `rungar runners rm`, the safe way
round. A scale set that needs it creates another.
