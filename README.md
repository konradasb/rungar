<h1>
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/static/images/logo-dark.svg">
    <img src="docs/static/images/logo.svg" alt="" width="40" height="40" align="top">
  </picture>
  Rungar
</h1>

[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/konradasb/rungar/badge)](https://scorecard.dev/viewer/?uri=github.com/konradasb/rungar)
[![test](https://github.com/konradasb/rungar/actions/workflows/test.yaml/badge.svg)](https://github.com/konradasb/rungar/actions/workflows/test.yaml)
[![lint](https://github.com/konradasb/rungar/actions/workflows/lint.yaml/badge.svg)](https://github.com/konradasb/rungar/actions/workflows/lint.yaml)
[![security](https://github.com/konradasb/rungar/actions/workflows/security.yaml/badge.svg)](https://github.com/konradasb/rungar/actions/workflows/security.yaml)
[![License: MIT](https://img.shields.io/github/license/konradasb/rungar)](LICENSE)

Run GitHub Actions runners, a fresh machine for every job, on hosts of your own
and in the cloud.

Rungar keeps GitHub runner scale sets supplied with runners. Each one is a
machine that takes one job and is then deleted, so nothing is shared
between two jobs. Where the machines run is a provider's business: Rungar
reaches each backend through a provider, and ships with providers for
[Dicer](https://github.com/konradasb/dicer) hosts, Proxmox VE clusters,
GCP and AWS. A scale set can span several,
filling hosts of your own before a cloud.

## Requirements

- A backend Rungar has a provider for: [Dicer](https://github.com/konradasb/dicer)
  hosts, a Proxmox VE cluster with a runner template, a Google Cloud
  project, or an AWS account
- A GitHub App, or a personal access token, for the organisation or
  repository the scale set belongs to

## Install

```console
curl -fsSL https://raw.githubusercontent.com/konradasb/rungar/main/scripts/install.sh | sudo bash
```

This installs `rungar` and its systemd service. On Debian, Ubuntu, Fedora,
RHEL and openSUSE, the `rungar` package from `pkg.rungar.sh` does the same;
see [Installation](docs/content/docs/getting-started/installation.md). Or run
the container image, `ghcr.io/konradasb/rungar`, or in Kubernetes, its
[Helm chart](charts/rungar/README.md),
`oci://ghcr.io/konradasb/charts/rungar`.

## Quickstart

Write `/etc/rungar/config.yaml`:

```yaml
version: 1

github:
  url: https://github.com/my-org
  app_client_id: Iv1.abc123
  app_installation_id: 12345678
  app_private_key_path: /etc/rungar/app.pem

providers:
  - name: compute1
    type: dicer
    address: 10.10.0.101:7443
    runner:
      image: ghcr.io/actions/actions-runner:latest

scale_sets:
  - name: rungar-c2-m4
    max_runners: 10
    providers:
      - name: compute1
        runner: {vcpus: 2, memory: 4GiB}
```

Check the configuration, start the daemon, and see what it is doing:

```console
$ sudo -u rungar rungar validate
$ sudo systemctl enable --now rungar
$ sudo -u rungar rungar status
```

Workflows then target the scale set by name: `runs-on: rungar-c2-m4`.

## Configuration

`rungar` reads `/etc/rungar/config.yaml`, and unknown keys are rejected.
The [configuration reference](https://rungar.sh/docs/reference/configuration/) documents every key, and `rungar validate` checks a
file without starting anything.

## License

MIT. See [LICENSE](LICENSE).
