package exploration

// 继承上下文（Inherited Context）：把本探索与「直连来源任务」的探索图拼在一起读。
// 原实现在 db/exploration_sources.go（ExplorationStore 方法族）。批 3 第九刀的边界：
//   - 组合逻辑（WithSources 函数族 + 继承标注 helper）归探索域，收窄为包级函数；
//   - 来源关系 SQL 读 tasks × task_relations（任务域表），按规范三-2 留 db 装配层，
//     以 SourceResolver 函数值注入——懒求值（本地未命中才触发），与原行为一致；
//   - 兄弟探索的读取不新开句柄，直接复用同一个 Querier（节点 id 全局唯一）。
//
// 继承条目永不与本探索节点冲突：node_id 全局唯一，所以命中判定唯一的。

import (
	"sort"

	"finduai/core/store"
)

// SourceRef 是一条直连来源关系的最小投影（由 db 装配层解析，读 tasks/task_relations）。
// 完整 DTO（TaskSource + Store 句柄）仍留在 db，供 server/agent 的展示与跳转使用。
type SourceRef struct {
	TaskID        int64
	ExplorationID int64
}

// SourceResolver 解析一个探索的直连来源（懒调用：仅本地未命中/合并页需要时触发）。
type SourceResolver = func(q store.Querier, expID int64) ([]SourceRef, error)

func markInheritedNode(n *Node, taskID int64) *Node {
	if n == nil {
		return nil
	}
	n.SourceTaskID = taskID
	n.Inherited = true
	return n
}

func markInheritedActivities(in []Activity, taskID int64) []Activity {
	for i := range in {
		in[i].SourceTaskID = taskID
		in[i].Inherited = true
	}
	return in
}

func inheritedIntentTerminal(state string) bool {
	switch state {
	case "done", "blocked", "exhausted", "stopped":
		return true
	default:
		return false
	}
}

// ListByKindWithSources returns local nodes followed by nodes from each direct
// source in relation order. The limit remains per exploration, matching the
// existing ListByKind contract while ensuring one large task cannot hide all
// inherited context from another source.
func ListByKindWithSources(q store.Querier, expID int64, kind string, limit int, resolve SourceResolver) ([]*Node, error) {
	out, err := ListByKind(q, expID, kind, limit)
	if err != nil {
		return nil, err
	}
	sources, err := resolve(q, expID)
	if err != nil {
		return nil, err
	}
	for _, source := range sources {
		nodes, err := ListByKind(q, source.ExplorationID, kind, limit)
		if err != nil {
			return nil, err
		}
		for _, node := range nodes {
			if kind == KindIntent && !inheritedIntentTerminal(node.State) {
				continue
			}
			out = append(out, markInheritedNode(node, source.TaskID))
		}
	}
	return out, nil
}

// ListByKindPageWithSources is the paginated, keyword-filterable sibling of
// ListByKindWithSources: it returns one newest-first page (id < before, before<=0
// = newest) spanning this exploration and its direct sources, plus hasMore and the
// filtered total across all of them. Node ids are globally unique, so merging each
// store's own page and re-sorting by id DESC yields the true global page; fetching
// limit+1 per store guarantees the merged top-`limit` is complete.
func ListByKindPageWithSources(q store.Querier, expID int64, kind string, before int64, limit int, kw string, resolve SourceResolver) (nodes []*Node, hasMore bool, total int, err error) {
	if limit <= 0 {
		limit = 20
	}
	own, err := listByKindPageFiltered(q, expID, kind, before, limit, kw)
	if err != nil {
		return nil, false, 0, err
	}
	merged := own
	total, err = countByKindFiltered(q, expID, kind, kw)
	if err != nil {
		return nil, false, 0, err
	}

	sources, err := resolve(q, expID)
	if err != nil {
		return nil, false, 0, err
	}
	for _, source := range sources {
		page, err := listByKindPageFiltered(q, source.ExplorationID, kind, before, limit, kw)
		if err != nil {
			return nil, false, 0, err
		}
		for _, n := range page {
			merged = append(merged, markInheritedNode(n, source.TaskID))
		}
		cnt, err := countByKindFiltered(q, source.ExplorationID, kind, kw)
		if err != nil {
			return nil, false, 0, err
		}
		total += cnt
	}

	sort.Slice(merged, func(i, j int) bool { return merged[i].ID > merged[j].ID })
	hasMore = len(merged) > limit
	if hasMore {
		merged = merged[:limit]
	}
	return merged, hasMore, total, nil
}

