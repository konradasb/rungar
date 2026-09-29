---
title: Providers
weight: 2
description: "What every provider type has in common, and each type's own keys."
icon: server
---

A provider is one backend that makes machines, and places them itself. Its
entry in `providers` has two kinds of keys. Six are Rungar's, and mean the
same on every type:

| Key | Description |
|---|---|
| `name` | What scale sets, logs and metrics call the provider. |
| `type` | Which provider type reads the rest. |
| `weight` | How attractive the provider is to placement. Zero is 1; a negative weight is refused. See [Placement](../../concepts/placement#weights). |
| `max_runners` | The most runners it may have, of every scale set: a ceiling of your own where the backend would take more. Unset leaves it to the backend. |
| `disabled` | Takes the provider out of placement, leaving its runners to finish their jobs. |
| `runner` | The runner block every scale set on it starts from. |

Everything else is the type's: what the backend is, and how it is reached.
It is checked when the configuration loads, and a key the type does not know
is refused. Anchors and merge keys are resolved first, so settings several
providers share can be written once:

```yaml
providers:
  - &dicer
    name: compute1
    type: dicer
    address: 10.10.0.101:7443
  - <<: *dicer
    name: compute2
    address: 10.10.0.102:7443
```

## Capacity

Placement never asks a provider whether it has room: it asks it for a
runner's machine, and the provider makes it or refuses. Whatever the
refusal, the runner is tried on the scale set's next provider, and this one
is skipped by that scale set for a while. A type says when a refusal is
the backend being **full** -- out of CPU, memory or disk, a quota, a zone
out of stock -- which lets a scale set of higher priority keep lower ones
off it until it finds room.

Each type's page says what its refusals are. See
[Placement](../../concepts/placement).

## Runner blocks

The keys of a `runner` block are the type's. How the blocks combine is
Rungar's, the same for every type: a scale set's block for a provider is
written over the provider's.

- A key given later replaces the one before.
- A mapping, such as an environment, is merged key by key, at every depth.
- A list, such as mounts, is replaced whole.
- A key set to `null` is cleared, as if it had never been given.

What results is checked by the type, as the runner it will actually be, when
the configuration loads.

## Types

{{< section-cards >}}
