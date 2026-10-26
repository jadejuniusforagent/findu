package db

// =====================================================================
// 探索域 —— 装配层
//
// 探索图（exploration_nodes/edges/anchors）、activity 台账、token 计量、
// 主会话段、冷摘要、继承上下文、补录文摘的 SQL 已迁入 domain/exploration
// （批 3 第九刀，见 docs/refactor/STEP3-MIGRATION-MAP.md §十九）。
// 本文件保留：
//   - 值类型别名与错误值（调用点零改动，别名统一收口在批 4/5）；
//   - ExplorationStore 句柄与纯探索函数的一行转发（域函数首参收
//     store.Querier / store.Beginner 窄接口，db.DB 天然满足）；
//   - 跨域事务与跨域读取的装配层原体：SoftDeleteIntent / CancelIntent
//     （探索×会话×漏洞）、AddStandaloneFinding / SetFindingReproduction
//     （转发漏洞域）、TaskListMetricsAll（任务列表聚合，读 tasks/findings）、
//     ExplorationDiag（读 tasks 探查 FK 血缘）。
// =====================================================================

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"finduai/core/model"
	"finduai/domain/exploration"
)

// ExplorationStore is a handle onto one exploration's reasoning graph + activity.
type ExplorationStore struct {
	db    *DB
	expID int64
}

// Exploration returns a handle bound to an exploration id.
func (d *DB) Exploration(id int64) *ExplorationStore { return &ExplorationStore{db: d, expID: id} }

func (s *ExplorationStore) ID() int64 { return s.expID }

// =====================================================================
// 探索根 / 图节点 / 边（转发 domain/exploration）
// =====================================================================

// CreateExploration creates a new exploration root and returns its id.
func (d *DB) CreateExploration(description, goal string) (int64, error) {
	return exploration.CreateExploration(d, description, goal)
}

// Root returns the exploration's description and goal.
func (s *ExplorationStore) Root() (description, goal string, err error) {
	return exploration.Root(s.db, s.expID)
}

// AddNode writes a typed reasoning node with optional anchors to asset ids.
func (s *ExplorationStore) AddNode(kind string, payload map[string]any, priority int, state, origin string, anchors []string) (int64, error) {
	return exploration.AddNode(s.db, s.expID, kind, payload, priority, state, origin, anchors)
}

// AddIntent is a convenience: an open intent.
func (s *ExplorationStore) AddIntent(payload map[string]any, priority int, anchors []string, origin string) (int64, error) {
	return exploration.AddIntent(s.db, s.expID, payload, priority, anchors, origin)
}

// AddGoal writes a goal node (state open).
func (s *ExplorationStore) AddGoal(payload map[string]any, origin string) (int64, error) {
	return exploration.AddGoal(s.db, s.expID, payload, origin)
}

// OriginFactID returns this exploration's root fact id (0 when none exists).
func (s *ExplorationStore) OriginFactID() (int64, error) {
	return exploration.OriginFactID(s.db, s.expID)
}

// Anchor records a node→asset reference edge (many-to-many), idempotent;
// it only preserves the reasoning trail (visibility is never gated by it).
func (s *ExplorationStore) Anchor(nodeID int64, assetID string) error {
	return exploration.Anchor(s.db, s.expID, nodeID, assetID)
}

// Link adds a typed exploration edge (idempotent).
func (s *ExplorationStore) Link(from int64, rel string, to int64) error {
	return exploration.Link(s.db, s.expID, from, rel, to)
}

// SetNodeState updates any node's state (never deletes); content_version bumps.
func (s *ExplorationStore) SetNodeState(id int64, state string) error {
	return exploration.SetNodeState(s.db, s.expID, id, state)
}

// UpdateGoalPayload rewrites a goal node's payload text (and optional vulnclass).
func (s *ExplorationStore) UpdateGoalPayload(id int64, text, vulnclass string) error {
	return exploration.UpdateGoalPayload(s.db, s.expID, id, text, vulnclass)
}

// DeleteGoal hard-deletes a goal node (scoped to kind='goal').
func (s *ExplorationStore) DeleteGoal(id int64) error {
	return exploration.DeleteGoal(s.db, s.expID, id)
}

// ResetRunningIntents returns any intent left in 'running' back to 'open' so it
// is re-claimed. Called on startup.
func (s *ExplorationStore) ResetRunningIntents() (int64, error) {
	return exploration.ResetRunningIntents(s.db, s.expID)
}

