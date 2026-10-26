package database

// 本文件是 P4「数据层收敛」的地基：让 findu 的 SQLite 方言代码原样跑在 PG 上。
//
// 设计取舍（为什么不在 500+ 个调用点逐个改 SQL）：
//   - internal/database 里 515 处 Exec/Query 调用、96 处事务内调用、27 处 Begin。
//     逐处改写等于把 15K 行全部重写一遍，且每次改动都可能引入新错。
//   - 改在 driver 层：所有 SQL 在交给 pgx 之前统一过一遍方言重写，
//     调用点零改动，事务内（走同一个 Conn）同样覆盖。
//
// 只处理确定无歧义的语法差异；语义需要判断的（INSERT OR REPLACE 要知道主键）
// 不在这里硬猜，留给调用点手改——猜错会静默丢数据，比报错更糟。

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"regexp"
	"strings"
	"sync"

	pgxstdlib "github.com/jackc/pgx/v5/stdlib"
)

// PGDriverName 是注册给 database/sql 的重写驱动名。
const PGDriverName = "findu-pg"

func init() {
	sql.Register(PGDriverName, &dialectDriver{base: pgxstdlib.GetDefaultDriver()})
}

// dialectDriver 把 pgx 驱动包一层，在 Open 时套上 SQL 重写。
type dialectDriver struct{ base driver.Driver }

func (d *dialectDriver) Open(name string) (driver.Conn, error) {
	c, err := d.base.Open(name)
	if err != nil {
		return nil, err
	}
	return &dialectConn{Conn: c}, nil
}

// dialectConn 重写所有出站 SQL。其余能力全部透传给 pgx，
// 尤其是 NamedValueChecker —— 丢掉它会让 time.Time / nil / []byte 的参数处理退化。
type dialectConn struct{ driver.Conn }

func (c *dialectConn) Prepare(query string) (driver.Stmt, error) {
	return c.Conn.Prepare(RewriteSQLSQLiteToPG(query))
}

func (c *dialectConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	q := RewriteSQLSQLiteToPG(query)
	if p, ok := c.Conn.(driver.ConnPrepareContext); ok {
		return p.PrepareContext(ctx, q)
	}
	return c.Conn.Prepare(q)
}

func (c *dialectConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	q := RewriteSQLSQLiteToPG(query)
	if e, ok := c.Conn.(driver.ExecerContext); ok {
		return e.ExecContext(ctx, q, args)
	}
	return nil, driver.ErrSkip
}

func (c *dialectConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	q := RewriteSQLSQLiteToPG(query)
	if qr, ok := c.Conn.(driver.QueryerContext); ok {
		return qr.QueryContext(ctx, q, args)
	}
	return nil, driver.ErrSkip
}

func (c *dialectConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if b, ok := c.Conn.(driver.ConnBeginTx); ok {
		return b.BeginTx(ctx, opts)
	}
	return c.Conn.Begin()
}

// CheckNamedValue 必须透传：pgx 靠它把 time.Time、nil、[]byte、int 转成正确的 PG 类型。
func (c *dialectConn) CheckNamedValue(nv *driver.NamedValue) error {
	if ck, ok := c.Conn.(driver.NamedValueChecker); ok {
		return ck.CheckNamedValue(nv)
	}
	return driver.ErrSkip
}

func (c *dialectConn) Ping(ctx context.Context) error {
	if p, ok := c.Conn.(driver.Pinger); ok {
		return p.Ping(ctx)
	}
	return nil
}

func (c *dialectConn) ResetSession(ctx context.Context) error {
	if r, ok := c.Conn.(driver.SessionResetter); ok {
		return r.ResetSession(ctx)
	}
	return nil
}

func (c *dialectConn) IsValid() bool {
	if v, ok := c.Conn.(driver.Validator); ok {
		return v.IsValid()
	}
	return true
}

// ---------- 方言重写 ----------

