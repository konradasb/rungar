---
title: GCP
weight: 3
description: "Runners as Compute Engine instances."
icon: cloud
sidebar:
  open: false
aliases:
  - /docs/reference/providers/gcp/
---

Runs each runner on a fresh Compute Engine instance, spread across the zones you list.

## Requirements

- An image with the Actions runner; see [Building an image]({{< relref "building-an-image" >}}).
- An identity for Rungar with `roles/compute.instanceAdmin.v1` on the
  project, and `roles/iam.serviceAccountUser` on the account the instances
  run as, if any. On GKE, it can be the Helm chart's service account, by
  Workload Identity: see
  [On GKE](../../getting-started/kubernetes#on-gke).

## Configuration

```yaml
providers:
  - name: gcp
    type: gcp
    project: my-ci-123456
    zones: [europe-west1-b, europe-west1-c]
    runner:
      image: projects/my-ci-123456/global/images/family/actions-runner
      machine_type: e2-standard-4
```

Every key is under [Configuration]({{< relref "configuration" >}}).

## Notes

- A job can use its instance's service account. Leave `service_account`
  unset, or give runners an account of their own.
- Anyone who can view the project's instances can read a registration before
  it is used. Keep runners in a project of their own.
- The `default` network allows SSH from anywhere. Use a network of the
  runners' own.
- Scale set names must start with a letter, and be lower case letters, digits
  and hyphens. A runner's name, its instance's, is at most 63 characters: a
  scale set name longer than 54 is cut short in it.
