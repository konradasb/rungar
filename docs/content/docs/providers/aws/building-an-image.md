---
title: Building an image
weight: 1
description: "The AMI an aws provider's runners boot: building it with Packer, and keeping it current."
icon: cloud
related:
  - /docs/providers/aws
  - /docs/guides/designing-runner-sizes
---

An `aws` provider's runners boot from an AMI of yours. With the default user
data, the AMI needs the Actions runner unpacked in `/home/runner`, owned by a
`runner` user, and cloud-init, which every public Debian, Ubuntu and Amazon
Linux AMI has. The repository has a [Packer](https://developer.hashicorp.com/packer)
template that builds one from Debian 12, in `build/runner-image/aws`. It
installs the runner with the same script as the
[gcp image]({{< relref "/docs/providers/gcp/building-an-image" >}}),
`build/runner-image/install-runner.sh`, so the two have the same contents.

## What the image has

- Debian 12, upgraded when built, with automatic upgrades turned off: they
  would hold apt's lock while a job wants it. A new image is the upgrade.
- The Actions runner in `/home/runner`, checked against the digest GitHub
  publishes for it, and the libraries it needs.
- A `runner` user, with `sudo` and no password, as GitHub's hosted runners
  have: a job owns its instance anyway (see the provider's
  [security notes]({{< relref "/docs/providers/aws#notes" >}})).
- `git`, which `actions/checkout` needs for a real clone rather than a
  download of the files, `curl`, `jq`, `zip` and `unzip`.

Nothing else: no Docker, no language toolchains. Jobs install what they use,
or add it to the image (see [Adding tools](#adding-tools)).

## Building it

You need Packer, and credentials it can use: `aws sso login`, or any other
credential the AWS SDK finds, allowed to run instances and create AMIs in the
region. The build runs a `t3.small`, or a `t4g.small` with `-var arch=arm64`,
with a public IP address in the region's default VPC for about five minutes,
and costs about a cent.

```console
$ packer init build/runner-image/aws
$ packer build -var region=eu-north-1 build/runner-image/aws
```

The AMI is named for its runner version and when it was built:
`actions-runner-2.337.0-x64-20261002101500`. Name its ID in the provider's
`runner` block:

```yaml
providers:
  - name: aws
    type: aws
    region: eu-north-1
    subnets: [subnet-0123456789abcdef0]
    runner:
      image: ami-0a1b2c3d4e5f60718
      instance_type: m7i.xlarge
```

To find the newest:

```console
$ aws ec2 describe-images --owners self --filters 'Name=name,Values=actions-runner-*-x64-*' \
    --query 'sort_by(Images, &CreationDate)[-1].ImageId' --output text
```

| Variable | |
|---|---|
| `region` | The region the AMI is built and kept in. Required. |
| `runner_version` | The Actions runner release, such as `2.337.0`. |
| `arch` | `x64`, or `arm64` for Graviton instance types such as `t4g` and `m7g`. |
| `sudo` | `false` leaves the `runner` user without `sudo`. |

## Keeping it current

GitHub releases a new runner every few weeks. A runner older than the
newest updates itself when it starts, which adds a minute or so to every
boot, and GitHub stops sending jobs to versions it has left behind. Build a
new AMI when a runner is released, monthly at the least, and put its ID in
the `runner` block:

```console
$ packer build -var region=eu-north-1 -var runner_version=2.338.0 build/runner-image/aws
```

Runners created from then on boot the new AMI; those already running keep
theirs until their job ends. To go back, put the old ID back.

Old AMIs cost their snapshot's storage, about $0.05 a month each: deregister
those you will not go back to, and delete their snapshots.

## Adding tools

Add to `build/runner-image/install-runner.sh`, which both templates run, or
a provisioner of your own after it in `build/runner-image/aws/runner.pkr.hcl`,
what every job wants and takes long to install: Docker, a language
toolchain, a warm cache. Each is time saved on every job, and paid for once
in the build. What only some jobs want is better installed by them, or in an
AMI of its own for a scale set of its own.

## Without Packer

The AMI is only what [What the image has](#what-the-image-has) lists. Any way
of creating one works: run `install-runner.sh` as root on an instance of
Debian 12 with `RUNNER_VERSION`, `RUNNER_ARCH` and `RUNNER_SUDO` set, stop
it, and create an AMI of it with `aws ec2 create-image`.
