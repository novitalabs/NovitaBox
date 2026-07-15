#!/usr/bin/env bash
set -Eeuo pipefail

# One-command NovitaBox installer for a single Linux host.
#
# Default usage from the repository root:
#   sudo scripts/install-linux.sh
#
# Common overrides:
#   ROOT_DIR=/data/novitabox IMAGE_SIZE=100G DOMAIN=novitabox.localhost sudo -E scripts/install-linux.sh
#   FIRECRACKER_URL=https://.../firecracker-amd64 KERNEL_URL=https://.../vmlinux.bin-amd64 sudo -E scripts/install-linux.sh
#   FIRECRACKER_PATH=/path/to/firecracker KERNEL_PATH=/path/to/vmlinux.bin sudo -E scripts/install-linux.sh
#   CONFIGURE_DOCKER_MIRRORS=1 DOCKER_REGISTRY_MIRRORS=https://docker.m.daocloud.io,https://docker.1ms.run sudo -E scripts/install-linux.sh
#   SKIP_BUILD=1 SOURCE_DIR=/path/to/prebuilt-layout sudo -E scripts/install-linux.sh

ROOT_DIR="${ROOT_DIR:-/data/novitabox}"
IMAGE_PATH="${IMAGE_PATH:-/data/novitabox.img}"
IMAGE_SIZE="${IMAGE_SIZE:-50G}"
DOMAIN="${DOMAIN:-novitabox.localhost}"
RELEASE_VERSION="${RELEASE_VERSION:-v0.0.1}"
SOURCE_DIR="${SOURCE_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
INSTALL_GO="${INSTALL_GO:-auto}"
GO_VERSION="${GO_VERSION:-1.26.4}"
GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"
CONFIGURE_DOCKER_MIRRORS="${CONFIGURE_DOCKER_MIRRORS:-0}"
DOCKER_REGISTRY_MIRRORS="${DOCKER_REGISTRY_MIRRORS:-https://docker.m.daocloud.io,https://docker.1ms.run}"
SKIP_BUILD="${SKIP_BUILD:-0}"
ENABLE_DNS="${ENABLE_DNS:-1}"
ENABLE_CADDY="${ENABLE_CADDY:-1}"
REQUIRE_KVM="${REQUIRE_KVM:-1}"
BOXAPI_ADDR="${BOXAPI_ADDR:-127.0.0.1:8080}"
BOXLET_ADDR="${BOXLET_ADDR:-127.0.0.1:8081}"
BOXPROXY_ADDR="${BOXPROXY_ADDR:-127.0.0.1:8082}"

FIRECRACKER_URL="${FIRECRACKER_URL:-}"
KERNEL_URL="${KERNEL_URL:-}"
JAILER_URL="${JAILER_URL:-}"
FIRECRACKER_PATH="${FIRECRACKER_PATH:-}"
KERNEL_PATH="${KERNEL_PATH:-}"
JAILER_PATH="${JAILER_PATH:-}"
CURL_PROXY="${CURL_PROXY:-${https_proxy:-${HTTPS_PROXY:-${http_proxy:-${HTTP_PROXY:-}}}}}"

COMMANDS=(boxapi boxctl boxd boxlet boxproxy boxshim)

log() {
  printf '[novitabox] %s\n' "$*"
}

warn() {
  printf '[novitabox] warning: %s\n' "$*" >&2
}

die() {
  printf '[novitabox] error: %s\n' "$*" >&2
  exit 1
}

need_root() {
  if [[ "${EUID}" -ne 0 ]]; then
    die "run as root, for example: sudo -E scripts/install-linux.sh"
  fi
}

need_no_spaces() {
  local path
  for path in "$ROOT_DIR" "$IMAGE_PATH" "$SOURCE_DIR"; do
    case "$path" in
      *" "*) die "ROOT_DIR, IMAGE_PATH, and SOURCE_DIR must not contain spaces: $path" ;;
    esac
  done
}

command_exists() {
  command -v "$1" >/dev/null 2>&1
}

curl_retry_all_errors_args=()
if curl --help all 2>/dev/null | grep -q -- '--retry-all-errors'; then
  curl_retry_all_errors_args=(--retry-all-errors)
