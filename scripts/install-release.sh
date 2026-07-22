#!/usr/bin/env bash
set -Eeuo pipefail

# Install NovitaBox from prebuilt GitHub release artifacts.
#
# Linux:
#   sudo -E scripts/install-release.sh
#
# macOS with Lima:
#   scripts/install-release.sh
#
# Common overrides:
#   RELEASE_VERSION=v0.0.1 scripts/install-release.sh
#   RELEASE_BASE_URL=https://github.com/novitalabs/NovitaBox/releases/download/v0.0.1 scripts/install-release.sh
#   RUNTIME_ASSET_VERSION=v0.0.1 scripts/install-release.sh
#   RUNSC_VERSION=v0.0.3 scripts/install-release.sh
#   SOURCE_REF=main scripts/install-release.sh
#   DOWNLOAD_DIR=/tmp/novitabox-release scripts/install-release.sh
#   INSTALL_HOMEBREW=0 INSTALL_LIMA=0 scripts/install-release.sh

SCRIPT_SOURCE="${BASH_SOURCE[0]:-}"
DEFAULT_SOURCE_DIR=""
if [[ -n "${SCRIPT_SOURCE}" && -f "${SCRIPT_SOURCE}" ]]; then
  CANDIDATE_SOURCE_DIR="$(cd "$(dirname "${SCRIPT_SOURCE}")/.." && pwd)"
  if [[ -f "${CANDIDATE_SOURCE_DIR}/scripts/install-linux.sh" ]]; then
    DEFAULT_SOURCE_DIR="${CANDIDATE_SOURCE_DIR}"
  fi
fi

RELEASE_VERSION="${RELEASE_VERSION:-v0.0.1}"
RELEASE_BASE_URL="${RELEASE_BASE_URL:-https://github.com/novitalabs/NovitaBox/releases/download/${RELEASE_VERSION}}"
RUNTIME_ASSET_VERSION="${RUNTIME_ASSET_VERSION:-v0.0.1}"
SOURCE_REF="${SOURCE_REF:-main}"
SOURCE_BASE_URL="${SOURCE_BASE_URL:-https://raw.githubusercontent.com/novitalabs/NovitaBox/${SOURCE_REF}}"
SOURCE_DIR="${SOURCE_DIR:-${DEFAULT_SOURCE_DIR}}"
if [[ -n "${SOURCE_DIR}" ]]; then
  DOWNLOAD_DIR="${DOWNLOAD_DIR:-${SOURCE_DIR}/.release}"
else
  DOWNLOAD_DIR="${DOWNLOAD_DIR:-${TMPDIR:-/tmp}/novitabox-release-download}"
fi
FORCE_DOWNLOAD="${FORCE_DOWNLOAD:-0}"
INSTALL_HOMEBREW="${INSTALL_HOMEBREW:-1}"
INSTALL_LIMA="${INSTALL_LIMA:-1}"
CURL_PROXY="${CURL_PROXY:-${https_proxy:-${HTTPS_PROXY:-${http_proxy:-${HTTP_PROXY:-}}}}}"

COMMANDS=(boxapi boxctl boxd boxlet boxproxy boxshim)

log() {
  printf '[novitabox-release] %s\n' "$*" >&2
}

die() {
  printf '[novitabox-release] error: %s\n' "$*" >&2
  exit 1
}

command_exists() {
  command -v "$1" >/dev/null 2>&1
}

detect_os() {
  case "$(uname -s)" in
    Linux) echo "linux" ;;
    Darwin) echo "darwin" ;;
    *) die "unsupported operating system: $(uname -s)" ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64 | amd64) echo "amd64" ;;
    arm64 | aarch64) echo "arm64" ;;
    *) die "unsupported architecture: $(uname -m)" ;;
  esac
}

linux_arch_for_lima() {
  case "${LIMA_ARCH:-}" in
    "" ) detect_arch ;;
    x86_64 | amd64) echo "amd64" ;;
    aarch64 | arm64) echo "arm64" ;;
    *) die "unsupported Lima architecture: ${LIMA_ARCH}" ;;
  esac
}

