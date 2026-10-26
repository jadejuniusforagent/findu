package exploration

import "finduai/core/store"

// ToolUsage 是一次工具调用台账行。只记归属维度，工具参数与结果刻意不入账
// （避免把用户 payload 写进统计表）。
type ToolUsage struct {
	ToolKey       string `json:"tool_key"`
	AgentKey      string `json:"agent_key"`
	TaskID        int64  `json:"task_id"`
	ExplorationID int64  `json:"exploration_id"`
	IntentID      int64  `json:"intent_id"`
	SessionID     string `json:"session_id"`
}

// InsertToolUsage 追加一行。运行时把计量当 best-effort：统计失败不能打断工具本身。
func InsertToolUsage(q store.Querier, u *ToolUsage) error {
	_, err := q.Exec(`
INSERT INTO tool_usage(tool_key, agent_key, task_id, exploration_id, intent_id, session_id)
VALUES ($1, NULLIF($2,''), $3, $4, $5, NULLIF($6,''))`,
		u.ToolKey, u.AgentKey, nullIfZero(u.TaskID), nullIfZero(u.ExplorationID),
		nullIfZero(u.IntentID), u.SessionID)
	return err
}

// Counts 按工具键汇总调用次数。没有调用的工具不会出现在结果里（调用方自行补零）。
//
// 注意：这里只统计 tool_usage 实时表。历史任务的归档聚合（task_archives 里的
// tool 计数）是任务域的数据，由装配层合并——见 db.ToolUsageCounts 的转发实现。
func Counts(q store.Querier) (map[string]int, error) {
	rows, err := q.Query(`
SELECT tool_key, COUNT(*)
FROM tool_usage
GROUP BY tool_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var key string
		var calls int
		if err := rows.Scan(&key, &calls); err != nil {
			return nil, err
		}
		out[key] = calls
	}
	return out, rows.Err()
}

// nullIfZero 把 0 变成 NULL（三张外键列在库里可空，0 是「不属于任何任务」）。
func nullIfZero(v int64) any {
	if v > 0 {
		return v
	}
	return nil
}
