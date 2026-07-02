#!/usr/bin/env bash
set -Eeuo pipefail

# Remove a macOS Lima-based NovitaBox installation.
#
# Default usage:
#   scripts/uninstall-macos-lima.sh
#
# Common overrides:
#   KEEP_VM=1 scripts/uninstall-macos-lima.sh
#   KEEP_LIMA_IMAGE=1 KEEP_ASSETS=1 scripts/uninstall-macos-lima.sh
#   FORCE=1 scripts/uninstall-macos-lima.sh

VM_NAME="${VM_NAME:-novitabox}"
LIMA_TEMPLATE_NAME="${LIMA_TEMPLATE_NAME:-ubuntu-24.04-novitabox}"
LIMA_TEMPLATE_DIR="${LIMA_TEMPLATE_DIR:-${HOME}/.lima/_templates}"
LIMA_TEMPLATE_PATH="${LIMA_TEMPLATE_PATH:-${LIMA_TEMPLATE_DIR}/${LIMA_TEMPLATE_NAME}.yaml}"
LIMA_IMAGE_PATH="${LIMA_IMAGE_PATH:-}"
SOURCE_DIR="${SOURCE_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
ASSET_DIR="${ASSET_DIR:-${SOURCE_DIR}/assets}"
BREW_BIN="${BREW_BIN:-}"
BREW_PREFIX="${BREW_PREFIX:-}"
MAC_DNSMASQ_MAIN_CONF="${MAC_DNSMASQ_MAIN_CONF:-}"
MAC_DNSMASQ_CONF="${MAC_DNSMASQ_CONF:-}"
MAC_RESOLVER_DIR="${MAC_RESOLVER_DIR:-/etc/resolver}"
MAC_CADDYFILE="${MAC_CADDYFILE:-}"
MAC_CADDY_CA_PATH="${MAC_CADDY_CA_PATH:-${SOURCE_DIR}/assets/caddy-local-root.crt}"
LIMA_USER="${LIMA_USER:-novitabox}"
VM_INSTALL_DIR="${VM_INSTALL_DIR:-/home/${LIMA_USER}/novitabox-install}"
ROOT_DIR="${ROOT_DIR:-/data/novitabox}"
IMAGE_PATH="${IMAGE_PATH:-/data/novitabox.img}"
DOMAIN="${DOMAIN:-novitabox.local}"

KEEP_VM="${KEEP_VM:-0}"
KEEP_TEMPLATE="${KEEP_TEMPLATE:-0}"
KEEP_LIMA_IMAGE="${KEEP_LIMA_IMAGE:-0}"
KEEP_ASSETS="${KEEP_ASSETS:-0}"
FORCE="${FORCE:-0}"

log() {
  printf '[novitabox-macos-uninstall] %s\n' "$*"
}

warn() {
  printf '[novitabox-macos-uninstall] warning: %s\n' "$*" >&2
}

die() {
  printf '[novitabox-macos-uninstall] error: %s\n' "$*" >&2
  exit 1
}

command_exists() {
  command -v "$1" >/dev/null 2>&1
}

need_macos() {
  if [[ "$(uname -s)" != "Darwin" ]]; then
    die "this uninstaller must run on macOS"
  fi
}

need_lima() {
  if ! command_exists limactl; then
    die "Lima is not installed; nothing to remove through limactl"
  fi
}

resolve_macos_service_paths() {
  if command_exists brew; then
    BREW_BIN="${BREW_BIN:-$(command -v brew)}"
  fi
  if [[ -z "${BREW_PREFIX}" ]] && [[ -n "${BREW_BIN}" ]]; then
    BREW_PREFIX="$("${BREW_BIN}" --prefix)"
  fi
  if [[ -n "${BREW_PREFIX}" ]]; then
    MAC_DNSMASQ_MAIN_CONF="${MAC_DNSMASQ_MAIN_CONF:-${BREW_PREFIX}/etc/dnsmasq.conf}"
    MAC_DNSMASQ_CONF="${MAC_DNSMASQ_CONF:-${BREW_PREFIX}/etc/dnsmasq.d/novitabox.conf}"
    MAC_CADDYFILE="${MAC_CADDYFILE:-${BREW_PREFIX}/etc/Caddyfile}"
  else
    MAC_DNSMASQ_MAIN_CONF="${MAC_DNSMASQ_MAIN_CONF:-}"
    MAC_DNSMASQ_CONF="${MAC_DNSMASQ_CONF:-}"
    MAC_CADDYFILE="${MAC_CADDYFILE:-}"
  fi
}

