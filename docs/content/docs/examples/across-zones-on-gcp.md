---
title: Across zones on Google Cloud
weight: 3
description: "Spot runners on Compute Engine, spread over three zones, with machine types to fall back on when a zone runs out."
related:
  - /docs/providers/gcp
  - /docs/providers/gcp/building-an-image
  - /docs/concepts/placement
  - /docs/examples/spot-on-aws
---

For a team with no machines of its own, or none to spare: runners on
Compute Engine, created when a job asks and deleted when it is done, so
nothing is paid for between jobs. They are Spot VMs, much cheaper than
standard ones, and spread over three zones of one region with a choice of
machine types, so that a zone out of one type is not a job left waiting.

Rungar runs anywhere it can reach Compute Engine and GitHub: a small VM in
the same project, or GKE.

## The configuration

```yaml {filename="/etc/rungar/config.yaml"}
version: 1

github:
  url: https://github.com/my-org
  app_client_id: Iv23liAbCdEf123456
  app_installation_id: 12345678
  app_private_key_path: /etc/rungar/app.pem

providers:
  - name: gcp
    type: gcp
    project: my-ci-123456
    zones: [europe-west1-b, europe-west1-c, europe-west1-d]
    network: runners
    subnetwork: runners-europe-west1
    external_ip: false
    max_runners: 30
    runner:
      image: projects/my-ci-123456/global/images/family/actions-runner
      spot: true
      labels: { team: ci }

scale_sets:
  - name: rungar-c4-m16
    max_runners: 24
    providers:
      - name: gcp
        runner:
          machine_type: [c3-standard-4, n2-standard-4, n2d-standard-4]

  - name: rungar-c8-m32
    max_runners: 8
    providers:
      - name: gcp
        runner:
          machine_type: [c3-standard-8, n2-standard-8, n2d-standard-8]
          disk_size: 100GiB
          disk_type: pd-ssd

metrics:
  enable: true
```

Workflows run on it with `runs-on: rungar-c4-m16`, or `rungar-c8-m32` for
the builds worth the cores.

## What each part does

**`github`** is the organisation, and the GitHub App Rungar acts as; see
[GitHub credentials](../../guides/github-credentials).

**The provider** is the project, and Rungar authenticates as whatever
Application Default Credentials find: the VM's service account, or the Helm
chart's on GKE. It needs `roles/compute.instanceAdmin.v1` on the project;
see [GCP](../../providers/gcp#requirements). The project is the runners'
own, since anyone who can view its instances can read a runner's
registration before it is used.

- **`zones`** are three zones of one region. A runner is tried in each of
  them in turn, and each of a scale set's runners starts at the zone after
  the one its last runner started at, so that they spread across all three.
- **`network`** and **`subnetwork`** are a network of the runners' own,
  rather than the `default` network, which allows SSH from anywhere. With
  **`external_ip: false`** the instances have no public address, and reach
  GitHub through Cloud NAT on the subnetwork, which you set up.
- **`max_runners: 30`** is a ceiling on the whole provider, of every scale
  set together, and so on what the project can cost at once: the two
  scale sets could have 32 between them. A provider at its limit is not
  tried; see [Limits](../../concepts/placement#limits).
- The **`runner`** block is what both sizes share: the
  [image](../../providers/gcp/building-an-image), built by the repository's
  Packer template, named by its family so that each new runner boots the
  newest build; `spot: true`; and a label, which
  Compute Engine's billing can be split by.

**Each scale set** says only its size, as a list of machine types of that
size, the one preferred first. `machine_type` accepts one type or a list;
see [`runner.machine_type`](../../providers/gcp/configuration#runner-machine-type).
The large size has a larger, faster disk; the small one keeps the default,
50 GiB of `pd-balanced`.

Instances are created without a service account, so a job has no Google
Cloud identity unless you give it one. See the [provider's
notes](../../providers/gcp#notes) before setting `service_account`.

## When a zone runs out

Spot capacity comes and goes by zone and by machine type. Say runner
`rungar-c4-m16-1ff1015a` starts at europe-west1-c:

1. Rungar asks for a `c3-standard-4` there. The zone is out of stock --
   `ZONE_RESOURCE_POOL_EXHAUSTED` -- and nothing was created, or what was
   is deleted.
2. It asks for an `n2-standard-4` in the same zone, which is created. The
   job starts on it.

Every machine type is tried in a zone before the next zone, since on Spot a
different type in the same zone finds capacity more often than the same type
in another zone. A zone that does not offer a type at all is passed over,
without asking; Rungar asks Compute Engine once an hour which do. A type the
project is out of **quota** for is not tried again in the other zones, since
quotas are the region's or the project's.

When every type in every zone is out of stock or out of quota, the provider
refuses the runner as **full**. It is not created: its registration is
removed, and the job waits on GitHub. The scale set leaves the provider alone
for 15 seconds, doubling each time it is refused in a row, up to 2 minutes,
and tries again after that, on the next message from GitHub or within
`reconcile_interval`. One runner created resets the wait. `rungar status`
shows the provider found full, and for how much longer it is skipped;
`rungar events --provider gcp` lists each refusal, with Compute Engine's
reason for each zone and type.

Some refusals stop the search at once rather than trying the next zone:

- A runner that can never be created -- a machine type that does not exist,
  an image that is not there. It is skipped as failing, up to two minutes at
  a time, until the configuration is fixed; see
  [Troubleshooting](../../guides/troubleshooting).
- A refusal every zone would share, such as a missing permission or too many
  requests.
- An insert whose outcome Compute Engine leaves unknown, even after Rungar
  has asked again. The instance may have been created, and another zone could
  create a second, so Rungar stops there. The provider deletes whatever
  instance has the runner's name, its registration is removed, and the scale
  set skips the provider for a while, as failing.

A Spot VM Compute Engine takes back is deleted, and the job on it fails.
Rungar finds the runner gone at its next reconciliation, records it as lost,
and creates another if the scale set still wants one. The job is re-run as
any failed job is. `rungar_runners_lost_total`, by provider, says how
often; see [Monitoring](../../guides/monitoring).

## Variations

- **Standard VMs.** Leave `spot` out. Runners cost more and are never taken
  back, and a stockout is rarer, but zones and machine types are tried the
  same way.
- **Standard VMs when Spot runs out.** A second gcp provider without
  `spot`, listed after the first with `placement: pack`, as [Spot on
  AWS](../spot-on-aws) does on EC2. Rungar refuses two gcp providers in one
  project and region, since both would create runners there and count them
  apart, so the second is in another region or project.
- **Arm.** A scale set of its own, with Arm machine types such as
  `t2a-standard-4`, and the `actions-runner-arm64` image family in its
  runner block; see [Building an
  image](../../providers/gcp/building-an-image).
- **More than the keys say.** An
  [`instance_template`](../../providers/gcp/configuration#runner-instance-template)
  gives a size GPUs, local SSDs or a minimum CPU platform.
