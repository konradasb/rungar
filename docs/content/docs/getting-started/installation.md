---
title: Installation
weight: 1
description: "What Rungar needs, and how to install it: a package, the install script, or the container image."
icon: download
related_title: Next steps
related:
  - /docs/getting-started/quickstart
  - /docs/guides/github-credentials
  - /docs/reference/configuration
---

Rungar is one daemon, `rungar`, managed by systemd, which is also the command
line that asks it what it is doing. On Debian, Ubuntu, Fedora, RHEL and its
rebuilds, and openSUSE, install the `rungar` package; elsewhere, the install
script downloads a release and sets the service up. To run it in a
container, see [Run it in a container](#run-it-in-a-container); in
Kubernetes, see [Installing on Kubernetes](../kubernetes).

## Requirements

**The machine Rungar runs on**

- Linux on x86_64 or aarch64, with systemd. The binary runs on macOS too,
  without the service.
- Outbound HTTPS to GitHub -- `github.com` and `api.github.com`, or your
  GitHub Enterprise host -- and a route to each provider's API.

Rungar is a network client: it creates no machines itself, and needs no
KVM, and little CPU or memory. It can run on a host that also runs runners,
such as a Dicer host, or on a small machine of its own.

**What it talks to**

- A GitHub App, or a personal access token, for the organisation, repository
  or enterprise the runners serve. See
  [GitHub credentials](../../guides/github-credentials).
- At least one backend Rungar has a provider for: a
  [Dicer](https://github.com/konradasb/dicer) host, a Proxmox VE cluster, a Google Cloud project, or an AWS account. See
  [Providers](../../providers).

**For the install script**

- `curl` and `tar`. With
  [`cosign`](https://docs.sigstore.dev/cosign/system_config/installation/)
  installed, the script also checks the release's signature.

## Install from packages

Releases are published to an apt and a dnf repository at `pkg.rungar.sh`,
signed with Rungar's key. Add it, then install `rungar`:

{{< tabs >}}
  {{< tab name="Debian, Ubuntu" >}}
  ```console
  $ sudo install -d -m 0755 /etc/apt/keyrings
  $ curl -fsSL https://pkg.rungar.sh/gpg.key | sudo gpg --dearmor -o /etc/apt/keyrings/rungar.gpg
  $ echo "deb [signed-by=/etc/apt/keyrings/rungar.gpg] https://pkg.rungar.sh/deb stable main" \
      | sudo tee /etc/apt/sources.list.d/rungar.list
  $ sudo apt update
  $ sudo apt install rungar
  ```
  {{< /tab >}}
  {{< tab name="Fedora, RHEL, Rocky, AlmaLinux" >}}
  ```console
  $ sudo curl -fsSL -o /etc/yum.repos.d/rungar.repo https://pkg.rungar.sh/rpm/rungar.repo
  $ sudo dnf install rungar
  ```

  dnf asks you to accept the repository's key the first time.
  {{< /tab >}}
  {{< tab name="openSUSE" >}}
  ```console
  $ sudo zypper addrepo https://pkg.rungar.sh/rpm/rungar.repo
  $ sudo zypper install rungar
  ```

  zypper asks you to trust the repository's key the first time.
  {{< /tab >}}
{{< /tabs >}}

The package installs `rungar` to `/usr/bin`, and its service, creates the
`rungar` user, and creates `/etc/rungar`, readable by the `rungar` group alone.
Bash, zsh and fish complete `rungar`'s commands. The package files
are also attached to each
[release](https://github.com/konradasb/rungar/releases).

## Install with the script

```console
$ curl -fsSL https://raw.githubusercontent.com/konradasb/rungar/main/scripts/install.sh | sudo bash
```

The script:

1. downloads the latest release of `rungar` for the machine's system and
   architecture;
2. checks it against the release's checksums, and the checksums against
   their signature when `cosign` is installed;
3. installs `rungar` to `/usr/local/bin`;
4. creates the `rungar` system user, and `/etc/rungar`, readable by root and
   the `rungar` group alone;
5. installs `rungar.service`, without enabling or starting it.

`--version` installs a given release instead of the latest, and
`--no-service` installs the binary alone, as it does on macOS:

```console
$ curl -fsSL https://raw.githubusercontent.com/konradasb/rungar/main/scripts/install.sh | sudo bash -s -- --version v0.1.0
```

## Run it in a container

Each release is also an image, `ghcr.io/konradasb/rungar`, for linux/amd64
and linux/arm64, with nothing in it but `rungar`. Mount the configuration
and the credentials it names read-only, and keep the events in a volume:

```console
$ docker run -d --name rungar --restart unless-stopped --read-only \
    -v /etc/rungar:/etc/rungar:ro \
    -v rungar-events:/var/log/rungar \
    --tmpfs /run/rungar:uid=65532,gid=65532,mode=0750 \
    ghcr.io/konradasb/rungar
$ docker exec rungar rungar status
```

In Kubernetes, install the Helm chart,
`oci://ghcr.io/konradasb/charts/rungar`: see
[Installing on Kubernetes](../kubernetes).

## Configure and start

From the package or the script, Rungar is installed but not started: it has
nothing to do until it knows which GitHub to serve and where to put runners,
and there is no useful default for either. (The container and the Helm chart
take the same configuration, but mounted or given as values; the `systemctl`
steps below are for a package or script install.) Write
`/etc/rungar/config.yaml` -- the [Quickstart](../quickstart) builds a first
one, and the
[configuration reference]({{< relref "/docs/reference/configuration" >}})
describes every setting -- then check it and start the daemon:

```console
$ sudo -u rungar rungar validate
$ sudo systemctl enable --now rungar
```

## Verify

```console
$ rungar --version
$ systemctl status rungar
$ sudo -u rungar rungar status
```

`rungar status` shows the GitHub the daemon serves and whether its
credentials work, each scale set and whether it is listening for jobs, each
provider and whether it answers, and every runner. The `rungar` commands ask
the running daemon through its socket, which only root and the `rungar` group
may use: hence `sudo -u rungar`.

{{< callout type="warning" >}}
  A member of the `rungar` group can read the GitHub credential and remove
  runners. Add only whom you would trust with both.
{{< /callout >}}

## Upgrade and remove

Upgrading Rungar disturbs no running job; removing it leaves its runners and
scale sets behind unless they are removed first. See
[Upgrading and uninstalling](../../guides/upgrading-and-uninstalling).