var (
	reDatetimeNowOffset = regexp.MustCompile(`(?i)datetime\(\s*'now'\s*,\s*'(-?\d+)\s+(day|days|hour|hours|minute|minutes)'\s*\)`)
	reDatetimeNow       = regexp.MustCompile(`(?i)datetime\(\s*'now'\s*\)`)
	reAutoinc           = regexp.MustCompile(`(?i)INTEGER\s+PRIMARY\s+KEY\s+AUTOINCREMENT`)
	reDatetimeType      = regexp.MustCompile(`(?i)\bDATETIME\b`)
	reBlobType          = regexp.MustCompile(`(?i)\bBLOB\b`)
	reBoolDefault0      = regexp.MustCompile(`(?i)(\bBOOLEAN\b[^,\n]*?\bDEFAULT\s+)0\b`)
	reBoolDefault1      = regexp.MustCompile(`(?i)(\bBOOLEAN\b[^,\n]*?\bDEFAULT\s+)1\b`)

	// SQLite 的元数据表值函数 → PG 的 information_schema。
	// 用在 30+ 处「给旧库补列」的迁移函数里；必须先于占位符重写处理，否则
	// pragma_table_info(?) 里的 ? 已经变成 $1，与捕获组混淆。
	rePragmaColumnsWhere = regexp.MustCompile(`(?i)FROM\s+pragma_table_info\s*\(([^)]*)\)\s+WHERE\s+name\s*=`)
	rePragmaColumns      = regexp.MustCompile(`(?i)FROM\s+pragma_table_info\s*\(([^)]*)\)`)
	reSqliteMaster       = regexp.MustCompile(`(?i)FROM\s+sqlite_master\s+WHERE\s+type\s*=\s*'table'\s+AND\s+name\s*=\s*('[^']*')`)
	// PRAGMA foreign_key_list(t) 是整条语句式的元数据查询，不是表值函数，
	// 单独处理：改写后仍是完整 SQL，直接返回。
	rePragmaFKList = regexp.MustCompile(`(?i)PRAGMA\s+foreign_key_list\s*\(\s*'?([a-zA-Z_][a-zA-Z0-9_]*)'?\s*\)`)

	// SQLite 的隐式 rowid（插入序）→ PG 的 ctid（物理行位置）。
	// 只用于 ORDER BY 平手排序：findu 里 19 处都是这个用法。
	// 取值比较（GetProcessDetailOffset）不能靠 ctid，那里已改成只按 created_at 比较。
	reRowid = regexp.MustCompile(`(?i)([a-zA-Z_][a-zA-Z0-9_]*\.)?rowid\b`)
)

// RewriteSQLSQLiteToPG 把 SQLite 方言的 SQL 改写成 PG 方言。
// 纯函数、无状态，方便单测；对 PG 已经合法的 SQL 是恒等变换。
func RewriteSQLSQLiteToPG(q string) string {
	if q == "" {
		return q
	}
	out := rewriteMetadataSQL(q)
	out = rewritePlaceholders(out)
	out = rewriteInsertOrIgnore(out)
	out = reDatetimeNowOffset.ReplaceAllString(out, "(now() - interval '$1 $2')")
	out = reDatetimeNow.ReplaceAllString(out, "now()")
	// datetime(...) 必须先于 reDatetimeType 处理：后者会把残留的 DATETIME 字样
	// 当作列类型改成 TIMESTAMPTZ，若先跑就会把 datetime(x) 误伤成 TIMESTAMPTZ(x)，
	// PG 侧报 "function timestamptz(unknown) is not unique"。
	out = rewriteDatetimeCalls(out)
	out = rewriteStrftime(out)
	out = rewriteJulianday(out)
	out = rewriteInstr(out)
	out = rewriteDateCalls(out)
	out = rewriteScalarExtremum(out)
	out = reAutoinc.ReplaceAllString(out, "BIGSERIAL PRIMARY KEY")
	out = reDatetimeType.ReplaceAllString(out, "TIMESTAMPTZ")
	out = reBlobType.ReplaceAllString(out, "BYTEA")
	out = reBoolDefault0.ReplaceAllString(out, "${1}FALSE")
	out = reBoolDefault1.ReplaceAllString(out, "${1}TRUE")
	out = reRowid.ReplaceAllString(out, "${1}ctid")
	out = deferForeignKeys(out)
	return out
}

