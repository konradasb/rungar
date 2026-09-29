---
title: Draining, pausing and removing
weight: 4
description: "Taking a provider or a scale set out of use while the daemon runs, removing scale sets and runners, and reconciling at once."
icon: switch-horizontal
related:
  - /docs/guides/running-the-daemon
  - /docs/concepts/providers
  - /docs/concepts/scale-sets
---

The commands below change what the running daemon does, without a restart
and without disturbing a job. They are run as `rungar` or root, as every
command that asks the daemon is; see
[Running the daemon](../running-the-daemon#the-service).

## Draining a provider

To take a provider out of use -- to work on a host, or to retire it --
disable it. No new runner goes to it, and its runners are left alone: busy
ones finish their jobs and go, and idle ones take the next job that comes,
and go. `rungar providers drain` disables it and waits until the last runner
has gone:

```console
$ sudo -u rungar rungar providers drain compute2
Provider compute2 is disabled until the daemon restarts.
Waiting for 2 runners on compute2 to finish
Waiting for 1 runner on compute2 to finish
compute2 is drained: no runner is left on it
```

`rungar providers disable` does the same without waiting, and
`rungar providers enable` puts a provider back. A scale set left with no
enabled provider creates no runners: its jobs wait on GitHub until one is
enabled again.

The change is the running daemon's, and takes effect at once. A restart goes
back to what the configuration says, since Rungar keeps no state of its own.
To make it last, set `disabled: true` on the provider in the configuration
and restart the daemon, and remove it, or set it back to `false`, to put the
provider back for good. A provider disabled or enabled against its
configuration shows so in `rungar status` and `rungar providers ls`:
`DISABLED until the daemon restarts`. While a provider drains, it shows as
`DRAINING`, with how many runners are left, and `DISABLED` once it has none.

Removing a provider that still has runners leaves them running, no longer
looked at or counted; see
[Providers](../../concepts/providers#moving-things-around).

## Pausing a scale set

To stop one scale set taking jobs -- its runner image is broken, its
providers are wanted for another size, or its runner group is being changed
on GitHub -- pause it. Rungar tells GitHub it has no room, so no job is
assigned to it, and creates no runner for it, not even its `min_runners`. Its
idle runners are removed at once; those running jobs finish them and go, as
they always do. The other scale sets carry on. `--wait` waits until the last
of its runners has gone:

```console
$ sudo -u rungar rungar scale-sets pause rungar-c4-m8 --wait
Scale set rungar-c4-m8 is paused until the daemon restarts.
Waiting for 3 runners of rungar-c4-m8 to finish
Waiting for 1 runner of rungar-c4-m8 to finish
rungar-c4-m8 is paused, and no runner of it is left
```

`rungar scale-sets resume` has it take jobs again: its `min_runners` are created
at once, and the jobs waiting for it are assigned within a minute, as GitHub
next hears it has room.

The jobs that target a paused scale set are not lost, but nor are they
taken: they wait on GitHub, and GitHub cancels a job that has waited for a
day. A pause shows wherever the scale set does -- `PAUSED` in
`rungar status` and `rungar scale-sets ls`, with how many runners it has
left -- and in `rungar events`, and `rungar_scale_set_paused` is 1 while it
lasts, which is worth an alert if a pause is not meant to outlive the
afternoon.

A pause is the running daemon's, and a restart goes back to the
configuration. To pause a scale set for good, set `paused: true` on it in
the configuration and restart the daemon. A scale set paused or resumed
against its configuration says so under the table in `rungar status`, and
that a restart undoes it.

## Scale sets and runners

`rungar scale-sets`, `rungar runners` and `rungar providers` ask the daemon
what the configuration, GitHub and the fleet each have, and have it remove
what is no longer wanted. They touch only what carries this installation's
labels.

```console
$ sudo -u rungar rungar scale-sets ls
NAME           STATUS      ID   GROUP     RUNNERS   ASSIGNED   RUNNING   IDLE
rungar-c2-m4   LISTENING   3    Default   0         0          0         0
rungar-c4-m8   LISTENING   4    Default   1         0          0         1
rungar-tmp     LEFTOVER    7    Default   1         0          0         1
```

A configured scale set is `LISTENING` once it is taking jobs; `STARTING`
while it is looked up on GitHub and its runners adopted; `WAITING` on standby
while another daemon's message session on it, or on the lead scale set,
ends; `UNREACHABLE` while GitHub does not answer its request for a session;
`HOLDING BACK` while a scale set of higher priority on one of its providers
waits for room; and `PAUSED` while it is [paused](#pausing-a-scale-set).

A scale set `LEFTOVER` is one no longer configured whose runners are still
on the fleet: it was removed or renamed. Nothing looks after its runners, and
GitHub still lists it. GitHub cannot be asked for every scale set it has, only
for one by name, so a scale set that is neither configured nor has a runner
does not appear here; the commands below still find it by name.

`rungar scale-sets inspect NAME` shows one: what the configuration says,
what the daemon is doing with it -- how many runners it has and wants --
what GitHub has of it, and its runners. `rungar runners ls` shows every runner,
or one scale set's with `--scale-set`, or one provider's with `--provider`.
A busy runner's `JOB` is its job's repository and name; `--json` adds the
workflow, the workflow run and when the job started. The daemon learns of a
job only as it starts, so a runner that took its job before the daemon
started shows none.

```console
$ sudo -u rungar rungar providers ls
NAME       TYPE    STATUS   RUNNERS   WEIGHT   SCALE SETS
compute1   dicer   OK       1         1        rungar-c2-m4,rungar-c4-m8
compute2   dicer   OK       0         1        rungar-c2-m4,rungar-c4-m8
```

A provider is `OK` while placement may use it; `DISABLED` or `DRAINING`
when it has been [taken out of use](#draining-a-provider); `HELD` while a
scale set of higher priority that found it full keeps lower ones off it
until it has room; and `UNREACHABLE` when its last listing failed, with
why.

`rungar providers inspect NAME` shows one provider: where it is, whether it
answers, its runners against its `max_runners`, the scale sets on it and
why any of them skips it -- full, and for how much longer, or unable to create
its runner -- and its runners. To disable or enable one, see
[Draining a provider](#draining-a-provider).

The `ls` and `inspect` commands take `--json`.

### Removing a scale set

Take it out of the configuration first, and restart the daemon; then:

```console
$ sudo -u rungar rungar scale-sets rm rungar-tmp
Removed runner rungar-tmp-129f7d8a from compute1
Removed scale set "rungar-tmp" (7) from GitHub
```

Its runners are removed the safe way round -- each one's registration first,
which GitHub refuses for a runner running a job -- and then the scale set on
GitHub. A runner running a job is left to finish, and the scale set is kept
until it has: run the command again later, or with `--wait` to wait for it.
A scale set still in the configuration is refused, since a daemon starting
from it would create it again; and so is any while a provider cannot be
listed, since that provider may hold its runners, running jobs. `--group` names the runner
group to find it in on GitHub, `Default` unless given.

### Removing a runner

```console
$ sudo -u rungar rungar runners rm rungar-c4-m8-32fa8bc9
rungar-c4-m8-32fa8bc9: removed from compute1
```

The same way round: a runner running a job is left, and said so, and so is
one still being created, to be removed once it is. A runner
of a configured scale set is removed by the daemon that runs it, which
forgets it at once, and creates another if the scale set needs one -- which is
what makes removing one the cure for a runner that is stuck.

A runner with no machine on the fleet -- its host lost -- that GitHub lists
offline, of a configured scale set, has only its registration removed:

```console
$ sudo -u rungar rungar runners rm rungar-c4-m8-9e01d7a2
rungar-c4-m8-9e01d7a2: registration removed from GitHub; it had no machine
```

### Reconciling now

Each scale set is compared with the fleet every `reconcile_interval`:
runners whose machines have ended are replaced, those that never connected are
removed, and any it is short of are created. To have that done
now -- after fixing a provider, say -- rather than wait:

```console
$ sudo -u rungar rungar reconcile
rungar-c2-m4: 0 runners, 0 desired
rungar-c4-m8: 1 runner, 1 desired
```

It names scale sets to reconcile only those, and returns once they are done.
