---
title: Dicer
weight: 1
description: "Runners as virtual machines on Dicer hosts."
icon: server
sidebar:
  open: false
aliases:
  - /docs/reference/providers/dicer/
---

Runs each runner in a fresh virtual machine on a
[Dicer](https://github.com/konradasb/dicer) host, booted from a container
image.

## Requirements

- `dicerd` v0.3.0 or later on each host.
- A client certificate `dicerd` trusts, or access to its socket.

## Configuration

One provider per host. A YAML anchor keeps what they share in one place:

```yaml
providers:
  - &dicer
    name: compute1
    type: dicer
    address: 10.10.0.101:7443
    tls:
      ca_file: /etc/rungar/ca.pem
      cert_file: /etc/rungar/rungar.pem
      key_file: /etc/rungar/rungar-key.pem
    runner:
      image: ghcr.io/actions/actions-runner:latest
  - <<: *dicer
    name: compute2
    address: 10.10.0.102:7443
```

Every key is under [Configuration]({{< relref "configuration" >}}).

## Notes

- Without `tls`, a TCP address is plaintext and unauthenticated.
- `dicerd` lets four vCPUs share each CPU unless its
  `resources.cpu_overcommit` says otherwise.
- A read-write volume can be mounted by one runner at a time.
- Docker in a runner needs its data on a `tmpfs` at `/var/lib/docker`; see
  [running Docker inside an instance](https://dicer.sh/docs/examples/running-docker-inside-an-instance/).
