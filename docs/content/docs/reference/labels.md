---
title: Labels
weight: 6
description: "The labels on every machine Rungar creates, and the installation ID."
icon: tag
---

Rungar keeps no record of its runners but the machines themselves: every
machine it creates carries labels saying whose it is, and a daemon starting
finds its runners again by them. See
[State and adoption](../../concepts/state-and-adoption).

## The labels

| Label | Value |
|---|---|
| `rungar.sh/managed` | `true`: the machine is Rungar's. |
| `rungar.sh/installation` | The installation that created it; see [below](#the-installation). |
| `rungar.sh/scale-set` | Its scale set's name. |
| `rungar.sh/runner` | Its runner's name, which is also the machine's. |
| `rungar.sh/revision` | The revision of the runner it was created from; see [below](#the-revision). |

Rungar acts on, and has deleted, only machines carrying the first three with
its own values: `managed`, its installation and one of its scale sets. Anything
else on a provider is left alone, and counts only as room the provider no
longer has.

## The installation

The installation keeps two Rungars sharing a fleet from adopting each
other's runners. Unless `installation` is set, it is derived from
`github.url`:

```text
gh-  +  the first 12 hex digits of SHA-256( github.url, lower-cased, with surrounding spaces and trailing slashes removed )
```

So `https://github.com/my-org` and `https://GitHub.com/my-org/` are the same
installation, and two Rungars serving different GitHub URLs never share one.
Two Rungars serving the *same* URL on one fleet must set `installation` to
different values.

An `installation` that is set must be 1 to 63 letters, digits, `.`, `_` and
`-`, starting and ending with a letter or digit, so that every backend can
keep it. `rungar status` shows the one in use.

## The revision

The revision is a hash of the runner a scale set creates on a provider: its
resolved `runner` block, the provider's with the scale set's over it. A
runner whose revision is not the current one was created from an older block,
and is replaced once it is not running a job, after a restart as much as
before. See [Runner lifecycle](../../concepts/runner-lifecycle).

An image named by tag has the same revision whatever the tag points at, so
pushing a new image under the same tag replaces no runner. Pin the image by
digest to have a new image reach every runner.

## How each provider keeps them

| Provider | Where the labels are |
|---|---|
| [`dicer`](../../providers/dicer) | The instance's labels, as they are. |
| [`proxmox`](../../providers/proxmox) | The VM's description, exactly, as JSON with when the VM was created. The VM is also tagged `rungar-` and a hash of each label, which listing narrows VMs down by. |
| [`gcp`](../../providers/gcp) | The `rungar-labels` metadata item, exactly, as JSON. Each is also a GCP label, which instances are found by: `rungar.sh/scale-set` is `rungar_sh_scale-set`, and a value GCP does not allow is `h-` and a hash of it. |
| [`aws`](../../providers/aws) | The instance's and its volume's tags, as they are. |

## Compatibility

The labels are how one version of Rungar finds the runners another created, so
they are kept stable across releases: a daemon upgraded in place adopts every
runner the old one left. A release that changes them says so in its notes.

Keep them off machines Rungar did not create, and do not change them on
machines it did. A machine given Rungar's labels by hand is taken for one of
its runners, and deleted as one GitHub has no registration for; a runner
whose labels are changed is no longer found, and is left running and
registered until removed by hand.
