#!/usr/bin/env bash
# findu 部署验收入口：六项基线 + 生产行数前后比对
#
# 用法（在 WSL 内、仓库任意位置）：
#   bash scripts/verify_deploy.sh snapshot   # 部署前：写行数基线
#   bash scripts/verify_deploy.sh verify     # 部署后：健康/冒烟/异常路径/引擎/行数 diff
#
# 环境变量：FINDU_APP_CONTAINER（默认 findu-findu）、FINDU_PG_CONTAINER（默认 findu-postgres）、
#           FINDU_ROWS_SNAPSHOT（默认 /tmp/findu_rows_before.txt）
set -u
ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT" || exit 1
APP=${FINDU_APP_CONTAINER:-findu-findu}
PG=${FINDU_PG_CONTAINER:-findu-postgres}
SNAP=${FINDU_ROWS_SNAPSHOT:-/tmp/findu_rows_before.txt}
FAIL=0

ROWS_SQL="SELECT 'explorations='||count(*) FROM explorations UNION ALL SELECT 'exploration_nodes='||count(*) FROM exploration_nodes UNION ALL SELECT 'exploration_edges='||count(*) FROM exploration_edges UNION ALL SELECT 'exploration_anchors='||count(*) FROM exploration_anchors UNION ALL SELECT 'activity='||count(*) FROM activity UNION ALL SELECT 'main_sessions='||count(*) FROM main_sessions UNION ALL SELECT 'task_constraints='||count(*) FROM task_constraints UNION ALL SELECT 'tool_usage='||count(*) FROM tool_usage UNION ALL SELECT 'tasks='||count(*) FROM tasks UNION ALL SELECT 'task_relations='||count(*) FROM task_relations UNION ALL SELECT 'task_llm_profiles='||count(*) FROM task_llm_profiles UNION ALL SELECT 'task_scope='||count(*) FROM task_scope UNION ALL SELECT 'task_asset_links='||count(*) FROM task_asset_links UNION ALL SELECT 'task_categories='||count(*) FROM task_categories UNION ALL SELECT 'task_templates='||count(*) FROM task_templates UNION ALL SELECT 'task_intercept_rules='||count(*) FROM task_intercept_rules UNION ALL SELECT 'task_archives='||count(*) FROM task_archives UNION ALL SELECT 'companies='||count(*) FROM companies UNION ALL SELECT 'company_scope='||count(*) FROM company_scope UNION ALL SELECT 'findings='||count(*) FROM findings UNION ALL SELECT 'side_question_sessions='||count(*) FROM side_question_sessions"

rows() { docker exec "$PG" psql -U findu -d findu -tA -c "$ROWS_SQL"; }

case "${1:-verify}" in
snapshot)
  rows > "$SNAP"
  echo "[snapshot] -> $SNAP"
  cat "$SNAP"
  ;;
