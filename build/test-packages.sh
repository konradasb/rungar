#!/usr/bin/env bash
# Copyright 2026 Rungar Authors
# SPDX-License-Identifier: MIT

#
# Tests the packages GoReleaser built, and the repository build/repo makes of
# them, in containers: installs the package from the repository, with its
# signatures checked, on each distribution the documentation names, as it
# says to, then installs it again and removes it.
#
#   build/test-packages.sh DIST
#
# DIST holds the packages, as `make packages` leaves them in dist/. Those
# for this machine's architecture are tested. The repository is signed with
# a key made for the test, which also signs the RPMs, as the release's key
# does. Needs docker.
#

set -euo pipefail

dist=$(cd "$1" && pwd)
root=$(cd "$(dirname "$0")/.." && pwd)

case $(uname -m) in
x86_64 | amd64) deb_arch=amd64 rpm_arch=x86_64 ;;
aarch64 | arm64) deb_arch=arm64 rpm_arch=aarch64 ;;
*) echo "no packages for $(uname -m)" >&2 && exit 1 ;;
esac

work=$(mktemp -d)
net=rungar-test-packages-$$
server=$net-repo
cleanup() {
  docker rm -f "$server" >/dev/null 2>&1 || true
  docker network rm "$net" >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT

mkdir -p "$work/incoming" "$work/repo"
cp "$dist"/*_"$deb_arch".deb "$dist"/*."$rpm_arch".rpm "$work/incoming/"

echo "--- Building the repository"
# As root, so what it writes is given back to whoever runs this, to remove.
docker run --rm -v "$work:/work" -v "$root/build/repo:/build/repo:ro" \
  -e OWNER="$(id -u):$(id -g)" ubuntu:24.04 bash -euc '
  apt-get update -qq >/dev/null
  apt-get install -y -qq apt-utils createrepo-c gpg rpm >/dev/null
  gpg --batch --quiet --passphrase "" --quick-gen-key "Rungar Test <test@example.invalid>" ed25519 sign never
  rpmsign --define "__gpg /usr/bin/gpg" --define "_gpg_name Rungar Test" \
    --addsign /work/incoming/*.rpm >/dev/null 2>&1
  /build/repo/build.sh /work/incoming /work/repo
  chmod -R a+rX /work/repo
  chown -R "$OWNER" /work
'

docker network create "$net" >/dev/null
docker run -d --name "$server" --network "$net" --network-alias pkg \
  -v "$work/repo:/usr/share/nginx/html:ro" nginx:alpine >/dev/null

# Each client adds the repository as the installation guide does, with
# http://pkg in place of https://pkg.rungar.sh, then installs the package and
# checks what it put where. Without systemd running, as in a container, the
# package must still install and remove cleanly. What is in single quotes is
# expanded in the container.
# shellcheck disable=SC2016
check='
  test -x /usr/bin/rungar
  test -f /usr/lib/systemd/system/rungar.service
  getent passwd rungar >/dev/null
  test "$(stat -c %U:%G:%a /etc/rungar)" = root:rungar:750
  rungar --version
'

# What an administrator writes into /etc/rungar is kept when the package is
# installed again, as an upgrade does, and the directory stays the rungar
# group's.
write_config='
  echo "log_level: info" > /etc/rungar/config.yaml
'
# shellcheck disable=SC2016
kept_config='
  test -f /etc/rungar/config.yaml
  test "$(stat -c %U:%G:%a /etc/rungar)" = root:rungar:750
'

apt_client='
  apt-get update -qq >/dev/null
  apt-get install -y -qq curl gpg >/dev/null
  install -d -m 0755 /etc/apt/keyrings
  curl -fsSL http://pkg/gpg.key | gpg --dearmor -o /etc/apt/keyrings/rungar.gpg
  echo "deb [signed-by=/etc/apt/keyrings/rungar.gpg] http://pkg/deb stable main" \
    > /etc/apt/sources.list.d/rungar.list
  apt-get update -qq
  apt-get install -y -qq rungar >/dev/null
  '"$check$write_config"'
  apt-get install -y -qq --reinstall rungar >/dev/null
  '"$kept_config"'
  apt-get purge -y -qq rungar >/dev/null
  test ! -e /usr/bin/rungar
  test ! -e /etc/rungar
'

dnf_client='
  curl -fsSL http://pkg/rpm/rungar.repo | sed "s#https://pkg.rungar.sh#http://pkg#" \
    > /etc/yum.repos.d/rungar.repo
  dnf install -y -q rungar
  '"$check$write_config"'
  dnf reinstall -y -q rungar
  '"$kept_config"'
  dnf remove -y -q rungar
  test ! -e /usr/bin/rungar
'

zypper_client='
  curl -fsSL http://pkg/rpm/rungar.repo | sed "s#https://pkg.rungar.sh#http://pkg#" \
    > /tmp/rungar.repo
  zypper -q addrepo /tmp/rungar.repo
  zypper -q -n --gpg-auto-import-keys install rungar
  '"$check$write_config"'
  zypper -q -n install -f rungar
  '"$kept_config"'
  zypper -q -n remove rungar
  test ! -e /usr/bin/rungar
'

clients=(
  "debian:stable|$apt_client"
  "ubuntu:24.04|$apt_client"
  "fedora:latest|$dnf_client"
  "rockylinux/rockylinux:9|$dnf_client"
  "opensuse/tumbleweed|$zypper_client"
)

failed=()
for client in "${clients[@]}"; do
  image=${client%%|*}
  script=${client#*|}
  echo "--- $image"
  if ! docker run --rm --network "$net" "$image" bash -euxc "$script" >"$work/log" 2>&1; then
    tail -30 "$work/log"
    failed+=("$image")
  fi
done

if ((${#failed[@]})); then
  echo "failed on: ${failed[*]}" >&2
  exit 1
fi
echo "--- All passed"
