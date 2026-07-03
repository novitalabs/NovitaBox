#!/usr/bin/env bash
set -Eeuo pipefail

# macOS Lima installer for NovitaBox.
#
# This script prepares a Lima Linux VM on macOS, builds Linux binaries
# locally, uploads only the binaries and installer into the VM, then runs the
# Linux installer inside the VM.
#
# Usage from repository root:
#   scripts/install-macos-lima.sh
#
# Common overrides:
#   LIMA_CPUS=4 LIMA_MEMORY=8GiB LIMA_DISK=80GiB scripts/install-macos-lima.sh
#   LIMA_IMAGE_URL=https://cloud-images.ubuntu.com/releases/noble/release/ubuntu-24.04-server-cloudimg-arm64.img scripts/install-macos-lima.sh
#   LIMA_IMAGE_PATH=$HOME/.lima/_templates/ubuntu-24.04-server-cloudimg-arm64.img scripts/install-macos-lima.sh
#   ENABLE_MAC_DNS=1 ENABLE_MAC_PROXY=1 scripts/install-macos-lima.sh
#   ENABLE_VM_DNS=1 ENABLE_VM_CADDY=1 scripts/install-macos-lima.sh
#   CONFIGURE_DOCKER_MIRRORS=1 DOCKER_REGISTRY_MIRRORS=https://docker.m.daocloud.io,https://docker.1ms.run scripts/install-macos-lima.sh
#   FIRECRACKER_URL=https://.../firecracker-arm64 KERNEL_URL=https://.../vmlinux.bin-arm64 scripts/install-macos-lima.sh

VM_NAME="${VM_NAME:-novitabox}"
LIMA_TEMPLATE_NAME="${LIMA_TEMPLATE_NAME:-ubuntu-24.04-novitabox}"
LIMA_TEMPLATE_DIR="${LIMA_TEMPLATE_DIR:-${HOME}/.lima/_templates}"
LIMA_TEMPLATE_PATH="${LIMA_TEMPLATE_PATH:-${LIMA_TEMPLATE_DIR}/${LIMA_TEMPLATE_NAME}.yaml}"
LIMA_IMAGE_PATH="${LIMA_IMAGE_PATH:-}"
LIMA_IMAGE_URL="${LIMA_IMAGE_URL:-}"
LIMA_IMAGE_DIGEST="${LIMA_IMAGE_DIGEST:-}"
RELEASE_VERSION="${RELEASE_VERSION:-v0.0.1}"
BUILD_ARCH="${BUILD_ARCH:-}"
DARWIN_ARCH="${DARWIN_ARCH:-}"
ASSET_ARCH="${ASSET_ARCH:-}"
FIRECRACKER_URL="${FIRECRACKER_URL:-}"
KERNEL_URL="${KERNEL_URL:-}"
FIRECRACKER_PATH="${FIRECRACKER_PATH:-}"
KERNEL_PATH="${KERNEL_PATH:-}"

LIMA_CPUS="${LIMA_CPUS:-2}"
LIMA_MEMORY="${LIMA_MEMORY:-4GiB}"
LIMA_DISK="${LIMA_DISK:-80GiB}"
LIMA_USER="${LIMA_USER:-novitabox}"

VM_INSTALL_DIR="${VM_INSTALL_DIR:-/home/${LIMA_USER}/novitabox-install}"
ROOT_DIR="${ROOT_DIR:-/data/novitabox}"
IMAGE_PATH="${IMAGE_PATH:-/data/novitabox.img}"
IMAGE_SIZE="${IMAGE_SIZE:-50G}"
DOMAIN="${DOMAIN:-novitabox.localhost}"
ENABLE_MAC_DNS="${ENABLE_MAC_DNS:-${ENABLE_DNS:-0}}"
ENABLE_MAC_PROXY="${ENABLE_MAC_PROXY:-${ENABLE_CADDY:-0}}"
ENABLE_VM_DNS="${ENABLE_VM_DNS:-0}"
ENABLE_VM_CADDY="${ENABLE_VM_CADDY:-0}"
REQUIRE_KVM="${REQUIRE_KVM:-1}"
GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"
CONFIGURE_DOCKER_MIRRORS="${CONFIGURE_DOCKER_MIRRORS:-0}"
DOCKER_REGISTRY_MIRRORS="${DOCKER_REGISTRY_MIRRORS:-https://docker.m.daocloud.io,https://docker.1ms.run}"