// ReopenIntent flips ONE not-successfully-finished intent back to 'open'.
func (s *ExplorationStore) ReopenIntent(id int64) (bool, error) {
	return exploration.ReopenIntent(s.db, s.expID, id)
}

// ReopenBlockedIntents flips EVERY 'blocked' intent back to 'open' in this exploration.
func (s *ExplorationStore) ReopenBlockedIntents() (int64, error) {
	return exploration.ReopenBlockedIntents(s.db, s.expID)
}

// SetIntentBlockedReason 把 intent 置为 blocked 并记录原因（第十刀补收，自
// db/task_context.go 迁入 domain/exploration）。
func (s *ExplorationStore) SetIntentBlockedReason(id int64, reason string) error {
	return exploration.SetIntentBlockedReason(s.db, s.expID, id, reason)
}

// ReopenIntentsByBlockedReason 按被阻塞原因重开 intents（第十刀补收，同上）。
func (s *ExplorationStore) ReopenIntentsByBlockedReason(reason string) (int64, error) {
	return exploration.ReopenIntentsByBlockedReason(s.db, s.expID, reason)
}

func (s *ExplorationStore) SetIntentState(id int64, state string) error {
	return exploration.SetIntentState(s.db, s.expID, id, state)
}

// CompareAndSetIntentState transitions one local intent only when its current
// state still matches expected (pause/resume 与 worker 结算的状态边界).
func (s *ExplorationStore) CompareAndSetIntentState(id int64, expected, state string) (bool, error) {
	return exploration.CompareAndSetIntentState(s.db, s.expID, id, expected, state)
}

// ListByKind lists nodes of a kind (newest first).
func (s *ExplorationStore) ListByKind(kind string, limit int) ([]*exploration.Node, error) {
	return exploration.ListByKind(s.db, s.expID, kind, limit)
}

// ListByKindPage returns one newest-first page of nodes of a kind for reverse
// pagination (before<=0 = the newest page); hasMore reports whether still-older
// nodes exist.
func (s *ExplorationStore) ListByKindPage(kind string, before int64, limit int) ([]*exploration.Node, bool, error) {
	return exploration.ListByKindPage(s.db, s.expID, kind, before, limit)
}

// CountFinishedIntents counts this exploration's intents in a terminal explored
// state (done/blocked/exhausted) — graph_overview's done_intents_total.
func (s *ExplorationStore) CountFinishedIntents() (int, error) {
	return exploration.CountFinishedIntents(s.db, s.expID)
}

// CountOpenIntents counts this exploration's open intents — graph_overview's
// frontier_open.
func (s *ExplorationStore) CountOpenIntents() (int, error) {
	return exploration.CountOpenIntents(s.db, s.expID)
}

// GetNode returns one node of this exploration by id (nil, nil if not found).
func (s *ExplorationStore) GetNode(id int64) (*exploration.Node, error) {
	return exploration.GetNode(s.db, s.expID, id)
}

// Nodes lists all nodes (for graph viz).
func (s *ExplorationStore) Nodes(limit int) ([]*exploration.Node, error) {
	return exploration.Nodes(s.db, s.expID, limit)
}

// NodesPage returns one 1-based page of this exploration's nodes plus the total
// matching count.
func (s *ExplorationStore) NodesPage(f exploration.NodeFilter, page, size int) ([]*exploration.Node, int, error) {
	return exploration.NodesPage(s.db, s.expID, f, page, size)
}

// NodesByIDs loads the given nodes of this exploration in id order.
func (s *ExplorationStore) NodesByIDs(ids []int64) ([]*exploration.Node, error) {
	return exploration.NodesByIDs(s.db, s.expID, ids)
}

// EdgesTouching returns every edge with one end among ids.
func (s *ExplorationStore) EdgesTouching(ids []int64) ([]exploration.Edge, error) {
	return exploration.EdgesTouching(s.db, s.expID, ids)
}

// Edges lists exploration edges (for graph viz).
func (s *ExplorationStore) Edges(limit int) ([]exploration.Edge, error) {
	return exploration.Edges(s.db, s.expID, limit)
}

// FactsYielded returns the ids of fact nodes an intent produced (oldest first).
func (s *ExplorationStore) FactsYielded(intentID int64) ([]int64, error) {
	return exploration.FactsYielded(s.db, s.expID, intentID)
}

// FindingLineage returns the sub-DAG that leads FROM the exploration root TO the
// given node (origin → … → finding rendering).
func (s *ExplorationStore) FindingLineage(nodeID int64) ([]*exploration.Node, []exploration.Edge, error) {
	return exploration.FindingLineage(s.db, s.expID, nodeID)
}

