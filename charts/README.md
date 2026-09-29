# Helm chart

The `rungar` Helm chart, which runs the container image in Kubernetes, and the
OCI repository at `oci://ghcr.io/konradasb/charts` it is published to.

| Path | |
|---|---|
| `rungar/` | The chart. Its [README](rungar/README.md) is for those installing it, and is what Artifact Hub shows. |
| `rungar/ci/` | The values files the chart is linted and tested with. |
| `test-chart.sh` | Installs the chart with each of them in a kind cluster, with the image built from this checkout. |
| `checkov.yaml` | Checkov's configuration: the checks the chart does not meet by design, and why. |

## Building and testing

```console
make lint-chart  # helm lint --strict, and Checkov, with each values file in rungar/ci
make test-chart  # installs it in a kind cluster, in docker, and checks the daemon runs
make chart       # packages it into dist/
```

`make lint-chart` runs Checkov through `pipx`, which fetches the pinned
version on first use. Checkov scans what the chart renders with each values
file, as Kubernetes manifests; a check the chart does not meet fails the
lint, unless [checkov.yaml](checkov.yaml) skips it, with the reason why.

`make test-chart` builds the image, creates a cluster of its own, and deletes
it after. For each values file it installs the chart, runs `helm test`, asks
the daemon for `rungar status` through its socket, scrapes the metrics where
they are enabled, and checks a changed configuration replaces the pod. The
test configuration has no scale sets, so the daemon runs without a real
GitHub credential or a Dicer host.

The [test-chart workflow](../.github/workflows/test-chart.yaml) runs both
for a pull request that changes the chart or what makes the image.

## Publishing

The chart has no version of its own: `version` and `appVersion` in
`Chart.yaml` stay `0.0.0-dev` and `latest`, and each release publishes it
with its own version as both, so chart `0.2.0` runs image `0.2.0`. A change
to the chart is published with the next release.

Publishing a release runs the [chart workflow](../.github/workflows/chart.yaml),
which packages the chart at the release's tag, pushes it to
`oci://ghcr.io/konradasb/charts/rungar` with the workflow's own token, and
signs it by digest with cosign, keyless, as the workflow. A pre-release is
published too, with its version: Helm installs it only when asked for by
that version, or with `--devel`.

Run the workflow by hand, with a tag, to publish a release's chart again.
It pushes the same version over the last, and signs it again.

## Setting it up

Nothing is needed before the first publish: the workflow pushes with
`GITHUB_TOKEN`, and GitHub creates the package, `charts/rungar`, on the first
push, connected to this repository. Once it has:

1. **Make it public**, if it is not: *your profile → Packages → charts/rungar
   → Package settings → Change visibility*. A public package cannot be made
   private again. The image the chart runs, `rungar`, needs the same, or
   neither installs without credentials.

2. **Check the repository can still push to it**: under *Manage Actions
   access* on the same page, `konradasb/rungar` with the *Write* role. It is
   there when the workflow created the package; a package of that name
   created some other way needs it adding, or every push is refused.

Optionally, list it on [Artifact Hub](https://artifacthub.io): *Control
Panel → Add repository*, of kind *Helm charts*, with the URL
`oci://ghcr.io/konradasb/charts/rungar`. It reads the chart's metadata and
its README from the registry.