SOURCE_DIR="${SOURCE_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
ASSET_DIR="${ASSET_DIR:-${SOURCE_DIR}/assets}"
BREW_BIN="${BREW_BIN:-}"
BREW_PREFIX="${BREW_PREFIX:-}"
MAC_DNSMASQ_MAIN_CONF="${MAC_DNSMASQ_MAIN_CONF:-}"
MAC_DNSMASQ_CONF="${MAC_DNSMASQ_CONF:-}"
MAC_RESOLVER_DIR="${MAC_RESOLVER_DIR:-/etc/resolver}"
MAC_CADDYFILE="${MAC_CADDYFILE:-}"
MAC_CADDY_CA_PATH="${MAC_CADDY_CA_PATH:-${SOURCE_DIR}/assets/caddy-local-root.crt}"

log() {
  printf '[novitabox-macos] %s\n' "$*"
}

warn() {
  printf '[novitabox-macos] warning: %s\n' "$*" >&2
}

die() {
  printf '[novitabox-macos] error: %s\n' "$*" >&2
  exit 1
}

command_exists() {
  command -v "$1" >/dev/null 2>&1
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
    printf "      '    \"%s\"%s' \\\\\n" "${escaped}" "${suffix}"
  done
}

docker_registry_mirrors_provision_block() {
  local json_lines
  if [[ "${CONFIGURE_DOCKER_MIRRORS}" != "1" ]]; then
    return
  fi
  json_lines="$(docker_registry_mirrors_json_lines "$1")"
  if [[ -z "${json_lines}" ]]; then
    return
  fi

  cat <<EOF
    mkdir -p /etc/docker
    printf '%s\n' \\
      '{' \\
      '  "registry-mirrors": [' \\
${json_lines}
      '  ]' \\
      '}' >/etc/docker/daemon.json
EOF
}

sha256_file() {
  local path="$1"
  local output

  if command_exists shasum; then
    output="$(LC_ALL=C LANG=C shasum -a 256 "${path}")"
    echo "${output%% *}"
    return
  fi

  if command_exists sha256sum; then
    output="$(LC_ALL=C LANG=C sha256sum "${path}")"
    echo "${output%% *}"
    return
  fi

  if command_exists openssl; then
    output="$(LC_ALL=C LANG=C openssl dgst -sha256 -r "${path}")"
    echo "${output%% *}"
    return
  fi

  die "cannot calculate sha256: shasum, sha256sum, and openssl are all missing"
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

need_macos() {
  if [[ "$(uname -s)" != "Darwin" ]]; then
    die "this installer must run on macOS"
  fi
}

need_lima() {
  if ! command_exists limactl; then
    die "Lima is not installed. Install it with: brew install lima"
  fi
}

need_brew() {
  if ! command_exists brew; then
    die "Homebrew is required for macOS DNS/proxy setup. Install it first or rerun with ENABLE_MAC_DNS=0 ENABLE_MAC_PROXY=0"
  fi
}

need_build_tools() {
  if ! command_exists go; then
    die "Go is not installed. Install it with: brew install go"
  fi

  if ! command_exists make; then
    die "make is not installed. Install Xcode Command Line Tools with: xcode-select --install"
  fi

  if ! command_exists curl; then
    die "curl is not installed or not in PATH"
  fi

  if ! command_exists tar; then
    die "tar is not installed or not in PATH"
  fi
}

need_source_dir() {
  if [[ ! -f "${SOURCE_DIR}/scripts/install-linux.sh" ]]; then
    die "SOURCE_DIR must point to the NovitaBox repository root: ${SOURCE_DIR}"
  fi
}

resolve_macos_service_paths() {
  if [[ "${ENABLE_MAC_DNS}" != "1" && "${ENABLE_MAC_PROXY}" != "1" ]]; then
    return
  fi
  need_brew

  if [[ -z "${BREW_PREFIX}" ]]; then
    BREW_BIN="$(command -v brew)"
    BREW_PREFIX="$("${BREW_BIN}" --prefix)"
  elif [[ -z "${BREW_BIN}" ]]; then
    BREW_BIN="$(command -v brew)"
  fi
  MAC_DNSMASQ_MAIN_CONF="${MAC_DNSMASQ_MAIN_CONF:-${BREW_PREFIX}/etc/dnsmasq.conf}"
  MAC_DNSMASQ_CONF="${MAC_DNSMASQ_CONF:-${BREW_PREFIX}/etc/dnsmasq.d/novitabox.conf}"
  MAC_CADDYFILE="${MAC_CADDYFILE:-${BREW_PREFIX}/etc/Caddyfile}"
}

normalize_lima_arch() {
  case "$1" in
    aarch64 | arm64) echo "aarch64" ;;
    x86_64 | amd64) echo "x86_64" ;;
    *) die "unsupported Lima architecture: $1" ;;
  esac
}

