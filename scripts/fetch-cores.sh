#!/bin/bash
# fetch-cores.sh — downloads the mihomo core binaries for both macOS
# architectures plus the GeoIP database, into resources/, so
# `mygo build -platform darwin/universal` bundles a self-contained app
# with no runtime download (and no slow first-run geo data download).
#
# Usage: scripts/fetch-cores.sh [version]   (default: v1.19.33)
set -euo pipefail

cd "$(dirname "$0")/.."

VERSION="${1:-v1.19.33}"
BASE="https://github.com/MetaCubeX/mihomo/releases/download/${VERSION}"
GEOBASE="https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest"

fetch_arch() {
  local arch="$1"   # arm64 | amd64
  local dir="resources/darwin-${arch}"
  local url="${BASE}/mihomo-darwin-${arch}-${VERSION}.gz"
  mkdir -p "${dir}"
  local tmp="$(mktemp -d)/mihomo.gz"
  echo "==> ${url}"
  curl -fSL --retry 3 "${url}" -o "${tmp}"
  gunzip -f "${tmp}" -c > "${dir}/mihomo"
  chmod +x "${dir}/mihomo"
  echo "==> wrote ${dir}/mihomo ($(du -h "${dir}/mihomo" | cut -f1))"
}

# GeoIP and GeoSite databases ship once (not per-arch): GEOIP rules use
# the former, and nameserver-policy's geosite:cn,private uses the latter.
fetch_geoip() {
  local dest="resources/geoip.metadb"
  echo "==> ${GEOBASE}/geoip.metadb"
  curl -fSL --retry 3 "${GEOBASE}/geoip.metadb" -o "${dest}"
  echo "==> wrote ${dest} ($(du -h "${dest}" | cut -f1))"
}

fetch_geosite() {
  local dest="resources/geosite.dat"
  # The release asset is occasionally unavailable; jsDelivr carries the
  # same V2Ray geosite database.
  for url in "${GEOBASE}/geosite.dat" \
    "https://cdn.jsdelivr.net/gh/Loyalsoldier/v2ray-rules-dat@release/geosite.dat"; do
    echo "==> ${url}"
    if curl -fSL --retry 3 "${url}" -o "${dest}"; then
      echo "==> wrote ${dest} ($(du -h "${dest}" | cut -f1))"
      return
    fi
  done
  echo "error: could not download geosite.dat" >&2
  return 1
}

fetch_arch arm64
fetch_arch amd64
fetch_geoip
fetch_geosite
echo "done"
