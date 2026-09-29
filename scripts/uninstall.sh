#!/usr/bin/env bash
# Copyright 2026 Rungar Authors
# SPDX-License-Identifier: MIT

#
# Rungar Uninstall Script
#
# Removes what install.sh installed: the rungar binary, its systemd service,
# its configuration and the rungar user. The events are kept unless --purge is
# given. A rungar installed from a package is removed with the package
# manager instead.
#
# Runners already on the fleet, and the scale sets on GitHub, are not
# touched: remove them first, while the daemon is running, with
# 'rungar scale-sets rm'.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/konradasb/rungar/main/scripts/uninstall.sh | sudo bash
#   curl -fsSL https://raw.githubusercontent.com/konradasb/rungar/main/scripts/uninstall.sh | sudo bash -s -- --purge
#

set -euo pipefail

INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/rungar"
LOGS_DIR="/var/log/rungar"
SYSTEMD_DIR="/etc/systemd/system"
SERVICE_NAME="rungar"
SERVICE_USER="rungar"

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
Uninstall the Rungar daemon.

Usage: uninstall.sh [options]

Options:
  --purge      Also remove the events (${LOGS_DIR}). Cannot be undone.
  -h, --help   Show this help
EOF
}

PURGE=false

while [[ $# -gt 0 ]]; do
  case "$1" in
  --purge)
    PURGE=true
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

# A package owns its files, and removing them from under it leaves the
# package manager believing they are still there.
if command -v dpkg-query >/dev/null && dpkg-query -W -f='${Status}' rungar 2>/dev/null | grep -q "ok installed"; then
  error "rungar was installed from a package; remove it with 'apt remove rungar' (or 'apt purge rungar')"
fi
if command -v rpm >/dev/null && rpm -q rungar >/dev/null 2>&1; then
  error "rungar was installed from a package; remove it with 'dnf remove rungar' or 'zypper remove rungar'"
fi

# =============================================================================
# The service
# =============================================================================

if command -v systemctl >/dev/null; then
  if systemctl is-active --quiet "$SERVICE_NAME" 2>/dev/null; then
    # Rungar leaves every runner where it is when it stops, so this disturbs
    # no job; nothing will look after the runners afterwards, though.
    info "Stopping ${SERVICE_NAME}"
    systemctl stop "$SERVICE_NAME"
  fi

  if systemctl is-enabled --quiet "$SERVICE_NAME" 2>/dev/null; then
    info "Disabling ${SERVICE_NAME}"
    systemctl disable --quiet "$SERVICE_NAME"
  fi

  UNIT="${SYSTEMD_DIR}/${SERVICE_NAME}.service"
  DROPINS="${SYSTEMD_DIR}/${SERVICE_NAME}.service.d"
  if [[ -f $UNIT || -d $DROPINS ]]; then
    rm -f "$UNIT"
    # Drop-ins made with 'systemctl edit rungar'.
    rm -rf "$DROPINS"
    systemctl daemon-reload
    info "Removed ${UNIT}"
  fi
fi

# =============================================================================
# The binary
# =============================================================================

if [[ -f "${INSTALL_DIR}/rungar" ]]; then
  rm -f "${INSTALL_DIR}/rungar"
  info "Removed ${INSTALL_DIR}/rungar"
fi

# =============================================================================
# The configuration, and the credentials it names
# =============================================================================

if [[ -d $CONFIG_DIR ]]; then
  rm -rf "$CONFIG_DIR"
  info "Removed ${CONFIG_DIR}"
fi

# =============================================================================
# The events
# =============================================================================

if [[ -d $LOGS_DIR ]]; then
  if [[ $PURGE == true ]]; then
    rm -rf "$LOGS_DIR"
    info "Removed ${LOGS_DIR}"
  else
    warn "${LOGS_DIR} was kept; run with --purge to remove it too"
  fi
fi

# =============================================================================
# The user
# =============================================================================

# Kept while the events are, so that they still belong to someone.
if id -u "$SERVICE_USER" >/dev/null 2>&1; then
  if [[ -d $LOGS_DIR ]]; then
    warn "the ${SERVICE_USER} user was kept, since it owns ${LOGS_DIR}"
  else
    userdel "$SERVICE_USER"
    info "Removed the ${SERVICE_USER} user"
  fi
fi

cat <<EOF

Rungar is uninstalled.

Runners it left on the fleet, and its scale sets on GitHub, are still there.
A Rungar installed again with the same configuration adopts them; otherwise,
remove the machines on their providers, and the scale sets on GitHub under
Settings -> Actions -> Runners. 'rungar scale-sets rm', run before
uninstalling while the daemon is up, does both.

EOF
