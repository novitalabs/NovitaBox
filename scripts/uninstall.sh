#!/usr/bin/env bash
set -Eeuo pipefail

# Remove a Linux NovitaBox installation.
#
# Default usage:
#   sudo scripts/uninstall.sh
#
# Common overrides:
#   ROOT_DIR=/data/novitabox IMAGE_PATH=/data/novitabox.img sudo -E scripts/uninstall.sh
#   KEEP_DATA=1 sudo -E scripts/uninstall.sh

ROOT_DIR="${ROOT_DIR:-/data/novitabox}"
IMAGE_PATH="${IMAGE_PATH:-/data/novitabox.img}"
DOMAIN="${DOMAIN:-novitabox.local}"
KEEP_DATA="${KEEP_DATA:-0}"
KEEP_PACKAGES="${KEEP_PACKAGES:-1}"
FORCE="${FORCE:-0}"

SERVICES=(novitabox-boxapi.service novitabox-boxproxy.service novitabox-boxlet.service)
COMMANDS=(boxapi boxctl boxd boxlet boxproxy boxshim firecracker vmlinux.bin)

log() {
  printf '[novitabox-uninstall] %s\n' "$*"
}

warn() {
  printf '[novitabox-uninstall] warning: %s\n' "$*" >&2
}

die() {
  printf '[novitabox-uninstall] error: %s\n' "$*" >&2
  exit 1
}

need_root() {
  if [[ "${EUID}" -ne 0 ]]; then
    die "run as root, for example: sudo -E scripts/uninstall.sh"
  fi
}

confirm_destructive() {
  if [[ "${FORCE}" == "1" ]]; then
    return
  fi

  cat <<EOF
This will uninstall NovitaBox from this host.

It will remove:
  services:      ${SERVICES[*]}
  root:          ${ROOT_DIR}
  image:         ${IMAGE_PATH}
  sysctl:        /etc/sysctl.d/99-novitabox.conf
  dnsmasq:       /etc/dnsmasq.d/novitabox.conf
  resolved:      /etc/systemd/resolved.conf.d/novitabox.conf
  Caddy CA:      /usr/local/share/ca-certificates/caddy-local.crt

Set KEEP_DATA=1 to keep ${ROOT_DIR} and ${IMAGE_PATH}.
Set FORCE=1 to skip this prompt.

EOF

  read -r -p "Continue? [y/N] " answer
  case "${answer}" in
    y | Y | yes | YES) ;;
    *) die "aborted" ;;
  esac
}

command_exists() {
  command -v "$1" >/dev/null 2>&1
}

stop_services() {
  log "stopping NovitaBox services"
  if ! command_exists systemctl; then
    warn "systemctl not found; skipping service cleanup"
    return
  fi

  systemctl stop "${SERVICES[@]}" >/dev/null 2>&1 || true
  systemctl disable "${SERVICES[@]}" >/dev/null 2>&1 || true

  local service
  for service in "${SERVICES[@]}"; do
    rm -f "/etc/systemd/system/${service}"
  done

  systemctl daemon-reload || true
  systemctl reset-failed "${SERVICES[@]}" >/dev/null 2>&1 || true
}

kill_leftover_processes() {
  log "stopping leftover NovitaBox processes"
  pkill -TERM -f "${ROOT_DIR}/boxapi" >/dev/null 2>&1 || true
  pkill -TERM -f "${ROOT_DIR}/boxproxy" >/dev/null 2>&1 || true
  pkill -TERM -f "${ROOT_DIR}/boxlet" >/dev/null 2>&1 || true
  pkill -TERM -f "${ROOT_DIR}/boxshim" >/dev/null 2>&1 || true
  pkill -TERM -f "${ROOT_DIR}/firecracker" >/dev/null 2>&1 || true
  sleep 1
  pkill -KILL -f "${ROOT_DIR}/boxapi" >/dev/null 2>&1 || true
  pkill -KILL -f "${ROOT_DIR}/boxproxy" >/dev/null 2>&1 || true
  pkill -KILL -f "${ROOT_DIR}/boxlet" >/dev/null 2>&1 || true
  pkill -KILL -f "${ROOT_DIR}/boxshim" >/dev/null 2>&1 || true
  pkill -KILL -f "${ROOT_DIR}/firecracker" >/dev/null 2>&1 || true
}

cleanup_network() {
  if ! command_exists ip; then
    warn "ip command not found; skipping network namespace cleanup"
    return
  fi

  log "removing NovitaBox network namespaces and veth links"
  local ns
  while read -r ns; do
    [[ -n "${ns}" ]] || continue
    ip netns pids "${ns}" 2>/dev/null | xargs -r kill -TERM >/dev/null 2>&1 || true
    sleep 0.1
    ip netns pids "${ns}" 2>/dev/null | xargs -r kill -KILL >/dev/null 2>&1 || true
    ip netns del "${ns}" >/dev/null 2>&1 || true
  done < <(ip netns list 2>/dev/null | awk '{print $1}' | grep '^nb-' || true)

  local link
  while read -r link; do
    [[ -n "${link}" ]] || continue
    ip link del "${link}" >/dev/null 2>&1 || true
  done < <(ip -o link show 2>/dev/null | awk -F': ' '{print $2}' | cut -d@ -f1 | grep '^vh-' || true)
}

