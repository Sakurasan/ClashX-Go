#!/bin/sh
# Install the web dashboard served at /ui (external-ui). Prefers the
# zashboard bundle shipped inside a locally installed ClashX Meta.app; when
# it is missing (CI, plain checkouts) a pinned zashboard release is
# downloaded. Set DASH_SRC to use another directory, ZASH_VERSION to change
# the release ("latest" resolves through the GitHub API, which is rate
# limited on CI, so a pinned tag is the default).
set -e
cd "$(dirname "$0")/.."
SRC="${DASH_SRC:-/Applications/ClashX Meta.app/Contents/Resources/dashboard/zashboard}"
if [ ! -f "$SRC/index.html" ]; then
  VERSION="${ZASH_VERSION:-v3.29.1}"
  TMP="$(mktemp -d)"
  trap 'rm -rf "$TMP"' EXIT
  if [ "$VERSION" = "latest" ]; then
    TAG=$(curl -fsSL --retry 3 --retry-delay 2 \
      https://api.github.com/repos/Zephyruso/zashboard/releases/latest |
      sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1)
  else
    TAG="$VERSION"
  fi
  [ -n "$TAG" ] || { echo "无法解析 zashboard 版本（可设置 ZASH_VERSION）" >&2; exit 1; }
  echo "下载 zashboard $TAG"
  curl -fsSL --retry 3 --retry-delay 2 \
    "https://github.com/Zephyruso/zashboard/releases/download/${TAG}/dist-no-fonts.zip" -o "$TMP/dist.zip"
  unzip -q "$TMP/dist.zip" -d "$TMP/dist"
  # Archives put index.html at the root or in one top-level directory.
  if [ -f "$TMP/dist/index.html" ]; then
    SRC="$TMP/dist"
  else
    SRC=$(dirname "$(find "$TMP/dist" -name index.html -maxdepth 3 | head -1)")
  fi
fi
if [ ! -f "$SRC/index.html" ]; then
  echo "dashboard assets not found in $SRC" >&2
  exit 1
fi
rm -rf resources/dashboard
mkdir -p resources/dashboard
cp -R "$SRC"/. resources/dashboard
DEST="$HOME/.config/clashx-go/dashboard"
if [ -d "$HOME/.config/clashx-go" ]; then
  rm -rf "$DEST"
  mkdir -p "$DEST"
  cp -R "$SRC"/. "$DEST"
  echo "dashboard 已安装: resources/dashboard 与 $DEST"
else
  echo "dashboard 已安装: resources/dashboard"
fi
