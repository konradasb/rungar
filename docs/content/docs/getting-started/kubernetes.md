---
title: Installing on Kubernetes
weight: 2
description: "Running the daemon in Kubernetes with the Helm chart, on any cluster, on GKE with Workload Identity, or on EKS with an IAM role."
icon: collection
related_title: Next steps
related:
  - /docs/getting-started/quickstart
  - /docs/guides/github-credentials
  - /docs/reference/configuration
  - /docs/guides/upgrading-and-uninstalling
---

The Helm chart, `oci://ghcr.io/konradasb/charts/rungar`, runs the daemon,
`rungar`, as one pod. The runners are not in the cluster: they are machines
on the providers -- Dicer hosts, Proxmox VE, Compute Engine, EC2 -- which
the pod reaches over the network, as a daemon on a machine of its own would.

## Requirements

- Kubernetes 1.25 or later, and Helm 3.8 or later, which installs charts
  from an OCI registry.
- Outbound HTTPS from the pod to GitHub -- `github.com` and
  `api.github.com`, or your GitHub Enterprise host -- and a route to each
  provider's API.
- A GitHub App, or a personal access token; see
  [GitHub credentials](../../guides/github-credentials).

The pod needs no access to the Kubernetes API, little CPU, and 64 MiB of
memory or so.

## Install

Write the daemon's configuration, `config.yaml`, as for any install: the
[Quickstart](../quickstart) builds a first one, and the
[configuration reference]({{< relref "/docs/reference/configuration" >}})
describes every setting. Files it names -- the GitHub App's key, a Dicer
host's TLS material -- go in `/etc/rungar`, where the chart puts them:

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
    tls:
      ca_file: /etc/rungar/ca.pem
      cert_file: /etc/rungar/client.pem
      key_file: /etc/rungar/client-key.pem
scale_sets:
  - name: rungar-c2-m4
    max_runners: 12
    providers:
      - name: compute1
        runner: {vcpus: 2, memory: 4GiB}
```

Leave `socket` and `events.file` unset: the chart gives the pod room for
both at their defaults. Then install the chart, with the configuration and
each file it names:

```console
$ helm install rungar oci://ghcr.io/konradasb/charts/rungar \
    --namespace rungar --create-namespace \
    --set-file configFile=config.yaml \
    --set-file files.app\.pem=app.pem \
    --set-file files.ca\.pem=ca.pem \
    --set-file files.client\.pem=client.pem \
    --set-file files.client-key\.pem=client-key.pem
```

The configuration and the files are kept in a Secret, mounted read-only in
`/etc/rungar` and readable by the pod's group alone. `configFile` keeps the
file as it is written, comments and anchors and all, unless the chart has to
add the [metrics](#metrics) settings to it; `config` takes the same
configuration as YAML in a values file instead. To keep the credentials out
of Helm's values, make the Secret yourself, or have an external secrets
operator make it, with a key for each file, `config.yaml` among them, and
name it with `--set existingSecret=rungar-config`.

The chart's version is the Rungar release it runs: chart `0.3.0` runs image
`0.3.0`. `--version` installs a given one instead of the latest.

## Verify

```console
$ helm test rungar -n rungar
$ kubectl exec -n rungar deploy/rungar -- rungar status
$ kubectl logs -n rungar deploy/rungar -f
```

`helm test` runs `rungar validate` against the configuration as the pod has
it mounted, reading every file it names. `rungar status` shows the GitHub
the daemon serves and whether its credentials work, each scale set and
provider, and every runner, as [Installation](../installation#verify)
describes.

Every other command runs the same way: `rungar events`, `rungar runners ls`,
`rungar scale-sets pause`. They ask the daemon through its socket,
`/run/rungar/rungar.sock`, which is in the pod alone: whoever may
`kubectl exec` into it can remove runners and take providers out of
placement.

## Events

What the daemon did to runners, providers and scale sets, which
`rungar events` shows, is kept in `/var/log/rungar/events.jsonl`, apart from
the pod's log, which a log collector reads. By default the
directory is an `emptyDir`: the events outlive the container restarting, but
not the pod being replaced, which an upgrade or a changed configuration
does. To keep them, give them a PersistentVolumeClaim:

```console
$ helm upgrade rungar oci://ghcr.io/konradasb/charts/rungar -n rungar --reuse-values \
    --set events.persistence.enabled=true
