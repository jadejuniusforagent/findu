// Package migrations 以 go:embed 形式携带迁移脚本，供 core/migrate 执行。
// 目录结构：migrations/core/*.up.sql 与 *.down.sql —— 内核迁移；
// 插件迁移各自放在 plugins/<id>/migrations/，由插件自行 embed。
package migrations

import "embed"

// CoreFS 是内核迁移脚本集合。
//
//go:embed core/*.sql
var CoreFS embed.FS
