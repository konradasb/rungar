---
title: Providers
weight: 4
description: "Where runners run: Dicer hosts, Proxmox VE, GCP and AWS."
icon: server
aliases:
  - /docs/reference/providers/
---

A provider is one backend that creates runners' machines. Every provider has
these keys, whatever its type:

| Key | Description |
|---|---|
| `name` | What scale sets, logs and metrics call it. |
| `type` | `dicer`, `proxmox`, `gcp` or `aws`. |
| `weight` | How attractive it is to placement; 1 if unset. See [Placement]({{< relref "/docs/concepts/placement#weights" >}}). |
| `max_runners` | The most runners it may have, across scale sets. Unset leaves it to the backend. |
| `disabled` | Takes it out of placement; its runners finish their jobs. |
| `runner` | The runner block every scale set on it starts from. |

The rest are its type's, listed on each type's Configuration page.

## Runner blocks

A scale set's `runner` block for a provider is written over the provider's:

- a key given later replaces the one before;
- a mapping, such as `env`, is merged key by key;
- a list, such as `mounts`, is replaced whole;
- a key set to `null` is cleared.

## Full providers

A provider that refuses a runner is skipped by the scale set for a while --
15 seconds, doubling with each refusal in a row up to 2 minutes -- and the
runner is tried on the scale set's next provider. A provider that refused for
want of room is full, and also holds back scale sets of lower priority, so
that the next room to open up there goes to the one waiting. See
[Placement]({{< relref "/docs/concepts/placement" >}}).

## Types

{{< section-cards >}}