```

The claim is 1 GiB, `ReadWriteOnce`, of the cluster's default storage class;
`events.persistence.storageClass`, `size` and `existingClaim` change that.
The daemon writes the volume as user and group 65532, which the pod's
`fsGroup` gives it, so use storage that honours `fsGroup`, as block storage
does. Some NFS provisioners, and `hostPath` volumes, ignore it, and leave a
volume the daemon cannot write: it then fails to start, with
`open the event log: write events: ... permission denied`.

{{< callout type="info" >}}
  A `ReadWriteOnce` volume is attached to one node at a time. If the node the
  pod runs on is lost, the replacement waits for the volume to be detached
  from it, which Kubernetes does after about six minutes, and no runners are
  created meanwhile. Without persistence, the pod starts again at once, with
  no events.
{{< /callout >}}

## Metrics

```console
$ helm upgrade rungar oci://ghcr.io/konradasb/charts/rungar -n rungar --reuse-values \
    --set metrics.enabled=true \
    --set metrics.serviceMonitor.enabled=true
```

This sets `metrics.enable` and `metrics.listen` in the configuration, puts a
Service, `rungar-metrics`, in front of port 9102, and with the Prometheus
Operator a ServiceMonitor. The endpoint has no authentication: restrict who
can reach it with `networkPolicy.metricsFrom`; see
[Network policy](#network-policy). With `existingSecret`, the chart cannot
change the configuration, so set `metrics.enable: true` and
`metrics.listen: 0.0.0.0:9102` in it yourself. See
[Monitoring](../../guides/monitoring) for what is served.

## Network policy

The chart gives the pod a NetworkPolicy that admits nothing but the metrics
port, when they are enabled: the daemon listens on nothing else, and the
command line reaches it through its socket, by `kubectl exec`, which no
policy sees. It takes a network plugin that enforces NetworkPolicy, such as
Calico or Cilium, or GKE Dataplane V2, and does nothing without one.

To admit only Prometheus to the metrics, and let the daemon connect only
where it must, set the policy's peers and egress rules in a values file:

```yaml
networkPolicy:
  metricsFrom:
    - namespaceSelector:
        matchLabels:
          kubernetes.io/metadata.name: monitoring
      podSelector:
        matchLabels:
          app.kubernetes.io/name: prometheus
  egress:
    # DNS.
    - to:
        - namespaceSelector: {}
          podSelector:
            matchLabels:
              k8s-app: kube-dns
      ports:
        - {port: 53, protocol: UDP}
        - {port: 53, protocol: TCP}
    # GitHub, and the cloud providers' APIs.
    - ports:
        - {port: 443, protocol: TCP}
    # Dicer hosts.
    - to:
        - ipBlock: {cidr: 10.10.0.0/24}
      ports:
        - {port: 7443, protocol: TCP}
```

With no `egress`, the daemon may connect anywhere. With it, it may connect
only where the rules say, so they must cover DNS, GitHub and every provider,
or the daemon cannot reach them: a Proxmox VE cluster's API on 8006, say. On
GKE, Workload Identity's credentials come from the metadata server, and on
EKS, Pod Identity's from its agent, at `169.254.170.23` on port 80: allow
those too, as your cluster's documentation says. `networkPolicy.enabled:
false` leaves the policy out.

## Health checks

The pod's probes run `rungar events --tail 1`, which asks the daemon for its
last event through its socket, and which it answers from memory. A daemon
that stops answering is taken out of the metrics Service within half a
minute, and restarted within about two minutes, disturbing no job, as any
restart does. GitHub
or a provider being unreachable fails no probe, as restarting the daemon
would not fix it: `rungar status` and the log say so instead. The startup
probe gives the daemon five minutes to open its providers and serve its
socket. `startupProbe`, `livenessProbe` and `readinessProbe` change them,
and `null` leaves one out.

## Changing the configuration

The daemon reads its configuration when it starts. `helm upgrade` with a
changed configuration or file replaces the pod, which disturbs no running
job: the new daemon adopts every runner the old one left. Check a file
before putting it in place:

```console
$ rungar validate -f config.yaml
$ helm upgrade rungar oci://ghcr.io/konradasb/charts/rungar -n rungar --reuse-values \
    --set-file configFile=config.yaml
```

`rungar validate` here is a local `rungar`, and the files the configuration
names must be where it says, so on a machine without them, run `helm test`
after the upgrade instead. A Secret of your own, given with
`existingSecret`, is not watched: restart the pod once it has changed.

```console
$ kubectl rollout restart -n rungar deploy/rungar
```

There is one replica, always, and the old pod stops before the new one
starts, so two daemons never serve the same scale sets at once.

## On GKE

A `gcp` provider authenticates with Application Default Credentials when it
has no `credentials_file`, and on GKE those come from Workload Identity, with
no key to keep. With Workload Identity enabled on the cluster and its node
pool, let the chart's Kubernetes service account act as a Google service
account that has the provider's roles -- see
[GCP](../../providers/gcp#requirements):

```console
$ gcloud iam service-accounts add-iam-policy-binding \
    rungar@my-ci-123456.iam.gserviceaccount.com \
    --role roles/iam.workloadIdentityUser \
    --member "serviceAccount:my-ci-123456.svc.id.goog[rungar/rungar]"