// FindingIntents maps each finding id to the intent that produced it.
func (s *ExplorationStore) FindingIntents() (map[int64]int64, error) {
	return exploration.FindingIntents(s.db, s.expID)
}

// FindingIntentsTerminal is the inherited-history variant (terminal intents only).
func (s *ExplorationStore) FindingIntentsTerminal() (map[int64]int64, error) {
	return exploration.FindingIntentsTerminal(s.db, s.expID)
}

// Frontier returns open intents ordered by priority desc then id (FIFO tiebreak).
func (s *ExplorationStore) Frontier(limit int) ([]*exploration.Node, error) {
	return exploration.Frontier(s.db, s.expID, limit)
}

// HasActiveIntent reports whether this exploration has any intent still in play
// (open OR running — race-proof first-round kick).
func (s *ExplorationStore) HasActiveIntent() (bool, error) {
	return exploration.HasActiveIntent(s.db, s.expID)
}

// HasOpenGoal reports whether this exploration still has any goal in state 'open'.
func (s *ExplorationStore) HasOpenGoal() (bool, error) {
	return exploration.HasOpenGoal(s.db, s.expID)
}

// ClaimIntent atomically moves an open intent to running. Returns true if claimed.
func (s *ExplorationStore) ClaimIntent(id int64, owner string) (bool, error) {
	return exploration.ClaimIntent(s.db, s.expID, id, owner)
}

// Stats returns node counts grouped by kind (for dashboard).
func (s *ExplorationStore) Stats() (map[string]int, error) {
	return exploration.Stats(s.db, s.expID)
}

// AssetRefs returns the intents / facts / findings in THIS exploration anchored to
// the given asset id (newest first).
func (s *ExplorationStore) AssetRefs(assetID string) ([]exploration.AssetRef, error) {
	return exploration.AssetRefs(s.db, s.expID, assetID)
}

// =====================================================================
// activity 台账 / token 计量 / 主会话段 / 继承读边界（转发）
// =====================================================================

// TokenDailyAll aggregates token consumption by calendar day (UTC) for the
// past `days` days across all explorations.
func (d *DB) TokenDailyAll(days int) ([]exploration.DailyTokenBucket, error) {
	return exploration.TokenDailyAll(d, days)
}

// AppendActivity records one worker step and returns its id.
func (s *ExplorationStore) AppendActivity(a model.Activity) (int64, error) {
	return exploration.AppendActivity(s.db, s.expID, a)
}

// TokenTotal sums token usage across ALL workers for this exploration.
func (s *ExplorationStore) TokenTotal() (exploration.TokenUsage, error) {
	return exploration.TokenTotal(s.db, s.expID)
}

// TokenTotalsAll returns the whole-task token total for every exploration in one
// query (exploration_id → total).
func (d *DB) TokenTotalsAll() (map[int64]exploration.TokenUsage, error) {
	return exploration.TokenTotalsAll(d)
}

// LastActivityAll returns the unix time of the most recent activity per
// exploration (exploration_id → max created_at epoch), one query for all tasks.
func (d *DB) LastActivityAll() (map[int64]int64, error) {
	return exploration.LastActivityAll(d)
}

// GoalCountsAll returns goal totals (total / met) per exploration id, one query
// for all tasks — used by listTasks so the task list shows progress for every task.
func (d *DB) GoalCountsAll() (map[int64]exploration.GoalCounts, error) {
	return exploration.GoalCountsAll(d)
}

// TokenStatsByWorker sums token usage per worker for this exploration.
func (s *ExplorationStore) TokenStatsByWorker() ([]exploration.TokenUsage, error) {
	return exploration.TokenStatsByWorker(s.db, s.expID)
}

// TokenStatsBySession aggregates every persisted completed run.
func (s *ExplorationStore) TokenStatsBySession() ([]exploration.SessionTokenUsage, error) {
	return exploration.TokenStatsBySession(s.db, s.expID)
}

// ActivityList returns steps after sinceID (exclusive), optionally filtered by node.
func (s *ExplorationStore) ActivityList(nodeID *int64, sinceID int64, limit int) ([]model.Activity, int64, error) {
	return exploration.ActivityList(s.db, s.expID, nodeID, sinceID, limit)
}

// ActivityListForTerminalIntent is the inherited, incremental-read boundary.
func (s *ExplorationStore) ActivityListForTerminalIntent(nodeID, sinceID int64, limit int) ([]model.Activity, int64, error) {
	return exploration.ActivityListForTerminalIntent(s.db, s.expID, nodeID, sinceID, limit)
}

