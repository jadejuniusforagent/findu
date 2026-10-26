package exploration

// 探索图（exploration_nodes / exploration_edges）的节点与边读写。
// 批 3 第九刀自 db/exploration.go 上提：原 ExplorationStore 方法收窄为
// 包级函数（首参 store.Querier / store.Beginner + expID 显式）。探索图自身的事务
// （多锚点落点、边幂等）由 Beginner 参数开启，锁序与原实现逐行等价。

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"finduai/core/store"
)

const nodeCols = `id, kind, payload, priority, state, COALESCE(origin,''), COALESCE(owner,''), COALESCE(blocked_reason,''), COALESCE(delete_reason,''), created_at`

func scanNode(sc interface{ Scan(...any) error }) (*Node, error) {
	var n Node
	var payload []byte
	if err := sc.Scan(&n.ID, &n.Kind, &payload, &n.Priority, &n.State, &n.Origin, &n.Owner, &n.BlockedReason, &n.DeleteReason, &n.CreatedAt); err != nil {
		return nil, err
	}
	n.Payload = json.RawMessage(payload)
	return &n, nil
}

func scanNodes(rows *sql.Rows) ([]*Node, error) {
	var out []*Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// AddNode writes a typed reasoning node with optional anchors to asset ids.
func AddNode(b store.Beginner, expID int64, kind string, payload map[string]any, priority int, state, origin string, anchors []string) (int64, error) {
	raw, _ := json.Marshal(payload)
	tx, err := b.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var id int64
	if err := tx.QueryRow(`
INSERT INTO exploration_nodes(exploration_id, kind, payload, priority, state, origin)
VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		expID, kind, string(raw), priority, state, origin).Scan(&id); err != nil {
		return 0, err
	}
	for _, a := range anchors {
		if _, err := tx.Exec(`INSERT INTO exploration_anchors(node_id, asset_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, id, a); err != nil {
			return 0, err
		}
	}
	return id, tx.Commit()
}

// AddIntent is a convenience: an open intent.
func AddIntent(b store.Beginner, expID int64, payload map[string]any, priority int, anchors []string, origin string) (int64, error) {
	if origin == "" {
		origin = "planner"
	}
	return AddNode(b, expID, "intent", payload, priority, "open", origin, anchors)
}

// AddGoal writes a goal node (state open).
func AddGoal(b store.Beginner, expID int64, payload map[string]any, origin string) (int64, error) {
	return AddNode(b, expID, KindGoal, payload, 0, "open", origin, nil)
}

// OriginFactID returns this exploration's root fact id — the KindFact node with
// state='origin' seeded at task creation (the task root). Every intent traces
// back to it. Returns 0 (no error) when none exists (legacy explorations created
// under the old 'begin' root, or none yet).
func OriginFactID(q store.Querier, expID int64) (int64, error) {
	var id int64
	err := q.QueryRow(`SELECT id FROM exploration_nodes WHERE exploration_id=$1 AND kind='fact' AND state='origin' ORDER BY id LIMIT 1`, expID).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return id, err
}

// Anchor records a node→asset reference edge (many-to-many): it captures which
// global assets an exploration node touched, as lineage/provenance. The asset
// graph itself is global and shared — anchoring no longer gates visibility (any
// task may read any asset via search); it only preserves the reasoning trail.
// Idempotent.
func Anchor(q store.Querier, expID int64, nodeID int64, assetID string) error {
	if nodeID <= 0 || assetID == "" {
		return nil
	}
	_, err := q.Exec(`INSERT INTO exploration_anchors(node_id, asset_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, nodeID, assetID)
	return err
}

// Link adds a typed exploration edge (idempotent).
func Link(q store.Querier, expID int64, from int64, rel string, to int64) error {
	_, err := q.Exec(`
INSERT INTO exploration_edges(exploration_id, src_id, rel, dst_id) VALUES ($1,$2,$3,$4)
ON CONFLICT (exploration_id, src_id, rel, dst_id) DO NOTHING`, expID, from, rel, to)
	return err
}

// SetNodeState updates any node's state (never deletes). content_version bumps
// so a folded member's state flip invalidates its digest's cached body (§5.3).
func SetNodeState(q store.Querier, expID, id int64, state string) error {
	_, err := q.Exec(`UPDATE exploration_nodes SET state=$1, blocked_reason=NULL, content_version=content_version+1 WHERE id=$2 AND exploration_id=$3`, state, id, expID)
	return err
}

// UpdateGoalPayload rewrites a goal node's payload text (and optional vulnclass);
// scoped to kind='goal' so it can never mutate an intent/fact by id. Returns an
// error if no such goal exists in this exploration.
func UpdateGoalPayload(q store.Querier, expID, id int64, text, vulnclass string) error {
	payload := map[string]any{"text": text}
	if vulnclass != "" {
		payload["vulnclass"] = vulnclass
	}
	raw, _ := json.Marshal(payload)
	res, err := q.Exec(`UPDATE exploration_nodes SET payload=$1 WHERE id=$2 AND exploration_id=$3 AND kind='goal'`, string(raw), id, expID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("目标不存在")
	}
	return nil
}

// DeleteGoal hard-deletes a goal node; scoped to kind='goal' so it can never drop
// an intent/fact by id. The edges (spawns) and anchors reference it with ON DELETE
// CASCADE, so they go with it; activity.node_id is ON DELETE SET NULL. Returns an
// error if no such goal exists in this exploration.
func DeleteGoal(q store.Querier, expID, id int64) error {
	res, err := q.Exec(`DELETE FROM exploration_nodes WHERE id=$1 AND exploration_id=$2 AND kind='goal'`, id, expID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("目标不存在")
	}
	return nil
}

// ResetRunningIntents returns any intent left in 'running' back to 'open' so it
// is re-claimed. Called on startup: a 'running' intent with no live worker (a
// backend restart or crashed worker goroutine left it stuck) would otherwise spin
// forever in the UI. Workers resume from their transcript, so reopening is safe.
func ResetRunningIntents(q store.Querier, expID int64) (int64, error) {
	res, err := q.Exec(`UPDATE exploration_nodes SET state='open', completed_at=NULL, blocked_reason=NULL
WHERE exploration_id=$1 AND kind='intent' AND state='running'`, expID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// ReopenIntent flips ONE not-successfully-finished intent (blocked/exhausted/stopped)
// back to 'open' so a worker re-claims it (graph writes it already produced stay).
// The worker resumes from its prior LLM transcript instead of restarting from scratch.
// done/open/running are left untouched. Returns whether a row changed. Used by "重跑".
func ReopenIntent(q store.Querier, expID, id int64) (bool, error) {
	res, err := q.Exec(`UPDATE exploration_nodes
SET state='open', completed_at=NULL, blocked_reason=NULL
WHERE id=$1 AND exploration_id=$2 AND kind='intent'
  AND state IN ('blocked','exhausted','stopped')`, id, expID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ReopenBlockedIntents flips EVERY 'blocked' intent in this exploration back to 'open'
// (batch rerun after e.g. an LLM/network outage that blocked many at once). Returns the
// number reopened. Workers resume from their transcripts; kept graph writes remain.
func ReopenBlockedIntents(q store.Querier, expID int64) (int64, error) {
	res, err := q.Exec(`UPDATE exploration_nodes SET state='open', completed_at=NULL, blocked_reason=NULL
WHERE exploration_id=$1 AND kind='intent' AND state='blocked'`, expID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// SetIntentState updates an intent node's state.
func SetIntentState(q store.Querier, expID, id int64, state string) error {
	// terminal states stamp completed_at; reopening (back to open/running) clears it.
	terminal := state == "done" || state == "blocked" || state == "exhausted" || state == "stopped"
	_, err := q.Exec(`UPDATE exploration_nodes
SET state=$1, blocked_reason=NULL, content_version=content_version+1, completed_at = CASE WHEN $4 THEN now() ELSE NULL END
WHERE id=$2 AND exploration_id=$3 AND kind='intent'`, state, id, expID, terminal)
	return err
}

// CompareAndSetIntentState transitions one local intent only when its current
// state still matches expected. It is the state boundary used by pause/resume and
// worker settlement, so a stale API read cannot overwrite a concurrent finish.
func CompareAndSetIntentState(q store.Querier, expID, id int64, expected, state string) (bool, error) {
	terminal := state == "done" || state == "blocked" || state == "exhausted" || state == "stopped"
	res, err := q.Exec(`UPDATE exploration_nodes
SET state=$1, blocked_reason=NULL, content_version=content_version+1, completed_at = CASE WHEN $5 THEN now() ELSE NULL END
WHERE id=$2 AND exploration_id=$3 AND kind='intent' AND state=$4`, state, id, expID, expected, terminal)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// ListByKind lists nodes of a kind (newest first).
func ListByKind(q store.Querier, expID int64, kind string, limit int) ([]*Node, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := q.Query(`SELECT `+nodeCols+` FROM exploration_nodes
WHERE exploration_id=$1 AND kind=$2 ORDER BY id DESC LIMIT $3`, expID, kind, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanNodes(rows)
}

// ListByKindPage returns one newest-first page of nodes of a kind for reverse
// pagination: up to `limit` nodes with id < `before` (before<=0 = the newest page).
// hasMore reports whether still-older nodes exist, so a client (e.g. the worker
// session list) can page past the old fixed cap instead of losing older intents.
func ListByKindPage(q store.Querier, expID int64, kind string, before int64, limit int) ([]*Node, bool, error) {
	if limit <= 0 {
		limit = 300
	}
	rows, err := q.Query(`SELECT `+nodeCols+` FROM exploration_nodes
WHERE exploration_id=$1 AND kind=$2 AND ($3 <= 0 OR id < $3)
ORDER BY id DESC LIMIT $4`, expID, kind, before, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	nodes, err := scanNodes(rows)
	if err != nil {
		return nil, false, err
	}
	hasMore := len(nodes) > limit
	if hasMore {
		nodes = nodes[:limit]
	}
	return nodes, hasMore, nil
}

// listByKindPageFiltered is ListByKindPage with an optional summary keyword
// filter (payload->>'summary' ILIKE %q%). Fetches one extra row so the caller can
// probe hasMore. Empty kw = no filter. Newest-first by id.
// （kw 即原实现的 q 过滤词参数；首参已让给 store.Querier 的 q。）
func listByKindPageFiltered(q store.Querier, expID int64, kind string, before int64, limit int, kw string) ([]*Node, error) {
	where := `exploration_id=$1 AND kind=$2 AND ($3 <= 0 OR id < $3)`
	args := []any{expID, kind, before}
	if kw != "" {
		args = append(args, "%"+kw+"%")
		where += fmt.Sprintf(` AND payload->>'summary' ILIKE $%d`, len(args))
	}
	args = append(args, limit+1)
	rows, err := q.Query(`SELECT `+nodeCols+` FROM exploration_nodes
WHERE `+where+` ORDER BY id DESC LIMIT $`+fmt.Sprintf("%d", len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanNodes(rows)
}

// countByKindFiltered counts nodes of a kind under the same summary filter, for a
// page's total. Empty kw = count all of that kind.
func countByKindFiltered(q store.Querier, expID int64, kind, kw string) (int, error) {
	where := `exploration_id=$1 AND kind=$2`
	args := []any{expID, kind}
	if kw != "" {
		args = append(args, "%"+kw+"%")
		where += fmt.Sprintf(` AND payload->>'summary' ILIKE $%d`, len(args))
	}
	var n int
	err := q.QueryRow(`SELECT COUNT(*) FROM exploration_nodes WHERE `+where, args...).Scan(&n)
	return n, err
}

// CountFinishedIntents counts this exploration's intents in a terminal explored
// state (done/blocked/exhausted) — graph_overview's done_intents_total, so the planner
// knows recent_done_intents (capped at a recent window) is a truncated view and
// stays cautious about "already tried" dedup. Excludes 'stopped' (killed/deleted),
// matching exactly what recent_done_intents surfaces.
func CountFinishedIntents(q store.Querier, expID int64) (int, error) {
	var n int
	err := q.QueryRow(`SELECT COUNT(*) FROM exploration_nodes
WHERE exploration_id=$1 AND kind='intent' AND state IN ('done','blocked','exhausted')`, expID).Scan(&n)
	return n, err
}

// CountOpenIntents counts this exploration's open intents — graph_overview's
// frontier_open, so the planner knows open_intents (capped at the top-N by
// priority) is a truncated view and there may be more claimable work.
func CountOpenIntents(q store.Querier, expID int64) (int, error) {
	var n int
	err := q.QueryRow(`SELECT COUNT(*) FROM exploration_nodes
WHERE exploration_id=$1 AND kind='intent' AND state='open'`, expID).Scan(&n)
	return n, err
}

// GetNode returns one node of this exploration by id (nil, nil if not found).
func GetNode(q store.Querier, expID, id int64) (*Node, error) {
	n, err := scanNode(q.QueryRow(`SELECT `+nodeCols+` FROM exploration_nodes WHERE id=$1 AND exploration_id=$2`, id, expID))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return n, err
}

// Nodes lists all nodes (for graph viz).
func Nodes(q store.Querier, expID int64, limit int) ([]*Node, error) {
	if limit <= 0 {
		limit = 2000
	}
	rows, err := q.Query(`SELECT `+nodeCols+` FROM exploration_nodes WHERE exploration_id=$1 ORDER BY id LIMIT $2`, expID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanNodes(rows)
}

// NodesPage returns one 1-based page of this exploration's nodes plus the total
// matching count. The 播报板 reads the graph as a time series, so it pages in SQL
// rather than pulling the whole graph like Nodes does. Ordering is by id, which
// is BIGSERIAL and therefore creation order — stable when several nodes share a
// created_at second.
func NodesPage(q store.Querier, expID int64, f NodeFilter, page, size int) ([]*Node, int, error) {
	if page < 1 {
		page = 1
	}
	if size <= 0 {
		size = 20
	}
	if size > 200 {
		size = 200
	}
	offset := (page - 1) * size

	conds := []string{"exploration_id=$1"}
	args := []any{expID}
	addIn := func(column string, values []string) {
		if len(values) == 0 {
			return
		}
		marks := make([]string, 0, len(values))
		for _, v := range values {
			args = append(args, v)
			marks = append(marks, "$"+fmt.Sprint(len(args)))
		}
		conds = append(conds, column+" IN ("+strings.Join(marks, ",")+")")
	}
	addIn("kind", f.Kinds)
	addIn("state", f.States)
	if kw := strings.TrimSpace(f.Query); kw != "" {
		args = append(args, "%"+kw+"%")
		mark := "$" + fmt.Sprint(len(args))
		ors := []string{"payload::text ILIKE " + mark, "COALESCE(origin,'') ILIKE " + mark}
		// 纯数字(或 UI 里带 # 前缀的形式,如「#41」)当作节点 id 精确匹配,方便直接定位某个节点。
		if idStr := strings.TrimPrefix(kw, "#"); idStr != "" {
			if id, err := strconv.ParseInt(idStr, 10, 64); err == nil {
				args = append(args, id)
				ors = append(ors, "id = $"+fmt.Sprint(len(args)))
			}
		}
		conds = append(conds, "("+strings.Join(ors, " OR ")+")")
	}
	where := " WHERE " + strings.Join(conds, " AND ")

	var total int
	if err := q.QueryRow(`SELECT COUNT(*) FROM exploration_nodes`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	order := "DESC"
	if f.Asc {
		order = "ASC"
	}
	args = append(args, size, offset)
	rows, err := q.Query(`SELECT `+nodeCols+` FROM exploration_nodes`+where+
		` ORDER BY id `+order+
		` LIMIT $`+fmt.Sprint(len(args)-1)+` OFFSET $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	nodes, err := scanNodes(rows)
	if err != nil {
		return nil, 0, err
	}
	return nodes, total, nil
}

// NodesByIDs loads the given nodes of this exploration in id order. Used to
// resolve the neighbours of a 播报板 page without fetching the whole graph.
func NodesByIDs(q store.Querier, expID int64, ids []int64) ([]*Node, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	args := make([]any, 0, len(ids)+1)
	args = append(args, expID)
	marks := make([]string, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
		marks = append(marks, "$"+fmt.Sprint(len(args)))
	}
	rows, err := q.Query(`SELECT `+nodeCols+` FROM exploration_nodes
WHERE exploration_id=$1 AND id IN (`+strings.Join(marks, ",")+`) ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanNodes(rows)
}

// EdgesTouching returns every edge with one end among ids — the 播报板 uses it to
// spell out where a node came from and what it produced.
func EdgesTouching(q store.Querier, expID int64, ids []int64) ([]Edge, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	args := make([]any, 0, len(ids)+1)
	args = append(args, expID)
	marks := make([]string, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
		marks = append(marks, "$"+fmt.Sprint(len(args)))
	}
	list := strings.Join(marks, ",")
	rows, err := q.Query(`SELECT src_id, rel, dst_id FROM exploration_edges
WHERE exploration_id=$1 AND (src_id IN (`+list+`) OR dst_id IN (`+list+`))`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Edge
	for rows.Next() {
		var e Edge
		if err := rows.Scan(&e.From, &e.Rel, &e.To); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Edges lists exploration edges (for graph viz).
func Edges(q store.Querier, expID int64, limit int) ([]Edge, error) {
	if limit <= 0 {
		limit = 5000
	}
	rows, err := q.Query(`SELECT src_id, rel, dst_id FROM exploration_edges WHERE exploration_id=$1 LIMIT $2`, expID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Edge
	for rows.Next() {
		var e Edge
		if err := rows.Scan(&e.From, &e.Rel, &e.To); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// FactsYielded returns the ids of fact nodes an intent produced (intent --yields-->
// fact), oldest first. Used to spell out the round's incremental facts to the planner
// alongside the finished worker's output. Best-effort: returns nil for an intent with
// no facts (or a missing one).
func FactsYielded(q store.Querier, expID, intentID int64) ([]int64, error) {
	rows, err := q.Query(`SELECT n.id
		FROM exploration_edges e
		JOIN exploration_nodes n ON n.id=e.dst_id AND n.exploration_id=e.exploration_id
		WHERE e.exploration_id=$1 AND e.src_id=$2 AND e.rel=$3 AND n.kind=$4
		ORDER BY n.id`, expID, intentID, RelYields, KindFact)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// FindingLineage returns the sub-DAG that leads FROM the exploration root TO the
// given node: the node itself plus all its ancestors (nodes reverse-reachable by
// following edges backward), and every edge whose both endpoints are in that set.
// Used to render "how this finding was reached" — origin → … → finding. Returns
// empty (not error) for a nonexistent node.
func FindingLineage(q store.Querier, expID, nodeID int64) ([]*Node, []Edge, error) {
	// anc = the node + everything that can reach it via edges (walk src<-dst).
	nodeRows, err := q.Query(`
WITH RECURSIVE anc(id) AS (
    SELECT $2::bigint
  UNION
    SELECT e.src_id
    FROM exploration_edges e
    JOIN anc ON e.dst_id = anc.id
    WHERE e.exploration_id = $1
)
SELECT `+nodeCols+`
FROM exploration_nodes
WHERE exploration_id = $1 AND id IN (SELECT id FROM anc)
ORDER BY id`, expID, nodeID)
	if err != nil {
		return nil, nil, err
	}
	nodes, err := scanNodes(nodeRows)
	if err != nil {
		return nil, nil, err
	}
	if len(nodes) == 0 {
		return nil, nil, nil
	}
	// edges among the ancestor set only (the lineage's internal relations).
	edgeRows, err := q.Query(`
WITH RECURSIVE anc(id) AS (
    SELECT $2::bigint
  UNION
    SELECT e.src_id
    FROM exploration_edges e
    JOIN anc ON e.dst_id = anc.id
    WHERE e.exploration_id = $1
)
SELECT src_id, rel, dst_id
FROM exploration_edges
WHERE exploration_id = $1
  AND src_id IN (SELECT id FROM anc)
  AND dst_id IN (SELECT id FROM anc)`, expID, nodeID)
	if err != nil {
		return nil, nil, err
	}
	defer edgeRows.Close()
	var edges []Edge
	for edgeRows.Next() {
		var e Edge
		if err := edgeRows.Scan(&e.From, &e.Rel, &e.To); err != nil {
			return nil, nil, err
		}
		edges = append(edges, e)
	}
	return nodes, edges, edgeRows.Err()
}

// FindingIntents maps each finding id to the intent that produced it (the
// intent --yields--> finding edge; report_finding links it). Findings with no
// producing intent are absent. Precise (JOIN, no edge-scan limit).
func FindingIntents(q store.Querier, expID int64) (map[int64]int64, error) {
	rows, err := q.Query(`SELECT e.dst_id, e.src_id
FROM exploration_edges e
JOIN exploration_nodes n ON n.id = e.dst_id AND n.exploration_id = e.exploration_id
WHERE e.exploration_id=$1 AND e.rel=$2 AND n.kind='finding'`, expID, RelYields)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int64{}
	for rows.Next() {
		var findingID, intentID int64
		if err := rows.Scan(&findingID, &intentID); err != nil {
			return nil, err
		}
		out[findingID] = intentID
	}
	return out, rows.Err()
}

// FindingIntentsTerminal is the inherited-history variant: it returns finding
// lineage only when the producing intent has reached an immutable terminal
// state. This keeps a source task's live worker topology private.
func FindingIntentsTerminal(q store.Querier, expID int64) (map[int64]int64, error) {
	rows, err := q.Query(`SELECT e.dst_id, e.src_id
	FROM exploration_edges e
	JOIN exploration_nodes finding ON finding.id=e.dst_id AND finding.exploration_id=e.exploration_id
	JOIN exploration_nodes intent ON intent.id=e.src_id AND intent.exploration_id=e.exploration_id
	WHERE e.exploration_id=$1 AND e.rel=$2 AND finding.kind='finding'
	  AND intent.kind='intent' AND intent.state IN ('done','blocked','exhausted','stopped')`, expID, RelYields)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int64{}
	for rows.Next() {
		var findingID, intentID int64
		if err := rows.Scan(&findingID, &intentID); err != nil {
			return nil, err
		}
		out[findingID] = intentID
	}
	return out, rows.Err()
}

// Frontier returns open intents ordered by priority desc then id (FIFO tiebreak).
func Frontier(q store.Querier, expID int64, limit int) ([]*Node, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := q.Query(`SELECT `+nodeCols+` FROM exploration_nodes
WHERE exploration_id=$1 AND kind='intent' AND state='open'
ORDER BY priority DESC, id ASC LIMIT $2`, expID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanNodes(rows)
}

// HasActiveIntent reports whether this exploration has any intent still in play
// (state open OR running). Used to decide whether to kick the FIRST planner round:
// a seeded task's intent may already have been claimed (open→running) by a worker
// before the check, so counting only 'open' (Frontier) races with worker claim and
// wrongly kicks a redundant first round. Counting open+running is race-proof.
func HasActiveIntent(q store.Querier, expID int64) (bool, error) {
	var exists bool
	err := q.QueryRow(`SELECT EXISTS(SELECT 1 FROM exploration_nodes
WHERE exploration_id=$1 AND kind='intent' AND state IN ('open','running'))`, expID).Scan(&exists)
	return exists, err
}

// HasOpenGoal reports whether this exploration still has any goal in state 'open'.
// false ⇒ 所有目标已 met/abandoned（或本任务无目标）⇒ 进入 goalless（人工直投）分支：
// planner 停跑，任务是否结束改由 frontier 是否抽干决定。met 与 abandoned 都算"已了结"。
func HasOpenGoal(q store.Querier, expID int64) (bool, error) {
	var exists bool
	err := q.QueryRow(`SELECT EXISTS(SELECT 1 FROM exploration_nodes
WHERE exploration_id=$1 AND kind='goal' AND state='open')`, expID).Scan(&exists)
	return exists, err
}

// ClaimIntent atomically moves an open intent to running. Returns true if claimed.
func ClaimIntent(q store.Querier, expID, id int64, owner string) (bool, error) {
	res, err := q.Exec(`UPDATE exploration_nodes SET state='running', owner=$1
WHERE id=$2 AND exploration_id=$3 AND kind='intent' AND state='open'`, owner, id, expID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// GoalCountsAll returns goal totals (total / met) per exploration id, one query
// for all tasks — used by listTasks so the task list shows progress for every task.
func GoalCountsAll(q store.Querier) (map[int64]GoalCounts, error) {
	rows, err := q.Query(`
		SELECT exploration_id,
		       COUNT(*) AS total,
		       COUNT(*) FILTER (WHERE state = 'met') AS met
		FROM exploration_nodes
		WHERE kind = 'goal'
		GROUP BY exploration_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]GoalCounts{}
	for rows.Next() {
		var eid int64
		var gc GoalCounts
		if err := rows.Scan(&eid, &gc.Total, &gc.Met); err != nil {
			return nil, err
		}
		out[eid] = gc
	}
	return out, rows.Err()
}

// AssetRefs returns the intents / facts / findings in THIS exploration anchored to
// the given asset id (newest first) — what this task tested / concluded about it.
func AssetRefs(q store.Querier, expID int64, assetID string) ([]AssetRef, error) {
	if assetID == "" {
		return []AssetRef{}, nil
	}
	rows, err := q.Query(`
SELECT en.id, en.kind, en.state, en.payload
FROM exploration_anchors ea
JOIN exploration_nodes en ON en.id = ea.node_id
WHERE ea.asset_id = $1 AND en.exploration_id = $2 AND en.kind IN ('intent','fact','finding')
ORDER BY en.id DESC`, assetID, expID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AssetRef{}
	for rows.Next() {
		var r AssetRef
		var payload []byte
		if err := rows.Scan(&r.ID, &r.Kind, &r.State, &payload); err != nil {
			return nil, err
		}
		var p map[string]any
		_ = json.Unmarshal(payload, &p)
		for _, k := range []string{"summary", "text"} {
			if v, ok := p[k].(string); ok && strings.TrimSpace(v) != "" {
				r.Summary = v
				break
			}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Stats returns node counts grouped by kind (for dashboard).
func Stats(q store.Querier, expID int64) (map[string]int, error) {
	rows, err := q.Query(`SELECT kind, count(*) FROM exploration_nodes WHERE exploration_id=$1 GROUP BY kind`, expID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var c int
		if err := rows.Scan(&k, &c); err != nil {
			return nil, err
		}
		out[k] = c
	}
	return out, rows.Err()
}

// SetIntentBlockedReason 把 intent 节点置为 blocked 并记录原因（空串经 NULLIF
// 落成 NULL）。批 3 第十刀补收：原是 db/task_context.go 里的 ExplorationStore
// 方法，按域归属迁入探索域。
func SetIntentBlockedReason(q store.Querier, expID, id int64, reason string) error {
	_, err := q.Exec(`UPDATE exploration_nodes
SET state='blocked', blocked_reason=NULLIF($1,''), completed_at=now()
WHERE id=$2 AND exploration_id=$3 AND kind='intent'`, reason, id, expID)
	return err
}

// ReopenIntentsByBlockedReason 重开所有因该原因被 blocked 的 intent（例如 LLM
// 配额恢复后按 IntentBlockedLLMQuota 重开）。返回重开的行数。
func ReopenIntentsByBlockedReason(q store.Querier, expID int64, reason string) (int64, error) {
	res, err := q.Exec(`UPDATE exploration_nodes
SET state='open', blocked_reason=NULL, completed_at=NULL
WHERE exploration_id=$1 AND kind='intent' AND state='blocked' AND blocked_reason=$2`, expID, reason)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}
