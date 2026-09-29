---
title: Designing runner sizes
weight: 2
description: "Several sizes of runner on one fleet: scale sets, reserves, priority and caches."
icon: adjustments
related:
  - /docs/concepts/scale-sets
  - /docs/concepts/placement
  - /docs/concepts/providers
  - /docs/reference/providers
---

A fleet usually offers more than one size of runner: a small one for linting
and unit tests, a large one for builds that are worth the cores. Each size is
a scale set, and a workflow picks one by name:

```yaml
jobs:
  lint:
    runs-on: rungar-c2-m4
  build:
    runs-on: rungar-c8-m16
```

This guide is about choosing those sizes, and making them share a fleet
well. It holds for every provider; its examples are written for the `dicer`
type, and what a runner block says in another type's terms is in that type's
[reference](../../reference/providers).

## One scale set per size

Name a scale set after what it offers, so that a workflow says what it needs
rather than where it runs: `rungar-c8-m16` is 8 vCPUs and 16 GiB. What the
sizes share -- the image, the network, the environment -- belongs in the
providers' `runner` block, and each scale set says only its size, once for
each of its providers. Hosts alike can share it with an anchor:

```yaml
providers:
  - &dicer
    name: compute1
    type: dicer
    address: 10.10.0.101:7443
    runner:
      image: ghcr.io/actions/actions-runner:latest
      kernel: linux-6.18
  - <<: *dicer
    name: compute2
    address: 10.10.0.102:7443

scale_sets:
  - name: rungar-c2-m4
    max_runners: 16
    providers:
      - name: compute1
        runner: &c2-m4 { vcpus: 2, memory: 4GiB }
      - name: compute2
        runner: *c2-m4
  - name: rungar-c8-m16
    max_runners: 4
    providers:
      - name: compute1
        runner: &c8-m16 { vcpus: 8, memory: 16GiB, disk: 50GiB }
      - name: compute2
        runner: *c8-m16
```

A scale set spanning providers of different types says its size in each
one's terms -- `vcpus` and `memory` on a Dicer host, a `machine_type` on
Compute Engine -- since each block is read by its own provider.

A size is only useful if a provider can fit it. A provider full for a size
refuses it, and the runner is tried on the next; one that can never fit it --
more vCPUs than a host has -- refuses it every time, and says why.
A size no provider can fit is never placed, and its jobs wait on GitHub.

Sizes that divide a machine evenly waste the least. A machine that allows 64
vCPUs and 124 GiB fits seven runners of 8 vCPUs and 16 GiB -- memory runs out
first -- leaving 8 vCPUs and 12 GiB that no runner of that size can use; it
fits thirty-one of 2 vCPUs and 4 GiB.

## How big a vCPU is

A vCPU is not always a CPU of its own. Many backends let several vCPUs share
each CPU, since most workloads leave theirs idle much of the time. A build
does not: several busy runners sharing CPUs are each as slow as the share
they get, whatever size they were given.

How far a provider overcommits is the backend's setting, not Rungar's; its
reference says where it is. For a fleet that runs builds, keep it low -- one
vCPU to a CPU, at best -- and size the runners for it: fewer fit, and each is
as fast as it says.

## Where runners go

A scale set lists the providers its runners may go on, which is how a size
is kept to the providers that suit it. A large size can be given only the
large machines, and a small one all of them:

```yaml
scale_sets:
  - name: rungar-c2-m4
    max_runners: 20
    providers:
      - { name: compute1, runner: &c2-m4 { vcpus: 2, memory: 4GiB } }
      - { name: compute2, runner: *c2-m4 }
      - { name: compute3, runner: *c2-m4 }
  - name: rungar-c32-m64
    max_runners: 2
    providers:
      - { name: compute3, runner: { vcpus: 32, memory: 64GiB } }
```

`placement` decides between them. `spread`, the default, evens runners out
across the providers. `pack` fills them in the order listed, which keeps
whole machines free for large runners when small ones share them; see
[Placement](../../concepts/placement).

## Keeping runners ready

A runner takes a while to boot, and longer where its image has not been
fetched yet. `min_runners` keeps that many waiting, idle, whether or not
any job is queued, so a job starts at once:

```yaml
  - name: rungar-c2-m4
    min_runners: 2
```

A runner kept ready holds its room on its provider all the while. It is worth it
for a size that jobs wait on -- one on the path of every pull request -- and
not for one used now and then.

## Priority

A size of small runners fills the gaps a large one needs. When the providers
are busy, a large runner waits for enough room to open up at a moment no small
runner is asking for it, which may not come. `priority` makes the large size
first in line: when it cannot find room, sizes of lower priority on the same
providers hold back for a while, so that the next room to open up is its own.

```yaml
  - name: rungar-c8-m16
    priority: 10
```

Give it to the sizes that are hardest to fit, not to the ones most in
demand: the small sizes will always find room somewhere. See
[Placement](../../concepts/placement#priority).

## Disks

A runner's disk is where the checkout, the build and everything the job
installs goes, and it goes with the runner: every job starts from the image.
How big it is by default, and whether it takes space before it is written
to, is the provider's; its reference says.

A job that runs out of disk fails with the disk full, not with anything that
says so plainly. Give a size that builds large things a larger disk.
A backend whose disk is full refuses runners as full, and they go to the next
provider.

## Caches

Every job starts from nothing, which is the point, and also means every job
downloads its dependencies again. There are a few ways to keep some of that:

- **In the image.** Tools and dependencies that change rarely -- a
  toolchain, an SDK -- are best built into the runner image. Every runner has
  them from boot, and nothing is shared between jobs.
- **Shared read-only storage.** Where the provider can attach storage to a
  runner, something filled by other means -- a package mirror, a dataset --
  can be shared by every runner, read-only. Storage written by several
  runners at once is a different matter, and most backends refuse it.
- **GitHub's cache.** `actions/cache` and the package managers' own caching
  actions work as on GitHub's runners, over the network.
- **Memory.** Scratch files in memory are faster than on disk, where the
  provider offers it, and count against the runner's memory: size it for
  them.

What storage a provider can attach, and how it is shared, is in its
reference.

## A runner image of your own

Whatever a provider boots -- a container image, a VM image, a template -- has
the Actions runner in it. A size that needs more -- a compiler, a cloud CLI,
a browser for tests -- is better served by an image built with it than by
installing the same things at the start of every job. For a provider that
boots container images, that is one built on the official
`ghcr.io/actions/actions-runner`:

```dockerfile
FROM ghcr.io/actions/actions-runner:latest
USER root
RUN apt-get update \
 && apt-get install -y --no-install-recommends build-essential jq \
 && rm -rf /var/lib/apt/lists/*
USER runner
```

Pin the image, where the provider allows it, so that every runner boots the
same one whatever happens to its tag. And keep it current: GitHub stops
accepting runners that are too far behind.

A job that uses Docker -- a `container:` job, a service container, or
`docker build` -- needs a Docker daemon in the runner, which the official
image does not run. What it takes to run one depends on the backend; each
provider's reference says.