need_tools() {
  if ! command_exists curl; then
    die "curl is required"
  fi
  if ! command_exists tar; then
    die "tar is required"
  fi
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

download_source_script() {
  local name="$1"
  local path="${SOURCE_DIR}/scripts/${name}"
  local url="${SOURCE_BASE_URL}/scripts/${name}"
  local tmp="${path}.tmp"

  log "downloading ${url}"
  if ! download_to_tmp "${url}" "${tmp}"; then
    die "download ${name} failed"
  fi
  mv -f "${tmp}" "${path}"
  chmod 0755 "${path}"
}

ensure_source_layout() {
  if [[ -n "${SOURCE_DIR}" && -f "${SOURCE_DIR}/scripts/install-linux.sh" && -f "${SOURCE_DIR}/scripts/install-macos-lima.sh" ]]; then
    return
  fi

  SOURCE_DIR="$(mktemp -d "${TMPDIR:-/tmp}/novitabox-install.XXXXXX")"
  mkdir -p "${SOURCE_DIR}/scripts"
  log "preparing temporary installer source in ${SOURCE_DIR}"
  download_source_script "install-linux.sh"
  download_source_script "install-macos-lima.sh"
  download_source_script "uninstall-linux.sh"
  download_source_script "uninstall-macos-lima.sh"

  if [[ "${DOWNLOAD_DIR}" == "${TMPDIR:-/tmp}/novitabox-release-download" ]]; then
    DOWNLOAD_DIR="${SOURCE_DIR}/.release"
  fi
}

load_homebrew_env() {
  if command_exists brew; then
    return
  fi
  if [[ -x /opt/homebrew/bin/brew ]]; then
    eval "$(/opt/homebrew/bin/brew shellenv)"
    return
  fi
  if [[ -x /usr/local/bin/brew ]]; then
    eval "$(/usr/local/bin/brew shellenv)"
    return
  fi
}

ensure_homebrew_on_macos() {
  load_homebrew_env
  if command_exists brew; then
    log "using Homebrew at $(command -v brew)"
    return
  fi

  if [[ "${INSTALL_HOMEBREW}" != "1" ]]; then
    die "Homebrew is required on macOS; install it or rerun without INSTALL_HOMEBREW=0"
  fi

  log "installing Homebrew"
  NONINTERACTIVE=1 /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
  load_homebrew_env

  if ! command_exists brew; then
    die "Homebrew installed but brew is not available in PATH"
  fi
}

ensure_lima_on_macos() {
  if command_exists limactl; then
    log "using Lima at $(command -v limactl)"
    return
  fi

  if [[ "${INSTALL_LIMA}" != "1" ]]; then
    die "Lima is required on macOS; install it with 'brew install lima' or rerun without INSTALL_LIMA=0"
  fi

  ensure_homebrew_on_macos
  log "installing Lima with Homebrew"
  brew install lima
}

archive_name() {
  local os="$1"
  local arch="$2"
  printf 'novitabox-%s-%s.tar.gz' "${os}" "${arch}"
}

download_archive() {
  local os="$1"
  local arch="$2"
  local name path url tmp

  name="$(archive_name "${os}" "${arch}")"
  path="${DOWNLOAD_DIR}/${name}"
  url="${RELEASE_BASE_URL}/${name}"

  mkdir -p "${DOWNLOAD_DIR}"
  if [[ "${FORCE_DOWNLOAD}" != "1" && -s "${path}" ]]; then
    log "using cached ${path}"
    printf '%s\n' "${path}"
    return
  fi

  log "downloading ${url}"
  tmp="${path}.tmp"
  if ! download_to_tmp "${url}" "${tmp}"; then
    die "download ${name} failed"
  fi
  mv "${tmp}" "${path}"
  printf '%s\n' "${path}"
}

find_payload_dir() {
  local extract_dir="$1"
  local os="$2"
  local arch="$3"

  if [[ -x "${extract_dir}/bin/${os}-${arch}/boxapi" ]]; then
    printf '%s\n' "${extract_dir}/bin/${os}-${arch}"
    return
  fi
  if [[ -x "${extract_dir}/${os}-${arch}/boxapi" ]]; then
    printf '%s\n' "${extract_dir}/${os}-${arch}"
    return
  fi
  if [[ -x "${extract_dir}/boxapi" ]]; then
    printf '%s\n' "${extract_dir}"
    return
  fi

  local found
  found="$(find "${extract_dir}" -type f -name boxapi -perm -111 -print -quit 2>/dev/null || true)"
  if [[ -n "${found}" ]]; then
    dirname "${found}"
    return
  fi

  die "release archive does not contain NovitaBox binaries for ${os}-${arch}"
}

install_components_from_archive() {
  local os="$1"
  local arch="$2"
  local archive extract_dir payload_dir target_dir cmd

  archive="$(download_archive "${os}" "${arch}")"
  extract_dir="$(mktemp -d "${TMPDIR:-/tmp}/novitabox-release.XXXXXX")"
  tar -xzf "${archive}" -C "${extract_dir}"
  payload_dir="$(find_payload_dir "${extract_dir}" "${os}" "${arch}")"

  target_dir="${SOURCE_DIR}/bin/${os}-${arch}"
  log "installing prebuilt ${os}-${arch} components to ${target_dir}"
  mkdir -p "${target_dir}"
  for cmd in "${COMMANDS[@]}"; do
    if [[ ! -x "${payload_dir}/${cmd}" ]]; then
      rm -rf "${extract_dir}"
      die "release archive is missing executable ${cmd}"
    fi
    cp "${payload_dir}/${cmd}" "${target_dir}/${cmd}"
    chmod 0755 "${target_dir}/${cmd}"
  done
  rm -rf "${extract_dir}"
}

install_linux() {
  local arch="$1"
  install_components_from_archive "linux" "${arch}"
  log "running Linux installer with prebuilt components"
  SKIP_BUILD=1 SOURCE_DIR="${SOURCE_DIR}" RELEASE_VERSION="${RUNTIME_ASSET_VERSION}" RUNSC_VERSION="${RELEASE_VERSION}" bash "${SOURCE_DIR}/scripts/install-linux.sh"
}

install_macos_lima() {
  local darwin_arch="$1"
  local linux_arch
  linux_arch="$(linux_arch_for_lima)"

  ensure_homebrew_on_macos
  ensure_lima_on_macos

  install_components_from_archive "darwin" "${darwin_arch}"
  install_components_from_archive "linux" "${linux_arch}"

  log "running macOS Lima installer with prebuilt components"
  SKIP_BUILD=1 SOURCE_DIR="${SOURCE_DIR}" RELEASE_VERSION="${RUNTIME_ASSET_VERSION}" RUNSC_VERSION="${RELEASE_VERSION}" bash "${SOURCE_DIR}/scripts/install-macos-lima.sh"
}

main() {
  log "starting NovitaBox release installer ${RELEASE_VERSION}"
  log "runtime assets version ${RUNTIME_ASSET_VERSION}"
  if [[ -n "${CURL_PROXY}" ]]; then
    log "using curl proxy ${CURL_PROXY}"
  fi
  need_tools
  ensure_source_layout

  local os arch
  os="$(detect_os)"
  arch="$(detect_arch)"

  case "${os}" in
    linux) install_linux "${arch}" ;;
    darwin) install_macos_lima "${arch}" ;;
    *) die "unsupported operating system: ${os}" ;;
  esac
}

main "$@"
