#!/usr/bin/env bash
# Copyright 2026 Rungar Authors
# SPDX-License-Identifier: MIT

#
# Rungar Install Script
#
# Downloads the latest rungar release, installs it, and sets up the systemd
# service. It does not write a configuration file: there is no useful default
# for which GitHub to talk to or which providers to place runners on, so that
# part is yours. See example.yml.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/konradasb/rungar/main/scripts/install.sh | sudo bash
#   curl -fsSL https://raw.githubusercontent.com/konradasb/rungar/main/scripts/install.sh | sudo bash -s -- --version v0.1.0
#

set -euo pipefail

REPO="konradasb/rungar"
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/rungar"
SYSTEMD_DIR="/etc/systemd/system"
SERVICE_NAME="rungar"
SERVICE_USER="rungar"
VERSION=""

RED='\033[38;2;255;110;110m'
GREEN='\033[38;2;92;190;83m'
YELLOW='\033[0;33m'
NC='\033[0m'

info() { echo -e "${GREEN}[INFO]${NC}  $1"; }
warn() { echo -e "${YELLOW}[WARN]${NC}  $1"; }
error() {
  echo -e "${RED}[ERROR]${NC} $1"
  exit 1
}

usage() {
  cat <<EOF
Install the Rungar daemon.

Usage: install.sh [options]

Options:
  --version VERSION   Release to install (default: the latest)
  --no-service        Install the binary only; do not touch systemd
  -h, --help          Show this help
EOF
}

NO_SERVICE=false
OS=""
ARCH=""

while [[ $# -gt 0 ]]; do
  case "$1" in
  --version)
    VERSION="${2:-}"
    [[ -n $VERSION ]] || error "--version needs a value"
    shift 2
    ;;
  --no-service)
    NO_SERVICE=true
    shift
    ;;
  -h | --help)
    usage
    exit 0
    ;;
  *)
    error "unknown option: $1"
    ;;
  esac
done

[[ $EUID -eq 0 ]] || error "run this as root (sudo)"

# Rungar is a network client and runs anywhere its providers are reachable
# from. The systemd service is Linux only; elsewhere the binary is installed
# and running it is yours to arrange.
case "$(uname -s)" in
Linux) OS="linux" ;;
Darwin)
  OS="darwin"
  NO_SERVICE=true
  ;;
*) error "unsupported system: $(uname -s); build from source with 'make build'" ;;
esac

case "$(uname -m)" in
x86_64) ARCH="amd64" ;;
aarch64 | arm64) ARCH="arm64" ;;
*) error "unsupported architecture: $(uname -m)" ;;
esac

for tool in curl tar; do
  command -v "$tool" >/dev/null || error "$tool is required"
done

if [[ -z $VERSION ]]; then
  info "Looking up the latest release"
  VERSION=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" |
    grep '"tag_name"' | head -1 | cut -d'"' -f4)
  [[ -n $VERSION ]] || error "could not determine the latest release; pass --version"
fi

info "Installing rungar ${VERSION} (${OS}/${ARCH})"

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

ARCHIVE="rungar_${VERSION#v}_${OS}_${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${VERSION}/${ARCHIVE}"

curl -fsSL -o "${TMP}/${ARCHIVE}" "$URL" ||
  error "could not download ${URL}"

# The checksums are signed with cosign by the release workflow. They are
# checked against the signature when cosign is installed, and the archive
# against them either way.
BASE="https://github.com/${REPO}/releases/download/${VERSION}"

curl -fsSL -o "${TMP}/checksums.txt" "${BASE}/checksums.txt" ||
  error "could not download the checksums for ${VERSION}"

if command -v cosign >/dev/null; then
  curl -fsSL -o "${TMP}/checksums.txt.sigstore.json" "${BASE}/checksums.txt.sigstore.json" ||
    error "could not download the checksums' signature"
  cosign verify-blob "${TMP}/checksums.txt" \
    --bundle "${TMP}/checksums.txt.sigstore.json" \
    --certificate-identity "https://github.com/${REPO}/.github/workflows/release.yaml@refs/tags/${VERSION}" \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com >/dev/null 2>&1 ||
    error "the checksums' signature does not verify"
  info "Signature verified"
else
  warn "cosign not found; checking the archive against the checksums without their signature"
fi

# macOS ships shasum rather than sha256sum.
if command -v sha256sum >/dev/null; then
  (cd "$TMP" && grep " ${ARCHIVE}\$" checksums.txt | sha256sum -c - >/dev/null) ||
    error "checksum mismatch for ${ARCHIVE}"
else
  (cd "$TMP" && grep " ${ARCHIVE}\$" checksums.txt | shasum -a 256 -c - >/dev/null) ||
    error "checksum mismatch for ${ARCHIVE}"
fi
info "Checksum verified"

tar -xzf "${TMP}/${ARCHIVE}" -C "$TMP"
install -m 0755 "${TMP}/rungar" "${INSTALL_DIR}/rungar"
info "Installed ${INSTALL_DIR}/rungar"

if [[ $NO_SERVICE == true ]]; then
  info "Done. Run 'rungar serve -f <config.yaml>' when you have a configuration."
  exit 0
fi

command -v systemctl >/dev/null || {
  warn "systemd not found; skipping the service"
  exit 0
}

# A system user of its own, so that the credentials the configuration names
# can be readable by the service and nobody else.
if ! id -u "$SERVICE_USER" >/dev/null 2>&1; then
  useradd --system --no-create-home --shell /usr/sbin/nologin "$SERVICE_USER"
  info "Created the ${SERVICE_USER} system user"
fi

mkdir -p "$CONFIG_DIR"
chown root:"$SERVICE_USER" "$CONFIG_DIR"
chmod 0750 "$CONFIG_DIR"

curl -fsSL -o "${SYSTEMD_DIR}/${SERVICE_NAME}.service" \
  "https://raw.githubusercontent.com/${REPO}/${VERSION}/scripts/rungar.service" ||
  error "could not download the service unit"

systemctl daemon-reload
info "Installed ${SYSTEMD_DIR}/${SERVICE_NAME}.service"

cat <<EOF

Rungar is installed but not started: it has nothing to do until it is
configured.

  1. Write ${CONFIG_DIR}/config.yaml, readable by the ${SERVICE_USER} group.
     See https://github.com/${REPO}/blob/${VERSION}/example.yml

  2. Check the configuration:
       sudo -u ${SERVICE_USER} rungar validate -f ${CONFIG_DIR}/config.yaml

  3. Start it:
       systemctl enable --now ${SERVICE_NAME}

EOF
