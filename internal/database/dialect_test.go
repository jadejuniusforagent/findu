package database

import (
	"os"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func TestRewritePlaceholders(t *testing.T) {
	cases := []struct{ in, want string }{
		{"SELECT * FROM t WHERE a=? AND b=?",
			"SELECT * FROM t WHERE a=$1 AND b=$2"},
		// 字符串字面量里的 ? 不能当占位符
		{"SELECT * FROM t WHERE name LIKE '%?%' AND id=?",
			"SELECT * FROM t WHERE name LIKE '%?%' AND id=$1"},
		// 注释里的 ? 同理
		{"SELECT 1 -- what? \nWHERE id=?", "SELECT 1 -- what? \nWHERE id=$1"},
		// 没有 ? 的原样返回
		{"SELECT 1", "SELECT 1"},
		// 转义单引号不打断字面量状态
		{"SELECT 'it''s ?' , ?", "SELECT 'it''s ?' , $1"},
	}
	for _, c := range cases {
		if got := rewritePlaceholders(c.in); got != c.want {
			t.Errorf("rewritePlaceholders(%q)\n got: %q\nwant: %q", c.in, got, c.want)
		}
	}
}

func TestRewriteSQLiteSyntax(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"insert or ignore",
			"INSERT OR IGNORE INTO rbac_roles (id,name) VALUES (?,?)",
			"INSERT INTO rbac_roles (id,name) VALUES ($1,$2) ON CONFLICT DO NOTHING"},
		{"insert or ignore 带分号",
			"INSERT OR IGNORE INTO t (a) VALUES (?);",
			"INSERT INTO t (a) VALUES ($1) ON CONFLICT DO NOTHING;"},
		{"datetime now",
			"SELECT * FROM t WHERE created_at >= datetime('now')",
			"SELECT * FROM t WHERE created_at >= now()"},
		{"datetime 偏移",
			"SELECT * FROM t WHERE last_seen_at >= datetime('now','-7 days')",
			"SELECT * FROM t WHERE last_seen_at >= (now() - interval '-7 days')"},
		{"datetime 列",
			"SELECT * FROM t WHERE datetime(last_scan_at) < datetime('now')",
			"SELECT * FROM t WHERE (last_scan_at)::timestamptz < now()"},
		{"strftime epoch",
			"SELECT strftime('%s', created_at) FROM t",
			"SELECT (extract(epoch from (created_at)::timestamptz))::bigint FROM t"},
		{"strftime 分桶",
			"SELECT strftime('%Y-%m-%d %H:00:00', start_time, 'localtime') AS bucket FROM t",
			"SELECT to_char(start_time, 'YYYY-MM-DD HH24:00:00') AS bucket FROM t"},
		{"DDL 自增",
			"id INTEGER PRIMARY KEY AUTOINCREMENT,",
			"id BIGSERIAL PRIMARY KEY,"},
		{"DDL 类型",
			"created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, blob_col BLOB",
			"created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP, blob_col BYTEA"},
		{"DDL 布尔默认值",
			"enabled BOOLEAN NOT NULL DEFAULT 1, deleted BOOLEAN NOT NULL DEFAULT 0",
			"enabled BOOLEAN NOT NULL DEFAULT TRUE, deleted BOOLEAN NOT NULL DEFAULT FALSE"},
		// 已经是 PG 写法的语句必须原样通过
		{"PG 语句幂等",
			"SELECT * FROM t WHERE a=$1 ON CONFLICT DO NOTHING",
			"SELECT * FROM t WHERE a=$1 ON CONFLICT DO NOTHING"},
	}
	for _, c := range cases {
		if got := RewriteSQLSQLiteToPG(c.in); got != c.want {
			t.Errorf("%s:\n got: %q\nwant: %q", c.name, got, c.want)
		}
	}
}

