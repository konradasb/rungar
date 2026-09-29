---
title: Proxmox VE
weight: 2
description: "Runners as virtual machines on a Proxmox VE cluster."
icon: server
sidebar:
  open: false
aliases:
  - /docs/reference/providers/proxmox/
---

Runs each runner in a fresh VM cloned from a template, on the cluster node
with the most memory free.

## Requirements

- A template VM; see [Preparing a template]({{< relref "preparing-a-template" >}}).
- An API token with `VM.Allocate`, `VM.Clone`, `VM.Config.*`,
  `VM.PowerMgmt`, `VM.Audit`, `VM.GuestAgent.*`, `Datastore.AllocateSpace`
  and `Sys.Audit` on the template, the nodes, and the storage and pool runners
  go in.

## Configuration

```yaml
providers:
  - name: pve
    type: proxmox
    url: https://pve.example.com:8006
    token_id: rungar@pve!rungar
    token_secret_path: /etc/rungar/pve-token
    runner:
      template: 9000
      cores: 4
      memory: 8GiB
```

Every key is under [Configuration]({{< relref "configuration" >}}).

## Notes

- Before Proxmox VE 9, the token needs `VM.Monitor` in place of
  `VM.GuestAgent.*`.
- The template must be on storage every node in `nodes` can reach.
- Linked clones need storage that supports them; otherwise set `full_clone`.
