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
#   DOWNLOAD_DIR=/tmp/novitabox-release scripts/install-release.sh
#   INSTALL_HOMEBREW=0 INSTALL_LIMA=0 scripts/install-release.sh

RELEASE_VERSION="${RELEASE_VERSION:-v0.0.1}"
RELEASE_BASE_URL="${RELEASE_BASE_URL:-https://github.com/novitalabs/NovitaBox/releases/download/${RELEASE_VERSION}}"
SOURCE_DIR="${SOURCE_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
DOWNLOAD_DIR="${DOWNLOAD_DIR:-${SOURCE_DIR}/.release}"
FORCE_DOWNLOAD="${FORCE_DOWNLOAD:-0}"
INSTALL_HOMEBREW="${INSTALL_HOMEBREW:-1}"
INSTALL_LIMA="${INSTALL_LIMA:-1}"

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
  rm -f "${tmp}"
  curl -fL --retry 3 --retry-delay 2 -o "${tmp}" "${url}"
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
  SKIP_BUILD=1 SOURCE_DIR="${SOURCE_DIR}" bash "${SOURCE_DIR}/scripts/install-linux.sh"
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
  SKIP_BUILD=1 SOURCE_DIR="${SOURCE_DIR}" bash "${SOURCE_DIR}/scripts/install-macos-lima.sh"
}

main() {
  need_tools

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
