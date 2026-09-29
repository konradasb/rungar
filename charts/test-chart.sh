#!/usr/bin/env bash
# Copyright 2026 Rungar Authors
# SPDX-License-Identifier: MIT

#
# Tests the chart in a kind cluster, with the image built from this checkout:
# installs it with each values file in charts/rungar/ci, waits for the daemon
# to be ready, runs `helm test`, which validates the configuration as it is
# mounted, and asks the daemon for its status through its socket, as the
# chart's notes say to. With metrics enabled, scrapes them through the
# Service. Then changes the configuration, which must replace the pod.
#
#   charts/test-chart.sh
#
# HELM and KIND name the binaries to use, helm and kind unless set. Needs
# docker and kubectl. The cluster is made for the test, and deleted after.
#

set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
helm=${HELM:-helm}
kind=${KIND:-kind}
chart=$root/charts/rungar
cluster=rungar-chart-test-$$
image=rungar-chart-test:dev

work=$(mktemp -d)
export KUBECONFIG=$work/kubeconfig
cleanup() {
  kill "${forward:-}" 2>/dev/null || true
  "$kind" delete cluster --name "$cluster" >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT

echo "--- Building the image"
docker build -q -t "$image" "$root" >/dev/null

echo "--- Making the cluster"
"$kind" create cluster --name "$cluster" --wait 2m >/dev/null 2>&1
"$kind" load docker-image "$image" --name "$cluster" >/dev/null 2>&1

# pod names the daemon's pod in namespace $1.
pod() {
  kubectl get pods -n "$1" -l app.kubernetes.io/name=rungar \
    --field-selector=status.phase=Running -o jsonpath='{.items[0].metadata.name}'
}

for values in "$chart"/ci/*-values.yaml; do
  ns=$(basename "$values" -values.yaml)
  echo "--- $ns"
  "$helm" install rungar "$chart" -n "$ns" --create-namespace -f "$values" \
    --wait --timeout 3m >/dev/null
  "$helm" test rungar -n "$ns" --logs >"$work/test.log" 2>&1 ||
    { cat "$work/test.log"; exit 1; }
  grep -q 'is valid' "$work/test.log"

  kubectl exec -n "$ns" deploy/rungar -- rungar status >"$work/status"
  grep -q '^Rungar ' "$work/status" || { cat "$work/status"; exit 1; }

  # The Secret is mounted readable by the pod's group alone, as the daemon
  # wants a credential to be.
  kubectl logs -n "$ns" deploy/rungar >"$work/log"
  if grep 'readable by anyone' "$work/log"; then
    exit 1
  fi

  if kubectl get svc -n "$ns" rungar-metrics >/dev/null 2>&1; then
    kubectl port-forward -n "$ns" svc/rungar-metrics 19102:9102 >/dev/null 2>&1 &
    forward=$!
    for _ in $(seq 20); do
      curl -fsS http://127.0.0.1:19102/metrics >"$work/metrics" 2>/dev/null && break
      sleep 1
    done
    kill "$forward"
    wait "$forward" 2>/dev/null || true
    grep -q '^rungar_' "$work/metrics" || { echo "no rungar_ metrics" >&2; exit 1; }
  fi

  # A changed configuration replaces the pod, as the daemon reads it once.
  before=$(pod "$ns")
  "$helm" upgrade rungar "$chart" -n "$ns" -f "$values" \
    --set files.token=ghp_anothertoken --wait --timeout 3m >/dev/null
  kubectl rollout status -n "$ns" deploy/rungar --timeout 2m >/dev/null
  after=$(pod "$ns")
  if [[ $before == "$after" ]]; then
    echo "the pod was not replaced when its configuration changed" >&2
    exit 1
  fi

  "$helm" uninstall rungar -n "$ns" --wait >/dev/null
done

echo "--- All passed"