go_arch_for_lima_arch() {
  case "$1" in
    aarch64) echo "arm64" ;;
    x86_64) echo "amd64" ;;
    *) die "unsupported Lima architecture: $1" ;;
  esac
}

go_arch_for_darwin_host() {
  case "$(uname -m)" in
    arm64 | aarch64) echo "arm64" ;;
    x86_64 | amd64) echo "amd64" ;;
    *) die "unsupported Darwin architecture: $(uname -m)" ;;
  esac
}

ubuntu_cloud_image_url_for_lima_arch() {
  case "$1" in
    aarch64) echo "https://cloud-images.ubuntu.com/releases/noble/release/ubuntu-24.04-server-cloudimg-arm64.img" ;;
    x86_64) echo "https://cloud-images.ubuntu.com/releases/noble/release/ubuntu-24.04-server-cloudimg-amd64.img" ;;
    *) die "unsupported Lima architecture: $1" ;;
  esac
}

ubuntu_cloud_image_name_for_lima_arch() {
  case "$1" in
    aarch64) echo "ubuntu-24.04-server-cloudimg-arm64.img" ;;
    x86_64) echo "ubuntu-24.04-server-cloudimg-amd64.img" ;;
    *) die "unsupported Lima architecture: $1" ;;
  esac
}

resolve_arch_config() {
  LIMA_ARCH="${LIMA_ARCH:-$(detect_host_lima_arch)}"
  LIMA_ARCH="$(normalize_lima_arch "${LIMA_ARCH}")"
  BUILD_ARCH="${BUILD_ARCH:-$(go_arch_for_lima_arch "${LIMA_ARCH}")}"
  DARWIN_ARCH="${DARWIN_ARCH:-$(go_arch_for_darwin_host)}"
  ASSET_ARCH="${ASSET_ARCH:-${BUILD_ARCH}}"
  LIMA_IMAGE_URL="${LIMA_IMAGE_URL:-$(ubuntu_cloud_image_url_for_lima_arch "${LIMA_ARCH}")}"
  LIMA_IMAGE_PATH="${LIMA_IMAGE_PATH:-${LIMA_TEMPLATE_DIR}/$(ubuntu_cloud_image_name_for_lima_arch "${LIMA_ARCH}")}"
  FIRECRACKER_URL="${FIRECRACKER_URL:-https://github.com/novitalabs/NovitaBox/releases/download/${RELEASE_VERSION}/firecracker-${ASSET_ARCH}}"
  KERNEL_URL="${KERNEL_URL:-https://github.com/novitalabs/NovitaBox/releases/download/${RELEASE_VERSION}/vmlinux.bin-${ASSET_ARCH}}"
  FIRECRACKER_PATH="${FIRECRACKER_PATH:-${ASSET_DIR}/firecracker-${ASSET_ARCH}}"
  KERNEL_PATH="${KERNEL_PATH:-${ASSET_DIR}/vmlinux.bin-${ASSET_ARCH}}"
}

size_to_mib() {
  local value="$1"
  local number unit

  if [[ "$value" =~ ^([0-9]+)([KkMmGgTt]i?[Bb]?)?$ ]]; then
    number="${BASH_REMATCH[1]}"
    unit="${BASH_REMATCH[2]}"
  else
    die "invalid size ${value}; use values like 50G, 80GiB, or 51200MiB"
  fi

  case "${unit}" in
    "" | [Mm] | [Mm][Bb] | [Mm]i | [Mm]i[Bb])
      echo "$number"
      ;;
    [Kk] | [Kk][Bb] | [Kk]i | [Kk]i[Bb])
      echo $(((number + 1023) / 1024))
      ;;
    [Gg] | [Gg][Bb] | [Gg]i | [Gg]i[Bb])
      echo $((number * 1024))
      ;;
    [Tt] | [Tt][Bb] | [Tt]i | [Tt]i[Bb])
      echo $((number * 1024 * 1024))
      ;;
    *)
      die "unsupported size unit in ${value}"
      ;;
  esac
}