// ActivityPage returns one page for reverse (newest-first) pagination of a session,
// in ASCENDING id order for direct display.
func (s *ExplorationStore) ActivityPage(f exploration.ActivitySessionFilter, before int64, limit int) ([]model.Activity, bool, error) {
	return exploration.ActivityPage(s.db, s.expID, f, before, limit)
}

// ActivityPageForTerminalIntent is the inherited session-history boundary.
func (s *ExplorationStore) ActivityPageForTerminalIntent(nodeID, before int64, limit int) ([]model.Activity, bool, error) {
	return exploration.ActivityPageForTerminalIntent(s.db, s.expID, nodeID, before, limit)
}

// ActivityMaxID returns the task's current maximum activity id (0 when empty).
func (s *ExplorationStore) ActivityMaxID() (int64, error) {
	return exploration.ActivityMaxID(s.db, s.expID)
}

// CurrentMainSeg returns the highest main-session segment (0 when none created yet).
func (s *ExplorationStore) CurrentMainSeg() (int, error) {
	return exploration.CurrentMainSeg(s.db, s.expID)
}

// ListMainSessions returns every main-session segment newest-first, always including
// the implicit original segment 0.
func (s *ExplorationStore) ListMainSessions() ([]exploration.MainSession, error) {
	return exploration.ListMainSessions(s.db, s.expID)
}

// NewMainSession creates the next main-session segment (seq = current+1).
func (s *ExplorationStore) NewMainSession() (exploration.MainSession, error) {
	return exploration.NewMainSession(s.db, s.expID)
}

// ActivityDetail lazily returns the full detail blob for one step.
func (s *ExplorationStore) ActivityDetail(id int64) (string, error) {
	return exploration.ActivityDetail(s.db, s.expID, id)
}

// ActivityTrace lists one work's steps (summaries only, thinking/usage dropped).
func (s *ExplorationStore) ActivityTrace(nodeID int64, limit int) ([]model.Activity, error) {
	return exploration.ActivityTrace(s.db, s.expID, nodeID, limit)
}

// ActivityTraceForTerminalIntent returns one inherited work trace only while its
// source intent is still terminal at query time.
func (s *ExplorationStore) ActivityTraceForTerminalIntent(nodeID int64, limit int) ([]model.Activity, error) {
	return exploration.ActivityTraceForTerminalIntent(s.db, s.expID, nodeID, limit)
}

// ActivityTraceSearch finds steps whose summary OR detail matches q
// (case-insensitive); nil nodeID searches across ALL works.
func (s *ExplorationStore) ActivityTraceSearch(nodeID *int64, q string, limit int) ([]model.Activity, error) {
	return exploration.ActivityTraceSearch(s.db, s.expID, nodeID, q, limit)
}

// ActivityTraceSearchForTerminalIntent is the scoped inherited search variant.
func (s *ExplorationStore) ActivityTraceSearchForTerminalIntent(nodeID int64, q string, limit int) ([]model.Activity, error) {
	return exploration.ActivityTraceSearchForTerminalIntent(s.db, s.expID, nodeID, q, limit)
}

// ActivityTraceSearchTerminalIntents searches inherited worker history.
func (s *ExplorationStore) ActivityTraceSearchTerminalIntents(q string, limit int) ([]model.Activity, error) {
	return exploration.ActivityTraceSearchTerminalIntents(s.db, s.expID, q, limit)
}

// ActivityTraceSearchExcluding keyword-searches every node's steps in this
// exploration EXCEPT one node's own (excludeNodeID<=0 → no exclusion).
func (s *ExplorationStore) ActivityTraceSearchExcluding(excludeNodeID int64, q string, limit int) ([]model.Activity, error) {
	return exploration.ActivityTraceSearchExcluding(s.db, s.expID, excludeNodeID, q, limit)
}

// ActivityByIDs loads full detail for specific step ids (the trace drill-down).
func (s *ExplorationStore) ActivityByIDs(ids []int64) ([]model.Activity, error) {
	return exploration.ActivityByIDs(s.db, s.expID, ids)
}

// ActivityByIDsForTerminalIntents is the inherited-detail read boundary.
func (s *ExplorationStore) ActivityByIDsForTerminalIntents(ids []int64) ([]model.Activity, error) {
	return exploration.ActivityByIDsForTerminalIntents(s.db, s.expID, ids)
}

// =====================================================================
// 跨域事务与跨域读取（装配层原体，规范三-2）
// =====================================================================

