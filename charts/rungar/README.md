# Rungar

[Rungar](https://github.com/konradasb/rungar) runs GitHub Actions jobs on
virtual machines, one per job, on hosts of your own and in the cloud. This
chart runs its daemon, `rungar`, in Kubernetes: one pod, which talks to GitHub
and to the providers -- Dicer hosts, Compute Engine and the rest -- over the
network. The runners are not in the cluster; they are machines on the
providers. [Installing on Kubernetes](https://rungar.sh/docs/getting-started/kubernetes/)
covers the same in more detail.

## Installing

```console
helm install rungar oci://ghcr.io/konradasb/charts/rungar \
  --namespace rungar --create-namespace \
  --set-file configFile=config.yaml \
  --set-file files.app\.pem=app.pem \
  --set-file files.ca\.pem=ca.pem
```

`config.yaml` is the daemon's configuration, as the
[configuration reference](https://rungar.sh/docs/reference/configuration/)
describes it; each file under `files` is put beside it in `/etc/rungar`,
where the configuration names it: `app_private_key_path: /etc/rungar/app.pem`.
Both are kept in a Secret. Or give the configuration as YAML under `config`
in a values file, or a Secret of your own with `existingSecret`, whose keys
are the files, `config.yaml` among them.

The chart's version is the Rungar release it runs. To check what is
installed, and what the daemon is doing:

```console
helm test rungar -n rungar
kubectl exec -n rungar deploy/rungar -- rungar status
```

`helm test` validates the configuration as the daemon has it mounted.

## On GKE

A `gcp` provider authenticates with Application Default Credentials when it
has no `credentials_file`, and on GKE those come from Workload Identity, with
no key to keep. With Workload Identity enabled on the cluster and its node
pool, let the chart's Kubernetes service account act as a Google service
account that has the provider's roles:

```console
gcloud iam service-accounts add-iam-policy-binding \
  rungar@my-ci-123456.iam.gserviceaccount.com \
  --role roles/iam.workloadIdentityUser \
  --member "serviceAccount:my-ci-123456.svc.id.goog[rungar/rungar]"
```

and name it on the Kubernetes one:

```yaml
serviceAccount:
  annotations:
    iam.gke.io/gcp-service-account: rungar@my-ci-123456.iam.gserviceaccount.com
```

`rungar/rungar` is the namespace and the service account's name, the
release's full name unless `serviceAccount.name` says otherwise.
`automountServiceAccountToken: false` stays as it is: GKE's metadata server
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
keep. Make a role with the permissions the
[aws provider](https://rungar.sh/docs/providers/aws/) lists, trusting the
cluster's OIDC provider for that service account:

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

and name it on the service account:

```yaml
serviceAccount:
  annotations:
    eks.amazonaws.com/role-arn: arn:aws:iam::111122223333:role/rungar
```

`rungar:rungar` is the namespace and the service account's name, as on GKE.
EKS gives the pod a token of its own for the role, so
`automountServiceAccountToken: false` stays as it is. The pod needs a route
to STS in the region as well as to EC2.

With EKS Pod Identity instead, leave the annotation off, have the role trust
`pods.eks.amazonaws.com` for `sts:AssumeRole` and `sts:TagSession`, and
associate it with the same namespace and service account.

Either way, leave the provider's `profile` and `credentials_file` unset, and
keep `AWS_ACCESS_KEY_ID` out of `env`: each can be used in place of the role.

## Upgrading

```console
helm upgrade rungar oci://ghcr.io/konradasb/charts/rungar --version 0.3.0 -n rungar --reuse-values
```

The old pod stops before the new one starts: two daemons never serve the
same scale sets at once. Runners keep running meanwhile, and the new daemon
adopts them, so an upgrade, or a changed configuration, which also replaces
the pod, disturbs no job. Read the release's notes first for a change to the
configuration.

## Verifying

The chart is signed with cosign by the workflow that publishes it:

```console
cosign verify ghcr.io/konradasb/charts/rungar:0.3.0 \
  --certificate-identity-regexp '^https://github\.com/konradasb/rungar/\.github/workflows/chart\.yaml@' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

## Values

[values.yaml](values.yaml) describes each. The ones most installs set:

| Value | |
|---|---|
| `config` / `configFile` | The configuration, as YAML, or as the file's text. |
| `files` | Credentials and TLS material, by file name, put in `/etc/rungar`. |
| `existingSecret` | A Secret of your own holding all of `/etc/rungar` instead. |
| `metrics.enabled` | Serve the Prometheus metrics, behind a Service. `metrics.serviceMonitor.enabled` adds a ServiceMonitor. |
| `events.persistence.enabled` | Keep the events `rungar events` shows across a new pod. |
| `networkPolicy.metricsFrom` / `networkPolicy.egress` | Who may scrape the metrics, and where the daemon may connect. The policy admits nothing else to the pod. |
| `startupProbe` / `livenessProbe` / `readinessProbe` | The probes, which ask the daemon through its socket; `null` leaves one out. |
| `env` | `HTTPS_PROXY` and `NO_PROXY`, say. |

The pod runs as the image's `nonroot` user with a read-only root filesystem
and no capabilities, and needs no access to the Kubernetes API.
