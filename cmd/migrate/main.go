// Command migrate 是 core/migrate 的运维入口。
//
// 用法：
//
//	migrate -status                          查询已应用版本
//	migrate -apply                           应用所有未执行迁移
//	migrate -revert 0002_fk_direction_fix    回滚指定版本
//
// DSN 取 -dsn；为空时读 FINDU_PG_DSN 环境变量。-dbname 可覆盖库名（演练库用）。
//
// 注意：不能用 pgx.ParseConfig 后回 ConnString 的方式改库名——ConnString 会
// 还原成原始串，覆盖失效（本命令早期版本就踩过，连到了主库还以为在演练库）。
// 必须 ParseConfig 后直接改 cfg.Database，再用 pgxpool.NewWithConfig。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"finduai/core/config"
	"finduai/core/migrate"
	"finduai/migrations"
)

func main() {
	var (
		dsn       = flag.String("dsn", "", "Postgres DSN，默认读 FINDU_PG_DSN")
		dbname    = flag.String("dbname", "", "覆盖 DSN 中的库名")
		dir       = flag.String("dir", "core", "迁移目录")
		apply     = flag.Bool("apply", false, "应用未执行迁移")
		status    = flag.Bool("status", false, "显示已应用版本")
		revert    = flag.String("revert", "", "回滚指定版本")
		dangerous = flag.Bool("allow-destructive", false, "允许执行含 DROP TABLE 的回滚（生产库需显式开启）")
	)
	flag.Parse()

	d := strings.TrimSpace(*dsn)
	if d == "" {
		v, src, err := config.PostgresDSN()
		if err != nil {
			log.Fatalf("缺少 DSN：用 -dsn 或设置 FINDU_PG_DSN（%v）", err)
		}
		d = v
		fmt.Printf("DSN 来源: %s\n", src)
	}

	cfg, err := pgxpool.ParseConfig(d)
	if err != nil {
		log.Fatalf("解析 DSN 失败: %v", err)
	}
	if *dbname != "" {
		cfg.ConnConfig.Database = *dbname
	}
	fmt.Printf("目标库: %s:%d/%s", cfg.ConnConfig.Host, cfg.ConnConfig.Port, cfg.ConnConfig.Database)
	fmt.Println()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		log.Fatalf("连接数据库失败: %v", err)
	}
	defer pool.Close()

	ms, err := migrate.Load(migrations.CoreFS, *dir)
	if err != nil {
		log.Fatalf("加载迁移失败: %v", err)
	}
	fmt.Printf("发现 %d 个迁移版本（目录 %s）", len(ms), *dir)
	fmt.Println()

	switch {
	case *status:
		done, err := migrate.Applied(ctx, pool)
		if err != nil {
			log.Fatalf("查询状态失败: %v", err)
		}
		if len(done) == 0 {
			fmt.Println("尚未应用任何迁移")
			return
		}
		names := make([]string, 0, len(done))
		for v := range done {
			names = append(names, v)
		}
		sort.Strings(names)
		for _, v := range names {
			fmt.Println("applied:", v)
		}
	case *apply:
		ran, err := migrate.Apply(ctx, pool, ms)
		if err != nil {
			log.Fatalf("应用迁移失败: %v", err)
		}
		if len(ran) == 0 {
			fmt.Println("无待应用迁移")
			return
		}
		for _, v := range ran {
			fmt.Println("applied:", v)
		}
	case *revert != "":
		for _, m := range ms {
			if m.Version != *revert {
				continue
			}
			if !*dangerous && migrate.IsDestructive(m.Down) {
				log.Fatalf("版本 %s 的 down 脚本含 DROP TABLE，拒绝执行；确认要回滚请加 -allow-destructive", m.Version)
			}
		}
		if err := migrate.Revert(ctx, pool, ms, *revert); err != nil {
			log.Fatalf("回滚失败: %v", err)
		}
		fmt.Println("reverted:", *revert)
	default:
		flag.Usage()
	}
}
