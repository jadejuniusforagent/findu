package exploration

// 探索域词表常量（节点 kind / 状态 / 边关系）。批 3 第九刀自 db/constants.go
// 全量上提：它们是探索图（exploration_nodes / exploration_edges）的词汇表，
// 仅探索域语义使用。db 侧以 const 转发保持调用点零改动。

// Exploration node kinds (exploration_nodes.kind).
const (
	KindBegin   = "begin"   // DEPRECATED: legacy task root; new tasks seed an origin fact (KindFact + StateOrigin) instead
	KindGoal    = "goal"    // a task objective
	KindIntent  = "intent"  // an exploration direction (planner-generated)
	KindFact    = "fact"    // a worker's exploration result/conclusion (incl. negative results), tied to its intent
	KindFinding = "finding" // a confirmed vulnerability (report_finding), distinct from a fact
	KindHint    = "hint"
	KindDigest  = "digest" // a compressed fold of cold intents/facts (cold-digest-spec §1); lossless — members kept, restorable by id
)

// Digest node states (kind='digest'). A digest is 'active' while it renders in
// graph_overview; major compaction retires a merged-away segment to 'superseded'
// (its covers edges repointed to the new digest) — cold-digest-spec §5.1.
const (
	StateDigestActive     = "active"
	StateDigestSuperseded = "superseded"
)

// StateOrigin marks the task root fact (KindFact) seeded at task creation — the
// exploration graph's origin. Every intent traces back to it, so "an intent must
// connect to a fact" holds uniformly from the very first intent. Worker-produced
// facts use state 'confirmed', so this never collides.
const StateOrigin = "origin"

// StateIntentDeleted marks an intent the user假删除(soft delete): it drops out of
// the frontier and graph_overview like other terminal states, but keeps its node
// and full lineage. The delete reason lives in exploration_nodes.delete_reason.
const StateIntentDeleted = "deleted"

// Exploration edge relations (exploration_edges.rel).
const (
	RelSpawns      = "spawns"
	RelDerivedFrom = "derived_from"
	RelYields      = "yields"
	RelProves      = "proves"
	RelCovers      = "covers" // digest --covers--> member (cold-digest-spec §1); source of truth for "which digest folds node X"
)

// IntentBlockedLLMQuota 是 intent 因 LLM 配额耗尽被标记 blocked 时的
// blocked_reason 词值（第十刀自 db/task_context.go 上提；与同批补收的两个
// 探索域方法 SetIntentBlockedReason / ReopenIntentsByBlockedReason 配套）。
const IntentBlockedLLMQuota = "llm_quota_exhausted"
