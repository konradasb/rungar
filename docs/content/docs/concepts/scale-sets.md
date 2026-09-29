---
title: Scale sets
weight: 2
description: "What GitHub sees, and how many runners Rungar keeps for it."
icon: collection
related:
  - /docs/concepts/runner-lifecycle
  - /docs/concepts/placement
  - /docs/guides/designing-runner-sizes
  - /docs/reference/configuration
---

A scale set is a group of runners that GitHub queues jobs for, and that
Rungar keeps supplied. It is GitHub's own idea -- the one its
`actions-runner-controller` uses too -- and it is what a workflow names when
it says what it runs on:

```yaml
jobs:
  build:
    runs-on: rungar-c2-m4
```

Each scale set in Rungar's configuration is one on GitHub, and one size of
runner:

```yaml
scale_sets:
  - name: rungar-c2-m4
    min_runners: 1
    max_runners: 8
    providers:
      - name: compute1
        runner: &c2-m4 {vcpus: 2, memory: 4GiB}
      - name: compute2
        runner: *c2-m4
```

## On GitHub

A scale set belongs to where `github.url` points: a repository, an
organisation or an enterprise. Its **name** is the label workflows target it
by, and further **labels** can be given beside it. Its **runner group**
decides which repositories may use it; unset is GitHub's `Default` group.

Rungar looks a scale set up by its name in its runner group when it starts,
and creates it only if GitHub does not have it. A restart therefore adopts
the scale set GitHub already knows, with the jobs already assigned to it,
rather than making a second one. The daemon never deletes one on its own:
stopping Rungar, or taking a scale set out of the configuration, leaves it on
GitHub, with jobs queued for it waiting until a runner comes. Only
`rungar scale-sets rm` deletes one.

Two things follow from adopting rather than creating:

- **Labels are set when the scale set is created.** GitHub keeps them from
  then on: it accepts a change and ignores it. So changing `labels` in the
  configuration does nothing to a scale set that exists, and Rungar says so
  when it starts. To change them, delete the scale set on GitHub and let
  Rungar create it again.
- **Renaming a scale set makes a new one.** The old name stays on GitHub,
  with nothing serving it, until it is deleted there.

A scale set is served by one Rungar at a time. GitHub allows one message
session per scale set, and a second daemon configured with the same one waits
until the first lets it go.

## How many runners

Every message GitHub sends, and every poll that brings none -- at least once
a minute -- carries the number of jobs assigned to the scale set: waiting for
a runner, or running on one. From it Rungar works out how many runners there
should be:

> runners = min(`max_runners`, `min_runners` + assigned jobs)

or none while the scale set is paused, when the capacity Rungar reports to
GitHub is none too. A scale set is paused with `paused: true` in the
configuration, or while the daemon runs with `rungar scale-sets pause`; see
[Pausing a scale set](../../guides/managing-the-fleet#pausing-a-scale-set).

- **`min_runners`** are kept idle and ready even with nothing queued, so that
  a job does not wait for a machine to boot. They are on top of the jobs: with
  `min_runners: 1` and three jobs assigned, there are four runners, one of
  them waiting for the next job.
- **`max_runners`** is the most there are at once. It is also the capacity
  Rungar reports to GitHub, so GitHub does not assign the scale set more jobs
  than it can run; the rest stay queued on GitHub.

When there are too few, Rungar creates runners until there are enough, or
until its providers refuse more. A fleet that is full is not an error: the jobs
wait on GitHub, and Rungar tries again on the next message and on every
reconciliation, so a runner is made within `reconcile_interval` of room
freeing up.

When there are too many -- jobs were cancelled, or other runners took them --
Rungar gives back idle runners until there are as many as there should be. It
never takes away a runner that is running a job, and before removing an idle
one it has GitHub remove its registration, which GitHub refuses for a runner
it has just given a job. See [Runner lifecycle](../runner-lifecycle).

## Several scale sets

A daemon runs any number of scale sets, which is how a fleet offers more than
one size of runner: `rungar-c2-m4` and `rungar-c8-m16` are two scale sets, and
a workflow picks one by name.

Each is placed on the [providers](../providers) it lists, and says what its
runners are made of in their terms. Scale sets sharing a provider share it
and nothing else: each has its own message session, its own runners -- told
apart by a label on every machine -- and its own metrics. What they compete
for is the providers' room: **`placement`** decides which of its providers a
scale set's runner is tried on first, and **`priority`** who gets the room
when there is not enough; see [Placement](../placement).

## Starting runners

**`start_timeout`** -- five minutes unless set -- is how long a runner has to
connect to GitHub before Rungar gives up on it and replaces it. It is what
keeps a machine that never becomes a runner from standing in for one that could
take a job, and it bounds nothing about a job, which runs for as long as it
runs; see [Runner lifecycle](../runner-lifecycle#runners-that-never-connect).

## Keeping runners fresh

A runner changed in the configuration applies to the runners made after it,
and Rungar replaces the others, one at a time, as they sit idle. A tag such as
`:latest` is another matter: the configuration has not changed, so a reserve
kept by `min_runners` can run jobs on whatever the tag pointed at when it was
made, for as long as it waits. Pin the image to a digest, or set
**`max_idle_age`** to replace runners that have waited that long.
**`max_age`** bounds every runner's life, one running a job included; it is a
last resort for a job that hangs, which GitHub's own `timeout-minutes` should
end first. Both are unset unless set.
