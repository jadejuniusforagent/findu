#!/usr/bin/env bash
# rebuild.sh —— 只重建二进制并热替换：不重建镜像、不换容器。
#
# 机制：compose 把宿主机 ./dist/amd64 挂到容器 /app/dist，start.sh 优先执行
# /app/findu（镜像内 /app/findu 仅作回滚兜底）。所以改完 Go 代码只需
# 编译 -> 原子替换 -> docker restart -> 健康检查。
#
# 用法：./rebuild.sh            VERSION=p9 ./rebuild.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONTAINER="${CONTAINER:-findu-findu}"
OUT="$ROOT/dist/amd64/findu"
VERSION="${VERSION:-$(date +%Y%m%d-%H%M%S)}"
HEALTH="${HEALTH:-http://127.0.0.1:8081/api/health}"

say() { echo "[rebuild] $*"; }

cd "$ROOT"
say "编译 Go 二进制（-tags embedui, CGO_ENABLED=1）版本 $VERSION"
CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -tags embedui -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$OUT.new" ./cmd/findu

[ -s "$OUT.new" ] || { echo "[rebuild] 产物为空，放弃" >&2; exit 1; }
chmod +x "$OUT.new"
say "冒烟：容器外跑 -h（验证 ELF 可加载 + Go 运行时能起）"
smoke="$("$OUT.new" -h 2>&1 || true)"
if ! printf "%s" "$smoke" | grep -q "Usage of"; then
  echo "[rebuild] 新二进制无法启动，保留线上版本：" >&2
  printf "%s
" "$smoke" | head -5 >&2
  rm -f "$OUT.new"
  exit 1
fi

if [ -f "$OUT" ]; then cp -f "$OUT" "$OUT.prev"; fi
mv -f "$OUT.new" "$OUT"
say "已替换 $OUT（上一份保留为 findu.prev）"

say "重启容器 $CONTAINER"
docker restart "$CONTAINER" >/dev/null

for i in $(seq 1 30); do
  if body="$(curl -fsS --max-time 2 "$HEALTH" 2>/dev/null)"; then
    say "健康检查通过：$body"
    say "容器内生效二进制：$(docker exec "$CONTAINER" sha256sum /app/findu 2>/dev/null | cut -d" " -f1)"
    exit 0
  fi
  sleep 2
done

say "健康检查 60 秒未通过，回滚"
if [ -f "$OUT.prev" ]; then
  cp -f "$OUT.prev" "$OUT"
  docker restart "$CONTAINER" >/dev/null
  say "已回滚到上一份二进制"
fi
exit 1
