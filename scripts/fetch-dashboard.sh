#!/bin/sh
# Install a dashboard for the core's external-ui (控制台 ⌘D serves it at
# http://127.0.0.1:<api>/ui). Prefers the zashboard bundle shipped inside a
# locally installed ClashX Meta.app; set DASH_SRC to point elsewhere.
set -e
cd "$(dirname "$0")/.."
SRC="${DASH_SRC:-/Applications/ClashX Meta.app/Contents/Resources/dashboard/zashboard}"
if [ ! -f "$SRC/index.html" ]; then
  echo "找不到 $SRC/index.html；下载 zashboard 后用 DASH_SRC=<目录> $0 重试" >&2
  exit 1
fi
rm -rf resources/dashboard
cp -R "$SRC" resources/dashboard
DEST="$HOME/Library/Application Support/clashx-go/dashboard"
rm -rf "$DEST"
mkdir -p "$DEST"
cp -R "$SRC"/. "$DEST"
echo "dashboard 已安装: $DEST"
