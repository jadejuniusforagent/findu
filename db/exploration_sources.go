package db

// =====================================================================
// 继承上下文（直连来源任务）—— 装配层
//
// 组合逻辑（WithSources 函数族 + 继承标注 helper）已迁 domain/exploration/sources.go
// （批 3 第九刀，见 docs/refactor/STEP3-MIGRATION-MAP.md §十九）。本文件保留：
//   - DirectSourceStore 值类型（TaskSource 属任务域，暂存 db）；
//   - DirectSourceStores / TaskID：SQL 读 tasks × task_relations（任务域表，
//     规范三-2），server/agent 展示与跳转用完整 DTO；
//   - sources() 最小投影解析器：注入探索域组合函数的懒求值来源读取；
//   - 12 个 WithSources 方法转发。
// =====================================================================

import (
	"database/sql"
	"finduai/core/model"
	"finduai/domain/task"

	"finduai/core/store"
	"finduai/domain/exploration"
)

// DirectSourceStore binds one directly related task to its exploration store.
// It is intentionally a read-side helper: callers keep using the receiver store
// for every graph mutation, frontier lookup, and intent claim.
type DirectSourceStore struct {
	Task  task.TaskSource
	Store *ExplorationStore
}

// DirectSourceStores resolves the live, direct task relations for this
// exploration. It deliberately queries on every call: relations disappear when
// a source task is deleted, and inherited context must not retain stale rows.
// Sources of a source are never expanded.
// 留在装配层：SQL 读任务域表（tasks × task_relations）。
func (s *ExplorationStore) DirectSourceStores() ([]DirectSourceStore, error) {
	rows, err := s.db.Query(`
SELECT source.id, source.exploration_id, source.description, source.goal, source.status
FROM tasks owner
JOIN task_relations relation ON relation.task_id=owner.id
JOIN tasks source ON source.id=relation.source_task_id AND source.deleted_at IS NULL
WHERE owner.exploration_id=$1 AND owner.deleted_at IS NULL
ORDER BY relation.created_at, source.id`, s.expID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DirectSourceStore{}
	for rows.Next() {
		var source task.TaskSource
		if err := rows.Scan(&source.TaskID, &source.ExplorationID, &source.Description, &source.Goal, &source.Status); err != nil {
			return nil, err
		}
		out = append(out, DirectSourceStore{Task: source, Store: s.db.Exploration(source.ExplorationID)})
	}
	return out, rows.Err()
}

// TaskID returns the live task bound to this exploration. Explorations created
// directly in tests or maintenance code have no task and return zero.
// 留在装配层：读 tasks（任务域表）。
func (s *ExplorationStore) TaskID() (int64, error) {
	var id int64
	err := s.db.QueryRow(`SELECT id FROM tasks WHERE exploration_id=$1 AND deleted_at IS NULL`, s.expID).Scan(&id)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, nil
		}
		return 0, err
	}
	return id, nil
}

// sources 是注入探索域组合函数的来源解析器：同一 join 的最小投影
// （task_id + source exploration_id）。懒求值与原行为一致（本地未命中才触发）。
func (s *ExplorationStore) sources(q store.Querier, expID int64) ([]exploration.SourceRef, error) {
	rows, err := q.Query(`
SELECT source.id, source.exploration_id
FROM tasks owner
JOIN task_relations relation ON relation.task_id=owner.id
JOIN tasks source ON source.id=relation.source_task_id AND source.deleted_at IS NULL
WHERE owner.exploration_id=$1 AND owner.deleted_at IS NULL
ORDER BY relation.created_at, source.id`, expID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []exploration.SourceRef{}
	for rows.Next() {
		var r exploration.SourceRef
		if err := rows.Scan(&r.TaskID, &r.ExplorationID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListByKindWithSources returns local nodes followed by nodes from each direct
// source in relation order.
func (s *ExplorationStore) ListByKindWithSources(kind string, limit int) ([]*exploration.Node, error) {
	return exploration.ListByKindWithSources(s.db, s.expID, kind, limit, s.sources)
}

// ListByKindPageWithSources is the paginated, keyword-filterable sibling of
// ListByKindWithSources (merged newest-first page + filtered total).
func (s *ExplorationStore) ListByKindPageWithSources(kind string, before int64, limit int, q string) ([]*exploration.Node, bool, int, error) {
	return exploration.ListByKindPageWithSources(s.db, s.expID, kind, before, limit, q, s.sources)
}

// GetNodeWithSources reads a node only when it belongs to this exploration or
// one of its direct sources.
func (s *ExplorationStore) GetNodeWithSources(id int64) (*exploration.Node, error) {
	return exploration.GetNodeWithSources(s.db, s.expID, id, s.sources)
}

// FindingIntentsWithSources combines finding lineage for the current
// exploration and each direct source.
func (s *ExplorationStore) FindingIntentsWithSources() (map[int64]int64, error) {
	return exploration.FindingIntentsWithSources(s.db, s.expID, s.sources)
}

// ActivityTraceWithSources returns a work trace when its intent belongs to the
// current exploration or a direct source.
func (s *ExplorationStore) ActivityTraceWithSources(nodeID int64, limit int) ([]model.Activity, error) {
	return exploration.ActivityTraceWithSources(s.db, s.expID, nodeID, limit, s.sources)
}

// ActivityListWithSources is the source-aware equivalent used by
// get_worker_output.
func (s *ExplorationStore) ActivityListWithSources(nodeID, sinceID int64, limit int) ([]model.Activity, int64, error) {
	return exploration.ActivityListWithSources(s.db, s.expID, nodeID, sinceID, limit, s.sources)
}

// ActivityDetailWithSources keeps the legacy local-task lookup (including local
// thinking rows), while inherited details are restricted to terminal worker intents.
func (s *ExplorationStore) ActivityDetailWithSources(id int64) (string, error) {
	return exploration.ActivityDetailWithSources(s.db, s.expID, id, s.sources)
}

// ActivityTraceSearchWithSources performs the scoped worker-trace search used
// by get_worker_trace.
func (s *ExplorationStore) ActivityTraceSearchWithSources(nodeID int64, q string, limit int) ([]model.Activity, error) {
	return exploration.ActivityTraceSearchWithSources(s.db, s.expID, nodeID, q, limit, s.sources)
}

// ActivityTraceSearchAllWithSources searches local worker traces plus every
// direct source.
func (s *ExplorationStore) ActivityTraceSearchAllWithSources(excludeNodeID int64, q string, limit int) ([]model.Activity, error) {
	return exploration.ActivityTraceSearchAllWithSources(s.db, s.expID, excludeNodeID, q, limit, s.sources)
}

// ActivityByIDsWithSources loads local step details with the legacy behavior.
func (s *ExplorationStore) ActivityByIDsWithSources(ids []int64) ([]model.Activity, error) {
	return exploration.ActivityByIDsWithSources(s.db, s.expID, ids, s.sources)
}

// AssetRefsWithSources returns anchored nodes from this exploration and each
// direct source.
func (s *ExplorationStore) AssetRefsWithSources(assetID string) ([]exploration.AssetRef, error) {
	return exploration.AssetRefsWithSources(s.db, s.expID, assetID, s.sources)
}
