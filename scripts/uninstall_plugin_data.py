#!/usr/bin/env python3
# 插件卸载的数据处置：把插件私有表整表搬到 archive schema。
#
# 安全属性（设计约束，改脚本时不要破坏）：
#   1. 本脚本不删除任何表、任何数据 —— 只有 CREATE SCHEMA / ALTER TABLE ... SET SCHEMA。
#   2. 默认 dry-run，只打印将执行的 SQL；--apply 必须同时给 --yes。
#   3. 可逆：--restore 把 archive 里的表搬回 public。
#
# 策略与逐表分级见 docs/refactor/UNINSTALL-POLICY.md（两处必须一致）。
# 用法：
#   python3 scripts/uninstall_plugin_data.py --list
#   python3 scripts/uninstall_plugin_data.py --plugin intel            # dry-run
#   python3 scripts/uninstall_plugin_data.py --plugin intel --apply --yes
#   python3 scripts/uninstall_plugin_data.py --plugin intel --restore --yes
import argparse
import subprocess
import sys

CONTAINER = "findu-postgres"

# 分级：archive=搬到 archive schema；keep=保留（用户配置）；keep_audit=保留（审计留痕）
PLUGINS = {
    "intel": [
        ("fingerprint_rules", "archive", "指纹规则可由插件包重建"),
        ("vuln_kb", "archive", "漏洞库可重新导入"),
        ("vuln_intel", "archive", "情报可重新拉取"),
        ("fingerprint_sources", "archive", "源配置随插件包带出"),
    ],
    "hitl": [
        ("intercept_rules", "keep", "运维手配的拦截规则"),
        ("intercept_pending", "archive", "运行期待办"),
    ],
    "c2": [
        ("c2_events", "keep_audit", "安全事件留痕，默认保留"),
    ],
    "mcp": [
        ("mcp_servers", "keep", "用户手工配的 MCP 服务端"),
        ("mcp_tools_cache", "keep", "从属缓存，随主表 CASCADE"),
    ],
    "notify": [
        ("robot_binding_codes", "keep", "一次性绑定码"),
        ("robot_user_bindings", "keep", "身份绑定关系"),
        ("robot_user_sessions", "archive", "会话过期即失效"),
    ],
}


def psql(db, sql, tuples_only=False):
    argv = ["docker", "exec", CONTAINER, "psql", "-U", "findu", "-d", db, "-v", "ON_ERROR_STOP=1"]
    if tuples_only:
        argv += ["-t", "-A"]
    argv += ["-c", sql]
    r = subprocess.run(argv, capture_output=True, text=True)
    if r.returncode != 0:
        sys.exit("psql 失败: " + (r.stderr or r.stdout).strip())
    return r.stdout.strip()


def plan(entries, restore):
    stmts, skipped = [], []
    for table, action, why in entries:
        if action.startswith("keep"):
            skipped.append((table, action, why))
            continue
        if restore:
            stmts.append("ALTER TABLE IF EXISTS archive.%s SET SCHEMA public" % table)
        else:
            stmts.append("ALTER TABLE IF EXISTS public.%s SET SCHEMA archive" % table)
    return stmts, skipped


def main():
    ap = argparse.ArgumentParser(add_help=True)
    ap.add_argument("--list", action="store_true", help="列出分级")
    ap.add_argument("--plugin", choices=sorted(PLUGINS), help="按插件批量处置")
    ap.add_argument("--table", help="只处置一张表")
    ap.add_argument("--db", default="findu", help="目标库（默认 findu）")
    ap.add_argument("--apply", action="store_true", help="真正执行（默认 dry-run）")
    ap.add_argument("--yes", action="store_true", help="二次确认")
    ap.add_argument("--restore", action="store_true", help="把 archive 里的表搬回 public")
    a = ap.parse_args()

    if a.list or (not a.plugin and not a.table):
        for p, entries in PLUGINS.items():
            print("[%s]" % p)
            for t, act, why in entries:
                print("   %-22s %-11s %s" % (t, act, why))
        return 0

    entries = []
    if a.plugin:
        entries = PLUGINS[a.plugin]
    if a.table:
        for p, es in PLUGINS.items():
            for row in es:
                if row[0] == a.table:
                    entries = [row]
                    break
        if not entries:
            sys.exit("未知表：%s（用 --list 看清单）" % a.table)

    stmts, skipped = plan(entries, a.restore)
    mode = "RESTORE" if a.restore else "UNINSTALL"
    print("== %s  db=%s  模式=%s" % (mode, a.db, "apply" if a.apply else "dry-run"))
    for t, act, why in skipped:
        print("   跳过 %-22s %-11s %s" % (t, act, why))
    if not stmts:
        print("   没有需要搬动的表（全部为保留级别）")
        return 0

    if not a.restore:
        stmts = ["CREATE SCHEMA IF NOT EXISTS archive"] + stmts

    for s in stmts:
        print("   SQL: " + s)

    # 目标表是否存在，提前报出来，避免 apply 时才发现拼错表名
    for t, act, why in entries:
        if act.startswith("keep"):
            continue
        nsp = "archive" if a.restore else "public"
        exists = psql(a.db, "SELECT to_regclass('%s.%s') IS NOT NULL" % (nsp, t), tuples_only=True)
        print("   检查 %s.%s 存在=%s" % (nsp, t, exists))

    if not a.apply:
        print("== dry-run：未改动数据库。真执行请加 --apply --yes")
        return 0
    if not a.yes:
        sys.exit("拒绝执行：--apply 必须同时给 --yes（这是生产库操作）")

    for s in stmts:
        psql(a.db, s)
        print("   OK: " + s)
    print("== 完成。回滚：--plugin %s --restore --apply --yes" % (a.plugin or "-"))
    return 0


if __name__ == "__main__":
    sys.exit(main())