// TestPGInitTables 在真 PG 上跑一遍 initTables，验证 50 张表的 DDL 全部可建。
// 需要 FINDU_DB_DSN + TEST_PG_INIT=1，默认跳过（不能误连生产库）。
func TestPGInitTables(t *testing.T) {
	if os.Getenv("TEST_PG_INIT") != "1" {
		t.Skip("设 TEST_PG_INIT=1 且提供 FINDU_DB_DSN 才跑")
	}
	dsn := strings.TrimSpace(os.Getenv("FINDU_DB_DSN"))
	if dsn == "" {
		t.Fatal("FINDU_DB_DSN 为空")
	}
	db, err := NewDB("", zap.NewNop())
	if err != nil {
		t.Fatalf("NewDB(PG) 失败: %v", err)
	}
	defer db.Close()
	if !IsPostgres() {
		t.Fatal("pgMode 未置位")
	}

	var tables int
	if err := db.QueryRow(`SELECT count(*) FROM information_schema.tables WHERE table_schema = 'findu'`).Scan(&tables); err != nil {
		t.Fatalf("统计表数失败: %v", err)
	}
	t.Logf("findu schema 建表数: %d", tables)
	if tables < 45 {
		t.Fatalf("建表数偏少: %d（预期约 50）", tables)
	}

	// 冒烟：写一条会话再读回来，覆盖 INSERT/SELECT/时间类型/布尔
	conv, err := db.CreateConversation("P4 冒烟", ConversationCreateMeta{})
	if err != nil {
		t.Fatalf("CreateConversation 失败: %v", err)
	}
	got, err := db.GetConversation(conv.ID)
	if err != nil {
		t.Fatalf("GetConversation 失败: %v", err)
	}
	if got == nil || got.ID != conv.ID {
		t.Fatalf("读回不一致: %+v", got)
	}
	if err := db.DeleteConversation(conv.ID); err != nil {
		t.Fatalf("DeleteConversation 失败: %v", err)
	}
	t.Log("PG 冒烟通过：建表 / 写入 / 读回 / 删除")
}