cleanup_sysctl() {
  log "removing sysctl configuration"
  rm -f /etc/sysctl.d/99-novitabox.conf
  if command_exists sysctl; then
    sysctl --system >/dev/null 2>&1 || true
  fi
}

cleanup_dnsmasq() {
  log "removing dnsmasq configuration"
  rm -f /etc/dnsmasq.d/novitabox.conf
  if command_exists systemctl && systemctl list-unit-files dnsmasq.service >/dev/null 2>&1; then
    systemctl restart dnsmasq >/dev/null 2>&1 || true
  fi
}

cleanup_resolved() {
  log "removing systemd-resolved configuration"
  rm -f /etc/systemd/resolved.conf.d/novitabox.conf
  rmdir /etc/systemd/resolved.conf.d >/dev/null 2>&1 || true
  if command_exists systemctl && systemctl list-unit-files systemd-resolved.service >/dev/null 2>&1; then
    systemctl restart systemd-resolved >/dev/null 2>&1 || true
  fi
}

cleanup_caddy() {
  log "removing Caddy configuration written by installer"
  if [[ -f /etc/caddy/Caddyfile ]] && grep -q "reverse_proxy .*8080" /etc/caddy/Caddyfile && grep -q "reverse_proxy .*8082" /etc/caddy/Caddyfile; then
    rm -f /etc/caddy/Caddyfile
  else
    warn "leaving /etc/caddy/Caddyfile in place because it does not look installer-owned"
  fi

  rm -f /usr/local/share/ca-certificates/caddy-local.crt
  if command_exists update-ca-certificates; then
    update-ca-certificates >/dev/null 2>&1 || true
  fi
  if command_exists systemctl && systemctl list-unit-files caddy.service >/dev/null 2>&1; then
    systemctl restart caddy >/dev/null 2>&1 || true
  fi
}

remove_fstab_entry() {
  if [[ ! -f /etc/fstab ]]; then
    return
  fi

  log "removing fstab entry"
  local tmp
  tmp="$(mktemp)"
  awk -v image="${IMAGE_PATH}" -v root="${ROOT_DIR}" '
    !($1 == image && $2 == root && $3 == "btrfs") { print }
  ' /etc/fstab >"${tmp}"
  cat "${tmp}" >/etc/fstab
  rm -f "${tmp}"
}

unmount_root() {
  if mountpoint -q "${ROOT_DIR}"; then
    log "unmounting ${ROOT_DIR}"
    umount "${ROOT_DIR}" || {
      warn "normal unmount failed; trying lazy unmount"
      umount -l "${ROOT_DIR}" || warn "failed to unmount ${ROOT_DIR}"
    }
  fi
}

remove_data() {
  if [[ "${KEEP_DATA}" == "1" ]]; then
    log "KEEP_DATA=1; keeping ${ROOT_DIR} and ${IMAGE_PATH}"
    return
  fi

  remove_fstab_entry
  unmount_root

  log "removing NovitaBox data"
  rm -rf "${ROOT_DIR}"
  rm -f "${IMAGE_PATH}"

  local image_dir
  image_dir="$(dirname "${IMAGE_PATH}")"
  if [[ "${image_dir}" != "/" && "${image_dir}" != "." ]]; then
    rmdir "${image_dir}" >/dev/null 2>&1 || true
  fi
}

remove_packages_hint() {
  if [[ "${KEEP_PACKAGES}" == "1" ]]; then
    return
  fi

  log "KEEP_PACKAGES=0 requested; package removal is intentionally conservative"
  if command_exists apt-get; then
    apt-get remove -y dnsmasq caddy btrfs-progs >/dev/null 2>&1 || true
  elif command_exists dnf; then
    dnf remove -y dnsmasq caddy btrfs-progs >/dev/null 2>&1 || true
  elif command_exists yum; then
    yum remove -y dnsmasq caddy btrfs-progs >/dev/null 2>&1 || true
  fi
}

print_summary() {
  cat <<EOF

NovitaBox has been uninstalled.

Removed:
  services and systemd units
  network namespaces named nb-*
  veth links named vh-*
  sysctl/dnsmasq/resolved/Caddy installer config

Data:
  KEEP_DATA=${KEEP_DATA}
  root:  ${ROOT_DIR}
  image: ${IMAGE_PATH}

EOF
}

main() {
  need_root
  confirm_destructive
  stop_services
  kill_leftover_processes
  cleanup_network
  cleanup_sysctl
  cleanup_dnsmasq
  cleanup_resolved
  cleanup_caddy
  remove_data
  remove_packages_hint
  print_summary
}

main "$@"