// ---------- 外键延后建立 ----------
//
// SQLite 建表时不检查被引用表是否存在，PG 会检查。findu 的 50 张表里 42 处外键，
// 引用顺序是乱的（model_token_usage 引用 projects，但 projects 在它后面才建）。
//
// 而且外键不能一剥了之：代码明确依赖 ON DELETE CASCADE 来清理会话级联数据
// （conversation.go 里有注释直说删对话靠级联）。所以做法是「先剥后补」：
//   1. 建表时把 FOREIGN KEY 子句摘掉，收集成 ALTER 语句
//   2. 所有表建完后 applyDeferredForeignKeys 统一补回
// 这样既避开建表顺序，又保留级联语义。

var (
	reCreateTable   = regexp.MustCompile(`(?is)^\s*CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-zA-Z_][a-zA-Z0-9_]*)`)
	reForeignKey    = regexp.MustCompile(`(?is)\s*FOREIGN\s+KEY\s*\(([^)]*)\)\s*REFERENCES\s+([a-zA-Z_][a-zA-Z0-9_]*)\s*\(([^)]*)\)(\s+ON\s+DELETE\s+(?:CASCADE|SET\s+NULL|SET\s+DEFAULT|RESTRICT|NO\s+ACTION))?(\s+ON\s+UPDATE\s+(?:CASCADE|SET\s+NULL|SET\s+DEFAULT|RESTRICT|NO\s+ACTION))?\s*,?`)
	reDanglingComma = regexp.MustCompile(`,\s*\)`)

	fkMu        sync.Mutex
	deferredFKs []string
)

// deferForeignKeys 从 CREATE TABLE 里摘出外键子句，转成事后的 ALTER 语句。
// 非建表语句原样返回。
func deferForeignKeys(q string) string {
	m := reCreateTable.FindStringSubmatch(q)
	if m == nil {
		return q
	}
	if !strings.Contains(strings.ToUpper(q), "FOREIGN KEY") {
		return q
	}
	table := m[1]
	var stmts []string
	out := reForeignKey.ReplaceAllStringFunc(q, func(clause string) string {
		sub := reForeignKey.FindStringSubmatch(clause)
		if sub == nil {
			return clause
		}
		cols := strings.TrimSpace(sub[1])
		refTable := sub[2]
		refCols := strings.TrimSpace(sub[3])
		onDelete := strings.TrimSpace(sub[4])
		onUpdate := strings.TrimSpace(sub[5])
		name := "fk_" + table + "_" + strings.ReplaceAll(strings.ReplaceAll(cols, " ", ""), ",", "_")
		if len(name) > 60 {
			name = name[:60]
		}
		stmt := "ALTER TABLE " + table + " ADD CONSTRAINT " + strings.ToLower(name) +
			" FOREIGN KEY (" + cols + ") REFERENCES " + refTable + " (" + refCols + ")"
		if onDelete != "" {
			stmt += " " + onDelete
		}
		if onUpdate != "" {
			stmt += " " + onUpdate
		}
		stmts = append(stmts, stmt)
		return ""
	})
	if len(stmts) == 0 {
		return q
	}
	fkMu.Lock()
	deferredFKs = append(deferredFKs, stmts...)
	fkMu.Unlock()
	// 摘掉子句后可能留下 `, )` 这种悬空逗号
	return reDanglingComma.ReplaceAllString(out, ")")
}

// applyDeferredForeignKeys 在全部建表完成后补齐外键。幂等：约束已存在就跳过。
func applyDeferredForeignKeys(db *sql.DB) error {
	fkMu.Lock()
	stmts := append([]string(nil), deferredFKs...)
	fkMu.Unlock()
	for _, stmt := range stmts {
		// duplicate_object：重复执行时约束已存在，属正常
		wrapped := "DO $$ BEGIN " + stmt + "; EXCEPTION WHEN duplicate_object THEN NULL; END $$"
		if _, err := db.Exec(wrapped); err != nil {
			return fmt.Errorf("补外键失败 (%s): %w", stmt, err)
		}
	}
	return nil
}

