---
title: Several sizes on Proxmox VE
weight: 2
description: "Three sizes of runner on one Proxmox VE cluster, from one template, with a reserve and a priority."
related:
  - /docs/providers/proxmox
  - /docs/guides/designing-runner-sizes
  - /docs/concepts/placement
  - /docs/examples/cloud-for-overflow
---

For a team with a Proxmox VE cluster and room on it for CI: three sizes of
runner, each a VM cloned from one template and destroyed after its job. A
small size for linting and unit tests, kept warm in working hours; a medium
one for most builds; and a large one for the builds worth the cores, which
gets the room it needs before the others.

The cluster has three nodes, pve1 to pve3, each with 64 GiB of memory, about
60 GiB of it for VMs. Rungar runs on a machine of its own, or in a VM on the
cluster, and reaches the cluster's API over HTTPS.

## The configuration

```yaml {filename="/etc/rungar/config.yaml"}
version: 1

github:
  url: https://github.com/my-org
  app_client_id: Iv23liAbCdEf123456
  app_installation_id: 12345678
  app_private_key_path: /etc/rungar/app.pem

providers:
  - name: pve
    type: proxmox
    url: https://pve.example.com:8006
    token_id: rungar@pve!rungar
    token_secret_path: /etc/rungar/pve-token
    tls:
      ca_file: /etc/rungar/pve-root-ca.pem
    nodes: [pve1, pve2, pve3]
    runner:
      template: 9000
      pool: runners

scale_sets:
  # Linting and unit tests: two kept warm on weekdays.
  - name: rungar-c2-m4
    max_runners: 16
    schedule:
      timezone: Europe/Vilnius
      windows:
        - { days: [mon-fri], from: "08:00", to: "19:00", min_runners: 2 }
    providers:
      - { name: pve, runner: { cores: 2, memory: 4GiB } }

  # Most builds.
  - name: rungar-c4-m8
    max_runners: 8
    providers:
      - { name: pve, runner: { cores: 4, memory: 8GiB } }

  # The large builds: first in line for room.
  - name: rungar-c8-m16
    max_runners: 3
    priority: 10
    providers:
      - { name: pve, runner: { cores: 8, memory: 16GiB } }

metrics:
  enable: true
```

The token's secret is kept in `/etc/rungar/pve-token`, and the cluster's CA,
copied from `/etc/pve/pve-root-ca.pem` on any node, beside it; both owned
`root:rungar` and mode `0640`.

## How it is laid out

**One provider is the whole cluster.** Rungar reaches the API through one
node, and sees every node's VMs through it. The provider puts each VM on
the node with the most memory free. **`nodes`**
keeps runners to the three nodes named, should the cluster have others. A
second provider for another node of the same cluster would see the same VMs
and count them twice: add one for another cluster only. Should the node the
`url` names be down, the provider is unreachable until it is back, so a name
that moves to a node that is up, by DNS or a load balancer, serves it best.

**One template for every size.** The provider's **`runner`** block has what
the sizes share: template 9000, prepared as [Preparing a
template](../../providers/proxmox/preparing-a-template) says, and the pool
the VMs go in, which the API token needs its privileges on; see [Proxmox
VE](../../providers/proxmox#requirements). Each scale set says only its
cores and memory. Clones are linked, which is quick, and needs the template
on storage every node shares and that supports linked clones; otherwise set
`full_clone`. Every size gets the template's disk as it is: a size that needs
a larger one needs a template of its own.

**Memory is what runs out.** The provider refuses a runner when no node has
its memory free. Cores it checks only against a node's size, since VMs share
them. A node holds 60 GiB of VMs: fifteen small
runners, seven medium or three large, or any mix of that much memory.

**The ceilings add up to the cluster, and no more.** The three
`max_runners` are 64, 64 and 48 GiB of runners, 176 GiB against the cluster's
180. Proxmox VE judges what a node has free by the memory in use there now,
and a VM that has just booted uses less than it will once its build is
running. Ceilings past what the cluster holds would let it clone more VMs
than the nodes can run once they are busy. Rungar's limits are counted from
what it has created, so they are what keeps the cluster from being promised
too much; see [Limits](../../concepts/placement#limits).

**The reserve** is two small runners, idle and ready on weekdays from 08:00
to 19:00, so that a lint job starts at once. They hold 8 GiB while they
wait, which is cheap; a reserve of large runners would hold half a node. See
[Keeping runners ready](../../guides/designing-runner-sizes#keeping-runners-ready).

## Under load

Even with room enough in the cluster, a large runner may find none on any one
node. Each VM goes on the node with the most memory free, so the small and
medium ones spread over all three, leaving each node a little room, rather
than one node a lot. Say a busy afternoon has two large builds, sixteen small
jobs and eight medium ones running, 160 GiB between them, and every node with
about 6 GiB free.

1. A third large build is queued. Its runner needs 16 GiB on one node, and no
   node has it: the cluster has 20 GiB free, but in three pieces. pve refuses
   it as full, saying what each node has free. No runner is created, and the
   job waits on GitHub.
2. Because the large size has the higher priority and found pve full, it
   holds the two lower sizes back from it: `rungar status` shows pve `HELD`,
   and the small and medium scale sets `HOLDING BACK`. pve is their only
   provider, so their new jobs wait too.
3. A medium job finishes on pve2, Rungar removes its runner, and the provider
   deletes its VM: pve2 has about 14 GiB free, not yet enough. Another medium
   job is queued meanwhile, and waits, held back; let in, it would have taken
   8 of those 14 GiB.
4. A small job finishes on pve2 too, and pve2 has about 18 GiB free. The
   large scale set -- asking every `reconcile_interval`, renewing its hold,
   and trying pve again once its wait is up -- has its runner cloned there
   before anything smaller can take the room.
5. Once its runner is created, the hold ends, and the medium job gets a
   runner from the next room to free.

The large build waited for two jobs to finish, and no longer: without the
priority, each few GiB freed would go to whichever small or medium job asked
first, and the large one could wait as long as the others kept coming. The
cost is the medium job that waited behind it, and room that may sit free
while the large scale set's wait runs out: 15 seconds after the first
refusal, doubling up to 2 minutes. A hold lasts two `reconcile_interval`s, a
minute by default, and is renewed while the large scale set keeps trying, so
it ends on its own once the large jobs stop.

Priority is not a reservation: nothing is kept empty for the large size
before it asks, and a running runner is never taken away. See
[Priority](../../concepts/placement#priority).

## Watching it

`rungar_runners_desired` against `rungar_runners_current` says whether each
size keeps up, and `rungar_job_runner_wait_seconds` how long its jobs wait;
the large size waiting longest is the trade made above. A size whose jobs
wait all day needs more room, or a smaller ceiling for the sizes it competes
with. See [Monitoring](../../guides/monitoring).

## Growing it

- **A second cluster** is a second provider. The scale sets list both;
  `placement: pack` fills one before the other, and the default, `spread`,
  evens runners out between them. See
  [Placement](../../concepts/placement#trying-providers-in-order).
- **A cloud for the busy hours**, tried once the cluster is full: see [Own
  hardware first, the cloud for overflow](../cloud-for-overflow).