validate_disk_size() {
  local disk_mib image_mib
  disk_mib="$(size_to_mib "${LIMA_DISK}")"
  image_mib="$(size_to_mib "${IMAGE_SIZE}")"

  if (( disk_mib <= image_mib )); then
    die "LIMA_DISK (${LIMA_DISK}) must be larger than IMAGE_SIZE (${IMAGE_SIZE})"
  fi
}

print_config() {
  cat <<EOF
[novitabox-macos] Lima VM configuration:
  VM_NAME             ${VM_NAME}
  LIMA_TEMPLATE_NAME  ${LIMA_TEMPLATE_NAME}
  LIMA_ARCH           ${LIMA_ARCH}
  BUILD_ARCH          ${BUILD_ARCH}
  DARWIN_ARCH         ${DARWIN_ARCH}
  ASSET_ARCH          ${ASSET_ARCH}
  LIMA_CPUS           ${LIMA_CPUS}
  LIMA_MEMORY         ${LIMA_MEMORY}
  LIMA_DISK           ${LIMA_DISK}
  IMAGE_SIZE          ${IMAGE_SIZE}
  ROOT_DIR            ${ROOT_DIR}
  IMAGE_PATH          ${IMAGE_PATH}
  LIMA_IMAGE_PATH     ${LIMA_IMAGE_PATH}
  LIMA_IMAGE_URL      ${LIMA_IMAGE_URL}
  FIRECRACKER_PATH    ${FIRECRACKER_PATH}
  KERNEL_PATH         ${KERNEL_PATH}
  CONFIG_DOCKER_MIRR  ${CONFIGURE_DOCKER_MIRRORS}
  DOCKER_MIRRORS      ${DOCKER_REGISTRY_MIRRORS}
  ENABLE_MAC_DNS      ${ENABLE_MAC_DNS}
  ENABLE_MAC_PROXY    ${ENABLE_MAC_PROXY}
  MAC_DNSMASQ_MAIN    ${MAC_DNSMASQ_MAIN_CONF:-<disabled>}
  MAC_DNSMASQ_CONF    ${MAC_DNSMASQ_CONF:-<disabled>}
  MAC_RESOLVER_DIR    ${MAC_RESOLVER_DIR}
  MAC_CADDYFILE       ${MAC_CADDYFILE:-<disabled>}
  ENABLE_VM_DNS       ${ENABLE_VM_DNS}
  ENABLE_VM_CADDY     ${ENABLE_VM_CADDY}

Override example:
  LIMA_CPUS=4 LIMA_MEMORY=8GiB LIMA_DISK=100GiB IMAGE_SIZE=80G scripts/install-macos-lima.sh

EOF
}

build_local_binaries() {
  log "building linux-${BUILD_ARCH} components locally"
  make -C "${SOURCE_DIR}" "build-linux-${BUILD_ARCH}"
  log "building darwin-${DARWIN_ARCH} components locally"
  make -C "${SOURCE_DIR}" "build-darwin-${DARWIN_ARCH}"
}

prepare_template_dir() {
  log "preparing Lima template directory ${LIMA_TEMPLATE_DIR}"
  mkdir -p "${LIMA_TEMPLATE_DIR}"
}

download_asset_if_needed() {
  local name="$1"
  local path="$2"
  local url="$3"
  local mode="$4"

  if [[ -s "${path}" ]]; then
    log "using existing ${name} ${path}"
    chmod "${mode}" "${path}"
    return
  fi
  if [[ -z "${url}" ]]; then
    die "${name} is missing at ${path} and no download URL was provided"
  fi

  log "downloading ${name} from ${url}"
  mkdir -p "$(dirname "${path}")"
  curl -fL "${url}" -o "${path}.tmp"
  chmod "${mode}" "${path}.tmp"
  mv -f "${path}.tmp" "${path}"
}

prepare_runtime_assets() {
  mkdir -p "${ASSET_DIR}"
  download_asset_if_needed "firecracker" "${FIRECRACKER_PATH}" "${FIRECRACKER_URL}" 0755
  download_asset_if_needed "kernel" "${KERNEL_PATH}" "${KERNEL_URL}" 0644
}