// rewriteMetadataSQL 把 SQLite 的元数据查询翻成 information_schema。
// 覆盖 findu 里实际出现的两种写法：
//
//	SELECT COUNT(*) FROM pragma_table_info('t') WHERE name='c'
//	SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?
//	SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='t'
func rewriteMetadataSQL(q string) string {
	// PRAGMA foreign_key_list 是整条语句，改写完就结束，不参与后面的表值函数替换。
	if lower := strings.ToLower(q); strings.Contains(lower, "pragma") && strings.Contains(lower, "foreign_key_list") {
		return rePragmaFKList.ReplaceAllString(q, fkListSQL)
	}
	if !strings.Contains(q, "pragma_table_info") && !strings.Contains(q, "sqlite_master") {
		return q
	}
	q = rePragmaColumnsWhere.ReplaceAllString(q,
		"FROM information_schema.columns WHERE table_schema=current_schema() AND table_name = $1 AND column_name =")
	q = rePragmaColumns.ReplaceAllString(q,
		"FROM information_schema.columns WHERE table_schema=current_schema() AND table_name = $1")
	q = reSqliteMaster.ReplaceAllString(q,
		"FROM information_schema.tables WHERE table_schema=current_schema() AND table_type='BASE TABLE' AND table_name=$1")
	return q
}

