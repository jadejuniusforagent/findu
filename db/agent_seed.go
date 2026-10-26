package db

// 内置可编辑 agent 的一次性种子（原 server/finding_retests.go 的 seedFindingRetester 事务）。
// 批 4 第一刀从 server 收到装配层：
//   - agents / agent_prompts / settings 三表一个事务（跨配置表）；
//   - advisory lock 串行化并发启动，settings 标志位保证只种一次；
//   - ON CONFLICT DO NOTHING + 用户编辑保护：已有行与用户改动在重启后存活。

import (
	"database/sql"
	"errors"
)

// SeedBuiltinAgent inserts an editable built-in agent and its first prompt
// atomically under an advisory lock, guarded by a one-time flag in settings.
// seeded=false means the flag was already set (or lost the race) and nothing was
// written; a pre-existing agent row is preserved as-is.
func (d *DB) SeedBuiltinAgent(key, name, description, role, promptText, flag string) (seeded bool, err error) {
	tx, err := d.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback() //nolint:errcheck
	// Lock this migration, including concurrent server initialization.
	if _, err = tx.Exec(`SELECT pg_advisory_xact_lock(7337741010)`); err != nil {
		return false, err
	}
	var done string
	err = tx.QueryRow(`SELECT value FROM settings WHERE key=$1`, flag).Scan(&done)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if done == "true" {
		return false, nil
	}
	var id int64
	err = tx.QueryRow(`INSERT INTO agents(key,name,description,role,builtin,enabled)
	VALUES ($1,$2,$3,$4,false,true)
	ON CONFLICT (key) DO NOTHING RETURNING id`, key, name, description, role).Scan(&id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if id > 0 {
		var pid int64
		if err = tx.QueryRow(`INSERT INTO agent_prompts(agent_id,version,template_text,note,updated_by)
		VALUES ($1,1,$2,'内置默认','system') RETURNING id`, id, promptText).Scan(&pid); err != nil {
			return false, err
		}
		if _, err = tx.Exec(`UPDATE agents SET current_prompt_id=$1 WHERE id=$2`, pid, id); err != nil {
			return false, err
		}
	}
	if _, err = tx.Exec(`INSERT INTO settings(key,value) VALUES ($1,'true') ON CONFLICT(key) DO UPDATE SET value='true'`, flag); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return id > 0, nil
}