prepare_lima_image() {
  mkdir -p "$(dirname "${LIMA_IMAGE_PATH}")"

  if [[ -f "${LIMA_IMAGE_PATH}" ]]; then
    log "using existing Lima image ${LIMA_IMAGE_PATH}"
  else
    log "downloading Lima base image from ${LIMA_IMAGE_URL}"
    curl -fL "${LIMA_IMAGE_URL}" -o "${LIMA_IMAGE_PATH}.tmp"
    mv -f "${LIMA_IMAGE_PATH}.tmp" "${LIMA_IMAGE_PATH}"
  fi

  log "calculating Lima image sha256"
  LIMA_IMAGE_DIGEST="sha256:$(sha256_file "${LIMA_IMAGE_PATH}")"
}

write_lima_template() {
  local image_uri
  local docker_mirror_block
  image_uri="file://${LIMA_IMAGE_PATH}"
  docker_mirror_block="$(docker_registry_mirrors_provision_block "${DOCKER_REGISTRY_MIRRORS}")"

  log "writing Lima template ${LIMA_TEMPLATE_PATH}"
  cat >"${LIMA_TEMPLATE_PATH}" <<EOF
minimumLimaVersion: 2.0.0

vmType: vz
arch: ${LIMA_ARCH}
cpus: ${LIMA_CPUS}
memory: ${LIMA_MEMORY}
disk: ${LIMA_DISK}

images:
- location: "${image_uri}"
  arch: "${LIMA_ARCH}"
  digest: "${LIMA_IMAGE_DIGEST}"

containerd:
  system: false
  user: false

user:
  name: ${LIMA_USER}
  home: /home/${LIMA_USER}
  shell: /bin/bash

provision:
- mode: system
  script: |
    #!/usr/bin/env bash
    set -euxo pipefail
    export DEBIAN_FRONTEND=noninteractive
    apt-get update
    apt-get install -y \
      bash \
      btrfs-progs \
      ca-certificates \
      coreutils \
      curl \
      docker.io \
      findutils \
      gcc \
      git \
      iproute2 \
      iptables \
      kmod \
      libc6-dev \
      make \
      procps \
      util-linux
${docker_mirror_block}
    systemctl daemon-reload
    systemctl enable docker
    systemctl restart docker
    systemctl is-active --quiet docker
    usermod -aG docker ${LIMA_USER}

portForwards:
- guestPort: 8080
  hostPort: 8080
  hostIP: "127.0.0.1"
  proto: tcp
- guestPort: 8082
  hostPort: 8082
  hostIP: "127.0.0.1"
  proto: tcp

nestedVirtualization: true
EOF
}

start_lima_vm() {
  if limactl list --format '{{.Name}}' | grep -Fxq "${VM_NAME}"; then
    log "Lima VM ${VM_NAME} already exists"
    limactl start --tty=false "${VM_NAME}" >/dev/null || true
    return
  fi

  log "starting Lima VM ${VM_NAME} from template:${LIMA_TEMPLATE_NAME}"
  limactl start --tty=false --progress --name="${VM_NAME}" "template:${LIMA_TEMPLATE_NAME}"
}

wait_lima_vm() {
  log "waiting for Lima VM ${VM_NAME}"
  local deadline=$((SECONDS + 600))
  until limactl shell "${VM_NAME}" -- uname -a >/dev/null 2>&1; do
    if (( SECONDS >= deadline )); then
      die "Lima VM ${VM_NAME} did not become ready"
    fi
    sleep 2
  done
}

check_vm_kvm() {
  log "checking KVM inside Lima VM"
  if ! limactl shell "${VM_NAME}" -- test -e /dev/kvm; then
    if [[ "${REQUIRE_KVM}" == "1" ]]; then
      die "/dev/kvm is missing inside Lima VM. Firecracker requires nested virtualization."
    fi
    warn "/dev/kvm is missing inside Lima VM; Firecracker sandboxes will not start"
  fi
}

configure_macos_entrypoints() {
  if [[ "${ENABLE_MAC_DNS}" == "1" ]]; then
    configure_macos_dns
  fi
  if [[ "${ENABLE_MAC_PROXY}" == "1" ]]; then
    configure_macos_proxy
  fi
}