verify)
  echo "== 1 容器"
  if docker ps --format '{{.Names}}	{{.Status}}' | grep -qE "^${APP}[[:space:]]"; then
    docker ps --format '{{.Names}}	{{.Status}}' | grep -E "^${APP}[[:space:]]"
  else
    echo "  容器 $APP 不在运行 ✗"; FAIL=1
  fi

  echo "== 2 health"
  HEALTH=$(curl -s -m 5 "http://localhost:8081/api/health" || true)
  echo "  $HEALTH"
  case "$HEALTH" in *'"ok":true'*) ;; *) echo "  health 异常 ✗"; FAIL=1 ;; esac

  echo "== 3 冒烟字节数（findu 基线逐字节；控制台 HTML 为可变页面，用下限断言）"
  chk() { # name url want_bytes
    code=$(curl -s -o /dev/null -w '%{http_code}' -m 20 "$2")
    size=$(curl -s -o /dev/null -w '%{size_download}' -m 20 "$2")
    if [ "$code" = "200" ] && [ "$size" = "$3" ]; then
      echo "  $1: $code $size ✓"
    else
      echo "  $1: $code $size (want 200 $3) ✗"; FAIL=1
    fi
  }
  chkMin() { # name url min_bytes —— 控制台/仪表文案随版本漂移，只断言 > N 且 200
    code=$(curl -s -o /dev/null -w '%{http_code}' -m 20 "$2")
    size=$(curl -s -o /dev/null -w '%{size_download}' -m 20 "$2")
    if [ "$code" = "200" ] && [ "$size" -ge "$3" ]; then
      echo "  $1: $code $size (>= $3) ✓"
    else
      echo "  $1: $code $size (want 200 and >= $3) ✗"; FAIL=1
    fi
  }
  chkRedir() { # name url want_location_suffix —— 退役/跳转断言（302 且 Location 以目标结尾）
    code=$(curl -s -o /dev/null -w '%{http_code}' -m 20 "$2")
    loc=$(curl -s -o /dev/null -w '%{redirect_url}' -m 20 "$2")
    if [ "$code" = "302" ] && [[ "$loc" == *"$3" ]]; then
      echo "  $1: $code -> $loc ✓"
    else
      echo "  $1: $code -> $loc (want 302 -> *$3) ✗"; FAIL=1
    fi
  }
  # 2026-10-23 起 `/` 即控制台总览（不再重定向 /dashboard）；文案/构建产物漂移用下限，不改门禁。
  chkMin root      "http://localhost:8081/"               20000
  chkMin dashboard "http://localhost:8081/dashboard/"     20000
  # 2026-10-23 退役 /findu/ 旧 UI：首页 302 → 新控制台 /（findu 引擎保留服务 /findu/api、static）。
  chkRedir findu-retired "http://localhost:8081/findu/" "/"
  chk tools "http://localhost:8081/system/tools/"  24395

  echo "== 3.5 findu 静态资源清单"
  if docker exec "$APP" python3 /app/scripts/findu_static_manifest.py \
      --root /app/web-findu \
      --baseline /app/docs/refactor/findu-static-baseline.json \
      --verify; then
    :
  else
    echo "  web-findu 与基线不一致 ✗"; FAIL=1
  fi

  echo "== 4 异常路径"
  C404=$(curl -s -o /dev/null -w '%{http_code}' -m 10 "http://localhost:8081/api/definitely-not-exist-xyz")
  C401=$(curl -s -o /dev/null -w '%{http_code}' -m 10 "http://localhost:8081/api/tasks")
  echo "  404=$C404 401=$C401"
  [ "$C404" = "404" ] && [ "$C401" = "401" ] || { echo "  异常路径不符合预期 ✗"; FAIL=1; }

  echo "== 5 引擎日志"
  if docker logs "$APP" 2>&1 | grep -q "LLM configured from DB"; then
    echo "  LLM configured from DB ✓"
  else
    echo "  LLM configured from DB ✗"; FAIL=1
  fi

  echo "== 6 生产行数（与部署前基线比对）"
  NOW=/tmp/findu_rows_after.txt
  rows > "$NOW"
  cat "$NOW"
  if [ -f "$SNAP" ]; then
    if diff -u "$SNAP" "$NOW" >/dev/null; then
      echo "  前后一致 ✓"
    else
      echo "  与基线不一致 ✗"; diff -u "$SNAP" "$NOW" | head -40; FAIL=1
    fi
  else
    echo "  未找到基线 $SNAP（部署前先跑 snapshot）"; FAIL=1
  fi

  echo "== 回滚点"
  if [ -f dist/amd64/findu.prev ]; then
    ls -la dist/amd64/findu.prev
  else
    echo "  （无 findu.prev，注意）"
  fi

  if [ "$FAIL" = 0 ]; then echo "VERIFY_OK"; else echo "VERIFY_FAILED"; exit 1; fi
  ;;
*)
  echo "usage: $0 [snapshot|verify]"; exit 2 ;;
esac
