#!/bin/bash
# Copyright 2026 Rungar Authors
# SPDX-License-Identifier: MIT
#
# Installs the Actions runner as the gcp provider's default startup script
# and the aws provider's default user data run it: unpacked in /home/runner,
# owned by the runner user. Run as root, by gcp/runner.pkr.hcl and
# aws/runner.pkr.hcl beside it, with RUNNER_VERSION, RUNNER_ARCH and
# RUNNER_SUDO set.

set -euo pipefail

: "${RUNNER_VERSION:?}" "${RUNNER_ARCH:?}" "${RUNNER_SUDO:?}"
export DEBIAN_FRONTEND=noninteractive

# Upgrades on boot hold apt's lock while a job wants it, and change a machine
# that should be the image and nothing else. A new image is the upgrade.
systemctl disable --now unattended-upgrades.service apt-daily.timer apt-daily-upgrade.timer || true

apt-get update
apt-get upgrade -y
apt-get install -y --no-install-recommends ca-certificates curl git jq unzip zip

useradd --create-home --shell /bin/bash runner
if [ "$RUNNER_SUDO" = true ]; then
  echo 'runner ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/runner
  chmod 0440 /etc/sudoers.d/runner
fi

# The archive is checked against the digest GitHub keeps for the release's
# asset.
archive="actions-runner-linux-${RUNNER_ARCH}-${RUNNER_VERSION}.tar.gz"
digest=$(curl -fsSL "https://api.github.com/repos/actions/runner/releases/tags/v${RUNNER_VERSION}" |
  jq -r --arg name "$archive" '.assets[] | select(.name == $name) | .digest')
[[ "$digest" == sha256:* ]] || { echo "no digest for $archive" >&2; exit 1; }

cd /home/runner
curl -fsSL -o "$archive" "https://github.com/actions/runner/releases/download/v${RUNNER_VERSION}/${archive}"
echo "${digest#sha256:}  ${archive}" | sha256sum -c -
tar xzf "$archive"
rm "$archive"

./bin/installdependencies.sh
chown -R runner:runner /home/runner

apt-get clean
rm -rf /var/lib/apt/lists/*