detect_host_lima_arch() {
  local machine
  machine="$(uname -m)"
  case "${machine}" in
    arm64 | aarch64) echo "aarch64" ;;
    x86_64 | amd64) echo "x86_64" ;;
    *) echo "aarch64" ;;
  esac
}

default_lima_image_name() {
  case "$(detect_host_lima_arch)" in
    aarch64) echo "ubuntu-24.04-server-cloudimg-arm64.img" ;;
    x86_64) echo "ubuntu-24.04-server-cloudimg-amd64.img" ;;
  esac
}

resolve_paths() {
  LIMA_IMAGE_PATH="${LIMA_IMAGE_PATH:-${LIMA_TEMPLATE_DIR}/$(default_lima_image_name)}"
}

confirm_destructive() {
  if [[ "${FORCE}" == "1" ]]; then
    return
  fi

  cat <<EOF
This will uninstall the Lima-based NovitaBox environment.

It will remove:
  Lima VM:       ${VM_NAME}
  template:      ${LIMA_TEMPLATE_PATH}
  Lima image:    ${LIMA_IMAGE_PATH}
  asset cache:   ${ASSET_DIR}
  resolver:      ${MAC_RESOLVER_DIR}/${DOMAIN}
  dnsmasq main:  ${MAC_DNSMASQ_MAIN_CONF:-<unknown; brew not found>}
  dnsmasq conf:  ${MAC_DNSMASQ_CONF:-<unknown; brew not found>}
  Caddyfile:     ${MAC_CADDYFILE:-<unknown; brew not found>}
  Caddy CA copy: ${MAC_CADDY_CA_PATH}

It will first try to run the Linux uninstaller inside the VM.

Set KEEP_VM=1, KEEP_TEMPLATE=1, KEEP_LIMA_IMAGE=1, or KEEP_ASSETS=1 to keep those files.
Set FORCE=1 to skip this prompt.

EOF

  read -r -p "Continue? [y/N] " answer
  case "${answer}" in
    y | Y | yes | YES) ;;
    *) die "aborted" ;;
  esac
}

vm_exists() {
  limactl list --format '{{.Name}}' 2>/dev/null | grep -Fxq "${VM_NAME}"
}

run_linux_uninstaller_in_vm() {
  if ! vm_exists; then
    log "Lima VM ${VM_NAME} does not exist; skipping in-VM cleanup"
    return
  fi

  log "starting ${VM_NAME} for in-VM cleanup"
  limactl start "${VM_NAME}" >/dev/null 2>&1 || true

  if ! limactl shell "${VM_NAME}" -- test -f "${VM_INSTALL_DIR}/scripts/uninstall.sh" >/dev/null 2>&1; then
    warn "in-VM uninstaller not found at ${VM_INSTALL_DIR}/scripts/uninstall.sh; removing VM directly"
    return
  fi

  log "running Linux uninstaller inside ${VM_NAME}"
  limactl shell "${VM_NAME}" -- bash -lc "
    sudo env \
      ROOT_DIR='${ROOT_DIR}' \
      IMAGE_PATH='${IMAGE_PATH}' \
      DOMAIN='${DOMAIN}' \
      FORCE=1 \
      bash '${VM_INSTALL_DIR}/scripts/uninstall.sh'
  " || warn "in-VM uninstaller failed; continuing with Lima cleanup"
}

remove_vm() {
  if [[ "${KEEP_VM}" == "1" ]]; then
    log "KEEP_VM=1; keeping Lima VM ${VM_NAME}"
    return
  fi

  if vm_exists; then
    log "removing Lima VM ${VM_NAME}"
    limactl stop "${VM_NAME}" --force >/dev/null 2>&1 || true
    limactl delete "${VM_NAME}" --force
  else
    log "Lima VM ${VM_NAME} is already absent"
  fi
}

