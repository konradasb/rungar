#!/usr/bin/env bash
# Copyright 2026 Rungar Authors
# SPDX-License-Identifier: MIT

#
# Uploads the repository build.sh made to the bucket it is served from.
#
#   build/repo/push.sh REPO s3://BUCKET
#
# In an order that never leaves a client metadata naming what is not there:
# the packages first, then the indexes, then what is signed and names them.
# Nothing is removed: the rpm metadata files are named for their checksum,
# and a client may still be reading the last build's, which only takes a few
# kilobytes to keep. AWS_ENDPOINT_URL and the usual AWS_* variables say
# where the bucket is, and with what credentials.
#

set -euo pipefail

repo=$1
dest=${2%/}

signed=(
  --exclude 'deb/dists/stable/InRelease'
  --exclude 'deb/dists/stable/Release'
  --exclude 'deb/dists/stable/Release.gpg'
  --exclude 'rpm/repodata/repomd.xml'
  --exclude 'rpm/repodata/repomd.xml.asc'
)

# The packages, which never change once there.
aws s3 sync --only-show-errors "$repo" "$dest" \
  --exclude '*' --include '*.deb' --include '*.rpm'

# The indexes, and everything else that is not signed. A client that has not
# fetched the new signed metadata yet uses the old, which only names files
# still there.
aws s3 sync --only-show-errors "$repo" "$dest" "${signed[@]}" \
  --cache-control 'max-age=300'

# What clients read first.
for f in deb/dists/stable/Release deb/dists/stable/Release.gpg deb/dists/stable/InRelease \
  rpm/repodata/repomd.xml rpm/repodata/repomd.xml.asc; do
  aws s3 cp --only-show-errors "$repo/$f" "$dest/$f" --cache-control 'max-age=300'
done