// rewritePlaceholders 把 ? 改成 $N，跳过字符串字面量、双引号标识符与注释里的 ?。
func rewritePlaceholders(q string) string {
	if !strings.Contains(q, "?") {
		return q
	}
	var b strings.Builder
	b.Grow(len(q) + 8)
	n := 0
	inSingle, inDouble, inLine, inBlock := false, false, false, false
	for i := 0; i < len(q); i++ {
		c := q[i]
		switch {
		case inLine:
			b.WriteByte(c)
			if c == '\n' {
				inLine = false
			}
			continue
		case inBlock:
			b.WriteByte(c)
			if c == '*' && i+1 < len(q) && q[i+1] == '/' {
				b.WriteByte('/')
				i++
				inBlock = false
			}
			continue
		case inSingle:
			b.WriteByte(c)
			if c == '\'' {
				if i+1 < len(q) && q[i+1] == '\'' { // 转义的单引号
					b.WriteByte('\'')
					i++
				} else {
					inSingle = false
				}
			}
			continue
		case inDouble:
			b.WriteByte(c)
			if c == '"' {
				inDouble = false
			}
			continue
		}
		switch c {
		case '\'':
			inSingle = true
			b.WriteByte(c)
		case '"':
			inDouble = true
			b.WriteByte(c)
		case '-':
			if i+1 < len(q) && q[i+1] == '-' {
				inLine = true
				b.WriteString("--")
				i++
			} else {
				b.WriteByte(c)
			}
		case '/':
			if i+1 < len(q) && q[i+1] == '*' {
				inBlock = true
				b.WriteString("/*")
				i++
			} else {
				b.WriteByte(c)
			}
		case '?':
			n++
			b.WriteString(fmt.Sprintf("$%d", n))
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// rewriteInsertOrIgnore 把 INSERT OR IGNORE 变成 INSERT ... ON CONFLICT DO NOTHING。
// 语义等价于 SQLite 的「冲突就跳过这一行」。
func rewriteInsertOrIgnore(q string) string {
	up := strings.ToUpper(q)
	idx := strings.Index(up, "INSERT OR IGNORE")
	if idx < 0 {
		return q
	}
	q = q[:idx] + "INSERT" + q[idx+len("INSERT OR IGNORE"):]
	// 已有 ON CONFLICT 的不重复加
	if strings.Contains(strings.ToUpper(q), "ON CONFLICT") {
		return q
	}
	trimmed := strings.TrimRight(q, " \t\r\n")
	suffix := q[len(trimmed):]
	if strings.HasSuffix(trimmed, ";") {
		return strings.TrimSuffix(trimmed, ";") + " ON CONFLICT DO NOTHING;" + suffix
	}
	return trimmed + " ON CONFLICT DO NOTHING" + suffix
}

// ---------- 函数级改写 ----------
//
// SQLite 的日期/字符串函数在 PG 里要么不存在、要么语义不同。调用点的参数可能是
// 占位符（? 在上一步已经变成 $N），也可能是嵌套表达式（COALESCE(...)、CAST(...)），
// 正则匹配不了嵌套括号，所以这里用一个最小的「找调用 + 按顶层逗号切参数」扫描器。

type callSite struct {
	start, end int
	args       []string
}

// findCalls 找出 q 中所有 fn(...) 调用的位置与参数（参数保持原文）。
func findCalls(q, fn string) []callSite {
	var out []callSite
	lower, needle := strings.ToLower(q), strings.ToLower(fn)
	i := 0
	for {
		idx := strings.Index(lower[i:], needle)
		if idx < 0 {
			return out
		}
		idx += i
		// 左边界：前一字符不能是标识符字符，避免把 updated_at / validate 里的片段当成函数名。
		if idx > 0 {
			c := lower[idx-1]
			if c == '_' || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
				i = idx + len(needle)
				continue
			}
		}
		j := idx + len(needle)
		for j < len(q) && (q[j] == ' ' || q[j] == '\t' || q[j] == '\n' || q[j] == '\r') {
			j++
		}
		if j >= len(q) || q[j] != '(' {
			i = idx + len(needle)
			continue
		}
		depth, k, inStr := 0, j, false
		for ; k < len(q); k++ {
			c := q[k]
			if inStr {
				if c == '\'' {
					if k+1 < len(q) && q[k+1] == '\'' {
						k++
					} else {
						inStr = false
					}
				}
				continue
			}
			switch c {
			case '\'':
				inStr = true
			case '(':
				depth++
			case ')':
				depth--
			}
			if depth == 0 {
				break
			}
		}
		if k >= len(q) {
			return out
		}
		out = append(out, callSite{start: idx, end: k + 1, args: splitTopLevel(q[j+1 : k])})
		i = k + 1
	}
}

// splitTopLevel 按顶层逗号切分参数（忽略括号内与字符串内的逗号）。
func splitTopLevel(s string) []string {
	var parts []string
	depth, start, inStr := 0, 0, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr {
			if c == '\'' {
				if i+1 < len(s) && s[i+1] == '\'' {
					i++
				} else {
					inStr = false
				}
			}
			continue
		}
		switch c {
		case '\'':
			inStr = true
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	parts = append(parts, strings.TrimSpace(s[start:]))
	return parts
}

// applyCalls 从右往左替换，避免前面的替换让后面的位置失效。
func applyCalls(q, fn string, f func(args []string) (string, bool)) string {
	sites := findCalls(q, fn)
	for i := len(sites) - 1; i >= 0; i-- {
		s := sites[i]
		if rep, ok := f(s.args); ok {
			q = q[:s.start] + rep + q[s.end:]
		}
	}
	return q
}

// rewriteStrftime 处理 SQLite 的 strftime 两种实际用法：
//
//	strftime('%s', x)                 -> (extract(epoch from (x)::timestamptz))::bigint
//	strftime('%Y-%m-%d %H:00:00', x[, 'localtime']) -> to_char(x, 'YYYY-MM-DD HH24:00:00')
//
// 参数可能是列名、$N 占位符，所以不用正则（正则匹配不了占位符与嵌套表达式），
// 走统一的调用扫描器。
func rewriteStrftime(q string) string {
	return applyCalls(q, "strftime", func(args []string) (string, bool) {
		if len(args) < 2 {
			return "", false
		}
		format := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(args[0]), " ", ""))
		switch format {
		case "'%s'":
			return "(extract(epoch from (" + args[1] + ")::timestamptz))::bigint", true
		case "'%y-%m-%d%h:00:00'":
			return "to_char(" + args[1] + ", 'YYYY-MM-DD HH24:00:00')", true
		}
		return "", false
	})
}

// rewriteDatetimeCalls 把 SQLite 的 datetime(x) 改成 PG 的 ::timestamptz。
//
//	datetime(col)            -> (col)::timestamptz
//	datetime(COALESCE(a, b)) -> (COALESCE(a, b))::timestamptz
//	datetime(?)              -> ($1)::timestamptz
//	datetime('now', ?)       -> (now() + ($1)::interval)   -- 参数形如 "-7 days"
//
// 字面量形式 datetime('now') / datetime('now','-7 days') 已在前面的正则里改完。
func rewriteDatetimeCalls(q string) string {
	return applyCalls(q, "datetime", func(args []string) (string, bool) {
		if len(args) == 0 {
			return "", false
		}
		if len(args) >= 2 && strings.EqualFold(strings.TrimSpace(args[0]), "'now'") {
			return "(now() + (" + args[1] + ")::interval)", true
		}
		return "(" + args[0] + ")::timestamptz", true
	})
}

// rewriteDateCalls 把 date(x) / date(x,'localtime') 改成 PG 的 ::date。
// localtime 的时区差异在这里忽略：分桶按 PG 服务端时区的日界，与迁移后的存储一致。
func rewriteDateCalls(q string) string {
	return applyCalls(q, "date", func(args []string) (string, bool) {
		if len(args) == 0 {
			return "", false
		}
		return "(" + args[0] + ")::date", true
	})
}

// rewriteJulianday 用儒略日保持量纲：SQLite 里 julianday 差值乘 86400000 得毫秒，
// 改成 epoch 秒除以 86400 后，外层算式一行都不用改仍然成立。
func rewriteJulianday(q string) string {
	return applyCalls(q, "julianday", func(args []string) (string, bool) {
		if len(args) == 0 {
			return "", false
		}
		return "(extract(epoch from (" + args[0] + ")::timestamptz) / 86400.0)", true
	})
}

// rewriteInstr 改成 PG 的 strpos，参数顺序相同。
func rewriteInstr(q string) string {
	return applyCalls(q, "instr", func(args []string) (string, bool) {
		if len(args) != 2 {
			return "", false
		}
		return "STRPOS(" + args[0] + ", " + args[1] + ")", true
	})
}

// rewriteScalarExtremum 只改双参数形式：SQLite 的 MAX(a,b)/MIN(a,b) 是标量函数，
// PG 里没有对应，用 GREATEST/LEAST。单参数的 MAX(col)/MIN(col) 是聚合，必须原样保留。
func rewriteScalarExtremum(q string) string {
	q = applyCalls(q, "max", func(args []string) (string, bool) {
		if len(args) != 2 {
			return "", false
		}
		return "GREATEST(" + args[0] + ", " + args[1] + ")", true
	})
	return applyCalls(q, "min", func(args []string) (string, bool) {
		if len(args) != 2 {
			return "", false
		}
		return "LEAST(" + args[0] + ", " + args[1] + ")", true
	})
}

// fkListSQL 用 PG 的约束视图模拟 SQLite 的 PRAGMA foreign_key_list(t)，
// 列顺序与含义必须与 SQLite 一致，调用方按 id/seq/table/from/to/on_update/on_delete/match 扫描。
const fkListSQL = `SELECT 1 AS id, 1 AS seq, ccu.table_name AS "table", kcu.column_name AS "from",
	ccu.column_name AS "to", rc.update_rule AS on_update, rc.delete_rule AS on_delete, 'NONE' AS match
	FROM information_schema.table_constraints tc
	JOIN information_schema.key_column_usage kcu
	  ON tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema
	JOIN information_schema.constraint_column_usage ccu
	  ON tc.constraint_name = ccu.constraint_name AND tc.table_schema = ccu.table_schema
	JOIN information_schema.referential_constraints rc
	  ON tc.constraint_name = rc.constraint_name AND tc.constraint_schema = rc.constraint_schema
	WHERE tc.constraint_type = 'FOREIGN KEY'
	  AND tc.table_schema = current_schema()
	  AND tc.table_name = '$1'`
