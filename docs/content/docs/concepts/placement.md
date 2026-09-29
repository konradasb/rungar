---
title: Placement
weight: 5
description: "Which provider a runner goes to, what happens when one is full, and who gets the fleet when there is not enough to go round."
icon: map
related:
  - /docs/concepts/providers
  - /docs/concepts/scale-sets
  - /docs/guides/designing-runner-sizes
  - /docs/guides/troubleshooting
---

Every runner goes to one of its scale set's providers. Rungar puts them in
order, and tries them one after another until one creates the runner's
machine. Where inside the provider the machine then goes is the provider's
own decision: a cloud chooses a zone, and a Dicer host is the one machine it
can go on.

Rungar does not ask a provider whether it has room. It tries it, and a
provider that is full says so by refusing. That works the same for every
kind of backend -- a Dicer host that knows exactly what it has spare, and a
cloud that only finds out it is out of stock when asked for a machine -- and
it is what the backend itself decides, rather than Rungar's guess at it.

## Trying providers in order

A runner is registered with GitHub once, and then its providers are tried in
the order the scale set's `placement` puts them:

- **`spread`**, the default, tries first the provider with the fewest of the
  scale set's runners. Runners even out, and no provider fills while others
  sit idle -- right for a fleet of hosts of one size.
- **`pack`** tries first the provider with the highest `weight`, and
  providers of equal weight in the order the scale set lists them. Providers
  fill one after another: whole hosts stay free for large runners, which
  spread would scatter small ones across, and on-premises hosts are used
  before a cloud.

```yaml
scale_sets:
  - name: rungar-c4-m8
    max_runners: 10
    placement: pack
    providers:
      - { name: compute1, runner: &c4-m8 { vcpus: 4, memory: 8GiB } }
      - { name: compute2, runner: *c4-m8 }
      - { name: gcp, runner: { machine_type: e2-custom-4-8192 } }
```

Here a runner goes to compute1 while it takes them; when it refuses one for
being full, the same runner is tried on compute2 at once, and then on gcp.
The job waits no longer than the refusals take, which for a Dicer host is a
moment.

Whatever the refusal, Rungar tries the next provider, and the scale set skips
this one for a while: 15 seconds, doubling each time in a row, up to 2
minutes. Other scale sets still try it -- a provider too small for one
scale set's runner may have room for another's -- and one runner created there
forgives it. A provider that does not answer at all is skipped by every
scale set until it does.

A provider that says it is **full** -- out of CPU, memory or disk, a quota,
a zone out of stock -- also lets a scale set of higher
[priority](#priority) keep lower ones off it until it finds room. Any other
refusal, such as a runner the provider can never create, is skipped the same
way and holds no one off.

The provider stays in the fleet throughout, and is tried again once its time
is up; nothing has to notice that it recovered. A runner it can never create
is tried again every two minutes, and is created once its block is fixed.
`rungar status` shows which providers are skipped, for which scale sets, why,
and for how much longer.

When no provider takes the runner, it is not created: its registration is
removed, the scale set waits, its jobs stay queued on GitHub, and Rungar tries
again on the next message from GitHub -- by which time a job may have
finished and made room -- within `reconcile_interval` at most. When every
provider is being skipped, nothing is registered with GitHub at all.

## Limits

What a provider holds is its backend's to say, by refusing. What you would
rather it held is a limit:

- A scale set's **`max_runners`** is the most runners it has, on all its
  providers together.
- A provider's **`max_runners`** is the most runners it has, of every scale
  set placed on it: a ceiling of your own where the backend would take more,
  such as a cloud's cost, or a host you want to keep some of for something
  else. A provider at its limit is not tried.

```yaml
providers:
  - name: gcp
    type: gcp
    max_runners: 20
```

Both are counted by Rungar from what it has created, with the runners still
being created, so two scale sets placing at once never both take a provider's
last place. A provider's limit also counts the machines of runners that have
gone but are not yet deleted: a machine the provider fails to delete keeps its
place until a later attempt deletes it, so failing deletes never take a
provider past its limit.

## Weights

A provider's **`weight`** tells a fleet of unequal providers which to use
more. With `spread`, one of weight 2 is counted as having half the runners it
has, so it gets two runners for every one of an unweighted provider: give a
host twice the size of the others `weight: 2`. With `pack`, providers of the
highest weight are tried first, in their order.

A weight only orders providers. It never makes a provider take a runner: a
full one refuses whatever its weight.

## Priority

Scale sets sharing a provider compete for it, and by default they take their
turn. That is not fair to a large runner. Small runners fill the gaps a
large one needs, so without anything done about it, a large runner waits for
enough room to open up at a moment when no small runner is asking -- which on
a busy fleet may not happen.

A scale set's **`priority`** is what is done about it. Higher wins; the
default is 0. When a scale set finds a provider full, anything of lower
priority holds back from that provider for a while, so that the next room to
open up there is its own. The scale set asks again every
`reconcile_interval`, renewing its hold each time, and tries the provider
again once its backoff is up. The hold lasts two `reconcile_interval`s -- a
minute by default -- so it holds while the scale set keeps asking, and ends
when it places its runner or stops asking. Scale sets of the same priority never hold each other back, and
a scale set held back from one provider still uses its others. While a hold
lasts, `rungar status` shows the provider as `HELD`, and the scale sets it
keeps back as `HOLDING BACK`.

```yaml
scale_sets:
  - name: rungar-c2-m4
    max_runners: 12
    providers:
      - { name: compute1, runner: &c2-m4 { vcpus: 2, memory: 4GiB } }
      - { name: compute2, runner: *c2-m4 }
  - name: rungar-c8-m16
    max_runners: 2
    priority: 10
    providers:
      - { name: compute1, runner: &c8-m16 { vcpus: 8, memory: 16GiB } }
      - { name: compute2, runner: *c8-m16 }
```

Priority is not a reservation. Nothing is held empty in advance, and a runner
already running is never taken away to make room: a large runner waits for
the next room, and only gets it first. `placement: pack` helps it the other
way, by keeping the room it needs in one piece.
