#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""P4 数据迁移：findu 的 SQLite → PG(findu schema)。

设计原则：
  1. 只迁 PG 里存在的表，列取两边交集（SQLite 侧的历史列在 PG 新表里可能没有）
  2. 值转换交给 PG 自己解析（时间字符串直接传），只处理三件事：
     空串→NULL（非文本列）、0/1→bool（布尔列）、BLOB 原样走 bytea
  3. 幂等：先 TRUNCATE ... CASCADE 再灌，重复跑结果一致
  4. BIGSERIAL 表灌完要 setval，否则后续插入撞主键
"""
import os, sqlite3, sys
import psycopg2
from psycopg2.extras import execute_values

SRC = os.environ.get("SRC_DB", "/src/conversations.db")
DSN = os.environ["PG_DSN"]
SCHEMA = os.environ.get("PG_SCHEMA", "findu")

src = sqlite3.connect("file:%s?mode=ro" % SRC, uri=True)
src.row_factory = sqlite3.Row

# 老库里有 GBK 编码的中文（漏洞 evidence 等），默认按 UTF-8 解码会直接抛错。
# 先试 UTF-8，失败回退 GBK——比 utf-8/replace 更能保住原意。
def _decode(b):
    try:
        return b.decode("utf-8")
    except UnicodeDecodeError:
        try:
            return b.decode("gbk")
        except UnicodeDecodeError:
            return b.decode("utf-8", "replace")

src.text_factory = _decode
pg = psycopg2.connect(DSN)
pg.autocommit = False
cur = pg.cursor()

cur.execute("""select table_name from information_schema.tables
               where table_schema=%s and table_type='BASE TABLE'""", (SCHEMA,))
pg_tables = {r[0] for r in cur.fetchall()}

def pg_columns(t):
    cur.execute("""select column_name, data_type from information_schema.columns
                   where table_schema=%s and table_name=%s order by ordinal_position""", (SCHEMA, t))
    return cur.fetchall()

def sqlite_columns(t):
    return [r[1] for r in src.execute("PRAGMA table_info(%s)" % t).fetchall()]

tables = [r[0] for r in src.execute(
    "select name from sqlite_master where type='table' and name not like 'sqlite_%' order by name")]

migratable = [t for t in tables if t in pg_tables]
skipped = [t for t in tables if t not in pg_tables]

# 外键先摘后补：表与表的插入顺序不满足依赖（messages 早于 conversations），
# 逐个排拓扑序既脆弱又要维护；直接摘掉、灌完再原样加回，语义无损。
cur.execute("""select conrelid::regclass::text, conname, pg_get_constraintdef(oid)
               from pg_constraint
               where contype='f' and connamespace = %s::regnamespace""", (SCHEMA,))
fks = cur.fetchall()
for tbl, name, _ in fks:
    cur.execute("ALTER TABLE %s DROP CONSTRAINT IF EXISTS %s" % (tbl, name))
print("摘除外键 %d 个" % len(fks))

# 幂等清空（一次 CASCADE 覆盖全部外键依赖）
if migratable:
    cur.execute("TRUNCATE " + ", ".join('%s."%s"' % (SCHEMA, t) for t in migratable) + " CASCADE")

report, failures = [], []
for t in migratable:
    pgcols = pg_columns(t)
    pgtypes = {c: d for c, d in pgcols}
    cols = [c for c, _ in pgcols if c in set(sqlite_columns(t))]
    if not cols:
        failures.append((t, "无公共列"))
        continue
    rows = src.execute("select %s from \"%s\"" % (",".join('"%s"' % c for c in cols), t)).fetchall()
    if not rows:
        report.append((t, 0)); continue

    def conv(v, col):
        dt = pgtypes.get(col, "")
        if v is None: return None
        if isinstance(v, (bytes, bytearray)):
            return bytes(v)
        if dt == "boolean":
            return bool(v)
        if isinstance(v, str):
            if "\x00" in v:            # PG 的 text 不允许 NUL 字节
                v = v.replace("\x00", "")
            if v == "" and dt not in ("text", "character varying"):
                return None
        return v

    values = [tuple(conv(r[c], c) for c in cols) for r in rows]
    try:
        execute_values(cur, 'INSERT INTO %s."%s" (%s) VALUES %%s' % (
            SCHEMA, t, ", ".join('"%s"' % c for c in cols)), values, page_size=500)
        report.append((t, len(values)))
    except Exception as e:
        pg.rollback()
        failures.append((t, str(e)[:160]))
        # 重新开事务（TRUNCATE 已提交过则不需要）
        continue

# BIGSERIAL 修正
for t in migratable:
    cur.execute("""select column_name from information_schema.columns
                   where table_schema=%s and table_name=%s and column_default like 'nextval%%'""",
                (SCHEMA, t))
    for (col,) in cur.fetchall():
        cur.execute('SELECT setval(pg_get_serial_sequence(%s, %s), '
                    'COALESCE((SELECT max("%s") FROM %s."%s"), 1))' % (
                        "'%s.%s'" % (SCHEMA, t), "'%s'" % col, col, SCHEMA, t))

pg.commit()

restored, restore_fail = 0, []
for tbl, name, defn in fks:
    try:
        cur.execute("ALTER TABLE %s ADD CONSTRAINT %s %s" % (tbl, name, defn))
        restored += 1
    except Exception as e:
        pg.rollback()
        restore_fail.append((tbl, name, str(e).splitlines()[0][:120]))
pg.commit()

print("== 迁移结果 ==")
print("外键恢复: %d/%d" % (restored, len(fks)))
for t, n, e in restore_fail:
    print("  !! %s.%s %s" % (t, n, e))
print("源库: %s" % SRC)
print("表: 可迁 %d / 源 %d / PG 缺 %d" % (len(migratable), len(tables), len(skipped)))
total = 0
for t, n in report:
    total += n
    if n: print("  %-34s %d" % (t, n))
print("总行数: %d" % total)
if skipped: print("PG 里没有的表（跳过）: %s" % ", ".join(skipped))
if failures:
    print("!! 失败 %d 张:" % len(failures))
    for t, e in failures: print("  %-30s %s" % (t, e))
    sys.exit(1)
print("OK")