// SoftDeleteIntent 假删除一个待领/运行中/已暂停的意图:置 state='deleted' 并把用户填写的
// 删除原因记入 delete_reason 字段,保留意图节点及其全部产出/血缘(不再像旧实现那样在图上
// 另挂一条 fact)。返回删除前意图的 summary,供 planner 通知使用。副会话随删除态一并清理。
// 调用方须先停掉运行中的 worker,避免其后续写入。
// 留在装配层：一个事务写探索节点 + 清 side_question_sessions（会话域表）。
func (s *ExplorationStore) SoftDeleteIntent(id int64, reason string) (string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	var state string
	var rawPayload []byte
	if err := tx.QueryRow(`SELECT state, payload FROM exploration_nodes
		WHERE id=$1 AND exploration_id=$2 AND kind='intent' FOR UPDATE`, id, s.expID).Scan(&state, &rawPayload); err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("intent not found")
		}
		return "", err
	}
	if state != "running" && state != "paused" && state != "open" {
		return "", fmt.Errorf("%w: intent state %s cannot be deleted", exploration.ErrIntentStateConflict, state)
	}
	if _, err := tx.Exec(`UPDATE exploration_nodes
		SET state='deleted', delete_reason=$3, blocked_reason=NULL,
		    content_version=content_version+1, completed_at=now()
		WHERE id=$1 AND exploration_id=$2`, id, s.expID, reason); err != nil {
		return "", err
	}
	// 主意图审计轨迹保留,但副问答会话随删除态原子清理(延迟快照会拒绝它)。
	if _, err := tx.Exec(`DELETE FROM side_question_sessions WHERE intent_id=$1`, id); err != nil {
		return "", err
	}
	var summary string
	var p map[string]any
	if json.Unmarshal(rawPayload, &p) == nil {
		if sm, ok := p["summary"].(string); ok {
			summary = sm
		}
	}
	return summary, tx.Commit()
}