brew_install_if_missing() {
  local formula="$1"
  if brew list --formula "${formula}" >/dev/null 2>&1; then
    log "using existing Homebrew formula ${formula}"
    return
  fi
  log "installing Homebrew formula ${formula}"
  brew install "${formula}"
}

configure_macos_dns() {
  log "configuring macOS dnsmasq for *.${DOMAIN}"
  brew_install_if_missing dnsmasq

  mkdir -p "$(dirname "${MAC_DNSMASQ_CONF}")"
  touch "${MAC_DNSMASQ_MAIN_CONF}"
  if ! grep -Fqs "conf-dir=$(dirname "${MAC_DNSMASQ_CONF}")" "${MAC_DNSMASQ_MAIN_CONF}"; then
    {
      printf '\n# Managed by NovitaBox.\n'
      printf 'conf-dir=%s,*.conf\n' "$(dirname "${MAC_DNSMASQ_CONF}")"
    } >>"${MAC_DNSMASQ_MAIN_CONF}"
  fi

  cat >"${MAC_DNSMASQ_CONF}" <<EOF
# Managed by NovitaBox.
address=/${DOMAIN}/127.0.0.1
address=/.${DOMAIN}/127.0.0.1
EOF

  sudo mkdir -p "${MAC_RESOLVER_DIR}"
  printf 'nameserver 127.0.0.1\n' | sudo tee "${MAC_RESOLVER_DIR}/${DOMAIN}" >/dev/null

  sudo "${BREW_BIN}" services restart dnsmasq
  dscacheutil -flushcache >/dev/null 2>&1 || true
}

configure_macos_proxy() {
  log "configuring macOS Caddy reverse proxy for ${DOMAIN}"
  brew_install_if_missing caddy

  mkdir -p "$(dirname "${MAC_CADDYFILE}")" "$(dirname "${MAC_CADDY_CA_PATH}")"
  cat >"${MAC_CADDYFILE}" <<EOF
# Managed by NovitaBox.
{
	admin off
}

${DOMAIN} {
	tls internal
	reverse_proxy 127.0.0.1:8080
}

*.${DOMAIN} {
	tls internal
	reverse_proxy 127.0.0.1:8082
}
EOF

  sudo "${BREW_BIN}" services restart caddy

  local root_ca=""
  root_ca="$(find "${HOME}/Library/Application Support/Caddy" "${BREW_PREFIX}/var/lib/caddy" -path '*/pki/authorities/local/root.crt' -print -quit 2>/dev/null || true)"
  if [[ -n "${root_ca}" ]]; then
    cp "${root_ca}" "${MAC_CADDY_CA_PATH}"
    log "Caddy local CA copied to ${MAC_CADDY_CA_PATH}"
    warn "trust the Caddy local CA in macOS Keychain if HTTPS clients do not trust https://${DOMAIN}"
  else
    warn "Caddy local CA was not found; run 'caddy trust' or trust Caddy's local CA manually if HTTPS clients fail"
  fi
}

sync_install_payload_to_vm() {
  local payload_dir
  payload_dir="$(mktemp -d "${TMPDIR:-/tmp}/novitabox-lima-payload.XXXXXX")"

  mkdir -p "${payload_dir}/bin/linux-${BUILD_ARCH}" "${payload_dir}/scripts" "${payload_dir}/assets"
  cp "${SOURCE_DIR}/scripts/install-linux.sh" "${payload_dir}/scripts/install-linux.sh"
  cp "${SOURCE_DIR}/scripts/uninstall-linux.sh" "${payload_dir}/scripts/uninstall-linux.sh"
  chmod +x "${payload_dir}/scripts/install-linux.sh"
  chmod +x "${payload_dir}/scripts/uninstall-linux.sh"
  cp "${FIRECRACKER_PATH}" "${payload_dir}/assets/firecracker"
  cp "${KERNEL_PATH}" "${payload_dir}/assets/vmlinux.bin"
  chmod 0755 "${payload_dir}/assets/firecracker"
  chmod 0644 "${payload_dir}/assets/vmlinux.bin"

  local cmd
  for cmd in boxapi boxctl boxd boxlet boxproxy boxshim; do
    if [[ ! -x "${SOURCE_DIR}/bin/linux-${BUILD_ARCH}/${cmd}" ]]; then
      die "missing linux-${BUILD_ARCH} binary: ${SOURCE_DIR}/bin/linux-${BUILD_ARCH}/${cmd}"
    fi
    cp "${SOURCE_DIR}/bin/linux-${BUILD_ARCH}/${cmd}" "${payload_dir}/bin/linux-${BUILD_ARCH}/${cmd}"
  done

  log "uploading prebuilt components to ${VM_NAME}:${VM_INSTALL_DIR}"
  limactl shell "${VM_NAME}" -- rm -rf "${VM_INSTALL_DIR}"
  limactl shell "${VM_NAME}" -- mkdir -p "${VM_INSTALL_DIR}"

  tar \
    -C "${payload_dir}" \
    -cf - . | limactl shell "${VM_NAME}" -- tar -C "${VM_INSTALL_DIR}" -xf -

  rm -rf "${payload_dir}"
}

