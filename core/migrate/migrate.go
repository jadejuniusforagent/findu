// Package migrate 是最小可用的迁移执行器。
//
// 设计目标（见 docs/refactor/BASELINE.md 第七节）：
//
//	1 按文件名前缀版本号排序执行，失败即停，不继续后续迁移
//	2 每个迁移在自己的事务里执行，DDL 与版本记录同生共死
//	3 支持 down 回滚，回滚同样在一个事务里完成
//	4 已应用的版本记在 schema_migrations，可重复执行（幂等）
package migrate

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Migration 是一个版本的两段脚本。
type Migration struct {
	Version string
	Name    string
	Up      string
	Down    string
}

// Load 从 fsys 的 dir 目录读取 *.up.sql 与同名 *.down.sql，按版本升序返回。
// 文件名约定：<version>_<name>.up.sql，例如 0001_canonical.up.sql。
func Load(fsys fs.FS, dir string) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("migrate: 读取目录 %s: %w", dir, err)
	}

	ups := map[string]string{}
	downs := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		body, err := fs.ReadFile(fsys, dir+"/"+e.Name())
		if err != nil {
			return nil, fmt.Errorf("migrate: 读取 %s: %w", e.Name(), err)
		}
		switch {
		case strings.HasSuffix(e.Name(), ".up.sql"):
			ups[strings.TrimSuffix(e.Name(), ".up.sql")] = string(body)
		case strings.HasSuffix(e.Name(), ".down.sql"):
			downs[strings.TrimSuffix(e.Name(), ".down.sql")] = string(body)
		}
	}

	out := make([]Migration, 0, len(ups))
	for ver, up := range ups {
		out = append(out, Migration{
			Version: ver,
			Name:    strings.SplitN(ver, "_", 2)[0],
			Up:      up,
			Down:    downs[ver],
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

func ensureTable(ctx context.Context, conn *pgxpool.Pool) error {
	_, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
    version    text PRIMARY KEY,
    applied_at timestamptz NOT NULL DEFAULT now()
)`)
	return err
}

// Applied 返回已应用版本集合。
func Applied(ctx context.Context, pool *pgxpool.Pool) (map[string]time.Time, error) {
	if err := ensureTable(ctx, pool); err != nil {
		return nil, fmt.Errorf("migrate: 初始化版本表: %w", err)
	}
	rows, err := pool.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("migrate: 查询已应用版本: %w", err)
	}
	defer rows.Close()
	out := map[string]time.Time{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out[v] = time.Time{}
	}
	return out, rows.Err()
}

// Apply 顺序应用未执行的迁移，返回本次实际执行的版本列表。
func Apply(ctx context.Context, pool *pgxpool.Pool, ms []Migration) ([]string, error) {
	done, err := Applied(ctx, pool)
	if err != nil {
		return nil, err
	}
	var ran []string
	for _, m := range ms {
		if _, ok := done[m.Version]; ok {
			continue
		}
		if strings.TrimSpace(m.Up) == "" {
			return ran, fmt.Errorf("migrate: %s 缺少 up 脚本", m.Version)
		}
		if err := runOne(ctx, pool, m.Version, m.Up, true); err != nil {
			return ran, err
		}
		ran = append(ran, m.Version)
	}
	return ran, nil
}

// Revert 回滚指定版本（通常是最新一个），要求存在 down 脚本。
func Revert(ctx context.Context, pool *pgxpool.Pool, ms []Migration, version string) error {
	if err := ensureTable(ctx, pool); err != nil {
		return err
	}
	for _, m := range ms {
		if m.Version != version {
			continue
		}
		if strings.TrimSpace(m.Down) == "" {
			return fmt.Errorf("migrate: %s 缺少 down 脚本，拒绝回滚", version)
		}
		return runOne(ctx, pool, m.Version, m.Down, false)
	}
	return fmt.Errorf("migrate: 未找到版本 %s", version)
}

func runOne(ctx context.Context, pool *pgxpool.Pool, version, body string, record bool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("migrate: 开启事务 %s: %w", version, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, body); err != nil {
		return fmt.Errorf("migrate: 执行 %s 失败（已回滚）: %w", version, err)
	}
	if record {
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES ($1) ON CONFLICT (version) DO NOTHING`, version); err != nil {
			return fmt.Errorf("migrate: 记录 %s 失败: %w", version, err)
		}
	} else {
		if _, err := tx.Exec(ctx, `DELETE FROM schema_migrations WHERE version = $1`, version); err != nil {
			return fmt.Errorf("migrate: 移除 %s 版本记录失败: %w", version, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("migrate: 提交 %s 失败: %w", version, err)
	}
	return nil
}

// IsDestructive 判断迁移脚本是否包含破坏性语句（DROP TABLE / TRUNCATE）。
// 供运维入口在回滚前拦截：回滚一个会删表的迁移必须显式确认。
func IsDestructive(body string) bool {
	up := strings.ToUpper(body)
	return strings.Contains(up, "DROP TABLE") || strings.Contains(up, "TRUNCATE")
}

var _ = pgx.ErrNoRows
