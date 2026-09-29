---
title: Quickstart
weight: 2
description: "From one Dicer host to a workflow job running on a virtual machine of its own."
icon: lightning-bolt
related_title: Next steps
related:
  - /docs/concepts/how-rungar-works
  - /docs/guides/designing-runner-sizes
  - /docs/guides/github-credentials
  - /docs/guides/running-the-daemon
---

From one [Dicer](https://github.com/konradasb/dicer) host to a workflow job
running on a virtual machine of its own. Rungar runs on the Dicer host
itself here, and reaches Dicer through its socket, so there is no network
between the two to secure; the [Dicer provider](../../reference/providers/dicer)
says how to reach hosts elsewhere.

You need:

- a Linux host with Dicer
  [installed](https://dicer.sh/docs/getting-started/installation/), a
  network and a kernel, as Dicer's
  [Quickstart](https://dicer.sh/docs/getting-started/quickstart/) sets up;
- a GitHub repository you administer. The runners serve only it: an
  organisation takes one more permission, in
  [GitHub credentials](../../guides/github-credentials).

{{% steps %}}

### Create a GitHub App

Under your account or organisation, **Settings → Developer settings → GitHub
Apps → New GitHub App**:

- a name, and a homepage URL -- the repository's will do;
- **Webhook → Active** unticked: Rungar asks GitHub for what it needs;
- **Repository permissions**: **Administration** read and write. GitHub
  adds **Metadata** read by itself.

Create it, and note its **Client ID**. Under **Private keys**, generate one:
GitHub hands over a `.pem` file, once. Then **Install App**, on the
repository alone, and note the **installation ID**, the number that ends the
installation's URL: `.../settings/installations/12345678`.

### Install Rungar

On the Dicer host:

```console
$ curl -fsSL https://raw.githubusercontent.com/konradasb/rungar/main/scripts/install.sh | sudo bash
```

This installs `rungar` and its service, stopped, and the `rungar` user. See
[Installation](../installation) for the packages, and what else it does.

### Let it use Dicer

Dicer's socket is usable by the `dicer` group; the service runs as `rungar`.
Give it the group with a drop-in, which an upgrade leaves alone:

```console
$ sudo systemctl edit rungar
```

and add, between the comments:

```ini
[Service]
SupplementaryGroups=dicer
```

{{< callout type="warning" >}}
  The `dicer` group is as much as root on the host, and so is `rungar` with
  it. Keep its members, and the configuration's credentials, to whom you
  would give root.
{{< /callout >}}

### Configure it

Put the App's key where the service, and only the service, can read it:

```console
$ sudo install -o root -g rungar -m 0640 my-app.private-key.pem /etc/rungar/app.pem
```

Then write `/etc/rungar/config.yaml`, with the repository's URL, and the
App's client and installation IDs:

```yaml
github:
  url: https://github.com/my-org/my-repo
  app_client_id: Iv23liAbCdEf123456
  app_installation_id: 12345678
  app_private_key_path: /etc/rungar/app.pem

# One provider: the Dicer daemon on this host.
providers:
  - name: local
    type: dicer
    address: unix:///run/dicer/dicer.sock
    runner:
      image: ghcr.io/actions/actions-runner:latest

# One scale set: the label workflows target, and at most two runners of
# 2 vCPUs and 4 GiB on the provider above.
scale_sets:
  - name: rungar-c2-m4
    max_runners: 2
    providers:
      - name: local
        runner: {vcpus: 2, memory: 4GiB}
```

```console
$ sudo chown root:rungar /etc/rungar/config.yaml
$ sudo chmod 0640 /etc/rungar/config.yaml
```

### Start it

Check the configuration, start the daemon, and see what it is doing:

```console
$ sudo -u rungar rungar validate
/etc/rungar/config.yaml is valid: 1 provider, 1 scale set
$ sudo systemctl enable --now rungar
$ sudo -u rungar rungar status
```

`rungar status` should show the credentials working, `rungar-c2-m4`
`LISTENING`, and `local` answering. On GitHub, the repository's **Settings →
Actions → Runners** now lists the scale set. It has no runners yet: Rungar
makes one when a job asks for it. If something is not right, the daemon's
log says why:

```console
$ journalctl -u rungar -n 50
```

### Run a job

Add a workflow to the repository, `.github/workflows/rungar.yaml`:

```yaml
name: rungar
on: workflow_dispatch

jobs:
  hello:
    runs-on: rungar-c2-m4
    steps:
      - run: |
          uname -a
          nproc
          free -h
```

and run it from the repository's **Actions** tab. In the meantime, watch
Rungar answer:

```console
$ sudo -u rungar rungar events -f
2026-09-28 10:15:02   Runner   rungar-c2-m4-3f9a01c2   Created   Runner created on local: 2 vCPU, 4 GiB
2026-09-28 10:15:41   Runner   rungar-c2-m4-3f9a01c2   Removed   Runner removed from local: its job completed
```

The job was queued; GitHub told Rungar; Rungar booted a virtual machine on
Dicer, registered only for that job; the job ran on it, printing two CPUs;
and the machine was destroyed. A second run gets a machine of its own.
`dicer ps` shows it while it is there.

{{% /steps %}}

## Where next

- Keep a runner warm, so a job starts without waiting for a boot:
  `min_runners` on the scale set. See [Scale sets](../../concepts/scale-sets).
- More sizes, and more hosts: see
  [Designing runner sizes](../../guides/designing-runner-sizes) and
  [Placement](../../concepts/placement).
- Pin the runner image to a digest, and give Docker to jobs that need it:
  see the [Dicer provider](../../reference/providers/dicer#sizing-runners).
