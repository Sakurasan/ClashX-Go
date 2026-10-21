#!/bin/bash
# build-dnshelper.sh — builds the tiny Tun DNS helper for both macOS
# architectures, combines them into one universal binary in resources/, and
# signs it ad hoc (Apple Silicon refuses to run unsigned arm64 code). The
# app installs this binary as a root-owned setuid helper on first use.
set -euo pipefail

cd "$(dirname "$0")/.."

TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT

for arch in arm64 amd64; do
  echo "==> dns-helper darwin/${arch}"
  CGO_ENABLED=0 GOOS=darwin GOARCH="${arch}" \
    go build -trimpath -ldflags="-s -w" \
    -o "${TMP}/dns-helper-${arch}" ./cmd/dnshelper
done

mkdir -p resources
lipo -create -output resources/dns-helper \
  "${TMP}/dns-helper-arm64" "${TMP}/dns-helper-amd64"
chmod 0755 resources/dns-helper
codesign --force --sign - resources/dns-helper >/dev/null

echo "==> wrote resources/dns-helper ($(du -h resources/dns-helper | cut -f1))"
