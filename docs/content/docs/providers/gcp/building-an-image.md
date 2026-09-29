---
title: Building an image
weight: 1
description: "The image a gcp provider's runners boot: building it with Packer, and keeping it current."
icon: cloud
aliases:
  - /docs/guides/gcp-runner-image/
related:
  - /docs/providers/gcp
  - /docs/guides/designing-runner-sizes
---

A `gcp` provider's runners boot from an image of yours. With the default
startup script, the image needs the Actions runner unpacked in `/home/runner`,
owned by a `runner` user, and Google's guest environment, which every public
Google image has. The repository has a [Packer](https://developer.hashicorp.com/packer)
template that builds one from Debian 12, in `build/runner-image/gcp`.

## What the image has

- Debian 12, upgraded when built, with automatic upgrades turned off: they
  would hold apt's lock while a job wants it. A new image is the upgrade.
- The Actions runner in `/home/runner`, checked against the digest GitHub
  publishes for it, and the libraries it needs.
- A `runner` user, with `sudo` and no password, as GitHub's hosted runners
  have: a job owns its instance anyway (see the provider's
  [security notes]({{< relref "/docs/providers/gcp#notes" >}})).
- `git`, which `actions/checkout` needs for a real clone rather than a
  download of the files, `curl`, `jq`, `zip` and `unzip`.

Nothing else: no Docker, no language toolchains. Jobs install what they use,
or add it to the image (see [Adding tools](#adding-tools)).

## Building it

You need Packer, and credentials it can use: `gcloud auth
application-default login`, or a service account with
`roles/compute.instanceAdmin.v1` on the project. The build runs a preemptible
instance for about five minutes, with no service account of its own, and
costs about a cent.

```console
$ packer init build/runner-image/gcp
$ packer build -var project=my-ci-123456 build/runner-image/gcp
```

The image joins the family `actions-runner`, named for its runner version and
when it was built: `actions-runner-2-337-0-x64-20261001101500`. Name the
family in the provider's `runner` block, and each new runner boots the
family's newest image:

```yaml
providers:
  - name: gcp
    type: gcp
    project: my-ci-123456
    zones: [europe-west4-a, europe-west4-b]
    runner:
      image: projects/my-ci-123456/global/images/family/actions-runner
      machine_type: e2-standard-4
```

| Variable | |
|---|---|
| `project` | The project the image is built and kept in. Required. |
| `zone` | Where the build's instance runs; `europe-west4-a` by default. |
| `runner_version` | The Actions runner release, such as `2.337.0`. |
| `arch` | `x64`, or `arm64` for Arm machine types such as `t2a` and `c4a`, whose images go to the family `actions-runner-arm64`. |
| `image_family` | `actions-runner` by default. |
| `sudo` | `false` leaves the `runner` user without `sudo`. |

## Keeping it current

GitHub releases a new runner every few weeks. A runner older than the
newest updates itself when it starts, which adds a minute or so to every
boot, and GitHub stops sending jobs to versions it has left behind. Build a
new image when a runner is released, monthly at the least:

```console
$ packer build -var project=my-ci-123456 -var runner_version=2.338.0 build/runner-image/gcp
```

Runners created from then on boot the new image; those already running keep
theirs until their job ends. Nothing restarts. To go back, deprecate the new
image, and the family's newest is the one before:

```console
$ gcloud compute images deprecate actions-runner-2-338-0-x64-20261101090000 --state DEPRECATED
```

Old images cost their storage, about $0.05 a month each: delete those you
will not go back to.

## Adding tools

Add to `build/runner-image/install-runner.sh`, or a provisioner of your own
after it, what every job wants and takes long to install: Docker, a language
toolchain, a warm cache. Each is time saved on every job, and paid for once
in the build. What only some jobs want is better installed by them, or in an
image family of its own for a scale set of its own.

## Without Packer

The image is only what [What the image has](#what-the-image-has) lists. Any
way of creating one works: run `install-runner.sh` as root on an instance of
`debian-12` with `RUNNER_VERSION`, `RUNNER_ARCH` and `RUNNER_SUDO` set, stop
it, and create an image of its disk with `gcloud compute images create
--source-disk`. An image of another distribution works as long as it has
Google's guest environment, which runs the startup script.
