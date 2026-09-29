---
title: Spot on AWS, with on-demand to fall back on
weight: 4
description: "Runners on EC2 Spot Instances, and on-demand ones, up to a ceiling, when Spot has none."
related:
  - /docs/providers/aws
  - /docs/providers/aws/building-an-image
  - /docs/concepts/placement
  - /docs/examples/across-zones-on-gcp
---

For a team that runs its builds on EC2 and wants them cheap: every runner is
a Spot Instance while Spot has any, and an on-demand one, up to a ceiling,
when it has none, so that a Spot shortage costs more for a while rather than
leaving jobs queued. Each runner lives for one job, so the fleet goes back to
Spot by itself as the on-demand runners finish.

Rungar runs anywhere it can reach EC2 and GitHub: a small instance in the
account, or EKS.

## The configuration

```yaml {filename="/etc/rungar/config.yaml"}
version: 1

github:
  url: https://github.com/my-org
  app_client_id: Iv23liAbCdEf123456
  app_installation_id: 12345678
  app_private_key_path: /etc/rungar/app.pem

providers:
  # Tried first: Spot, in three zones of eu-west-1.
  - name: aws-spot
    type: aws
    region: eu-west-1
    subnets: [subnet-0a1b2c3d4e5f60718, subnet-0b2c3d4e5f6071829, subnet-0c3d4e5f607182930]
    security_groups: [sg-0a1b2c3d4e5f60718]
    runner:
      image: ami-0a1b2c3d4e5f60718
      spot: true
      tags: { team: ci }

  # Taken over when Spot is full: on-demand, in eu-central-1.
  - name: aws-on-demand
    type: aws
    region: eu-central-1
    subnets: [subnet-0d4e5f60718293a4b, subnet-0e5f60718293a4b5c]
    security_groups: [sg-0f60718293a4b5c6d]
    max_runners: 10
    runner:
      image: ami-0f1e2d3c4b5a69788
      tags: { team: ci }

scale_sets:
  - name: rungar-c4-m16
    max_runners: 30
    placement: pack
    providers:
      - name: aws-spot
        runner: { instance_type: [m7i.xlarge, m6i.xlarge, m7a.xlarge] }
      - name: aws-on-demand
        runner: { instance_type: [m7i.xlarge, m6i.xlarge] }

metrics:
  enable: true
```

Workflows run on it with `runs-on: rungar-c4-m16`.

## What each part does

**`github`** is the organisation, and the GitHub App Rungar acts as; see
[GitHub credentials](../../guides/github-credentials).

**Two providers**, one for each way of paying. Rungar authenticates to both
the same way, through the AWS SDK's default chain -- the instance's role, or
the Helm chart's service account on EKS -- with the permissions on
[AWS](../../providers/aws#requirements), in both regions. The account is the
runners' own, since anyone who can describe its instances can read a
runner's registration before it is used.

- **`aws-spot`** creates Spot Instances, `spot: true` in its runner block, in
  three subnets, each in an Availability Zone of its own.
- **`aws-on-demand`** creates on-demand ones, in another region: Rungar
  refuses two aws providers in one region, since both would create runners
  there and count them apart. Its AMI is the same image, copied to that
  region, or built there with `-var region=eu-central-1`; see [Building an
  image](../../providers/aws/building-an-image).
  **`max_runners: 10`** caps what the on-demand price is paid for at once.
- **`subnets`** route to the internet through a NAT gateway, so the instances
  need no public address; without one, set `public_ip: true`.
- **`tags`** go on every instance and its volume, for the bill.

**The scale set** lists Spot first, and **`placement: pack`** makes it try
its providers in that order, every time. The default, `spread`, would send
every other runner to on-demand, to even them out. See [Trying providers in
order](../../concepts/placement#trying-providers-in-order).

Each provider's block says the size as a list of **instance types**, all
4 vCPUs and 16 GiB, the preferred first. Spot is short of one type at a time,
so a list finds Spot capacity more often than more subnets do; on-demand
needs fewer. The root volume is the default, 50 GiB of gp3. See
[`runner.instance_type`](../../providers/aws/configuration#runner-instance-type).

## When Spot runs out

Say Spot has nothing left in eu-west-1 for runner `rungar-c4-m16-1ff1015a`:

1. Rungar asks aws-spot for an `m7i.xlarge` in the first subnet. EC2 has none
   -- `InsufficientInstanceCapacity` -- so it asks for an `m6i.xlarge` there,
   then an `m7a.xlarge`, and then the same in the second subnet and the
   third. A type the account is out of Spot quota for
   (`MaxSpotInstanceCountExceeded`) is not tried again in the other subnets,
   since the quota is the region's.
2. Every attempt refused, aws-spot refuses the runner as **full**, and the
   same runner is tried at once on aws-on-demand, which creates it. The job
   waits no longer than the refusals took.
3. The scale set now leaves aws-spot alone for 15 seconds, and its runners in
   that time go straight to aws-on-demand. Then aws-spot is tried first
   again; refused again, it is left alone for 30 seconds, then a minute, then
   two minutes at a time, until a Spot Instance is created and the wait is
   forgotten.
4. Should aws-on-demand reach its 10 runners while Spot is still full, it is
   not tried either, and no runner is created: the jobs wait on GitHub until
   a runner finishes or Spot has room.

`rungar status` shows how many runners each provider has, aws-on-demand's
against its limit, and a line for the provider found full:

```console
rungar-c4-m16 found aws-spot full; tries it again in 47s
```

`rungar events --provider aws-spot` lists each refusal, with EC2's reason for
each subnet and type; see [Troubleshooting](../../guides/troubleshooting).

Nothing is moved back afterwards, and nothing needs to be: an on-demand
runner takes one job, and Rungar removes it when the job completes. The next
runner goes to Spot if it has room.

A Spot Instance EC2 takes back is terminated, and the job on it fails.
Rungar finds the runner gone at its next reconciliation, records it as lost,
and creates another if the scale set still wants one. The job is re-run as
any failed job is. `rungar_runners_lost_total`, by provider, says how often,
and `rungar_provider_runners` how much of the fleet is on-demand at any
moment; see [Monitoring](../../guides/monitoring).

## Variations

- **Graviton.** A second scale set, of Arm instance types and an arm64 AMI,
  each in its runner block for each provider:

  ```yaml
  scale_sets:
    - name: rungar-arm64-c4-m16
      max_runners: 10
      placement: pack
      providers:
        - name: aws-spot
          runner: { image: ami-0b2c3d4e5f6071829, instance_type: [m7g.xlarge, m6g.xlarge] }
        - name: aws-on-demand
          runner: { image: ami-0c3d4e5f607182930, instance_type: [m7g.xlarge, m6g.xlarge] }
  ```

  The AMIs are built with `-var arch=arm64`. A scale set is one
  architecture: a list mixing Arm and x86 types would fail on whichever the
  AMI is not for.
- **No on-demand.** Leave aws-on-demand out. When Spot is full, jobs wait on
  GitHub, and Rungar tries Spot again once its wait is up, two minutes at
  the most.
- **More than the keys say.** A
  [`launch_template`](../../providers/aws/configuration#runner-launch-template)
  gives a size extra volumes, a placement group or a capacity reservation.