fi
curl_proxy_args=()
if [[ -n "${CURL_PROXY}" ]]; then
  curl_proxy_args=(--proxy "${CURL_PROXY}")
fi

download_to_tmp() {
  local url="$1"
  local tmp="$2"

  rm -f "${tmp}"
  if ! curl -fL --http1.1 --retry 5 "${curl_retry_all_errors_args[@]}" "${curl_proxy_args[@]}" --retry-delay 2 -o "${tmp}" "${url}"; then
    rm -f "${tmp}"
    return 1
  fi
}

trim_space() {
  local value="$1"
  value="${value#"${value%%[![:space:]]*}"}"
  value="${value%"${value##*[![:space:]]}"}"
  printf '%s' "${value}"
}

docker_registry_mirrors_json_lines() {
  local input="$1"
  local mirror escaped i suffix
  local mirrors=()

  if [[ -z "${input}" || "${input}" == "none" || "${input}" == "0" ]]; then
    return
  fi

  IFS=',' read -r -a raw_mirrors <<<"${input}"
  for mirror in "${raw_mirrors[@]}"; do
    mirror="$(trim_space "${mirror}")"
    if [[ -n "${mirror}" ]]; then
      mirrors+=("${mirror}")
    fi
  done

  for ((i = 0; i < ${#mirrors[@]}; i++)); do
    escaped="${mirrors[$i]}"
    escaped="${escaped//\\/\\\\}"
    escaped="${escaped//\"/\\\"}"
    suffix=","
    if (( i == ${#mirrors[@]} - 1 )); then
      suffix=""
    fi
    printf '    "%s"%s\n' "${escaped}" "${suffix}"
  done
}

detect_arch() {
  case "$(uname -m)" in
    x86_64 | amd64) echo "amd64" ;;
    aarch64 | arm64) echo "arm64" ;;
    *) die "unsupported architecture: $(uname -m)" ;;
  esac
}

default_firecracker_url() {
  local arch="$1"
  echo "https://github.com/novitalabs/NovitaBox/releases/download/${RELEASE_VERSION}/firecracker-${arch}"
}

default_kernel_url() {
	local arch="$1"
	echo "https://github.com/novitalabs/NovitaBox/releases/download/${RELEASE_VERSION}/vmlinux.bin-${arch}"
}

default_jailer_url() {
	local arch="$1"
	echo "https://github.com/novitalabs/NovitaBox/releases/download/${RELEASE_VERSION}/jailer-${arch}"
}

install_packages() {
 log "installing system packages"
  local packages=(
    bash ca-certificates curl git make gcc
    btrfs-progs coreutils findutils iptables kmod util-linux
  )
  if command_exists apt-get; then
    local apt_packages=("${packages[@]}" libc6-dev iproute2 procps)
    if [[ "$ENABLE_DNS" == "1" ]]; then
      apt_packages+=(dnsmasq)
    fi
    if [[ "$ENABLE_CADDY" == "1" ]]; then
      apt_packages+=(caddy)
    fi
    export DEBIAN_FRONTEND=noninteractive
    apt-get update
    apt-get install -y "${apt_packages[@]}"
    return
  fi

  if command_exists dnf; then
    local dnf_packages=("${packages[@]}" glibc-devel iproute procps-ng)
    if [[ "$ENABLE_DNS" == "1" ]]; then
      dnf_packages+=(dnsmasq)
    fi
    if [[ "$ENABLE_CADDY" == "1" ]]; then
      dnf_packages+=(caddy)
    fi
    dnf install -y "${dnf_packages[@]}"
    return
  fi

  if command_exists yum; then
    local yum_packages=("${packages[@]}" glibc-devel iproute procps-ng)
    if [[ "$ENABLE_DNS" == "1" ]]; then
      yum_packages+=(dnsmasq)
    fi
    if [[ "$ENABLE_CADDY" == "1" ]]; then
      yum_packages+=(caddy)
    fi
    yum install -y "${yum_packages[@]}"
    return
  fi

  die "unsupported package manager; install dependencies manually and rerun"
}

configure_docker() {
  if [[ "${CONFIGURE_DOCKER_MIRRORS}" != "1" ]]; then
    log "skipping Docker registry mirror configuration; set CONFIGURE_DOCKER_MIRRORS=1 to enable it"
    return
  fi
  if [[ -z "${DOCKER_REGISTRY_MIRRORS}" || "${DOCKER_REGISTRY_MIRRORS}" == "none" || "${DOCKER_REGISTRY_MIRRORS}" == "0" ]]; then
    log "skipping Docker registry mirror configuration"
    return
  fi
  if ! command_exists docker && ! systemctl list-unit-files docker.service >/dev/null 2>&1; then
    log "Docker is not installed; skipping Docker registry mirror configuration"
    return
  fi
  if [[ -f /etc/docker/daemon.json ]]; then
    log "Docker daemon.json already exists; leaving existing Docker configuration unchanged"
    return
  fi

  log "configuring Docker registry mirrors: ${DOCKER_REGISTRY_MIRRORS}"
  mkdir -p /etc/docker
  {
    printf '{\n'
    printf '  "registry-mirrors": [\n'
    docker_registry_mirrors_json_lines "${DOCKER_REGISTRY_MIRRORS}"
    printf '  ]\n'
    printf '}\n'
  } >/etc/docker/daemon.json

  if command_exists systemctl && systemctl list-unit-files docker.service >/dev/null 2>&1; then
    systemctl daemon-reload
    systemctl enable docker >/dev/null 2>&1 || true
    systemctl restart docker
    systemctl is-active --quiet docker || die "docker did not become active after restart"
  else
    warn "systemctl docker.service not found; restart Docker manually to apply /etc/docker/daemon.json"
  fi
}

version_ge() {
  local have="$1"
  local want="$2"
  [[ "$(printf '%s\n%s\n' "$want" "$have" | sort -V | head -n1)" == "$want" ]]
}

current_go_version() {
  if ! command_exists go; then
    return 1
  fi
  go env GOVERSION 2>/dev/null | sed 's/^go//'
}

ensure_go() {
  local arch="$1"
  local have=""
  have="$(current_go_version || true)"

  if [[ -n "$have" ]] && version_ge "$have" "1.26.0" && [[ "$INSTALL_GO" != "1" ]]; then
    log "using existing Go $have"
    return
  fi

  if [[ "$INSTALL_GO" == "0" ]]; then
    die "Go >= 1.26.0 is required, found ${have:-none}"
  fi

  log "installing Go ${GO_VERSION} for linux-${arch}"
  local tarball="/tmp/go${GO_VERSION}.linux-${arch}.tar.gz"
  curl -fL "https://go.dev/dl/go${GO_VERSION}.linux-${arch}.tar.gz" -o "$tarball"
  rm -rf "/usr/local/go${GO_VERSION}"
  mkdir -p "/usr/local/go${GO_VERSION}"
  tar -C "/usr/local/go${GO_VERSION}" --strip-components=1 -xzf "$tarball"
  ln -sf "/usr/local/go${GO_VERSION}/bin/go" /usr/local/bin/go
  ln -sf "/usr/local/go${GO_VERSION}/bin/gofmt" /usr/local/bin/gofmt
}

prepare_btrfs_root() {
  log "preparing btrfs root at ${ROOT_DIR}"
  mkdir -p "$(dirname "$IMAGE_PATH")" "$ROOT_DIR"

  if mountpoint -q "$ROOT_DIR"; then
    local fstype
    fstype="$(findmnt -rn --mountpoint "$ROOT_DIR" -o FSTYPE)"
    if [[ "$fstype" != "btrfs" ]]; then
      die "$ROOT_DIR is mounted as $fstype, but NovitaBox requires btrfs for this installer"
    fi
    log "$ROOT_DIR is already mounted as btrfs"
    return
  fi

  if [[ -n "$(find "$ROOT_DIR" -mindepth 1 -maxdepth 1 -print -quit 2>/dev/null)" ]] && [[ "${ALLOW_MOUNT_OVER_NONEMPTY:-0}" != "1" ]]; then
    die "$ROOT_DIR is not empty and is not a mount point; set ALLOW_MOUNT_OVER_NONEMPTY=1 to mount over it"
  fi

  if [[ ! -f "$IMAGE_PATH" ]]; then
    log "creating btrfs image ${IMAGE_PATH} size ${IMAGE_SIZE}"
    truncate -s "$IMAGE_SIZE" "$IMAGE_PATH"
    mkfs.btrfs -f "$IMAGE_PATH"
  else
    local existing_type=""
    existing_type="$(blkid -o value -s TYPE "$IMAGE_PATH" 2>/dev/null || true)"
    if [[ -z "$existing_type" ]]; then
      log "formatting existing image ${IMAGE_PATH} as btrfs"
      mkfs.btrfs -f "$IMAGE_PATH"
    elif [[ "$existing_type" != "btrfs" ]]; then
      die "$IMAGE_PATH has filesystem $existing_type, expected btrfs"
    fi
  fi

  mount -o loop,compress=zstd,noatime "$IMAGE_PATH" "$ROOT_DIR"

  local fstab_line="${IMAGE_PATH} ${ROOT_DIR} btrfs loop,compress=zstd,noatime 0 0"
  if ! grep -Fqs "$IMAGE_PATH $ROOT_DIR btrfs" /etc/fstab; then
    printf '%s\n' "$fstab_line" >>/etc/fstab
  fi
}

verify_reflink() {
  log "verifying reflink support"
  local tmpdir
  tmpdir="$(mktemp -d "${ROOT_DIR}/.reflink-test.XXXXXX")"
  printf test >"${tmpdir}/a"
  cp --reflink=always "${tmpdir}/a" "${tmpdir}/b"
  rm -rf "$tmpdir"
}

prepare_kernel_modules() {
  log "checking KVM and TUN"
  modprobe tun >/dev/null 2>&1 || true
  if [[ ! -c /dev/net/tun ]]; then
    die "/dev/net/tun is missing"
  fi

  modprobe kvm >/dev/null 2>&1 || true
  case "$(uname -m)" in
    x86_64 | amd64)
      if grep -Eq 'vmx' /proc/cpuinfo 2>/dev/null; then
        modprobe kvm_intel >/dev/null 2>&1 || true
      elif grep -Eq 'svm' /proc/cpuinfo 2>/dev/null; then
        modprobe kvm_amd >/dev/null 2>&1 || true
      fi
      ;;
  esac

  if [[ ! -e /dev/kvm ]]; then
    if [[ "$REQUIRE_KVM" == "1" ]]; then
      die "/dev/kvm is missing; enable virtualization/KVM or rerun with REQUIRE_KVM=0"
    fi
    warn "/dev/kvm is missing; Firecracker sandboxes will not start"
  fi
}

configure_sysctl() {
  log "configuring host networking sysctl"
  cat >/etc/sysctl.d/99-novitabox.conf <<'EOF'
net.ipv4.ip_forward=1
EOF
  sysctl --system >/dev/null
}

build_components() {
  local arch="$1"
  if [[ "$SKIP_BUILD" == "1" ]]; then
    log "skipping build; using prebuilt components from ${SOURCE_DIR}/bin/linux-${arch}"
    for cmd in "${COMMANDS[@]}"; do
      if [[ ! -x "${SOURCE_DIR}/bin/linux-${arch}/${cmd}" ]]; then
        die "missing prebuilt component: ${SOURCE_DIR}/bin/linux-${arch}/${cmd}"
      fi
    done
    return
  fi

  log "building NovitaBox linux-${arch} components"
  GOPROXY="$GOPROXY" make -C "$SOURCE_DIR" "build-linux-${arch}"
}

install_components() {
  local arch="$1"
  log "installing components to ${ROOT_DIR}"
  mkdir -p "$ROOT_DIR/db" "$ROOT_DIR/templates" "$ROOT_DIR/images" "$ROOT_DIR/sandboxes" "$ROOT_DIR/logs"
  for cmd in "${COMMANDS[@]}"; do
    install -m 0755 "${SOURCE_DIR}/bin/linux-${arch}/${cmd}" "${ROOT_DIR}/${cmd}"
  done
  rm -f "${ROOT_DIR}/uninstall.sh"
}

install_asset() {
  local name="$1"
  local src_path="$2"
  local url="$3"
  local dest="$4"
  local mode="$5"

  if [[ -n "$src_path" ]]; then
    log "installing ${name} from ${src_path}"
    install -m "$mode" "$src_path" "$dest"
    return
  fi

  if [[ -s "$dest" ]]; then
    log "${name} already exists at ${dest}"
    chmod "$mode" "$dest"
    return
  fi

  log "downloading ${name} from ${url}"
  if ! download_to_tmp "$url" "${dest}.tmp"; then
    die "download ${name} failed"
  fi
  chmod "$mode" "${dest}.tmp"
  mv -f "${dest}.tmp" "$dest"
}

install_runtime_assets() {
	local arch="$1"
	local firecracker_url="${FIRECRACKER_URL:-$(default_firecracker_url "$arch")}"
	local kernel_url="${KERNEL_URL:-$(default_kernel_url "$arch")}"
	local jailer_url="${JAILER_URL:-$(default_jailer_url "$arch")}"

	install_asset "firecracker" "$FIRECRACKER_PATH" "$firecracker_url" "${ROOT_DIR}/firecracker" 0755
	install_asset "kernel" "$KERNEL_PATH" "$kernel_url" "${ROOT_DIR}/vmlinux.bin" 0644
	install_asset "jailer" "$JAILER_PATH" "$jailer_url" "${ROOT_DIR}/jailer" 0755
}

write_systemd_units() {
	log "writing systemd services"
	local boxlet_args="--root ${ROOT_DIR} --addr ${BOXLET_ADDR}"
	cat >/etc/systemd/system/novitabox-boxlet.service <<EOF
[Unit]
Description=NovitaBox node agent
After=network-online.target
Wants=network-online.target
RequiresMountsFor=${ROOT_DIR}

[Service]
Type=simple
WorkingDirectory=${ROOT_DIR}
ExecStart=${ROOT_DIR}/boxlet ${boxlet_args}
Restart=always
RestartSec=2
KillMode=process
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
EOF

  cat >/etc/systemd/system/novitabox-boxapi.service <<EOF
[Unit]
Description=NovitaBox HTTP API
After=network-online.target novitabox-boxlet.service
Wants=network-online.target
Requires=novitabox-boxlet.service
RequiresMountsFor=${ROOT_DIR}

[Service]
Type=simple
WorkingDirectory=${ROOT_DIR}
ExecStart=${ROOT_DIR}/boxapi --root ${ROOT_DIR} --addr ${BOXAPI_ADDR} --boxlet-addr ${BOXLET_ADDR}
Restart=always
RestartSec=2
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
EOF

  cat >/etc/systemd/system/novitabox-boxproxy.service <<EOF
[Unit]
Description=NovitaBox sandbox proxy
After=network-online.target novitabox-boxlet.service
Wants=network-online.target
RequiresMountsFor=${ROOT_DIR}

[Service]
Type=simple
WorkingDirectory=${ROOT_DIR}
ExecStart=${ROOT_DIR}/boxproxy --root ${ROOT_DIR} --addr ${BOXPROXY_ADDR}
Restart=always
RestartSec=2
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
EOF

  systemctl daemon-reload
  systemctl enable --now novitabox-boxlet.service novitabox-boxapi.service novitabox-boxproxy.service
}

configure_dnsmasq() {
  if [[ "$ENABLE_DNS" != "1" ]]; then
    log "skipping dnsmasq configuration"
    return
  fi

  log "configuring dnsmasq for *.${DOMAIN}"
  cat >/etc/dnsmasq.d/novitabox.conf <<EOF
listen-address=127.0.0.1
bind-interfaces
no-resolv
server=1.1.1.1
server=8.8.8.8
address=/${DOMAIN}/127.0.0.1
address=/.${DOMAIN}/127.0.0.1
EOF
  systemctl enable --now dnsmasq
  systemctl restart dnsmasq

  if systemctl list-unit-files systemd-resolved.service >/dev/null 2>&1; then
    mkdir -p /etc/systemd/resolved.conf.d
    cat >/etc/systemd/resolved.conf.d/novitabox.conf <<EOF
[Resolve]
DNS=127.0.0.1
Domains=~${DOMAIN}
EOF
    systemctl restart systemd-resolved || warn "failed to restart systemd-resolved"
  fi
}

configure_caddy() {
  if [[ "$ENABLE_CADDY" != "1" ]]; then
    log "skipping Caddy reverse proxy"
    return
  fi
  if ! command_exists caddy; then
    die "caddy is not installed; install it manually or rerun with ENABLE_CADDY=0"
  fi

  log "configuring Caddy reverse proxy for ${DOMAIN}"
  cat >/etc/caddy/Caddyfile <<EOF
{
	admin off
}

${DOMAIN} {
	tls internal
	reverse_proxy ${BOXAPI_ADDR}
}

*.${DOMAIN} {
	tls internal
	reverse_proxy ${BOXPROXY_ADDR}
}
EOF

  systemctl enable --now caddy
  systemctl restart caddy

  curl -kfsS --resolve "${DOMAIN}:443:127.0.0.1" "https://${DOMAIN}/health" >/dev/null || true

  local root_ca=""
  root_ca="$(find /var/lib/caddy -path '*/pki/authorities/local/root.crt' -print -quit 2>/dev/null || true)"
  if [[ -n "$root_ca" ]]; then
    install -m 0644 "$root_ca" /usr/local/share/ca-certificates/caddy-local.crt
    update-ca-certificates >/dev/null || true
    log "installed Caddy local CA at /usr/local/share/ca-certificates/caddy-local.crt"
  else
    warn "Caddy local CA was not found; clients may need to trust Caddy's internal CA manually"
  fi
}

wait_http() {
  local url="$1"
  local name="$2"
  local deadline=$((SECONDS + 30))
  until curl -fsS "$url" >/dev/null 2>&1; do
    if (( SECONDS >= deadline )); then
      systemctl --no-pager --full status "novitabox-${name}.service" || true
      die "${name} did not become healthy at ${url}"
    fi
    sleep 1
  done
}

health_check() {
  log "checking services"
  wait_http "http://${BOXAPI_ADDR}/health" "boxapi"
  wait_http "http://${BOXPROXY_ADDR}/healthz" "boxproxy"
  systemctl is-active --quiet novitabox-boxlet.service || die "boxlet is not active"

  if [[ "$ENABLE_CADDY" == "1" ]]; then
    curl -kfsS --resolve "${DOMAIN}:443:127.0.0.1" "https://${DOMAIN}/health" >/dev/null || die "Caddy HTTPS health check failed"
  fi
}

print_summary() {
  cat <<EOF

NovitaBox is installed.

Root directory:
  ${ROOT_DIR}

Local endpoints:
  boxapi:   http://${BOXAPI_ADDR}
  boxproxy: http://${BOXPROXY_ADDR}
  public:   https://${DOMAIN}

Useful commands:
  systemctl status novitabox-boxlet novitabox-boxapi novitabox-boxproxy
  journalctl -u novitabox-boxlet -f
  ${ROOT_DIR}/boxctl --api http://${BOXAPI_ADDR} sandbox ls

SDK/CLI environment:
  export NOVITA_DOMAIN=${DOMAIN}
  export NOVITA_API_KEY=dummy
  export NOVITA_ACCESS_TOKEN=dummy
  export NODE_EXTRA_CA_CERTS=/usr/local/share/ca-certificates/caddy-local.crt
  export NO_PROXY=.${DOMAIN},localhost,127.0.0.1,::1

EOF
}

main() {
  need_root
  need_no_spaces

  local arch
  arch="$(detect_arch)"

  install_packages
  configure_docker
  if [[ "$SKIP_BUILD" != "1" ]]; then
    ensure_go "$arch"
  fi
  prepare_btrfs_root
  verify_reflink
  prepare_kernel_modules
  configure_sysctl
  build_components "$arch"
  install_components "$arch"
  install_runtime_assets "$arch"
  write_systemd_units
  configure_dnsmasq
  configure_caddy
  health_check
  print_summary
}

main "$@"
