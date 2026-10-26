package app

import (
	"path/filepath"
	"testing"

	"finduai/internal/config"
	"finduai/internal/logger"
)

// TestNewBootSmoke 启动冒烟：真实走完 app.New 的完整装配（建表、迁移、RBAC、
// handler 组装、路由注册）。它是组合根任何重构的安全网 —— 没有它，装配路径的
// 回归只能靠人工启动服务才发现。
//
// 隔离：数据库落在 t.TempDir() 的一次性 SQLite 文件，绝不触碰生产库；
// -short 下跳过（CI 快速档）。
//
// 来源：FindU 同上游 fork 的 internal/app/app_smoke_test.go（其版为 PostgreSQL，
// 本项目为 SQLite，故改为临时文件库 + t.Chdir 到仓库根取模板/静态资源）。
func TestNewBootSmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping boot smoke in -short")
	}

	dir := t.TempDir()
	// 路由注册的模板/静态资源用仓库根相对路径（web/templates、web/static）；
	// go test 的 CWD 是包目录，故切到仓库根（t.Chdir 在测试结束自动还原）。
	t.Chdir("../..")

	cfg := &config.Config{}
	cfg.Database.Path = filepath.Join(dir, "boot_smoke.db")
	cfg.Log.Level = "warn"

	cfgPath := filepath.Join(dir, "config.yaml")
	log := logger.New("warn", "")

	a, err := New(cfg, log, cfgPath)
	if err != nil {
		t.Fatalf("app.New 启动失败: %v", err)
	}
	if a == nil {
		t.Fatal("app.New 返回 nil 且无错误")
	}
	if a.db == nil || a.router == nil || a.auth == nil {
		t.Fatal("装配产物不完整：db/router/auth 均不可为空")
	}
	defer a.db.Close()

	// 组合根必须真正跑完迁移：关键表可查询即证明 database.NewDB 的建表/迁移链生效。
	for _, table := range []string{
		"conversations", "messages", "tool_executions",
		"audit_logs", "skill_stats", "projects", "vulnerabilities",
	} {
		var n int
		if err := a.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
			t.Errorf("迁移未生效：表 %s 不可查询: %v", table, err)
		}
	}
}