// CancelIntent 物理删除一个意图,以及"仅由该意图支撑"的全部独占子孙节点——从该意图沿
// yields/derived_from 向下可达、且所有父节点(所有指向它的边的源)都落在删除集内的节点。
// goal 与 origin fact 永不删除;还被删除集之外的意图/digest 引用的共享节点也保留,以免破坏
// 其它分支、产生断链。被删的每个 intent 先做 token rollup(保留不可逆计量)并清理其 activity
// 与副会话;被删的 finding 先删 findings 表行(node_id FK 为 ON DELETE SET NULL,否则留孤儿)。
// 边随节点 CASCADE 清理,整个清理保持在一个事务内。调用方须先停掉运行中的 worker 防止其后续写入。
// 留在装配层：一个事务横跨探索节点/activity + 会话域 side_question_sessions +
// 漏洞域 findings 行；token rollup 与边算法已在探索域（IntentTokenRollup）。
func (s *ExplorationStore) CancelIntent(id int64) (exploration.IntentCleanup, error) {
	var out exploration.IntentCleanup
	tx, err := s.db.Begin()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()

	// 锁定意图行确认存在(幂等:已删则 not found)。状态不校验——真删除对任何状态成立,
	// running 的 worker 由调用方先停。
	if err := tx.QueryRow(`SELECT 1 FROM exploration_nodes
		WHERE id=$1 AND exploration_id=$2 AND kind='intent' FOR UPDATE`, id, s.expID).Scan(new(int)); err != nil {
		if err == sql.ErrNoRows {
			return out, fmt.Errorf("intent not found")
		}
		return out, err
	}

	// 载入全图节点(判 protected)与边(算向下可达 + 父集)。图规模对真实任务很小。
	kind := map[int64]string{}
	protected := map[int64]bool{}
	nrows, err := tx.Query(`SELECT id, kind, state FROM exploration_nodes WHERE exploration_id=$1`, s.expID)
	if err != nil {
		return out, err
	}
	for nrows.Next() {
		var nid int64
		var k, st string
		if err := nrows.Scan(&nid, &k, &st); err != nil {
			nrows.Close()
			return out, err
		}
		kind[nid] = k
		if k == exploration.KindGoal || (k == exploration.KindFact && st == exploration.StateOrigin) {
			protected[nid] = true // 目标与任务根事实永不随意图删除
		}
	}
	nrows.Close()
	if err := nrows.Err(); err != nil {
		return out, err
	}

	parentsOf := map[int64][]int64{} // dst -> 所有指向它的边的 src(任意 rel,含 covers:被 digest 覆盖的成员因此有 digest 父而被保留)
	downOf := map[int64][]int64{}    // src -> 沿 yields/derived_from 的向下邻居
	erows, err := tx.Query(`SELECT src_id, rel, dst_id FROM exploration_edges WHERE exploration_id=$1`, s.expID)
	if err != nil {
		return out, err
	}
	for erows.Next() {
		var src, dst int64
		var rel string
		if err := erows.Scan(&src, &rel, &dst); err != nil {
			erows.Close()
			return out, err
		}
		parentsOf[dst] = append(parentsOf[dst], src)
		if rel == exploration.RelYields || rel == exploration.RelDerivedFrom {
			downOf[src] = append(downOf[src], dst)
		}
	}
	erows.Close()
	if err := erows.Err(); err != nil {
		return out, err
	}

	// 独占级联:从意图向下扩展,一个节点入删除集当且仅当它未被保护、且它的每个父都已在集内
	// (即除了经过被删节点外再无来路)。迭代到不动点。
	del := map[int64]bool{id: true}
	for changed := true; changed; {
		changed = false
		for src := range del {
			for _, dst := range downOf[src] {
				if del[dst] || protected[dst] {
					continue
				}
				exclusive := true
				for _, p := range parentsOf[dst] {
					if !del[p] {
						exclusive = false
						break
					}
				}
				if exclusive {
					del[dst] = true
					changed = true
				}
			}
		}
	}

	// 按类型分桶。
	var ids, intentIDs, findingIDs []int64
	for nid := range del {
		ids = append(ids, nid)
		switch kind[nid] {
		case exploration.KindIntent:
			intentIDs = append(intentIDs, nid)
			out.Intents++
		case exploration.KindFact:
			out.Facts++
		case exploration.KindFinding:
			findingIDs = append(findingIDs, nid)
			out.Findings++
		}
	}

	// 每个被删意图:token rollup(保留不可逆计量)后清 activity。rollup 行 node_id 为 NULL,
	// 不会被下面按 node_id 的删除命中。rollup 在探索域（tx 内调探索域函数）。
	for _, iid := range intentIDs {
		tokenBuckets, err := exploration.IntentTokenRollup(tx, s.expID, iid)
		if err != nil {
			return out, err
		}
		res, err := tx.Exec(`DELETE FROM activity WHERE exploration_id=$1 AND node_id=$2`, s.expID, iid)
		if err != nil {
			return out, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			out.Activities += n
		}
		for _, bucket := range tokenBuckets {
			metadata, _ := json.Marshal(map[string]any{
				"cancelled_intent_id": iid,
				"token_day":           bucket.Day.Format(time.DateOnly),
				"token_rollup":        true,
			})
			if _, err := tx.Exec(`INSERT INTO activity(
				exploration_id, worker, kind, summary, metadata,
				input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, created_at)
				VALUES ($1,'token-ledger','result',$2,$3,$4,$5,$6,$7,$8)`,
				s.expID, fmt.Sprintf("已取消意图 #%d 的 Token 计量", iid), metadata,
				bucket.Usage.InputTokens, bucket.Usage.OutputTokens,
				bucket.Usage.CacheReadTokens, bucket.Usage.CacheWriteTokens, bucket.Day); err != nil {
				return out, err
			}
		}
		if _, err := tx.Exec(`DELETE FROM side_question_sessions WHERE intent_id=$1`, iid); err != nil {
			return out, err
		}
	}

	// finding 节点删除前先删 findings 表行(否则留 node_id=NULL 的孤儿)。
	for _, fid := range findingIDs {
		if _, err := tx.Exec(`DELETE FROM findings WHERE node_id=$1`, fid); err != nil {
			return out, err
		}
	}

	// 删节点(边随 CASCADE 清理)。逐个删并核对行数,防并发改动。
	var removed int64
	for _, nid := range ids {
		res, err := tx.Exec(`DELETE FROM exploration_nodes WHERE id=$1 AND exploration_id=$2`, nid, s.expID)
		if err != nil {
			return out, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			removed += n
		}
	}
	if removed != int64(len(ids)) {
		return out, fmt.Errorf("intent cleanup changed concurrently")
	}
	return out, tx.Commit()
}

// AddStandaloneFinding writes a finding to the standalone findings table, which
// persists across task deletion (task_id / node_id become NULL when the task or
// exploration node is deleted). taskID and nodeID may be 0 (stored as NULL).
// 留在装配层：转发漏洞域 AddFinding（findings 表属探索×漏洞交叉写）。
func (s *ExplorationStore) AddStandaloneFinding(taskID, nodeID int64, vulnclass, name, severity, summary, evidence, worker string, assetIDs []string) (int64, error) {
	return s.db.AddFinding(taskID, nodeID, vulnclass, name, severity, summary, evidence, worker, assetIDs)
}

