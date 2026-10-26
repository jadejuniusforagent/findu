#!/usr/bin/env bash
# ============================================================
# redeploy.sh —— FindUAI 一键重建并重启
#
#   ./redeploy.sh            全流程：前端产物检查 → 编二进制 → 建镜像 → 换容器 → 健康检查
#   ./redeploy.sh --restart  跳过编译，只按当前 findu:local 重建容器
#   ./redeploy.sh --no-rollback  健康检查失败也不回滚（调试用）
#
# 设计约束：
#   1. 换容器前把现役镜像 tag 成 findu:prev —— 回滚基线只留一份（覆盖式）。
#   2. 健康检查失败 → 自动用 findu:prev 拉起，并打印失败日志尾部。
#   3. 所有容器参数与现役容器一致（network/挂载/日志轮转），避免"重建后行为变了"。
#   4. 不碰 postgres 容器；只确保它在跑。
# ============================================================
set -euo pipefail

ROOT="${ROOT:-/mnt/e/渗透agent/findu}"
IMAGE="${IMAGE:-findu:local}"
PREV_IMAGE="${PREV_IMAGE:-findu:prev}"
# 故意不叫 NAME：WSL 的 bash 环境里 NAME 常被设成主机名，会被继承成容器名。
CONTAINER_NAME="${CONTAINER_NAME:-findu-findu}"
NETWORK="${NETWORK:-findu_default}"
PG_NAME="${PG_NAME:-findu-postgres}"
HEALTH_URL="${HEALTH_URL:-http://127.0.0.1:8081/api/health}"
TEMPLATES_DIR="${TEMPLATES_DIR:-/root/nuclei-templates}"
DATA_DIR="${DATA_DIR:-/opt/findu/data}"
FINDU_DATA_DIR="${FINDU_DATA_DIR:-/opt/findu/data-findu}"
# findu 前端：默认只读挂载仓库里的 web-findu，避免容器 recreate 回退到镜像里的旧页面。
# 需要临时回退到镜像内置版本时设 WEB_FINDU_MOUNT=0。
WEB_FINDU_DIR="${WEB_FINDU_DIR:-$ROOT/web-findu}"
WEB_FINDU_MOUNT="${WEB_FINDU_MOUNT:-1}"
# supervisor / deep 子代理 Markdown：默认只读挂载仓库里的 agents，改分类不用重建镜像。
AGENTS_DIR="${AGENTS_DIR:-$ROOT/agents}"
# 知识库：业务数据，只读挂载仓库里的 knowledge_base（不随镜像传播）。
KNOWLEDGE_BASE_DIR="${KNOWLEDGE_BASE_DIR:-$ROOT/knowledge_base}"
# JWT 密钥独立挂载点：不能放 data 目录（那是可浏览工作区）
KEYS_DIR="${KEYS_DIR:-/opt/findu/keys}"
PYLIBS_DIR="${PYLIBS_DIR:-/opt/findu/pylibs}"
# 版本号注入：优先环境变量 VERSION，其次 git describe，最后 dev。
VERSION="${VERSION:-$(git -C "$ROOT" describe --tags --always --dirty 2>/dev/null || echo dev)}"

DO_BUILD=1
DO_ROLLBACK=1
for arg in "$@"; do
  case "$arg" in
    --restart)     DO_BUILD=0 ;;
    --no-rollback) DO_ROLLBACK=0 ;;
    -h|--help)     sed -n '2,17p' "$0"; exit 0 ;;
    *) echo "未知参数: $arg" >&2; exit 2 ;;
  esac
done

say() { printf '\n\033[1;36m==> %s\033[0m\n' "$*"; }
die() { printf '\n\033[1;31m!! %s\033[0m\n' "$*" >&2; exit 1; }

cd "$ROOT"

# ---------- 0. 前置检查 ----------
say "前置检查"
docker info >/dev/null 2>&1 || die "Docker 不可用"
[ -f "$ROOT/cmd/findu/main.go" ] || die "ROOT 不对: $ROOT"
mkdir -p "$DATA_DIR" "$FINDU_DATA_DIR" "$KEYS_DIR"; chmod 700 "$KEYS_DIR"
[ -d "$PYLIBS_DIR" ] || die "缺 $PYLIBS_DIR（report/playwright 依赖库）"
[ -d "$TEMPLATES_DIR" ] || echo "  提示: $TEMPLATES_DIR 不存在，nuclei 将没有模板"

if ! docker ps --format '{{.Names}}' | grep -qx "$PG_NAME"; then
  say "postgres 未运行，拉起 $PG_NAME"
  docker start "$PG_NAME" >/dev/null
