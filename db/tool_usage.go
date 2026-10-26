package db

// 工具调用台账（表 tool_usage）的读写已迁到 domain/exploration（Step 3 批 3 第二刀）。
//
// 唯一保留在装配层的是 Counts 的跨域合并：实时计数属探索域，历史归档的工具计数
// 属任务域（task_archives），两边合并不是任何单一 domain 该知道的事。
//
// 原实现见 git 历史（58 行）。

import "finduai/domain/exploration"

func (d *DB) InsertToolUsage(u *exploration.ToolUsage) error {
	return exploration.InsertToolUsage(d, u)
}

// ToolUsageCounts 返回实时表与归档聚合相加后的每工具调用次数。
func (d *DB) ToolUsageCounts() (map[string]int, error) {
	out, err := exploration.Counts(d)
	if err != nil {
		return nil, err
	}
	archived, err := d.archivedTaskAggregates()
	if err != nil {
		return nil, err
	}
	for _, aggregate := range archived {
		for key, calls := range aggregate.Tools {
			out[key] += calls
		}
	}
	return out, nil
}
