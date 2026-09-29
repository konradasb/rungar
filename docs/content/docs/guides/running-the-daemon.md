---
title: Running the daemon
weight: 3
description: "Running rungar under systemd: the service, its logs and events, changing the configuration, and what a restart does."
icon: terminal
related:
  - /docs/guides/managing-the-fleet
  - /docs/guides/upgrading-and-uninstalling
  - /docs/concepts/state-and-adoption
  - /docs/reference/files-and-environment
---

`rungar` is one process, run by systemd as the `rungar` user. It keeps no
state of its own, so everything below comes down to one fact: whatever
happens to the process, the runners on the fleet are found again by their
labels, and a job running at the time is not disturbed. See
[State and adoption](../../concepts/state-and-adoption).

## The service

The package, and `install.sh`, install `rungar.service`, and leave it
stopped until there is a configuration. Once there is:

```console
$ sudo -u rungar rungar validate
$ sudo systemctl enable --now rungar
$ sudo -u rungar rungar status
```

`rungar validate` is run as `rungar` because the configuration and the
credentials it names are readable by that group alone.

Every other command asks the running daemon, through the socket it serves
them on: `/run/rungar/rungar.sock`, which the service makes readable and
writable by `rungar` and root alone. So they are run as either, and whoever can
use the socket can remove runners and take providers out of placement. With
no daemon running they say so, and `rungar validate` and the journal are what
is left to look at. `--socket`, or `RUNGAR_SOCKET` in the environment, points
them at another; the `socket` key moves it.

The service runs `rungar serve --config /etc/rungar/config.yaml`, restarts it
five seconds after it fails, and confines it: it can read its configuration,
and write nothing but its socket and its events, in `/var/log/rungar`. To
change the service -- to give it a Dicer socket's group with
`SupplementaryGroups=`, say -- use a drop-in rather than editing the unit,
which an upgrade replaces:

```console
$ sudo systemctl edit rungar
```

Its log is the journal's:

```console
$ journalctl -u rungar -f
```

Each line says which part of the daemon wrote it -- `component=scaleset`,
`component=fleet`, `component=listener` -- and, where it is about one, which
scale set. Runners made and removed, jobs started and completed, providers
refusing runners and why, and runners removed for never connecting are all
logged at `info` or `warn`. `log_level: debug` in the configuration adds
what Rungar looks at on every reconciliation, for tracking a problem down.

What the daemon did to runners, providers and scale sets, and why, is also
kept apart from the journal, a line each, as events:

```console
$ sudo -u rungar rungar events --since 1h
2026-09-28 10:15:02   Runner     rungar-c2-m4-x7k2p   Created       Runner created on compute1: 2 vCPU, 4 GiB
2026-09-28 10:16:40   Provider   compute2            Full          Full for runner rungar-c2-m4-q9d1: out of disk; rungar-c2-m4 tries the next provider, and this one again in 15s
2026-09-28 10:21:13   Runner     rungar-c2-m4-x7k2p   Removed       Runner removed from compute1: its job completed
```

A runner is created, adopted, removed or lost -- forgotten without its
machine being removed, because the machine went or its provider stopped
answering. A provider is full for a scale set, or failing to make its
runner. A scale set is paused or resumed. `-f` follows new events as they
happen, `--scale-set`, `--provider` and `--runner` narrow them, and `--json`
writes them for a script. They are kept in `/var/log/rungar/events.jsonl`,
and outlive a restart: the last 10,000, unless `events.max_count` or
`events.max_age` in the configuration says otherwise. See
[Events](../../reference/events) for every event, and what each says.

## Changing the configuration

Rungar reads its configuration, and the files it names -- a token, a
provider's CA -- when it starts. A change is taken up by restarting it,
which disturbs no running job: see
[Stopping and restarting](#stopping-and-restarting). The one exception is a
Dicer or Docker provider's client certificate and key, which are read again
on every connection, so a renewed certificate is used without a restart.

A configuration that does not load stops the daemon from starting, so check
a file before putting it in place. It is checked in full -- providers, scale
sets and every runner block:

```console
$ sudo -u rungar rungar validate -f /etc/rungar/config.yaml.new
/etc/rungar/config.yaml.new is valid: 2 providers, 3 scale sets
```

`rungar validate` loads the file as the daemon would, and reads the files it
names -- the GitHub credential, each provider's TLS material -- without
contacting anything or needing a daemon, and exits non-zero if the daemon
would not start from it. `rungar config -f` prints the file as the daemon
would run it, with every default filled in and secrets hidden. Then:

```console
$ sudo systemctl restart rungar
```

## Stopping and restarting

When `rungar` stops, it closes each scale set's message session, so that
GitHub stops assigning it jobs, and leaves every runner where it is: busy
ones carry on with their jobs, and idle ones wait. When it starts, it adopts
them all. A restart therefore costs nothing, and is how a changed
configuration or a new version is taken up.

A `rungar` that dies without stopping -- killed, or its machine lost --
leaves its sessions for GitHub to expire, which a daemon starting waits for.
Otherwise it is no different: the runners carry on, and the next daemon
adopts them.

To be rid of the runners of a scale set, remove it: see
[Removing a scale set](../managing-the-fleet#removing-a-scale-set).