run_linux_installer() {
  log "running Linux installer inside Lima VM"
  limactl shell "${VM_NAME}" -- bash -lc "
    cd '${VM_INSTALL_DIR}' &&
    sudo env \
      ROOT_DIR='${ROOT_DIR}' \
      IMAGE_PATH='${IMAGE_PATH}' \
      IMAGE_SIZE='${IMAGE_SIZE}' \
      DOMAIN='${DOMAIN}' \
      ENABLE_DNS='${ENABLE_VM_DNS}' \
      ENABLE_CADDY='${ENABLE_VM_CADDY}' \
      REQUIRE_KVM='${REQUIRE_KVM}' \
      GOPROXY='${GOPROXY}' \
      FIRECRACKER_PATH='${VM_INSTALL_DIR}/assets/firecracker' \
      KERNEL_PATH='${VM_INSTALL_DIR}/assets/vmlinux.bin' \
      SKIP_BUILD=1 \
      SOURCE_DIR='${VM_INSTALL_DIR}' \
      bash scripts/install-linux.sh
  "
}

print_summary() {
  cat <<EOF

NovitaBox Lima VM is ready.

boxctl:
  ${SOURCE_DIR}/bin/darwin-${DARWIN_ARCH}/boxctl --api http://127.0.0.1:8080 template build my-template --from-image ubuntu:22.04 --run 'echo hello from novitabox'
  ${SOURCE_DIR}/bin/darwin-${DARWIN_ARCH}/boxctl --api http://127.0.0.1:8080 sandbox create <template-id>
  ${SOURCE_DIR}/bin/darwin-${DARWIN_ARCH}/boxctl --api http://127.0.0.1:8080 sandbox ls
  ${SOURCE_DIR}/bin/darwin-${DARWIN_ARCH}/boxctl --proxy http://127.0.0.1:8082 exec -it <sandbox-id> bash

novita-sandbox-cli:
  export NOVITA_DOMAIN=${DOMAIN}
  export NOVITA_API_KEY=dummy
  export NOVITA_ACCESS_TOKEN=dummy
  export NO_PROXY=.${DOMAIN},localhost,127.0.0.1,::1
  export NODE_EXTRA_CA_CERTS=${MAC_CADDY_CA_PATH}
  novita-sandbox-cli sbx create <template-id>

Health:
  curl http://127.0.0.1:8080/health
  curl http://127.0.0.1:8082/healthz

Forwarded ports:
  boxapi:   127.0.0.1:8080 -> VM 8080
  boxproxy: 127.0.0.1:8082 -> VM 8082

DNS/proxy:
  macOS DNS:   ENABLE_MAC_DNS=${ENABLE_MAC_DNS}
  macOS proxy: ENABLE_MAC_PROXY=${ENABLE_MAC_PROXY}
  domain:      ${DOMAIN}
  Caddy CA:    ${MAC_CADDY_CA_PATH}
  VM-side DNS/Caddy are disabled by default and controlled only by ENABLE_VM_DNS / ENABLE_VM_CADDY.

Template file:
  ${LIMA_TEMPLATE_PATH}

EOF
}

main() {
  need_macos
  need_lima
  need_build_tools
  need_source_dir
  resolve_arch_config
  resolve_macos_service_paths
  validate_disk_size
  print_config
  prepare_template_dir
  prepare_lima_image
  prepare_runtime_assets
  write_lima_template
  start_lima_vm
  wait_lima_vm
  check_vm_kvm
  build_local_binaries
  sync_install_payload_to_vm
  run_linux_installer
  configure_macos_entrypoints
  print_summary
}

main "$@"