// TestRewriteSQLiteFunctions 覆盖 SQLite 专有函数在 PG 上的等价改写。
// 这批是在 P4 联调时按真实报错补的：strftime 占位符形式、julianday、
// datetime(嵌套表达式)、INSTR、date()、双参数 MAX/MIN。
func TestRewriteSQLiteFunctions(t *testing.T) {
	cases := []struct{ name, in, want string }{
		// 占位符形式的 epoch 比较（sqliteEpochGE 生成的就是这种）
		{"strftime epoch 占位符",
			"WHERE strftime('%s', start_time) < strftime('%s', ?)",
			"WHERE (extract(epoch from (start_time)::timestamptz))::bigint < (extract(epoch from ($1)::timestamptz))::bigint"},
		// CAST 包一层也要跟着走
		{"strftime 带 CAST",
			"COALESCE(CAST(strftime('%s', created_at) AS INTEGER), 0)",
			"COALESCE(CAST((extract(epoch from (created_at)::timestamptz))::bigint AS INTEGER), 0)"},
		// 动态偏移：datetime('now', ?) 的参数运行时是 "-7 days"
		{"datetime 动态偏移",
			"WHERE datetime(first_seen_at)>=datetime('now',?)",
			"WHERE (first_seen_at)::timestamptz>=(now() + ($1)::interval)"},
		// 嵌套表达式做参数，这是 reDatetimeType 误伤成 TIMESTAMPTZ(...) 的那个
		{"datetime 嵌套 COALESCE",
			"WHERE datetime(COALESCE(decided_at, created_at)) < datetime(?)",
			"WHERE (COALESCE(decided_at, created_at))::timestamptz < ($1)::timestamptz"},
		// julianday 保量纲：差值仍然乘 86400000 得毫秒
		{"julianday 差值",
			"duration_ms = MAX(0, CAST((julianday(?) - julianday(start_time)) * 86400000 AS INTEGER))",
			"duration_ms = GREATEST(0, CAST(((extract(epoch from ($1)::timestamptz) / 86400.0) - (extract(epoch from (start_time)::timestamptz) / 86400.0)) * 86400000 AS INTEGER))"},
		{"julianday BETWEEN",
			"AND julianday(start_time) BETWEEN julianday(?) AND julianday(?)",
			"AND (extract(epoch from (start_time)::timestamptz) / 86400.0) BETWEEN (extract(epoch from ($1)::timestamptz) / 86400.0) AND (extract(epoch from ($2)::timestamptz) / 86400.0)"},
		// 双参数 MAX 是标量函数，PG 用 GREATEST
		{"标量 MAX",
			"SET failed_calls = MAX(0, failed_calls - 1)",
			"SET failed_calls = GREATEST(0, failed_calls - 1)"},
		{"标量 MIN",
			"SET n = MIN(10, n + 1)",
			"SET n = LEAST(10, n + 1)"},
		// 单参数 MAX/MIN 是聚合，不能动
		{"聚合 MAX 不动",
			"SELECT MAX(start_time) AS last_call_time FROM t",
			"SELECT MAX(start_time) AS last_call_time FROM t"},
		{"聚合 MIN 不动",
			"SELECT MIN(created_at) FROM t",
			"SELECT MIN(created_at) FROM t"},
		// INSTR -> strpos，参数顺序相同
		{"INSTR",
			"WHEN INSTR(content, ?) > 0 THEN content",
			"WHEN STRPOS(content, $1) > 0 THEN content"},
		// date() -> ::date
		{"date 列",
			"SELECT date(first_seen_at), COUNT(*) FROM assets GROUP BY date(first_seen_at)",
			"SELECT (first_seen_at)::date, COUNT(*) FROM assets GROUP BY (first_seen_at)::date"},
		{"date 带 localtime",
			"SELECT date(start_time, 'localtime') AS bucket FROM t",
			"SELECT (start_time)::date AS bucket FROM t"},
		{"date 带表别名",
			"SELECT date(mtu.created_at) AS k FROM model_token_usage mtu",
			"SELECT (mtu.created_at)::date AS k FROM model_token_usage mtu"},
		// 不能误伤：update / validate 这类别名里含 date 的标识符
		{"不误伤 update 标识符",
			"UPDATE t SET updated_at = ? WHERE id = ?",
			"UPDATE t SET updated_at = $1 WHERE id = $2"},
	}
	for _, c := range cases {
		if got := RewriteSQLSQLiteToPG(c.in); got != c.want {
			t.Errorf("%s:\n got: %q\nwant: %q", c.name, got, c.want)
		}
	}
}

// TestRewritePragmaForeignKeyList 覆盖 PRAGMA foreign_key_list 的等价改写。
// 它是整条语句，改写后必须能直接跑，且保持 SQLite 的 8 列顺序。
func TestRewritePragmaForeignKeyList(t *testing.T) {
	got := RewriteSQLSQLiteToPG("PRAGMA foreign_key_list(vulnerabilities)")
	if strings.Contains(strings.ToUpper(got), "PRAGMA") {
		t.Fatalf("PRAGMA 未被改写: %q", got)
	}
	for _, want := range []string{"information_schema", "delete_rule", "'vulnerabilities'"} {
		if !strings.Contains(got, want) {
			t.Errorf("改写结果缺少 %q:\n%s", want, got)
		}
	}
	// 带引号的写法也要认
	got2 := RewriteSQLSQLiteToPG("PRAGMA foreign_key_list('vulnerabilities')")
	if strings.Contains(strings.ToUpper(got2), "PRAGMA") {
		t.Fatalf("引号形式未被改写: %q", got2)
	}
	// 普通 PRAGMA（SQLite 调优用）必须原样通过，不能误伤
	tune := "PRAGMA wal_autocheckpoint=1000"
	if out := RewriteSQLSQLiteToPG(tune); out != tune {
		t.Errorf("调优 PRAGMA 被误改: %q -> %q", tune, out)
	}
}
