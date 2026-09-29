---
title: Own hardware first, the cloud for overflow
weight: 5
description: "Two Dicer hosts take every runner they have room for, and EC2 takes the rest, up to a ceiling."
related:
  - /docs/concepts/placement
  - /docs/providers/dicer
  - /docs/providers/aws
  - /docs/examples/spot-on-aws
---

For a team whose own machines are enough on most days and not on the
busiest: runners go on two [Dicer](https://github.com/konradasb/dicer) hosts
while they have room, and on EC2 when they are full, so that a busy
afternoon costs some cloud time rather than queued jobs. A ceiling on the
cloud bounds what that can cost.

Each host has 32 CPUs and 128 GiB of memory, and its Dicer daemon gives each
vCPU a CPU of its own, `resources.cpu_overcommit: 1`. Rungar runs on a third
machine, reaches the hosts over TLS, and EC2 with an IAM role.

## The configuration

```yaml {filename="/etc/rungar/config.yaml"}
version: 1

github:
  url: https://github.com/my-org
  app_client_id: Iv23liAbCdEf123456
  app_installation_id: 12345678
  app_private_key_path: /etc/rungar/app.pem

providers:
  # The hosts: tried first.
  - &dicer
    name: compute1
    type: dicer
    address: 10.10.0.101:7443
    tls:
      ca_file: /etc/rungar/ca.pem
      cert_file: /etc/rungar/rungar.pem
      key_file: /etc/rungar/rungar-key.pem
    runner:
      image: ghcr.io/actions/actions-runner:2.337.0@sha256:4fa04ffcb6472b581c0f3f8fc50371bf728efce2d1a0f70e7056206250a30294
  - <<: *dicer
    name: compute2
    address: 10.10.0.102:7443

  # The overflow: on-demand EC2, twelve runners at most.
  - name: aws
    type: aws
    region: eu-west-1
    subnets: [subnet-0a1b2c3d4e5f60718, subnet-0b2c3d4e5f6071829]
    security_groups: [sg-0a1b2c3d4e5f60718]
    max_runners: 12
    runner:
      image: ami-0a1b2c3d4e5f60718
      tags: { team: ci }

scale_sets:
  - name: rungar-c4-m8
    max_runners: 24
    placement: pack
    providers:
      - { name: compute1, runner: &c4-m8 { vcpus: 4, memory: 8GiB, disk: 40GiB } }
      - { name: compute2, runner: *c4-m8 }
      - { name: aws, runner: { instance_type: [c7i.xlarge, c6i.xlarge], disk_size: 40GiB } }

  - name: rungar-c8-m16
    max_runners: 6
    placement: pack
    providers:
      - { name: compute2, runner: &c8-m16 { vcpus: 8, memory: 16GiB, disk: 80GiB } }
      - { name: compute1, runner: *c8-m16 }
      - { name: aws, runner: { instance_type: [c7i.2xlarge, c6i.2xlarge], disk_size: 80GiB } }

metrics:
  enable: true
```

Put the digest of the image you use in place of the example's, and see
[Dicer's remote access](https://dicer.sh/docs/guides/remote-access/) for
the certificates and [Building an image](../../providers/aws/building-an-image)
for the AMI.

## How it is laid out

**The hosts** are a provider each, sharing the image and the TLS settings
through a YAML anchor, as in [Growing it into a
fleet](../single-host#growing-it-into-a-fleet). A host fits eight medium
runners or four large, or any mix of 32 vCPUs.

**The cloud** is one aws provider, with **`max_runners: 12`**: the most
instances it has at once, of both sizes together, whatever the scale sets'
ceilings would allow. It is the ceiling on what the overflow costs. A
provider at its limit is not tried; see [Limits](../../concepts/placement#limits).

**Each size is said for each provider**, in its own terms: vCPUs and memory
on a host, instance types of the same size on EC2, `c7i.xlarge` being
4 vCPUs and 8 GiB. The disks match, so that a job that fits on one fits on
the other. The AMI and the container image are built differently, and should
carry the same tools; see [A runner image of your
own](../../guides/designing-runner-sizes#a-runner-image-of-your-own).

**`placement: pack`** has each scale set try its providers in the order it
lists them, every time: the hosts first, and aws only when both refuse. The
medium size fills compute1 first and the large one compute2, so that medium
runners do not scatter over both hosts and leave no room whole for a large
one; see [Trying providers in
order](../../concepts/placement#trying-providers-in-order). Both list aws
last. Giving the hosts a higher `weight` instead would put aws last
whatever a scale set's order.

## Under load

On a quiet day every runner is on a host. On a busy one:

1. compute1 is full of medium runners, and refuses the next. The same runner
   is tried at once on compute2, and when that refuses too, on aws, which
   creates it. The job waits no longer than the refusals take: for a Dicer
   host, a moment.
2. The medium scale set now leaves compute1 and compute2 alone for 15
   seconds, doubling each time they refuse in a row, up to 2 minutes, and its
   runners in that time go straight to aws. A host that frees room meanwhile
   is used once its wait is up: the price of not asking the hosts at every
   runner is up to two minutes of runners on the cloud that a host could
   have taken.
3. With twelve runners on aws, it is at its limit, and is not tried. With
   the hosts full too, no runner is created, and the jobs wait on GitHub
   until one finishes.

Halfway through, `rungar status` shows the hosts full for both sizes, and
aws with eight of its twelve:

```console
$ rungar status
...
Scale sets (2)
  SCALE SET       STATUS      PROVIDERS                      LABELS          RUNNER                                       RUNNERS                    DESIRED   MIN   MAX   PRIORITY
  rungar-c4-m8    LISTENING   compute1,compute2,aws (pack)   rungar-c4-m8    4 vCPU, 8 GiB; c7i.xlarge or c6i.xlarge      18 (17 busy, 1 starting)   18        0     24    0
  rungar-c8-m16   LISTENING   compute2,compute1,aws (pack)   rungar-c8-m16   8 vCPU, 16 GiB; c7i.2xlarge or c6i.2xlarge   4 (4 busy)                 4         0     6     0

Providers (3)
  PROVIDER   TYPE    STATUS   RUNNERS
  compute1   dicer   OK       8
  compute2   dicer   OK       6
  aws        aws     OK       8/12
  rungar-c4-m8 found compute1 full; tries it again in 47s
  rungar-c8-m16 found compute1 full; tries it again in 2m0s
  rungar-c4-m8 found compute2 full; tries it again in 52s
  rungar-c8-m16 found compute2 full; tries it again in 1m48s
...
```

Nothing moves back to the hosts when the rush is over, and nothing needs
to: each runner takes one job, Rungar removes it when the job completes, and
the provider deletes its machine -- on EC2, terminates the instance. The
next runner goes to a host once a host has room.

A host that stops answering is unreachable, and left out of placement: its
scale sets' runners go to the other host or to aws in the meantime. The
runners on it are left alone for five minutes, in case it comes back, and
then written off and replaced; see [Providers that cannot be
reached](../../concepts/state-and-adoption#providers-that-cannot-be-reached).

## What the cloud costs

An overflow runner is paid for from its instance's creation to its
termination: its boot, its job, and the moments after. At most twelve run at
once. To see what the overflow is, watch:

- `rungar_provider_runners{provider="aws"}`: how many runners are on EC2 now,
  by scale set;
- `rungar_runners_created_total{provider="aws"}`: how many have been created
  there, which with the instance types' price and the jobs' length is the
  bill, roughly;
- `rungar_job_runner_wait_seconds`: whether jobs wait, which is what the
  limit trades against.

A cloud that takes runners every day is a sign the hosts are too few; one
that is at its limit for hours, that its limit is too low. See
[Monitoring](../../guides/monitoring).

## Variations

- **Spot for the overflow.** `spot: true` in aws's runner block makes the
  overflow cheaper, and its jobs fail when EC2 takes an instance back; see
  [Spot on AWS](../spot-on-aws).
- **Google Cloud for the overflow.** A gcp provider in place of aws, with
  machine types for each size; see [Across zones on Google
  Cloud](../across-zones-on-gcp).
- **A Proxmox VE cluster for the hosts.** A proxmox provider in place of the
  two Dicer ones; see [Several sizes on Proxmox VE](../several-sizes).
