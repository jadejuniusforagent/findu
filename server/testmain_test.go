package server

import (
	"database/sql"
	"finduai/core/config"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// guardTestDatabase 拒绝在非 _test 库上跑 server 测试套件。
//
// 事故背景（2026-10-19）：server 包的 chat_llm_resolve_test.go 需要「无 LLM
// profile」的初始状态，会遍历 llm_profiles 删除全部行。该用例在生产库
// （findu）上跑过一次，把唯一的 DeepSeek v4 Flash profile 删掉，引擎随之
// 进入 idle，重启日志变成 "no LLM provider configured"。
//
// 因此库名必须以 _test 结尾，否则整个 server 包直接退出，一个用例都不跑。
func guardTestDatabase(dsn string) {
	u, err := url.Parse(dsn)
	if err != nil {
		return // 解析不了就交给 db.Open 去报错
	}
	name := strings.TrimPrefix(u.Path, "/")
	if strings.HasSuffix(name, "_test") {
		return
	}
	fmt.Fprintf(os.Stderr, "[FATAL] 拒绝在非测试库 %q 上运行 server 测试：\n"+
		"该套件包含删除 llm_profiles 全表的用例，跑在生产库会清掉 LLM 配置。\n"+
		"请把 FINDU_PG_DSN 指向以 _test 结尾的库（如 findu_test）后重试。\n", name)
	os.Exit(1)
}

// TestMain acquires a PostgreSQL advisory lock (7337741002) for the entire
// server test suite so cross-package DELETE cleanup races with db/agent
// packages are avoided when running `go test ./...`.
func TestMain(m *testing.M) {
	dsn, _, err := config.PostgresDSN()
	if err != nil {
		os.Exit(m.Run())
	}
	guardTestDatabase(dsn)
	conn, err := sql.Open("pgx", dsn)
	if err != nil || conn.Ping() != nil {
		os.Exit(m.Run())
	}
	defer conn.Close()
	if _, err := conn.Exec(`SELECT pg_advisory_lock(7337741002)`); err != nil {
		os.Exit(m.Run())
	}
	defer conn.Exec(`SELECT pg_advisory_unlock(7337741002)`) //nolint:errcheck
	os.Exit(m.Run())
}