// GetNodeWithSources reads a node only when it belongs to this exploration or
// one of its direct sources. Inherited nodes are tagged so tool callers can keep
// them read-only and show their provenance.
func GetNodeWithSources(q store.Querier, expID, id int64, resolve SourceResolver) (*Node, error) {
	node, err := GetNode(q, expID, id)
	if err != nil || node != nil {
		return node, err
	}
	sources, err := resolve(q, expID)
	if err != nil {
		return nil, err
	}
	for _, source := range sources {
		node, err = GetNode(q, source.ExplorationID, id)
		if err != nil {
			return nil, err
		}
		if node != nil {
			if node.Kind == KindIntent && !inheritedIntentTerminal(node.State) {
				continue
			}
			return markInheritedNode(node, source.TaskID), nil
		}
	}
	return nil, nil
}

// FindingIntentsWithSources combines finding lineage for the current
// exploration and each direct source. Node ids are globally unique.
func FindingIntentsWithSources(q store.Querier, expID int64, resolve SourceResolver) (map[int64]int64, error) {
	out, err := FindingIntents(q, expID)
	if err != nil {
		return nil, err
	}
	sources, err := resolve(q, expID)
	if err != nil {
		return nil, err
	}
	for _, source := range sources {
		items, err := FindingIntentsTerminal(q, source.ExplorationID)
		if err != nil {
			return nil, err
		}
		for findingID, intentID := range items {
			out[findingID] = intentID
		}
	}
	return out, nil
}

// ActivityTraceWithSources returns a work trace when its intent belongs to the
// current exploration or a direct source. It never searches indirect sources.
func ActivityTraceWithSources(q store.Querier, expID, nodeID int64, limit int, resolve SourceResolver) ([]Activity, error) {
	acts, err := ActivityTrace(q, expID, nodeID, limit)
	if err != nil || len(acts) > 0 {
		return acts, err
	}
	sources, err := resolve(q, expID)
	if err != nil {
		return nil, err
	}
	for _, source := range sources {
		node, nodeErr := GetNode(q, source.ExplorationID, nodeID)
		if nodeErr != nil {
			return nil, nodeErr
		}
		if node == nil || node.Kind != KindIntent {
			continue
		}
		acts, err = ActivityTraceForTerminalIntent(q, source.ExplorationID, nodeID, limit)
		if err != nil {
			return nil, err
		}
		if len(acts) > 0 {
			return markInheritedActivities(acts, source.TaskID), nil
		}
	}
	return []Activity{}, nil
}

// ActivityListWithSources is the source-aware equivalent used by
// get_worker_output. Node ids are global, so the first owning exploration is
// unambiguous even when the work has not emitted any activity yet.
func ActivityListWithSources(q store.Querier, expID, nodeID, sinceID int64, limit int, resolve SourceResolver) ([]Activity, int64, error) {
	node, err := GetNode(q, expID, nodeID)
	if err != nil {
		return nil, sinceID, err
	}
	if node != nil {
		return ActivityList(q, expID, &nodeID, sinceID, limit)
	}
	sources, err := resolve(q, expID)
	if err != nil {
		return nil, sinceID, err
	}
	for _, source := range sources {
		node, err = GetNode(q, source.ExplorationID, nodeID)
		if err != nil {
			return nil, sinceID, err
		}
		if node == nil || node.Kind != KindIntent {
			continue
		}
		acts, cursor, err := ActivityListForTerminalIntent(q, source.ExplorationID, nodeID, sinceID, limit)
		if err != nil {
			return nil, sinceID, err
		}
		return markInheritedActivities(acts, source.TaskID), cursor, nil
	}
	return []Activity{}, sinceID, nil
}

