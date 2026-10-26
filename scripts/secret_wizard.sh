#!/usr/bin/env bash
# secret_wizard —— Step 6d Phase B3：findu 配置密钥交互式部署向导。
#
# 两种部署模式（用户定案）：
#   交互式  —— 本脚本扫描 config-findu.yaml 中 ${VAR} 引用，逐项询问并把值写进
#             .env（docker compose 自动透传，见 docker-compose.yml 环境块）；
#   直改    —— 直接编辑本仓库根目录的 config-findu.yaml（挂载进容器
#             /app/config-findu.yaml），填 api_key 明文或 ${VAR} 引用均可。
#
# 用法：
#   bash scripts/secret_wizard.sh            # 交互式收集
#   bash scripts/secret_wizard.sh --list     # 仅列出已有引用
#   FOO=bar bash scripts/secret_wizard.sh --set FOO    # 非交互设置单个引用
#   bash scripts/secret_wizard.sh --status   # 对照 .env 显示缺失项

set -euo pipefail
cd "$(dirname "$0")/.."

CFG="config-findu.yaml"
ENVF=".env"
MODE="${1:-interactive}"

ref_vars() {
  grep -oE '\$\{[A-Za-z_][A-Za-z0-9_]*(:-[^}]*)?\}' "$CFG" 2>/dev/null \
    | sed -E 's/^\$\{([A-Za-z_][A-Za-z0-9_]*).*/\1/' | sort -u
}

known_optional_vars() {
  # compose 已透传的可选密钥（与 docker-compose.yml 环境块保持一致）
  printf "FOFA_API_KEY\nZOOMEYE_API_KEY\nQUAKE_API_KEY\nSHODAN_API_KEY\nFINDU_TYPESAFE_API_KEY\nFINDU_VISION_API_KEY\n"
}

env_has() { grep -qE "^${1}=" "$ENVF" 2>/dev/null; }

set_env() {
  local var="$1" val="$2"
  touch "$ENVF"
  if env_has "$var"; then
    python3 - "$ENVF" "$var" "$val" <<'PY'
import re, sys
path, var, val = sys.argv[1], sys.argv[2], sys.argv[3]
lines = open(path, encoding="utf-8").read().splitlines()
pat = re.compile(rf"^{re.escape(var)}=.*$")
out, done = [], False
for l in lines:
    if pat.match(l):
        out.append(f"{var}={val}"); done = True
    else:
        out.append(l)
if not done:
    out.append(f"{var}={val}")
open(path, "w", encoding="utf-8").write("\n".join(out) + "\n")
PY
  else
    printf "%s=%s\n" "$var" "$val" >> "$ENVF"
  fi
  echo "  ✔ 已写入 .env: $var"
}

case "$MODE" in
  --list)
    echo "config-findu.yaml 中的环境引用："
    ref_vars
    ;;
  --status)
    cmp=0
    for v in $(ref_vars; known_optional_vars | sort -u); do
      if env_has "$v"; then echo "√ $v（已配置）"; else echo "× $v（未配置）"; cmp=1; fi
    done
    [ "$cmp" = "1" ] && exit 1 || echo "全部引用已配置。"
    ;;
  --set)
    var="${2:?用法: --set VAR}"
    val="${!V:-}"
    echo "输入 $var 的值（输入不回显到终端之外）:"
    read -r val
    [ -n "$val" ] && set_env "$var" "$val"
    ;;
  *)
    echo "== findu 配置密钥交互部署 =="
    VARS=$( { ref_vars; known_optional_vars; } | sort -u )
    need=0
    for v in $VARS; do
      if env_has "$v"; then
        echo "√ $v 已在 .env"
      else
        need=1
        printf "输入 %s 的值（直接回车=跳过/留空）: " "$v"
        read -r val
        [ -n "${val}" ] && set_env "$v" "$val"
      fi
    done
    [ "$need" = "0" ] && echo "所有引用均已配置。"
    echo ""
    echo "提示：值已写入 .env（compose 在 redeploy.sh 启动时自动注入容器）。"
    echo "      也可以直接编辑 config-findu.yaml（直改模式，挂载进容器无需重启进程外的再构建）。"
    echo "      完成后执行 ./redeploy.sh 使容器拿到新环境变量。"
    ;;
esac