fi
docker ps --format '{{.Names}}' | grep -qx "$PG_NAME" || die "postgres 起不来"

# ---------- 0.5 解析 PG DSN ----------
# 密码从 postgres 容器自身的环境变量取（不落文件、不回显）。
# 之前这里默认成 findu/findu，导致 auth failed 32s 重启循环——默认值必须来自真实容器。
if [ -n "${FINDU_PG_DSN:-}" ]; then
  PG_DSN="$FINDU_PG_DSN"
else
  PG_PW="$(docker exec "$PG_NAME" printenv POSTGRES_PASSWORD 2>/dev/null || true)"
  [ -n "$PG_PW" ] || die "拿不到 postgres 密码：请设 FINDU_PG_DSN 环境变量覆盖"
  PG_DSN="postgres://findu:${PG_PW}@postgres:5432/findu?sslmode=disable"
fi
export PG_DSN

# ---------- 1. 前端产物（内嵌 UI 必需）----------
if [ "$DO_BUILD" = "1" ]; then
  say "检查前端产物 web/out"
  if [ ! -f "$ROOT/web/out/index.html" ]; then
    echo "  web/out 缺失 → 跑 .deploy/build_web.sh（npm ci + next build，几分钟）"
    bash "$ROOT/.deploy/build_web.sh"
  fi
  [ -f "$ROOT/web/out/index.html" ] || die "前端构建后仍无 web/out/index.html"
  # go:embed all:webui/dist 要求该目录在编译期存在；缺父目录时先建。
  mkdir -p "$ROOT/server/webui"
  rm -rf "$ROOT/server/webui/dist"
  cp -r "$ROOT/web/out" "$ROOT/server/webui/dist"

  # ---------- 2. 编二进制 ----------
  say "编译 Go 二进制（-tags embedui）"
  mkdir -p "$ROOT/dist/amd64"
  # CGO_ENABLED=1 是必须的：findu 侧用 mattn/go-sqlite3（要求 cgo），
  # 关掉后 findu 子系统启动即报 "go-sqlite3 requires cgo to work. This is a stub"，
  # /findu/ 直接挂不上。findu 自身是纯 Go，开 cgo 不影响它。
  CGO_ENABLED=1 GOOS=linux GOARCH=amd64 \
    go build -tags embedui -trimpath -ldflags "-s -w -X main.version=$VERSION" \
    -o "$ROOT/dist/amd64/findu" ./cmd/findu
  ls -lh "$ROOT/dist/amd64/findu"

  # ---------- 3. 建镜像 ----------
  say "构建镜像 $IMAGE"
  docker build -f Dockerfile.deploy -t "$IMAGE" . 2>&1 | tail -20
fi

docker image inspect "$IMAGE" >/dev/null 2>&1 || die "镜像 $IMAGE 不存在（去掉 --restart 先完整构建）"

# ---------- 4. 记录回滚基线 ----------
if docker image inspect "$IMAGE" --format '{{.Id}}' >/dev/null 2>&1; then
  OLD_ID="$(docker inspect -f '{{.Image}}' "$CONTAINER_NAME" 2>/dev/null || true)"
  if [ -n "${OLD_ID:-}" ]; then
    say "保存回滚基线 $PREV_IMAGE（来自现役容器 $CONTAINER_NAME）"
    docker tag "$OLD_ID" "$PREV_IMAGE"
  fi
fi

