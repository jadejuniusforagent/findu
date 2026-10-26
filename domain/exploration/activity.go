package exploration

// activity 台账（每 worker 执行步骤）与其 token 计量、主会话段、以及继承读
// （terminal intent 边界）函数族。批 3 第九刀自 db/exploration.go 上提。
// activity 表的行值类型见 value.go（core/model.Activity 别名）。

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"finduai/core/store"
)

// TokenDailyAll aggregates token consumption by calendar day (UTC) for the
// past `days` days across all explorations. Only kind='result' rows carry token
// counts (the terminal per-run summary), so there is no double-counting.
func TokenDailyAll(q store.Querier, days int) ([]DailyTokenBucket, error) {
	if days <= 0 {
		days = 30
	}
	rows, err := q.Query(`
		SELECT TO_CHAR(DATE_TRUNC('day', created_at AT TIME ZONE 'UTC'), 'YYYY-MM-DD') AS day,
		       COALESCE(SUM(input_tokens), 0),
		       COALESCE(SUM(output_tokens), 0),
		       COALESCE(SUM(cache_read_tokens), 0)
		FROM activity
		WHERE kind = 'result'
		  AND created_at >= NOW() - ($1 * INTERVAL '1 day')
		GROUP BY day
		ORDER BY day`, days)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DailyTokenBucket
	for rows.Next() {
		var b DailyTokenBucket
		if err := rows.Scan(&b.Day, &b.InputTokens, &b.OutputTokens, &b.CacheReadTokens); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// AppendActivity records one worker step and returns its id.
func AppendActivity(q store.Querier, expID int64, a Activity) (int64, error) {
	metadata := a.Metadata
	if len(metadata) == 0 {
		metadata = []byte(`{}`)
	}
	var id int64
	err := q.QueryRow(`
INSERT INTO activity(exploration_id, node_id, worker, kind, tool, tool_use_id, is_error, summary, detail, metadata, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, main_seg)
VALUES ($1,$2,NULLIF($3,''),NULLIF($4,''),NULLIF($5,''),NULLIF($6,''),$7,NULLIF($8,''),NULLIF($9,''),$10,$11,$12,$13,$14,$15)
RETURNING id`, expID, a.NodeID, store.UTF8Clean(a.Worker), store.UTF8Clean(a.Kind), store.UTF8Clean(a.Tool), store.UTF8Clean(a.ToolUseID), a.IsError, store.UTF8Clean(a.Summary), store.UTF8Clean(a.Detail),
		metadata, a.InputTokens, a.OutputTokens, a.CacheReadTokens, a.CacheWriteTokens, a.MainSeg).Scan(&id)
	return id, err
}

// IntentTokenRollup returns exactly-once usage grouped by its original UTC day.
// Each result terminates one run. A result carrying usage is authoritative; an
// older error/cancel result without usage falls back to the latest cumulative
// usage frame since the previous result. A trailing frame represents a run that
// was interrupted before its terminal result could be persisted.
// （移动期导出：db 侧 CancelIntent 的跨域事务装配用它在 tx 内取桶。）
func IntentTokenRollup(q store.Querier, explorationID, intentID int64) ([]TokenRollupBucket, error) {
	rows, err := q.Query(`SELECT kind, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, created_at
		FROM activity
		WHERE exploration_id=$1 AND node_id=$2 AND kind IN ('usage','result')
		ORDER BY id`, explorationID, intentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byDay := make(map[time.Time]TokenUsage)
	add := func(at time.Time, usage TokenUsage) {
		if usage.InputTokens == 0 && usage.OutputTokens == 0 &&
			usage.CacheReadTokens == 0 && usage.CacheWriteTokens == 0 {
			return
		}
		utc := at.UTC()
		day := time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
		total := byDay[day]
		total.add(usage)
		byDay[day] = total
	}

	var pending TokenUsage
	var pendingAt time.Time
	hasPending := false
	for rows.Next() {
		var kind string
		var input, output, read, write sql.NullInt64
		var createdAt time.Time
		if err := rows.Scan(&kind, &input, &output, &read, &write, &createdAt); err != nil {
			return nil, err
		}
		current := TokenUsage{
			InputTokens:      int(input.Int64),
			OutputTokens:     int(output.Int64),
			CacheReadTokens:  int(read.Int64),
			CacheWriteTokens: int(write.Int64),
		}
		hasCurrent := input.Valid || output.Valid || read.Valid || write.Valid
		if kind == "usage" {
			if hasCurrent {
				pending, pendingAt, hasPending = current, createdAt, true
			}
			continue
		}

		if hasCurrent {
			// Preserve a partially populated legacy terminal row by filling only its
			// missing dimensions from the latest cumulative usage frame.
			if hasPending {
				if !input.Valid {
					current.InputTokens = pending.InputTokens
				}
				if !output.Valid {
					current.OutputTokens = pending.OutputTokens
				}
				if !read.Valid {
					current.CacheReadTokens = pending.CacheReadTokens
				}
				if !write.Valid {
					current.CacheWriteTokens = pending.CacheWriteTokens
				}
			}
			add(createdAt, current)
		} else if hasPending {
			add(createdAt, pending)
		}
		pending, pendingAt, hasPending = TokenUsage{}, time.Time{}, false
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if hasPending {
		add(pendingAt, pending)
	}

	buckets := make([]TokenRollupBucket, 0, len(byDay))
	for day, usage := range byDay {
		buckets = append(buckets, TokenRollupBucket{Day: day, Usage: usage})
	}
	sort.Slice(buckets, func(i, j int) bool { return buckets[i].Day.Before(buckets[j].Day) })
	return buckets, nil
}

// TokenTotal sums token usage across ALL workers for this exploration (whole-task
// total). Same source as TokenStatsByWorker (kind='result' rows), just ungrouped.
func TokenTotal(q store.Querier, expID int64) (TokenUsage, error) {
	var u TokenUsage
	err := q.QueryRow(`SELECT
		COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
		COALESCE(SUM(cache_read_tokens),0), COALESCE(SUM(cache_write_tokens),0)
	FROM activity WHERE exploration_id=$1 AND kind='result'`, expID).
		Scan(&u.InputTokens, &u.OutputTokens, &u.CacheReadTokens, &u.CacheWriteTokens)
	return u, err
}

// TokenTotalsAll returns the whole-task token total for every exploration in one
// query (exploration_id → total), so the task list can show per-task consumption
// without a query per row.
func TokenTotalsAll(q store.Querier) (map[int64]TokenUsage, error) {
	rows, err := q.Query(`SELECT exploration_id,
		COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
		COALESCE(SUM(cache_read_tokens),0), COALESCE(SUM(cache_write_tokens),0)
	FROM activity WHERE kind='result' GROUP BY exploration_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]TokenUsage{}
	for rows.Next() {
		var eid int64
		var u TokenUsage
		if err := rows.Scan(&eid, &u.InputTokens, &u.OutputTokens, &u.CacheReadTokens, &u.CacheWriteTokens); err != nil {
			return nil, err
		}
		out[eid] = u
	}
	return out, rows.Err()
}

// LastActivityAll returns the unix time of the most recent activity per
// exploration (exploration_id → max created_at epoch), one query for all tasks.
// Persisted (unlike Engine.LastActivity's in-memory map), so it survives restarts
// and gives终态任务 a stable "ran until" time for computing run duration.
func LastActivityAll(q store.Querier) (map[int64]int64, error) {
	rows, err := q.Query(`SELECT exploration_id, EXTRACT(EPOCH FROM MAX(created_at))::bigint FROM activity GROUP BY exploration_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int64{}
	for rows.Next() {
		var eid, ts int64
		if err := rows.Scan(&eid, &ts); err != nil {
			return nil, err
		}
		out[eid] = ts
	}
	return out, rows.Err()
}

// TokenStatsByWorker sums token usage per worker for this exploration (from the
// kind='result' records that carry usage). Used by the per-agent token display.
func TokenStatsByWorker(q store.Querier, expID int64) ([]TokenUsage, error) {
	rows, err := q.Query(`SELECT COALESCE(worker,''),
		COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
		COALESCE(SUM(cache_read_tokens),0), COALESCE(SUM(cache_write_tokens),0)
	FROM activity WHERE exploration_id=$1 AND kind='result'
	GROUP BY worker ORDER BY worker`, expID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TokenUsage{}
	for rows.Next() {
		var u TokenUsage
		if err := rows.Scan(&u.Worker, &u.InputTokens, &u.OutputTokens, &u.CacheReadTokens, &u.CacheWriteTokens); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// TokenStatsBySession aggregates every persisted completed run, independent of
// activity-history pagination. Main/planner use fixed keys; Worker executions use
// their intent node id, even when a different work#N process handled a retry.
func TokenStatsBySession(q store.Querier, expID int64) ([]SessionTokenUsage, error) {
	rows, err := q.Query(`SELECT
		CASE
			WHEN COALESCE(a.worker,'')='mainagent' THEN 'main:' || COALESCE(a.main_seg,0)::text
			WHEN COALESCE(a.worker,'')='planner' THEN 'plan'
			WHEN n.id IS NOT NULL THEN 'intent:' || n.id::text
			ELSE ''
		END AS session_key,
		COALESCE(SUM(a.input_tokens),0), COALESCE(SUM(a.output_tokens),0),
		COALESCE(SUM(a.cache_read_tokens),0), COALESCE(SUM(a.cache_write_tokens),0)
	FROM activity a
	LEFT JOIN exploration_nodes n
	  ON n.id=a.node_id AND n.exploration_id=a.exploration_id AND n.kind='intent'
	WHERE a.exploration_id=$1 AND a.kind='result'
	  AND (COALESCE(a.worker,'') IN ('mainagent','planner') OR n.id IS NOT NULL)
	GROUP BY session_key
	ORDER BY session_key`, expID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SessionTokenUsage{}
	for rows.Next() {
		var u SessionTokenUsage
		if err := rows.Scan(&u.Session, &u.InputTokens, &u.OutputTokens, &u.CacheReadTokens, &u.CacheWriteTokens); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// ActivityList returns steps after sinceID (exclusive), optionally filtered by node.
// Returns items and the new cursor (max id seen).
func ActivityList(q store.Querier, expID int64, nodeID *int64, sinceID int64, limit int) ([]Activity, int64, error) {
	if limit <= 0 {
		limit = 300
	}
	var rows *sql.Rows
	var err error
	const cols = `id, node_id, COALESCE(worker,''), COALESCE(kind,''), COALESCE(tool,''), COALESCE(tool_use_id,''), is_error, COALESCE(summary,''), metadata, created_at, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, main_seg`
	if nodeID != nil {
		rows, err = q.Query(`SELECT `+cols+`
FROM activity WHERE exploration_id=$1 AND node_id=$2 AND id>$3 ORDER BY id LIMIT $4`, expID, *nodeID, sinceID, limit)
	} else {
		rows, err = q.Query(`SELECT `+cols+`
FROM activity WHERE exploration_id=$1 AND id>$2 ORDER BY id LIMIT $3`, expID, sinceID, limit)
	}
	if err != nil {
		return nil, sinceID, err
	}
	defer rows.Close()
	out := []Activity{}
	cursor := sinceID
	for rows.Next() {
		var a Activity
		if err := rows.Scan(&a.ID, &a.NodeID, &a.Worker, &a.Kind, &a.Tool, &a.ToolUseID, &a.IsError, &a.Summary, &a.Metadata, &a.CreatedAt,
			&a.InputTokens, &a.OutputTokens, &a.CacheReadTokens, &a.CacheWriteTokens, &a.MainSeg); err != nil {
			return nil, sinceID, err
		}
		if a.ID > cursor {
			cursor = a.ID
		}
		out = append(out, a)
	}
	return out, cursor, rows.Err()
}

// ActivityListForTerminalIntent is the inherited, incremental-read boundary.
// The intent state check and activity read happen in one statement so a source
// intent reopened between API-level checks cannot expose its new in-flight trace.
// Model reasoning and accounting rows are never inherited.
func ActivityListForTerminalIntent(q store.Querier, expID, nodeID, sinceID int64, limit int) ([]Activity, int64, error) {
	if limit <= 0 {
		limit = 300
	}
	rows, err := q.Query(`SELECT a.id, a.node_id, COALESCE(a.worker,''), COALESCE(a.kind,''),
		COALESCE(a.tool,''), COALESCE(a.tool_use_id,''), a.is_error, COALESCE(a.summary,''),
		a.created_at, a.input_tokens, a.output_tokens, a.cache_read_tokens, a.cache_write_tokens
	FROM activity a
	JOIN exploration_nodes n ON n.id=a.node_id AND n.exploration_id=a.exploration_id
	WHERE a.exploration_id=$1 AND a.node_id=$2 AND a.id>$3
	  AND n.kind='intent' AND n.state IN ('done','blocked','exhausted','stopped')
	  AND a.kind NOT IN ('thinking','usage')
	ORDER BY a.id LIMIT $4`, expID, nodeID, sinceID, limit)
	if err != nil {
		return nil, sinceID, err
	}
	defer rows.Close()
	out := []Activity{}
	cursor := sinceID
	for rows.Next() {
		var a Activity
		if err := rows.Scan(&a.ID, &a.NodeID, &a.Worker, &a.Kind, &a.Tool, &a.ToolUseID, &a.IsError, &a.Summary, &a.CreatedAt,
			&a.InputTokens, &a.OutputTokens, &a.CacheReadTokens, &a.CacheWriteTokens); err != nil {
			return nil, sinceID, err
		}
		if a.ID > cursor {
			cursor = a.ID
		}
		out = append(out, a)
	}
	return out, cursor, rows.Err()
}

// ActivityPage returns one page for reverse (newest-first) pagination of a session:
// up to `limit` steps ending before id `before` (exclusive; before<=0 = the latest
// page), returned in ASCENDING id order for direct display. hasMore reports whether
// still-older steps exist before the returned window (so the client can stop loading
// on scroll-up). Summary-only columns — detail is lazy-loaded via ActivityDetail.
func ActivityPage(q store.Querier, expID int64, f ActivitySessionFilter, before int64, limit int) ([]Activity, bool, error) {
	if limit <= 0 {
		limit = 200
	}
	const cols = `id, node_id, COALESCE(worker,''), COALESCE(kind,''), COALESCE(tool,''), COALESCE(tool_use_id,''), is_error, COALESCE(summary,''), metadata, created_at, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, main_seg`
	args := []any{expID}
	cond, cargs := f.cond(len(args) + 1)
	args = append(args, cargs...)
	beforeArg := len(args) + 1
	args = append(args, before)
	limitArg := len(args) + 1
	args = append(args, limit+1) // one extra row to detect older history
	query := fmt.Sprintf(`SELECT `+cols+`
FROM activity WHERE exploration_id=$1%s AND ($%d <= 0 OR id < $%d)
ORDER BY id DESC LIMIT $%d`, cond, beforeArg, beforeArg, limitArg)
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	desc := []Activity{}
	for rows.Next() {
		var a Activity
		if err := rows.Scan(&a.ID, &a.NodeID, &a.Worker, &a.Kind, &a.Tool, &a.ToolUseID, &a.IsError, &a.Summary, &a.Metadata, &a.CreatedAt,
			&a.InputTokens, &a.OutputTokens, &a.CacheReadTokens, &a.CacheWriteTokens, &a.MainSeg); err != nil {
			return nil, false, err
		}
		desc = append(desc, a)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(desc) > limit
	if hasMore {
		desc = desc[:limit]
	}
	// reverse the newest-first window into ascending id order for display.
	out := make([]Activity, len(desc))
	for i, a := range desc {
		out[len(desc)-1-i] = a
	}
	return out, hasMore, nil
}

// ActivityPageForTerminalIntent is the inherited session-history boundary. It
// combines ownership, terminal-state and row-kind checks in one query, closing
// the race where a previously completed source intent is reopened before paging.
func ActivityPageForTerminalIntent(q store.Querier, expID, nodeID, before int64, limit int) ([]Activity, bool, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := q.Query(`SELECT a.id, a.node_id, COALESCE(a.worker,''), COALESCE(a.kind,''),
		COALESCE(a.tool,''), COALESCE(a.tool_use_id,''), a.is_error, COALESCE(a.summary,''),
		a.created_at, a.input_tokens, a.output_tokens, a.cache_read_tokens, a.cache_write_tokens
	FROM activity a
	JOIN exploration_nodes n ON n.id=a.node_id AND n.exploration_id=a.exploration_id
	WHERE a.exploration_id=$1 AND a.node_id=$2
	  AND n.kind='intent' AND n.state IN ('done','blocked','exhausted','stopped')
	  AND a.kind NOT IN ('thinking','usage')
	  AND ($3 <= 0 OR a.id < $3)
	ORDER BY a.id DESC LIMIT $4`, expID, nodeID, before, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	desc := []Activity{}
	for rows.Next() {
		var a Activity
		if err := rows.Scan(&a.ID, &a.NodeID, &a.Worker, &a.Kind, &a.Tool, &a.ToolUseID, &a.IsError, &a.Summary, &a.CreatedAt,
			&a.InputTokens, &a.OutputTokens, &a.CacheReadTokens, &a.CacheWriteTokens); err != nil {
			return nil, false, err
		}
		desc = append(desc, a)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(desc) > limit
	if hasMore {
		desc = desc[:limit]
	}
	out := make([]Activity, len(desc))
	for i, a := range desc {
		out[len(desc)-1-i] = a
	}
	return out, hasMore, nil
}

// ActivityMaxID returns the task's current maximum activity id (0 when empty). It is
// the task-level snapshot cursor handed to the SSE stream so history (id<=cursor) and
// the live tail (id>cursor) meet with no gap — deliberately task-level, not per
// session, since one SSE covers the whole task.
func ActivityMaxID(q store.Querier, expID int64) (int64, error) {
	var max sql.NullInt64
	err := q.QueryRow(`SELECT MAX(id) FROM activity WHERE exploration_id=$1`, expID).Scan(&max)
	if err != nil {
		return 0, err
	}
	return max.Int64, nil
}

// CurrentMainSeg returns the highest main-session segment (0 when none created yet).
func CurrentMainSeg(q store.Querier, expID int64) (int, error) {
	var seg int
	err := q.QueryRow(`SELECT COALESCE(MAX(seq),0) FROM main_sessions WHERE exploration_id=$1`, expID).Scan(&seg)
	return seg, err
}

// ListMainSessions returns every main-session segment newest-first, always including
// the implicit original segment 0. Segment 0 carries no stored timestamp.
func ListMainSessions(q store.Querier, expID int64) ([]MainSession, error) {
	rows, err := q.Query(`SELECT seq, created_at FROM main_sessions WHERE exploration_id=$1 ORDER BY seq DESC`, expID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MainSession{}
	for rows.Next() {
		var m MainSession
		if err := rows.Scan(&m.Seq, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out = append(out, MainSession{Seq: 0}) // implicit original session (oldest)
	return out, nil
}

// NewMainSession creates the next main-session segment (seq = current+1) and returns
// it. It touches only main_sessions — the task's exploration graph/assets/goal are
// untouched, so the new session starts with a clean transcript over the same task.
func NewMainSession(q store.Querier, expID int64) (MainSession, error) {
	var m MainSession
	err := q.QueryRow(`
INSERT INTO main_sessions(exploration_id, seq)
VALUES ($1, COALESCE((SELECT MAX(seq) FROM main_sessions WHERE exploration_id=$1),0)+1)
RETURNING seq, created_at`, expID).Scan(&m.Seq, &m.CreatedAt)
	return m, err
}

// ActivityDetail lazily returns the full detail blob for one step.
func ActivityDetail(q store.Querier, expID, id int64) (string, error) {
	var d sql.NullString
	err := q.QueryRow(`SELECT detail FROM activity WHERE id=$1 AND exploration_id=$2`, id, expID).Scan(&d)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return d.String, err
}

// scanTrace reads the summary-only column set shared by the worker-trace tools
// (ActivityTrace / ActivityTraceSearch). Detail is never selected here — it is
// pulled separately, on demand, via ActivityByIDs.
func scanTrace(rows *sql.Rows) ([]Activity, error) {
	out := []Activity{}
	for rows.Next() {
		var a Activity
		if err := rows.Scan(&a.ID, &a.NodeID, &a.Worker, &a.Kind, &a.Tool, &a.IsError, &a.Summary); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

const traceCols = `id, node_id, COALESCE(worker,''), COALESCE(kind,''), COALESCE(tool,''), is_error, COALESCE(summary,'')`

// ActivityTrace lists one work's steps (by intent/node id) for the trace tools:
// summaries only (detail is lazy — fetch via ActivityByIDs). thinking/usage rows
// are dropped so the caller sees the work's actions + observations, not the
// model's internal reasoning or token-accounting noise.
func ActivityTrace(q store.Querier, expID, nodeID int64, limit int) ([]Activity, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := q.Query(`SELECT `+traceCols+`
FROM activity WHERE exploration_id=$1 AND node_id=$2 AND kind NOT IN ('thinking','usage')
ORDER BY id LIMIT $3`, expID, nodeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTrace(rows)
}

// ActivityTraceForTerminalIntent returns one inherited work trace only while its
// source intent is still terminal at query time.
func ActivityTraceForTerminalIntent(q store.Querier, expID, nodeID int64, limit int) ([]Activity, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := q.Query(`SELECT a.id, a.node_id, COALESCE(a.worker,''), COALESCE(a.kind,''), COALESCE(a.tool,''), a.is_error, COALESCE(a.summary,'')
	FROM activity a
	JOIN exploration_nodes n ON n.id=a.node_id AND n.exploration_id=a.exploration_id
	WHERE a.exploration_id=$1 AND a.node_id=$2 AND n.kind='intent'
	  AND n.state IN ('done','blocked','exhausted','stopped')
	  AND a.kind NOT IN ('thinking','usage')
	ORDER BY a.id LIMIT $3`, expID, nodeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTrace(rows)
}

// ActivityTraceSearch finds steps whose summary OR detail matches kw
// (case-insensitive), returning summaries only. nodeID != nil scopes to one work;
// nil searches across ALL works (node_id IS NOT NULL — planner/main steps have no
// node_id, so this cleanly means "worker traces"). thinking/usage rows dropped.
// （kw 即原实现的 q 过滤词参数；首参已让给 store.Querier 的 q。）
func ActivityTraceSearch(q store.Querier, expID int64, nodeID *int64, kw string, limit int) ([]Activity, error) {
	if limit <= 0 {
		limit = 100
	}
	like := "%" + kw + "%"
	var rows *sql.Rows
	var err error
	if nodeID != nil {
		rows, err = q.Query(`SELECT `+traceCols+`
FROM activity WHERE exploration_id=$1 AND node_id=$2 AND kind NOT IN ('thinking','usage')
AND (summary ILIKE $3 OR detail ILIKE $3) ORDER BY id LIMIT $4`, expID, *nodeID, like, limit)
	} else {
		rows, err = q.Query(`SELECT `+traceCols+`
FROM activity WHERE exploration_id=$1 AND node_id IS NOT NULL AND kind NOT IN ('thinking','usage')
AND (summary ILIKE $2 OR detail ILIKE $2) ORDER BY id LIMIT $3`, expID, like, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTrace(rows)
}

// ActivityTraceSearchForTerminalIntent is the scoped inherited search variant;
// terminal eligibility is evaluated by the same statement that reads the rows.
func ActivityTraceSearchForTerminalIntent(q store.Querier, expID, nodeID int64, kw string, limit int) ([]Activity, error) {
	if limit <= 0 {
		limit = 100
	}
	like := "%" + kw + "%"
	rows, err := q.Query(`SELECT a.id, a.node_id, COALESCE(a.worker,''), COALESCE(a.kind,''), COALESCE(a.tool,''), a.is_error, COALESCE(a.summary,'')
	FROM activity a
	JOIN exploration_nodes n ON n.id=a.node_id AND n.exploration_id=a.exploration_id
	WHERE a.exploration_id=$1 AND a.node_id=$2 AND n.kind='intent'
	  AND n.state IN ('done','blocked','exhausted','stopped')
	  AND a.kind NOT IN ('thinking','usage')
	  AND (a.summary ILIKE $3 OR a.detail ILIKE $3)
	ORDER BY a.id LIMIT $4`, expID, nodeID, like, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTrace(rows)
}

// ActivityTraceSearchTerminalIntents searches inherited worker history without
// exposing planner/main rows or work that is still open/running in the source.
func ActivityTraceSearchTerminalIntents(q store.Querier, expID int64, kw string, limit int) ([]Activity, error) {
	if limit <= 0 {
		limit = 100
	}
	like := "%" + kw + "%"
	rows, err := q.Query(`SELECT a.id, a.node_id, COALESCE(a.worker,''), COALESCE(a.kind,''), COALESCE(a.tool,''), a.is_error, COALESCE(a.summary,'')
	FROM activity a
	JOIN exploration_nodes n ON n.id=a.node_id AND n.exploration_id=a.exploration_id
	WHERE a.exploration_id=$1 AND n.kind='intent'
	  AND n.state IN ('done','blocked','exhausted','stopped')
	  AND a.kind NOT IN ('thinking','usage')
	  AND (a.summary ILIKE $2 OR a.detail ILIKE $2)
	ORDER BY a.id LIMIT $3`, expID, like, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTrace(rows)
}

// ActivityTraceSearchExcluding keyword-searches every node's steps in this
// exploration EXCEPT one node's own (excludeNodeID) — so a worker's
// search_all_worker_traces doesn't return its own in-progress trace (which is
// already in its context). excludeNodeID<=0 → no exclusion (searches all nodes).
func ActivityTraceSearchExcluding(q store.Querier, expID, excludeNodeID int64, kw string, limit int) ([]Activity, error) {
	if excludeNodeID <= 0 {
		return ActivityTraceSearch(q, expID, nil, kw, limit)
	}
	if limit <= 0 {
		limit = 100
	}
	like := "%" + kw + "%"
	rows, err := q.Query(`SELECT `+traceCols+`
FROM activity WHERE exploration_id=$1 AND node_id IS NOT NULL AND node_id <> $2
AND kind NOT IN ('thinking','usage')
AND (summary ILIKE $3 OR detail ILIKE $3) ORDER BY id LIMIT $4`, expID, excludeNodeID, like, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTrace(rows)
}

// ActivityByIDs loads full detail for specific step ids (the trace drill-down),
// scoped to this exploration. thinking rows are skipped (never returned, even if
// their id is asked for). Order is ascending id, not the input order.
func ActivityByIDs(q store.Querier, expID int64, ids []int64) ([]Activity, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	ph := make([]string, len(ids))
	args := make([]any, len(ids)+1)
	args[0] = expID
	for i, id := range ids {
		ph[i] = fmt.Sprintf("$%d", i+2)
		args[i+1] = id
	}
	rows, err := q.Query(`SELECT id, node_id, COALESCE(kind,''), COALESCE(tool,''), is_error, COALESCE(detail,'')
FROM activity WHERE exploration_id=$1 AND kind<>'thinking' AND id IN (`+strings.Join(ph, ",")+`) ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Activity{}
	for rows.Next() {
		var a Activity
		if err := rows.Scan(&a.ID, &a.NodeID, &a.Kind, &a.Tool, &a.IsError, &a.Detail); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ActivityByIDsForTerminalIntents is the inherited-detail read boundary. It
// deliberately excludes node-less planner/main rows, non-intent activities, live
// source work, and thinking. Local callers that need legacy behavior use
// ActivityByIDs instead.
func ActivityByIDsForTerminalIntents(q store.Querier, expID int64, ids []int64) ([]Activity, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	ph := make([]string, len(ids))
	args := make([]any, len(ids)+1)
	args[0] = expID
	for i, id := range ids {
		ph[i] = fmt.Sprintf("$%d", i+2)
		args[i+1] = id
	}
	rows, err := q.Query(`SELECT a.id, a.node_id, COALESCE(a.kind,''), COALESCE(a.tool,''), a.is_error, COALESCE(a.detail,'')
	FROM activity a
	JOIN exploration_nodes n ON n.id=a.node_id AND n.exploration_id=a.exploration_id
		WHERE a.exploration_id=$1 AND a.kind NOT IN ('thinking','usage') AND n.kind='intent'
	  AND n.state IN ('done','blocked','exhausted','stopped')
	  AND a.id IN (`+strings.Join(ph, ",")+`) ORDER BY a.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Activity{}
	for rows.Next() {
		var a Activity
		if err := rows.Scan(&a.ID, &a.NodeID, &a.Kind, &a.Tool, &a.IsError, &a.Detail); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
