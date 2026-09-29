---
title: A single host
weight: 1
description: "Rungar and Dicer on one machine, with one size of runner."
related:
  - /docs/getting-started/quickstart
  - /docs/providers/dicer
  - /docs/concepts/scale-sets
  - /docs/examples/cloud-for-overflow
---

The smallest fleet worth running: one machine with
[Dicer](https://github.com/konradasb/dicer), Rungar on the same machine, and
one size of runner for an organisation's builds. It is the
[Quickstart](../../getting-started/quickstart)'s setup, made ready to keep:
the image pinned, the host sized for builds, a runner kept warm in working
hours, and metrics on.

The host here has 16 CPUs and 64 GiB of memory.

## The configuration

```yaml {filename="/etc/rungar/config.yaml"}
version: 1

github:
  url: https://github.com/my-org
  app_client_id: Iv23liAbCdEf123456
  app_installation_id: 12345678
  app_private_key_path: /etc/rungar/app.pem

providers:
  - name: local
    type: dicer
    address: unix:///run/dicer/dicer.sock
    runner:
      image: ghcr.io/actions/actions-runner:2.337.0@sha256:4fa04ffcb6472b581c0f3f8fc50371bf728efce2d1a0f70e7056206250a30294

scale_sets:
  - name: rungar-c4-m8
    max_runners: 4
    schedule:
      timezone: Europe/Vilnius
      windows:
        - { days: [mon-fri], from: "08:00", to: "19:00", min_runners: 1 }
    providers:
      - name: local
        runner: { vcpus: 4, memory: 8GiB, disk: 40GiB }

metrics:
  enable: true
```

Workflows run on it with `runs-on: rungar-c4-m8`.

## What each part does

**`github`** is the organisation, and the GitHub App Rungar acts as. On an
organisation, the App needs the organisation's **Self-hosted runners** read
and write, where the Quickstart's needs the repository's **Administration**;
see [GitHub credentials](../../guides/github-credentials#permissions). The
key is kept in `/etc/rungar`, owned `root:rungar` and mode `0640`, as the
configuration is.

**`providers`** has the one Dicer daemon, reached through its socket, so
there is no network between the two to secure and no TLS to set up. The
socket is usable by the `dicer` group, which the service is given with a
drop-in:

```ini {filename="/etc/systemd/system/rungar.service.d/override.conf"}
[Service]
SupplementaryGroups=dicer
```

The group is as much as root on the host, and so is Rungar with it.

The provider's **`runner`** block says what every runner boots: the official
runner image, pinned to a digest, so that every runner boots the same one
until you change it. Pinned, a new image is a change to the configuration,
and Rungar replaces the idle runners with it one at a time; see [Keeping
runners fresh](../../concepts/scale-sets#keeping-runners-fresh). Look the
digest up when you pin it, and put it in place of the example's:

```console
$ docker buildx imagetools inspect ghcr.io/actions/actions-runner:2.337.0
```

**`scale_sets`** has the one size, named for what it is: 4 vCPUs and 8 GiB,
with a 40 GiB disk for the checkout and the build. `max_runners` is what
the host fits. Dicer lets four vCPUs share each CPU unless told otherwise,
which suits idle services and not builds, so the host gives each vCPU a CPU
of its own:

```yaml {filename="/etc/dicerd/config.yaml"}
resources:
  cpu_overcommit: 1
```

That is 16 vCPUs, and 63 GiB of memory after Dicer's reserve: four runners,
held back by their CPUs, with half the memory to spare. A fifth job waits on
GitHub for one of the four to finish. See [Dicer's
capacity](https://dicer.sh/docs/guides/capacity/) guide for the arithmetic,
and [Designing runner sizes](../../guides/designing-runner-sizes#how-big-a-vcpu-is)
for why.

The **`schedule`** keeps one runner booted and idle on weekdays from 08:00
to 19:00, so that the first job of a quiet spell does not wait for a boot,
and none outside those hours, when nobody is waiting. Outside the window,
`min_runners` is the scale set's own, 0. See
[Schedules](../../concepts/scale-sets#schedules).

**`metrics`** serves Prometheus on `127.0.0.1:9102`; see
[Monitoring](../../guides/monitoring) for what to watch, and the alerts
worth having. The events, which `rungar events` shows, are kept in
`/var/log/rungar/events.jsonl` without being asked for.

Check it, and start the daemon:

```console
$ sudo -u rungar rungar validate
/etc/rungar/config.yaml is valid: 1 provider, 1 scale set
$ sudo systemctl enable --now rungar
$ sudo -u rungar rungar status
```

## Growing it into a fleet

A second host is a second provider. Rungar reaches it over the network, so
its Dicer daemon serves its API over TCP with TLS, and Rungar has a client
certificate it trusts; see [Dicer's remote
access](https://dicer.sh/docs/guides/remote-access/). The first host can
stay on the socket:

```yaml {filename="/etc/rungar/config.yaml"}
providers:
  - &dicer
    name: local
    type: dicer
    address: unix:///run/dicer/dicer.sock
    runner:
      image: ghcr.io/actions/actions-runner:2.337.0@sha256:4fa04ffcb6472b581c0f3f8fc50371bf728efce2d1a0f70e7056206250a30294
  - <<: *dicer
    name: compute2
    address: 10.10.0.102:7443
    tls:
      ca_file: /etc/rungar/ca.pem
      cert_file: /etc/rungar/rungar.pem
      key_file: /etc/rungar/rungar-key.pem

scale_sets:
  - name: rungar-c4-m8
    max_runners: 8
    schedule:
      timezone: Europe/Vilnius
      windows:
        - { days: [mon-fri], from: "08:00", to: "19:00", min_runners: 1 }
    providers:
      - name: local
        runner: &c4-m8 { vcpus: 4, memory: 8GiB, disk: 40GiB }
      - name: compute2
        runner: *c4-m8
```

The anchor shares the runner block; the `tls` block is the second host's
alone, since a socket refuses one. `max_runners` grows with the room, and
the scale set spreads its runners across both hosts, the default
[placement](../../concepts/placement). Restart the daemon to take it up:
the runners already running are left to finish their jobs.

From there:

- More sizes, and a priority for the largest: see [Several sizes on Proxmox
  VE](../several-sizes), whose scale sets work the same on Dicer hosts.
- A cloud for the busy hours, tried after the hosts are full: see [Own
  hardware first, the cloud for overflow](../cloud-for-overflow).
- A second Rungar on another machine, standing by in case this one stops:
  see [Running a
  standby](../../guides/running-the-daemon#running-a-standby).