// SetFindingReproduction stores the agent-captured structured reproduction recipe
// for the finding at nodeID (report_finding writes it inline).
func (s *ExplorationStore) SetFindingReproduction(nodeID int64, repro json.RawMessage) error {
	return s.db.SetFindingReproduction(nodeID, repro)
}

// ExplorationDiag probes, at the moment of an activity FK violation (23503), why the
// parent row is unreachable. It reports whether the exploration row still exists, how
// many task rows reference it (a live task should keep exactly 1 — and RESTRICT on
// tasks.exploration_id means the exploration CANNOT be deleted while that row lives),
// and the current MAX(explorations.id). Together these tell apart the failure modes:
//   - expExists=false, taskRefs=0 → the whole row set is gone (DB reset / wrong DB).
//   - expExists=false, taskRefs=1 → impossible under RESTRICT; would mean a broken FK.
//   - expExists=true              → the write's expID is NOT this exploration (stale/
//     wrong id in the in-memory store); compare against Store.ID().
//
// 留在装配层：读 tasks 的外键血缘（任务域）。
func (d *DB) ExplorationDiag(expID int64) (expExists bool, taskRefs int, maxExpID int64, err error) {
	err = d.QueryRow(`SELECT
		EXISTS(SELECT 1 FROM explorations WHERE id=$1),
		(SELECT COUNT(*) FROM tasks WHERE exploration_id=$1),
		COALESCE((SELECT MAX(id) FROM explorations),0)`, expID).Scan(&expExists, &taskRefs, &maxExpID)
	return
}

