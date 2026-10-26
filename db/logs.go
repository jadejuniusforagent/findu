package db

// 服务端日志持久化（表 server_logs）已迁到 core/store（Step 3 批 3 第二刀）。
//
// 分工不变：core/log 管进程内 ring buffer 与 SSE 实时流，core/store 管落库与历史检索。
//
// 原实现见 git 历史（78 行，3 个方法）。

import "finduai/core/store"

func (d *DB) InsertLog(level, tag, text string) (int64, error) {
	return store.InsertServiceLog(d, level, tag, text)
}

func (d *DB) RecentLogs(limit int) ([]*store.ServiceLog, error) {
	return store.RecentServiceLogs(d, limit)
}

func (d *DB) ListLogsBefore(beforeID int64, limit int) ([]*store.ServiceLog, error) {
	return store.ListServiceLogsBefore(d, beforeID, limit)
}
