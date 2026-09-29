#!/usr/bin/env bash
# Copyright 2026 Rungar Authors
# SPDX-License-Identifier: MIT

#
# Adds packages to the apt and dnf repository, and signs its metadata again.
#
#   build/repo/build.sh INCOMING REPO
#
# INCOMING holds the .deb and .rpm files to add; REPO is the repository as it
# is published, or an empty directory for a new one. The packages already in
# REPO stay: every published version remains installable. Adding a file that
# is already there with other contents fails, as a published version never
# changes.
#
# The metadata is signed with the key GPG_KEY_ID names, or gpg's default key,
# which must have no passphrase; every RPM must be signed with it. Needs
# apt-ftparchive (apt-utils), createrepo_c, rpmkeys (rpm) and gpg.
#
# REPO then holds:
#
#   gpg.key                          the public key both are signed with
#   deb/pool/main/f/rungar/*.deb
#   deb/dists/stable/                InRelease, Release and Release.gpg,
#                                    main/binary-{amd64,arm64}/Packages
#   rpm/rungar.repo                   for /etc/yum.repos.d or zypper addrepo
#   rpm/Packages/*.rpm
#   rpm/repodata/                    repomd.xml, repomd.xml.asc and the rest
#

set -euo pipefail

incoming=$1
repo=$2

here=$(cd "$(dirname "$0")" && pwd)
deb_arches=(amd64 arm64)
gpg_args=(--batch --yes ${GPG_KEY_ID:+--local-user "$GPG_KEY_ID"})

# add copies each file into dir, unless it is there already, as it must then
# be byte for byte.
add() {
  local dir=$1
  shift
  mkdir -p "$dir"
  local f dst
  for f in "$@"; do
    dst=$dir/$(basename "$f")
    if [[ -e $dst ]]; then
      cmp -s "$f" "$dst" || {
        echo "$dst is published already, with other contents" >&2
        exit 1
      }
    else
      cp "$f" "$dst"
    fi
  done
}

shopt -s nullglob
debs=("$incoming"/*.deb)
rpms=("$incoming"/*.rpm)
shopt -u nullglob
if ((${#debs[@]} + ${#rpms[@]} == 0)); then
  echo "no packages in $incoming" >&2
  exit 1
fi

# dnf checks each package's signature, so an RPM not signed with this key
# would not install: refuse it here instead. rpmkeys counts one with no
# signature at all as fine, so it is the signature itself that is checked.
if ((${#rpms[@]})); then
  rpmdb=$(mktemp -d)
  trap 'rm -rf "$rpmdb"' EXIT
  gpg --batch --armor --export ${GPG_KEY_ID:+"$GPG_KEY_ID"} >"$rpmdb/key.asc"
  rpmkeys --dbpath "$rpmdb" --import "$rpmdb/key.asc"
  for f in "${rpms[@]}"; do
    if ! rpmkeys --dbpath "$rpmdb" --checksig "$f" 2>&1 | grep -q ': digests signatures OK$'; then
      echo "$f is not signed with the repository's key" >&2
      exit 1
    fi
  done
fi

add "$repo/deb/pool/main/f/rungar" "${debs[@]}"
add "$repo/rpm/Packages" "${rpms[@]}"

# apt: an index per architecture, and a Release over them all, which is what
# apt checks the signature of.
dists=$repo/deb/dists/stable
for arch in "${deb_arches[@]}"; do
  dir=$dists/main/binary-$arch
  mkdir -p "$dir"
  (cd "$repo/deb" && apt-ftparchive --arch "$arch" packages pool) >"$dir/Packages"
  gzip -9nkf "$dir/Packages"
done
rm -f "$dists/Release" "$dists/InRelease" "$dists/Release.gpg"
apt-ftparchive \
  -o APT::FTPArchive::Release::Origin=Rungar \
  -o APT::FTPArchive::Release::Label=Rungar \
  -o APT::FTPArchive::Release::Suite=stable \
  -o APT::FTPArchive::Release::Codename=stable \
  -o APT::FTPArchive::Release::Architectures="${deb_arches[*]}" \
  -o APT::FTPArchive::Release::Components=main \
  -o APT::FTPArchive::Release::Description="Rungar, which runs GitHub Actions runners as virtual machines on hosts of your own" \
  release "$dists" >"$repo/Release.tmp"
mv "$repo/Release.tmp" "$dists/Release"
gpg "${gpg_args[@]}" --clearsign --output "$dists/InRelease" "$dists/Release"
gpg "${gpg_args[@]}" --armor --detach-sign --output "$dists/Release.gpg" "$dists/Release"

# dnf and zypper: one repository for both architectures, which each client
# filters. gzip, rather than createrepo_c's default zstd, for the older ones.
createrepo_c --quiet --general-compress-type=gz "$repo/rpm"
gpg "${gpg_args[@]}" --armor --detach-sign --output "$repo/rpm/repodata/repomd.xml.asc" "$repo/rpm/repodata/repomd.xml"
cp "$here/rungar.repo" "$repo/rpm/rungar.repo"

gpg --batch --armor --export ${GPG_KEY_ID:+"$GPG_KEY_ID"} >"$repo/gpg.key"