# ---------- 5. 换容器 ----------
# 注意：-e 列表中间不能插注释行 —— 反斜杠续行会把 # 之后的整行吃掉，
# docker run 会因缺 IMAGE 报 "requires at least 1 argument"。
run_container() {
  local img="$1"
  local web_mounts=()
  local agents_mounts=()
  local kb_mounts=()
  if [ "$WEB_FINDU_MOUNT" = "1" ] && [ -d "$WEB_FINDU_DIR" ]; then
    web_mounts+=(-v "$WEB_FINDU_DIR:/app/web-findu:ro")
    echo "  web-findu: $WEB_FINDU_DIR -> /app/web-findu (ro)"
  else
    echo "  web-findu: 使用镜像内置 /app/web-findu（WEB_FINDU_MOUNT=$WEB_FINDU_MOUNT）"
  fi
  if [ -d "$AGENTS_DIR" ]; then
    agents_mounts+=(-v "$AGENTS_DIR:/app/agents:ro")
    echo "  agents: $AGENTS_DIR -> /app/agents (ro)"
  else
    echo "  agents: 使用镜像内置 /app/agents（$AGENTS_DIR 不存在）"
  fi
  if [ -d "$KNOWLEDGE_BASE_DIR" ]; then
    kb_mounts+=(-v "$KNOWLEDGE_BASE_DIR:/app/knowledge_base:ro")
    echo "  knowledge_base: $KNOWLEDGE_BASE_DIR -> /app/knowledge_base (ro)"
  else
    echo "  knowledge_base: 使用镜像内置空目录（$KNOWLEDGE_BASE_DIR 不存在）"
  fi
  docker rm -f "$CONTAINER_NAME" >/dev/null 2>&1 || true
  docker run -d --name "$CONTAINER_NAME" \
    --restart unless-stopped \
    --network "$NETWORK" \
    -p 8081:8081 \
    -p 127.0.0.1:8788:8788 \
    -e "FINDU_PG_DSN=$PG_DSN" \
    -e "FINDU_DB_DSN=$PG_DSN" \
    -e PYTHONPATH=/opt/pylibs \
    -e FINDU_ENABLE=1 \
    -e FINDU_SKIP_ROBOTS=1 \
    -e "FINDU_RUNTIME_SPI=${FINDU_RUNTIME_SPI:-}" \
    -v "$DATA_DIR:/app/data" \
    -v "$KEYS_DIR:/app/keys" \
    -v "$FINDU_DATA_DIR:/app/data-findu" \
    -v "$ROOT/skills:/app/skills" \
    -v "$ROOT/config-findu.yaml:/app/config-findu.yaml:ro" \
    -v "$ROOT/dist/amd64/findu:/app/findu:ro" \
    -v "$ROOT/start.sh:/app/start.sh:ro" \
    -v "$ROOT/vulnkb:/app/vulnkb:ro" \
    -v "$PYLIBS_DIR:/opt/pylibs:ro" \
    -v "$TEMPLATES_DIR:/root/nuclei-templates:ro" \
    "${web_mounts[@]}" \
    "${agents_mounts[@]}" \
    "${kb_mounts[@]}" \
    --log-opt max-size=20m --log-opt max-file=3 \
    "$img" -addr :8081 -proxy :8788 >/dev/null
  # 第二块网络：知识库 embedding 要访问 findu-ollama（compose 里声明了两块网络，
  # 但 docker run 一次只能 --network 一个，这里补连；漏了会报
  # "lookup findu-ollama on 127.0.0.11:53: no such host" 并中断索引）。
  docker network connect "${FINDU_NETWORK:-findu_default}" "$CONTAINER_NAME" >/dev/null 2>&1 || true
}

health_ok() {
  curl -fsS -m 3 "$HEALTH_URL" >/dev/null 2>&1
}

say "启动容器 $CONTAINER_NAME"
run_container "$IMAGE"

say "健康检查（最多 60s）"
ok=0
for i in $(seq 1 30); do
  if ! docker ps --format '{{.Names}}' | grep -qx "$CONTAINER_NAME"; then
    echo "  容器已退出"; break
  fi
  if health_ok; then ok=1; echo "  第 ${i} 次探测通过"; break; fi
  sleep 2
done

if [ "$ok" != "1" ]; then
  echo "--- 容器日志尾部 ---"
  docker logs --tail 40 "$CONTAINER_NAME" 2>&1 || true
  if [ "$DO_ROLLBACK" = "1" ] && docker image inspect "$PREV_IMAGE" >/dev/null 2>&1; then
    say "健康检查失败 → 回滚到 $PREV_IMAGE"
    run_container "$PREV_IMAGE"
    for i in $(seq 1 15); do health_ok && { echo "  回滚后服务已恢复"; exit 1; }; sleep 2; done
    die "回滚后仍不健康，需人工介入"
  fi
  die "健康检查失败"
fi

# ---------- 6. 验收信息 ----------
say "验收"
echo "健康:      $(curl -fsS -m 3 "$HEALTH_URL" | head -c 200)"
echo "配方接入:  $(docker logs "$CONTAINER_NAME" 2>&1 | grep -o '\[recipes\].*' | tail -1 | cut -c1-200)"
echo "findu 挂载: $(docker logs "$CONTAINER_NAME" 2>&1 | grep -o '\[findu\].*' | tail -1)"
echo "容器:      $(docker ps --filter "name=$CONTAINER_NAME" --format '{{.Status}}  {{.Image}}')"
echo "8081:      $(curl -s -o /dev/null -w '%{http_code}' -m 3 http://127.0.0.1:8081/)"
echo "回滚基线:  $PREV_IMAGE"
say "完成"