// ActivityDetailWithSources keeps the legacy local-task lookup (including local
// thinking rows), while inherited details are restricted to terminal worker
// intents. Source planner/main rows have no node id and must never become part of
// inherited context.
func ActivityDetailWithSources(q store.Querier, expID, id int64, resolve SourceResolver) (string, error) {
	detail, err := ActivityDetail(q, expID, id)
	if err != nil || detail != "" {
		return detail, err
	}
	acts, err := ActivityByIDsWithSources(q, expID, []int64{id}, resolve)
	if err != nil || len(acts) == 0 {
		return "", err
	}
	return acts[0].Detail, nil
}

// ActivityTraceSearchWithSources performs the scoped worker-trace search used
// by get_worker_trace. A node id resolves to at most one exploration because
// exploration node ids are global.
func ActivityTraceSearchWithSources(q store.Querier, expID, nodeID int64, kw string, limit int, resolve SourceResolver) ([]Activity, error) {
	acts, err := ActivityTraceSearch(q, expID, &nodeID, kw, limit)
	if err != nil || len(acts) > 0 {
		return acts, err
	}
	sources, err := resolve(q, expID)
	if err != nil {
		return nil, err
	}
	for _, source := range sources {
		node, nodeErr := GetNode(q, source.ExplorationID, nodeID)
		if nodeErr != nil {
			return nil, nodeErr
		}
		if node == nil || node.Kind != KindIntent {
			continue
		}
		acts, err = ActivityTraceSearchForTerminalIntent(q, source.ExplorationID, nodeID, kw, limit)
		if err != nil {
			return nil, err
		}
		if len(acts) > 0 {
			return markInheritedActivities(acts, source.TaskID), nil
		}
	}
	return []Activity{}, nil
}

// ActivityTraceSearchAllWithSources searches local worker traces plus every
// direct source. The local owner is excluded only from the current exploration;
// inherited traces are immutable historical context.
func ActivityTraceSearchAllWithSources(q store.Querier, expID, excludeNodeID int64, kw string, limit int, resolve SourceResolver) ([]Activity, error) {
	if limit <= 0 {
		limit = 100
	}
	out, err := ActivityTraceSearchExcluding(q, expID, excludeNodeID, kw, limit)
	if err != nil {
		return nil, err
	}
	sources, err := resolve(q, expID)
	if err != nil {
		return nil, err
	}
	for _, source := range sources {
		acts, err := ActivityTraceSearchTerminalIntents(q, source.ExplorationID, kw, limit)
		if err != nil {
			return nil, err
		}
		out = append(out, markInheritedActivities(acts, source.TaskID)...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ActivityByIDsWithSources loads local step details with the legacy behavior. For
// direct sources it only returns rows attached to terminal intents, preventing an
// arbitrary global activity id from exposing source planner/main transcripts.
func ActivityByIDsWithSources(q store.Querier, expID int64, ids []int64, resolve SourceResolver) ([]Activity, error) {
	out, err := ActivityByIDs(q, expID, ids)
	if err != nil {
		return nil, err
	}
	sources, err := resolve(q, expID)
	if err != nil {
		return nil, err
	}
	for _, source := range sources {
		acts, err := ActivityByIDsForTerminalIntents(q, source.ExplorationID, ids)
		if err != nil {
			return nil, err
		}
		out = append(out, markInheritedActivities(acts, source.TaskID)...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// AssetRefsWithSources returns anchored nodes from this exploration and each
// direct source. Inherited entries retain their owning task id so API/UI callers
// can present them as immutable context.
func AssetRefsWithSources(q store.Querier, expID int64, assetID string, resolve SourceResolver) ([]AssetRef, error) {
	out, err := AssetRefs(q, expID, assetID)
	if err != nil {
		return nil, err
	}
	sources, err := resolve(q, expID)
	if err != nil {
		return nil, err
	}
	for _, source := range sources {
		refs, err := AssetRefs(q, source.ExplorationID, assetID)
		if err != nil {
			return nil, err
		}
		for i := range refs {
			if refs[i].Kind == KindIntent && !inheritedIntentTerminal(refs[i].State) {
				continue
			}
			refs[i].SourceTaskID = source.TaskID
			refs[i].Inherited = true
			out = append(out, refs[i])
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}
