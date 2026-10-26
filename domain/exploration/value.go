package exploration

// 探索域值类型（批 3 第九刀自 db/exploration.go 上提）：
// 节点 / Token 台账 / 事务产物 / 分页过滤 / 主会话 / 资产引用等行值与请求值。
// Activity 行值是探索域 activity 表与会话域 conversation_activities 表的共用
// 结构，类型别名按规范三-4 收口在 core/model（第六刀），本包直接引用。

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"finduai/core/model"
)

// ErrIntentStateConflict marks a failed compare-and-set transition. Callers use
// it to distinguish an expected control race (HTTP 409) from a storage failure.
var ErrIntentStateConflict = errors.New("intent state changed concurrently")

// Activity is one worker execution step (探索域 activity 表的行值类型，
// 与会话域 conversation_activities 同构共用：别名收口在 core/model，第六刀)。
type Activity = model.Activity

// Node is a typed reasoning node (= old task_nodes). kind ∈ goal|intent|finding|hint.
type Node struct {
	FindingID     int64           `json:"finding_id,omitempty"` // populated by finding-aware reads; never inferred from ID
	FindingNodeID int64           `json:"finding_node_id,omitempty"`
	TrafficCount  int             `json:"traffic_count,omitempty"`
	ID            int64           `json:"id"`
	Kind          string          `json:"kind"`
	Payload       json.RawMessage `json:"payload"`
	Priority      int             `json:"priority"`
	State         string          `json:"state"`
	Origin        string          `json:"origin,omitempty"`
	Owner         string          `json:"owner,omitempty"`
	BlockedReason string          `json:"blocked_reason,omitempty"`
	DeleteReason  string          `json:"delete_reason,omitempty"` // 仅意图假删除(state='deleted')时非空
	Anchors       []int64         `json:"anchors,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	SourceTaskID  int64           `json:"source_task_id,omitempty"`
	Inherited     bool            `json:"inherited,omitempty"`
}

// TokenUsage is a per-worker token aggregate (TokenStatsByWorker).
type TokenUsage struct {
	Worker           string `json:"worker"`
	InputTokens      int    `json:"input_tokens"`
	OutputTokens     int    `json:"output_tokens"`
	CacheReadTokens  int    `json:"cache_read_tokens"`
	CacheWriteTokens int    `json:"cache_write_tokens"`
}

func (u *TokenUsage) add(other TokenUsage) {
	u.InputTokens += other.InputTokens
	u.OutputTokens += other.OutputTokens
	u.CacheReadTokens += other.CacheReadTokens
	u.CacheWriteTokens += other.CacheWriteTokens
}

// SessionTokenUsage is the authoritative completed-run total for one task UI
// session. Worker sessions are keyed by intent rather than the reusable work#N
// executor name, so retries and reassignment remain attached to the same row.
type SessionTokenUsage struct {
	Session          string `json:"session"`
	InputTokens      int    `json:"input_tokens"`
	OutputTokens     int    `json:"output_tokens"`
	CacheReadTokens  int    `json:"cache_read_tokens"`
	CacheWriteTokens int    `json:"cache_write_tokens"`
}

// DailyTokenBucket is one day's global token aggregate across all tasks.
type DailyTokenBucket struct {
	Day             string `json:"day"` // "YYYY-MM-DD"
	InputTokens     int    `json:"input_tokens"`
	OutputTokens    int    `json:"output_tokens"`
	CacheReadTokens int    `json:"cache_read_tokens"`
}

// TokenRollupBucket is one UTC day's exactly-once usage produced by
// IntentTokenRollup (取消意图后的不可逆计量回写)。
type TokenRollupBucket struct {
	Day   time.Time
	Usage TokenUsage
}

// IntentCleanup summarizes the blackboard records removed when a user cancels
// one worker intent. Global assets and traffic are deliberately not part of this
// operation; exploration anchors disappear only because their owning nodes do.
type IntentCleanup struct {
	Intents    int64 `json:"intents"`
	Facts      int64 `json:"facts"`
	Findings   int64 `json:"findings"`
	Activities int64 `json:"activities"`
}

// Edge is one row from exploration_edges.
type Edge struct {
	From int64
	Rel  string
	To   int64
}

// GoalCounts is the goal summary for one exploration.
type GoalCounts struct{ Total, Met int }

// FindingSeverityCounts breaks a task's findings down by severity for the task
// list (severity 白名单外/为空的记录不计入任一档)。
type FindingSeverityCounts struct{ Critical, High, Medium, Low int }

// TaskListMetrics contains the aggregates rendered in task lists.
type TaskListMetrics struct {
	Tokens         TokenUsage
	LastActivity   int64
	Goals          GoalCounts
	RunningIntents int                   // kind='intent' 且 state='running' 的条数，即运行中 Worker 数
	Findings       FindingSeverityCounts // findings 表里该任务的漏洞数（按严重度分档）
}

// NodeFilter narrows a NodesPage query. Empty fields include every value.
type NodeFilter struct {
	Kinds  []string // exploration_nodes.kind
	States []string // exploration_nodes.state
	Query  string   // case-insensitive substring over payload text / origin
	Asc    bool     // true = oldest first (replay); default newest first (broadcast)
}

// ActivitySessionFilter selects one UI "session" within a task's activity stream.
// Exactly one field is meaningful:
//   - Main == true  → the main-agent session (worker="mainagent") for one segment
//     (MainSeg; nil/0 = the original segment, which also matches legacy NULL rows).
//   - Worker != ""  → filter by worker name (Plan = "planner").
//     Goal Agent 的第 0 轮拆解也以 worker="planner" 落库，故 Plan 会话完整覆盖 Goal+Planner。
//   - NodeID != nil → a Worker session, filtered by node_id (= intent id).
//
// A zero value (all empty) matches the whole task (no session filter).
type ActivitySessionFilter struct {
	Worker  string
	NodeID  *int64
	Main    bool // main-agent session
	MainSeg *int // segment for the main session (nil == current, resolved by caller; 0 == original)
}

func (f ActivitySessionFilter) cond(argStart int) (string, []any) {
	switch {
	case f.NodeID != nil:
		return fmt.Sprintf(" AND node_id=$%d", argStart), []any{*f.NodeID}
	case f.Main:
		seg := 0
		if f.MainSeg != nil {
			seg = *f.MainSeg
		}
		if seg == 0 { // original segment also owns legacy rows with NULL main_seg
			return " AND worker='mainagent' AND COALESCE(main_seg,0)=0", nil
		}
		return fmt.Sprintf(" AND worker='mainagent' AND main_seg=$%d", argStart), []any{seg}
	case f.Worker != "":
		return fmt.Sprintf(" AND worker=$%d", argStart), []any{f.Worker}
	default:
		return "", nil
	}
}

// MainSession is one resettable main-agent conversation segment of a task. Segment 0
// is the original session (implicit, never stored); further segments are created by
// "新建会话" to start the main agent on a clean transcript while the task's graph,
// assets and goal stay shared.
type MainSession struct {
	Seq       int       `json:"seq"`
	CreatedAt time.Time `json:"created_at"`
}

// AssetRef is a compact exploration node (intent/fact/finding) that references an
// asset via an anchor — for the coverage graph's node drawer.
type AssetRef struct {
	ID           int64  `json:"id"`
	Kind         string `json:"kind"`
	State        string `json:"state"`
	Summary      string `json:"summary"`
	SourceTaskID int64  `json:"source_task_id,omitempty"`
	Inherited    bool   `json:"inherited,omitempty"`
}

// StampOp is one cold_since_round change: Set stamps Round; !Set clears it.
type StampOp struct {
	ID    int64
	Set   bool
	Round int64
}
