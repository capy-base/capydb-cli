#!/bin/sh
# CapyDB CLI installer.
#
#   curl -fsSL https://raw.githubusercontent.com/capydatabase/capydb-cli/main/scripts/install.sh | sh
#
# Downloads the latest GitHub release archive for this OS/arch, verifies its
# SHA-256 against checksums.txt, and installs the `capydb` binary.
#
# Environment overrides:
#   CAPYDB_INSTALL_DIR   target directory (default /usr/local/bin, falls back
#                        to ~/.local/bin when not writable and sudo is absent)
#   CAPYDB_VERSION       install a specific tag (e.g. v1.2.3) instead of latest
#   CAPYDB_REPO          owner/name of the release repo (default capydatabase/capydb-cli)
set -eu

REPO="${CAPYDB_REPO:-capydatabase/capydb-cli}"
PROJECT="capydb"

err() { printf 'install.sh: %s\n' "$*" >&2; exit 1; }

command -v curl >/dev/null 2>&1 || err "curl is required"
command -v tar >/dev/null 2>&1 || err "tar is required"

# --- platform detection (must mirror .goreleaser.yaml's name_template) -------
os_raw="$(uname -s)"
case "$os_raw" in
  Darwin) os="macOS" ;;
  Linux) os="linux" ;;
  *) err "unsupported OS: $os_raw (use 'go install github.com/capydatabase/capydb-cli/cmd/capydb@latest')" ;;
esac

arch_raw="$(uname -m)"
case "$arch_raw" in
  x86_64 | amd64) arch="x86_64" ;;
  arm64 | aarch64) arch="arm64" ;;
  *) err "unsupported architecture: $arch_raw" ;;
esac

# --- resolve version ----------------------------------------------------------
if [ -n "${CAPYDB_VERSION:-}" ]; then
  tag="$CAPYDB_VERSION"
else
  tag="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" |
    grep -m1 '"tag_name"' | cut -d '"' -f 4)"
  [ -n "$tag" ] || err "could not resolve the latest release of ${REPO}"
fi
version="${tag#v}"

archive="${PROJECT}_${version}_${os}_${arch}.tar.gz"
base_url="https://github.com/${REPO}/releases/download/${tag}"

workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT

echo "Downloading ${archive} (${tag})..."
curl -fsSL -o "${workdir}/${archive}" "${base_url}/${archive}" ||
  err "download failed: ${base_url}/${archive}"
curl -fsSL -o "${workdir}/checksums.txt" "${base_url}/checksums.txt" ||
  err "download failed: ${base_url}/checksums.txt"

# --- checksum verification ----------------------------------------------------
expected="$(grep " ${archive}\$" "${workdir}/checksums.txt" | cut -d ' ' -f 1)"
[ -n "$expected" ] || err "no checksum recorded for ${archive}"
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "${workdir}/${archive}" | cut -d ' ' -f 1)"
else
  actual="$(shasum -a 256 "${workdir}/${archive}" | cut -d ' ' -f 1)"
fi
[ "$expected" = "$actual" ] || err "checksum mismatch for ${archive} (expected ${expected}, got ${actual})"

tar -xzf "${workdir}/${archive}" -C "$workdir" "$PROJECT"

# --- install ------------------------------------------------------------------
install_dir="${CAPYDB_INSTALL_DIR:-/usr/local/bin}"
if [ ! -w "$install_dir" ]; then
  if [ -z "${CAPYDB_INSTALL_DIR:-}" ] && command -v sudo >/dev/null 2>&1; then
    echo "Installing to ${install_dir} (sudo required)..."
    sudo install -m 0755 "${workdir}/${PROJECT}" "${install_dir}/${PROJECT}"
  else
    install_dir="${HOME}/.local/bin"
    mkdir -p "$install_dir"
    install -m 0755 "${workdir}/${PROJECT}" "${install_dir}/${PROJECT}"
    case ":${PATH}:" in
      *":${install_dir}:"*) ;;
      *) echo "NOTE: add ${install_dir} to your PATH" ;;
    esac
  fi
else
  install -m 0755 "${workdir}/${PROJECT}" "${install_dir}/${PROJECT}"
fi

echo "Installed: $("${install_dir}/${PROJECT}" --version 2>/dev/null || echo "${PROJECT} ${tag}")"