remove_template() {
  if [[ "${KEEP_TEMPLATE}" == "1" ]]; then
    log "KEEP_TEMPLATE=1; keeping ${LIMA_TEMPLATE_PATH}"
    return
  fi

  log "removing Lima template ${LIMA_TEMPLATE_PATH}"
  rm -f "${LIMA_TEMPLATE_PATH}"
}

remove_lima_image() {
  if [[ "${KEEP_LIMA_IMAGE}" == "1" ]]; then
    log "KEEP_LIMA_IMAGE=1; keeping ${LIMA_IMAGE_PATH}"
    return
  fi

  log "removing Lima base image ${LIMA_IMAGE_PATH}"
  rm -f "${LIMA_IMAGE_PATH}" "${LIMA_IMAGE_PATH}.tmp"
}

remove_assets() {
  if [[ "${KEEP_ASSETS}" == "1" ]]; then
    log "KEEP_ASSETS=1; keeping ${ASSET_DIR}"
    return
  fi

  log "removing runtime asset cache ${ASSET_DIR}"
  rm -rf "${ASSET_DIR}"
}

remove_macos_entrypoints() {
  log "removing macOS DNS/proxy configuration"

  if [[ -f "${MAC_RESOLVER_DIR}/${DOMAIN}" ]]; then
    sudo rm -f "${MAC_RESOLVER_DIR}/${DOMAIN}"
    sudo rmdir "${MAC_RESOLVER_DIR}" >/dev/null 2>&1 || true
  fi

  if [[ -n "${MAC_DNSMASQ_CONF}" ]]; then
    rm -f "${MAC_DNSMASQ_CONF}"
    rmdir "$(dirname "${MAC_DNSMASQ_CONF}")" >/dev/null 2>&1 || true
  fi
  if [[ -n "${MAC_DNSMASQ_MAIN_CONF}" ]] && [[ -f "${MAC_DNSMASQ_MAIN_CONF}" ]]; then
    sed -i.bak '/# Managed by NovitaBox\./,+1d' "${MAC_DNSMASQ_MAIN_CONF}" || true
    rm -f "${MAC_DNSMASQ_MAIN_CONF}.bak"
  fi
  if [[ -n "${BREW_BIN}" ]] && "${BREW_BIN}" list --formula dnsmasq >/dev/null 2>&1; then
    sudo "${BREW_BIN}" services restart dnsmasq >/dev/null 2>&1 || true
  fi

  if [[ -n "${MAC_CADDYFILE}" ]] && [[ -f "${MAC_CADDYFILE}" ]]; then
    if grep -q "Managed by NovitaBox" "${MAC_CADDYFILE}" || grep -q "reverse_proxy 127.0.0.1:8080" "${MAC_CADDYFILE}"; then
      rm -f "${MAC_CADDYFILE}"
    else
      warn "leaving ${MAC_CADDYFILE} in place because it does not look installer-owned"
    fi
  fi
  if [[ -n "${BREW_BIN}" ]] && "${BREW_BIN}" list --formula caddy >/dev/null 2>&1; then
    sudo "${BREW_BIN}" services restart caddy >/dev/null 2>&1 || true
  fi

  rm -f "${MAC_CADDY_CA_PATH}"
  dscacheutil -flushcache >/dev/null 2>&1 || true
}

print_summary() {
  cat <<EOF

NovitaBox Lima environment has been uninstalled.

State:
  KEEP_VM          ${KEEP_VM}
  KEEP_TEMPLATE    ${KEEP_TEMPLATE}
  KEEP_LIMA_IMAGE  ${KEEP_LIMA_IMAGE}
  KEEP_ASSETS      ${KEEP_ASSETS}

EOF
}

main() {
  need_macos
  need_lima
  resolve_paths
  resolve_macos_service_paths
  confirm_destructive
  run_linux_uninstaller_in_vm
  remove_vm
  remove_template
  remove_lima_image
  remove_assets
  remove_macos_entrypoints
  print_summary
}

main "$@"
