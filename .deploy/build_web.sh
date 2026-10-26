#!/bin/bash
set -euo pipefail
SRC=/mnt/e/渗透agent/findu/web
DST=/root/build-web
mkdir -p "$DST"
rsync -a --delete --exclude node_modules --exclude .next --exclude out "$SRC"/ "$DST"/
cd "$DST"
if [ ! -d node_modules ]; then
  echo "[web] npm ci ..."
  npm ci --registry=https://registry.npmmirror.com --no-audit --no-fund 2>&1 | tail -5
fi
echo "[web] next build ..."
NEXT_EXPORT=1 NEXT_TELEMETRY_DISABLED=1 npx next build 2>&1 | tail -25
echo "[web] copy out -> repo"
mkdir -p /mnt/e/渗透agent/findu/web/out
rsync -a --delete "$DST"/out/ /mnt/e/渗透agent/findu/web/out/
ls /mnt/e/渗透agent/findu/web/out | head -5
echo "[web] DONE"
