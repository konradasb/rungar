---
title: Providers
weight: 3
description: "What runners run on: a provider is one backend that creates machines, and places them itself."
icon: server
related:
  - /docs/providers
  - /docs/concepts/placement
  - /docs/concepts/scale-sets
---

Rungar does not run machines itself. It asks a **provider** for them: one
backend that can create, list and delete machines -- a Dicer host, a Proxmox
VE cluster, a cloud project or account. Everything about the
backend -- how it is reached, what a runner's machine is made of, how the
machine is told which runner to become -- is the provider's, and the rest of
Rungar works the same whichever one it is.

Where on the backend a machine goes is the backend's business too. A cloud
schedules its servers, a cluster its nodes; a Dicer host is one machine, and
so is its own answer. Rungar chooses only which provider a runner goes to --
the same way for every type -- and never which machine inside it. See
[Placement](../placement).

Which backends Rungar can use are its provider **types**, each with a page in
[Providers](../../providers). Everything on this page holds for
every type.

## Providers

`providers` lists them. Each has a **name**, which scale sets, logs and
metrics call it by, and a **type**. It may have a **`weight`**, a
**`max_runners`**, a **`disabled`** and a [**`runner`**
block](#the-runner-block). Those are Rungar's, and mean the same on every
type. Everything else in the entry is its type's to read -- here, where a
Dicer daemon is and how it is reached -- and is checked when the
configuration loads, so a misspelt key is refused before anything starts:

```yaml
providers:
  - &dicer
    name: compute1
    type: dicer
    address: 10.10.0.101:7443
    tls:
      ca_file: /etc/rungar/ca.pem
      cert_file: /etc/rungar/client.pem
      key_file: /etc/rungar/client-key.pem
    runner:
      image: ghcr.io/actions/actions-runner:latest
      kernel: linux-6.18
  - <<: *dicer
    name: compute2
    address: 10.10.0.102:7443
    weight: 2
```

A fleet of machines that each make their own decisions, like Dicer hosts, is
a provider per machine. What they share is written once, with a YAML anchor
(`&dicer`), and brought into the others with a merge key (`<<: *dicer`); each
then says only what differs. Rungar reads a provider's entry with its anchors
resolved, so the type sees it as if it had been written out.

A provider is listed once. Two naming the same backend -- the same address,
written differently -- are refused, since both would place runners on it and
count them apart.

Three keys every provider has shape how placement uses it:
**`weight`** and **`max_runners`**, see [Placement](../placement#limits);
and **`disabled`**, which takes it out of placement while leaving its
runners to finish their jobs, see
[Draining a provider](../../guides/managing-the-fleet#draining-a-provider).

## Room

What a provider has room for is its backend's to say, and it says it the one
way every backend can: by creating a runner's machine, or refusing to because
it is full. Rungar tries a provider and, when it is full, the next; see
[Placement](../placement#trying-providers-in-order). It never assumes a
provider empty because Rungar put nothing there, and never keeps a count of
its own of what a backend holds. Whether vCPUs may be overcommitted is the
backend's setting, not Rungar's.

A provider that cannot be reached is shown as such, and left out of
placement; its runners are written off only after five minutes. See
[State and adoption](../state-and-adoption#providers-that-cannot-be-reached).

## The runner block

What a runner's machine is made of is written in the provider type's terms -- an
image and a size, a machine type and a network -- in a `runner` block. It is
written in two places, the second over the first:

1. the provider's own, which every scale set on it starts from: what the
   scale sets share, such as the image;
2. the scale set's for that provider, which says what makes its runners
   different there -- usually their size.

```yaml
scale_sets:
  - name: rungar-c2-m4
    max_runners: 8
    providers:
      - name: compute1
        runner: &c2-m4 {vcpus: 2, memory: 4GiB}
      - name: compute2
        runner: *c2-m4
  - name: rungar-c8-m16
    max_runners: 2
    providers:
      - name: compute1
        runner: &c8-m16 {vcpus: 8, memory: 16GiB}
      - name: compute2
        runner: {<<: *c8-m16, disk: 100GiB}
```

Each block is its provider's alone, so a scale set says its size once for
each provider; providers alike share it with a YAML anchor, as above. The
keys are the type's, but how the two blocks combine is Rungar's, the same on
every type:

- a key given later replaces the one before;
- a mapping, such as an environment, is merged key by key;
- a list, such as mounts, is replaced whole;
- a key set to `null` is cleared, as if it had never been given.

What results is handed to each provider's type, which checks it as the runner
it will actually be when the configuration loads -- so a scale set that
inherits no image is refused before anything starts, and names the provider
it would have been refused on.

A scale set can span providers of different types, each block in its own
provider's terms:

```yaml
scale_sets:
  - name: rungar-c4-m8
    max_runners: 10
    providers:
      - name: compute1
        runner: {vcpus: 4, memory: 8GiB}
      - name: gcp
        runner: {machine_type: e2-custom-4-8192}
```

## Moving things around

Because runners are found by their labels rather than remembered, changing
the configuration is safe, but a few changes leave something behind:

- **Removing a provider**, or taking it out of a scale set's list, leaves its
  runners where they are, no longer looked at or counted. Busy ones finish
  their job and go; idle ones take the next job that comes, and go. Mark it
  `disabled` first, and remove it once it is empty.
- **Renaming a provider** changes only what logs, metrics and
  `rungar status` call it: its runners are found by their labels, on whatever
  backend it names.
