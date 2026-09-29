# Rungar

[Rungar](https://github.com/konradasb/rungar) runs GitHub Actions jobs on
virtual machines, one per job, on hosts of your own. This chart runs its
daemon, `rungar`, in Kubernetes: one pod, which talks to GitHub and to the
providers -- Dicer hosts -- over the network. The runners are not in the
cluster; they are virtual machines on the providers.

## Installing

```console
helm install rungar oci://ghcr.io/konradasb/charts/rungar \
  --namespace rungar --create-namespace \
  --set-file configFile=config.yaml \
  --set-file files.app\.pem=app.pem \
  --set-file files.ca\.pem=ca.pem
```

`config.yaml` is the daemon's configuration, as the repository's
[example.yml](https://github.com/konradasb/rungar/blob/main/example.yml)
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
| `env` | `HTTPS_PROXY` and `NO_PROXY`, say. |

The pod runs as the image's `nonroot` user with a read-only root filesystem
and no capabilities, and needs no access to the Kubernetes API.
