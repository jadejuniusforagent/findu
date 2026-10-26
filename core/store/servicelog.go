package store

import (
	"context"
	"time"
)

// 服务端日志持久化（表 server_logs）。与 core/log 的分工：
//   - core/log 负责「写在进程里、能被 SSE 实时读」的 ring buffer 与格式；
//   - 本文件负责「落库、供 UI 历史检索」的那一份。
// 两者由 server/logsink.go 同时喂，语义不同，不要合并。

// ServiceLog 是 server_logs 的一行。
type ServiceLog struct {
	ID        int64
	CreatedAt time.Time
	Level     string
	Tag       string
	Text      string
}

// InsertServiceLog 追加一行并返回自增 id。
func InsertServiceLog(q ContextQuerier, level, tag, text string) (int64, error) {
	var id int64
	err := q.QueryRowContext(context.Background(),
		"INSERT INTO server_logs(level,tag,text) VALUES($1,$2,$3) RETURNING id",
		level, tag, text,
	).Scan(&id)
	return id, err
}

// RecentServiceLogs 返回最近 limit 行，按时间正序（最旧在前，便于前端追加渲染）。
func RecentServiceLogs(q ContextQuerier, limit int) ([]*ServiceLog, error) {
	rows, err := q.QueryContext(context.Background(),
		`SELECT id, created_at, level, tag, text
		   FROM server_logs
		  ORDER BY id DESC
		  LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanLogsNewestFirst(rows)
}

// ListServiceLogsBefore 返回 id < beforeID 的至多 limit 行，同样正序输出。
func ListServiceLogsBefore(q ContextQuerier, beforeID int64, limit int) ([]*ServiceLog, error) {
	rows, err := q.QueryContext(context.Background(),
		`SELECT id, created_at, level, tag, text
		   FROM server_logs
		  WHERE id < $1
		  ORDER BY id DESC
		  LIMIT $2`, beforeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanLogsNewestFirst(rows)
}

// scanLogsNewestFirst 按 id DESC 取出的行在读完后翻成正序，两处调用共用。
func scanLogsNewestFirst(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]*ServiceLog, error) {
	var out []*ServiceLog
	for rows.Next() {
		l := &ServiceLog{}
		if err := rows.Scan(&l.ID, &l.CreatedAt, &l.Level, &l.Tag, &l.Text); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}