```

and name it on the Kubernetes one, in a values file:

```yaml
serviceAccount:
  annotations:
    iam.gke.io/gcp-service-account: rungar@my-ci-123456.iam.gserviceaccount.com
```

`rungar/rungar` is the namespace and the service account's name, the
release's full name unless `serviceAccount.name` says otherwise. The pod
mounts no service account token, and needs none: GKE's metadata server
identifies the pod itself.

Without a Google service account, leave the annotation off and grant the
provider's roles to the Kubernetes service account itself, as the member
`principal://iam.googleapis.com/projects/123456789012/locations/global/workloadIdentityPools/my-ci-123456.svc.id.goog/subject/ns/rungar/sa/rungar`,
where 123456789012 is the project's number.

Either way, leave the provider's `credentials_file` unset, and keep
`GOOGLE_APPLICATION_CREDENTIALS` out of `env`: each is used in place of
Workload Identity.

## On EKS

An `aws` provider finds its credentials as the AWS SDK does, and on EKS
those come from an IAM role for the chart's service account, with no key to
keep. Make a role with the permissions [AWS](../../providers/aws#requirements)
lists, trusting the cluster's OIDC provider for that service account:

```json
{
  "Effect": "Allow",
  "Principal": {
    "Federated": "arn:aws:iam::111122223333:oidc-provider/oidc.eks.eu-west-1.amazonaws.com/id/EXAMPLED539D4633E53DE1B71EXAMPLE"
  },
  "Action": "sts:AssumeRoleWithWebIdentity",
  "Condition": {
    "StringEquals": {
      "oidc.eks.eu-west-1.amazonaws.com/id/EXAMPLED539D4633E53DE1B71EXAMPLE:sub": "system:serviceaccount:rungar:rungar",
      "oidc.eks.eu-west-1.amazonaws.com/id/EXAMPLED539D4633E53DE1B71EXAMPLE:aud": "sts.amazonaws.com"
    }
  }
}
```

and name it on the service account, in a values file:

```yaml
serviceAccount:
  annotations:
    eks.amazonaws.com/role-arn: arn:aws:iam::111122223333:role/rungar
```

`rungar:rungar` is the namespace and the service account's name, as on GKE.
EKS gives the pod a token of its own for the role, so the pod still mounts
no service account token. The pod needs a route to STS in the region as well
as to EC2.

With EKS Pod Identity instead, leave the annotation off, have the role trust
`pods.eks.amazonaws.com` for `sts:AssumeRole` and `sts:TagSession`, and
associate it with the same namespace and service account:

```console
$ aws eks create-pod-identity-association --cluster-name my-cluster \
    --namespace rungar --service-account rungar \
    --role-arn arn:aws:iam::111122223333:role/rungar
```

Either way, leave the provider's `profile` and `credentials_file` unset, and
keep `AWS_ACCESS_KEY_ID` out of `env`: each can be used in place of the role.

## Upgrading

```console
$ helm upgrade rungar oci://ghcr.io/konradasb/charts/rungar --version 0.3.0 -n rungar --reuse-values
```

As with a changed configuration, the pod is replaced and the runners carry
on. Read the release's notes first for a change to the configuration, and
see [Migrating the configuration](../../guides/upgrading-and-uninstalling#migrating-the-configuration).

The chart is signed with cosign by the workflow that publishes it. To
check it:

```console
$ cosign verify ghcr.io/konradasb/charts/rungar:0.3.0 \
    --certificate-identity-regexp '^https://github\.com/konradasb/rungar/\.github/workflows/chart\.yaml@' \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

## Uninstalling

Remove the scale sets first, while the daemon is running, as
[Uninstalling](../../guides/upgrading-and-uninstalling#uninstalling) says:
uninstalling the chart leaves the runners and the scale sets behind. Empty
`scale_sets` in the configuration, upgrade, and remove each one, now
`LEFTOVER`:

```console
$ helm upgrade rungar oci://ghcr.io/konradasb/charts/rungar -n rungar --reuse-values \
    --set-file configFile=config.yaml
$ kubectl exec -n rungar deploy/rungar -- rungar scale-sets ls
$ kubectl exec -n rungar deploy/rungar -- rungar scale-sets rm rungar-c2-m4 --wait
$ helm uninstall rungar -n rungar
```

This removes the pod, the Secret the chart made, with the credentials in it,
and the events' claim, if the chart made it. A Secret or claim of your own
is left alone.