// TaskListMetricsAll returns list aggregates for every live task in one query.
// The lateral lookups use the per-exploration indexes instead of grouping the
// complete activity history on every poll.
// 留在装配层：读 tasks / findings（任务域×漏洞域）聚合探索域指标，任务列表专供。
func (d *DB) TaskListMetricsAll() (map[int64]exploration.TaskListMetrics, error) {
	rows, err := d.Query(`
		SELECT task.exploration_id,
		       COALESCE(token_metrics.input_tokens,0),
		       COALESCE(token_metrics.output_tokens,0),
		       COALESCE(token_metrics.cache_read_tokens,0),
		       COALESCE(token_metrics.cache_write_tokens,0),
		       COALESCE(latest_activity.created_at,0),
		       COALESCE(goal_metrics.total,0),
		       COALESCE(goal_metrics.met,0),
		       COALESCE(intent_metrics.running,0),
		       COALESCE(finding_metrics.critical,0),
		       COALESCE(finding_metrics.high,0),
		       COALESCE(finding_metrics.medium,0),
		       COALESCE(finding_metrics.low,0)
		FROM tasks task
		LEFT JOIN LATERAL (
			SELECT SUM(input_tokens) AS input_tokens,
			       SUM(output_tokens) AS output_tokens,
			       SUM(cache_read_tokens) AS cache_read_tokens,
			       SUM(cache_write_tokens) AS cache_write_tokens
			FROM activity
			WHERE exploration_id=task.exploration_id AND kind='result'
		) token_metrics ON true
		LEFT JOIN LATERAL (
			SELECT EXTRACT(EPOCH FROM created_at)::bigint AS created_at
			FROM activity
			WHERE exploration_id=task.exploration_id
			ORDER BY created_at DESC
			LIMIT 1
		) latest_activity ON true
		LEFT JOIN LATERAL (
			SELECT COUNT(*) AS total,
			       COUNT(*) FILTER (WHERE state='met') AS met
			FROM exploration_nodes
			WHERE exploration_id=task.exploration_id AND kind='goal'
		) goal_metrics ON true
		LEFT JOIN LATERAL (
			SELECT COUNT(*) AS running
			FROM exploration_nodes
			WHERE exploration_id=task.exploration_id AND kind='intent' AND state='running'
		) intent_metrics ON true
		LEFT JOIN LATERAL (
			SELECT COUNT(*) FILTER (WHERE severity='critical') AS critical,
			       COUNT(*) FILTER (WHERE severity='high')     AS high,
			       COUNT(*) FILTER (WHERE severity='medium')   AS medium,
			       COUNT(*) FILTER (WHERE severity='low')      AS low
			FROM findings
			WHERE task_id=task.id
		) finding_metrics ON true
		WHERE task.deleted_at IS NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]exploration.TaskListMetrics{}
	for rows.Next() {
		var explorationID int64
		var metrics exploration.TaskListMetrics
		if err := rows.Scan(
			&explorationID,
			&metrics.Tokens.InputTokens,
			&metrics.Tokens.OutputTokens,
			&metrics.Tokens.CacheReadTokens,
			&metrics.Tokens.CacheWriteTokens,
			&metrics.LastActivity,
			&metrics.Goals.Total,
			&metrics.Goals.Met,
			&metrics.RunningIntents,
			&metrics.Findings.Critical,
			&metrics.Findings.High,
			&metrics.Findings.Medium,
			&metrics.Findings.Low,
		); err != nil {
			return nil, err
		}
		out[explorationID] = metrics
	}
	return out, rows.Err()
}

// =====================================================================
// 冷摘要（cold-digest）转发 —— 源 db/digest.go 已全量迁 domain/exploration
// =====================================================================

// BumpRound advances this exploration's planner-round counter (§2.3).
func (s *ExplorationStore) BumpRound() (int64, error) {
	return exploration.BumpRound(s.db, s.expID)
}

// RoundNo returns the current planner-round counter.
func (s *ExplorationStore) RoundNo() (int64, error) {
	return exploration.RoundNo(s.db, s.expID)
}

// ColdStamps returns cold_since_round for every foldable node (intent/fact).
func (s *ExplorationStore) ColdStamps() (map[int64]*int64, error) {
	return exploration.ColdStamps(s.db, s.expID)
}

// ApplyStampOps writes a batch of cold_since_round changes in one transaction.
func (s *ExplorationStore) ApplyStampOps(ops []exploration.StampOp) error {
	return exploration.ApplyStampOps(s.db, s.expID, ops)
}

// ContentVersions returns id → content_version for all nodes (§5.3 signature).
func (s *ExplorationStore) ContentVersions() (map[int64]int, error) {
	return exploration.ContentVersions(s.db, s.expID)
}

// ActiveDigests returns the exploration's live digest nodes (state='active').
func (s *ExplorationStore) ActiveDigests() ([]*exploration.Node, error) {
	return exploration.ActiveDigests(s.db, s.expID)
}

// AddDigest writes one digest node and its covers edges in a single transaction.
func (s *ExplorationStore) AddDigest(payload map[string]any, memberIDs []int64) (int64, error) {
	return exploration.AddDigest(s.db, s.expID, payload, memberIDs)
}

// CoveredMembers maps member id → covering digest id, for ACTIVE digests only.
func (s *ExplorationStore) CoveredMembers() (map[int64]int64, error) {
	return exploration.CoveredMembers(s.db, s.expID)
}

// DigestMembers returns the member ids a digest covers (covers edges), sorted.
func (s *ExplorationStore) DigestMembers(digestID int64) ([]int64, error) {
	return exploration.DigestMembers(s.db, s.expID, digestID)
}

// SupersedeDigests retires digest nodes AND removes their covers edges atomically.
func (s *ExplorationStore) SupersedeDigests(ids []int64) error {
	return exploration.SupersedeDigests(s.db, s.expID, ids)
}

// NodeAssets maps each of the given node ids → the asset ids it is anchored to.
func (s *ExplorationStore) NodeAssets(ids []int64) (map[int64][]string, error) {
	return exploration.NodeAssets(s.db, s.expID, ids)
}

// =====================================================================
// 补录文摘 / 意图准入回滚转发
// =====================================================================

// ExplorationSynthesis returns the agent-generated text for an exploration.
func (d *DB) ExplorationSynthesis(expID int64, maxChars int) string {
	return exploration.ExplorationSynthesis(d, expID, maxChars)
}

// FindingReproTranscript returns a REQUEST/RESPONSE-focused transcript for the
// finding's intent (reproduction-recipe enrichment).
func (d *DB) FindingReproTranscript(nodeID int64, maxChars int) string {
	return exploration.FindingReproTranscript(d, nodeID, maxChars)
}

// FindingSourceTranscript returns the real work transcript that produced a
// finding node.
func (d *DB) FindingSourceTranscript(nodeID int64, maxChars int) string {
	return exploration.FindingSourceTranscript(d, nodeID, maxChars)
}

// ExplorationBeginNode returns the origin/begin node id of an exploration (0 if none).
func (d *DB) ExplorationBeginNode(expID int64) int64 {
	return exploration.ExplorationBeginNode(d, expID)
}

// DiscardOpenIntent compensates a follow-up creation when task admission fails.
func (s *ExplorationStore) DiscardOpenIntent(id int64) error {
	return exploration.DiscardOpenIntent(s.db, s.expID, id)
}
