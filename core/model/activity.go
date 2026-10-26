// Package model 存放跨域共享的行值类型。
//
// 放这里的判据：一个类型的行写进了多个域的表（或被多个域读取），任何单一 domain
// 定义它都会造成反向依赖——按 STEP3-CONVENTIONS 规范三-4 上提。目前只有 Activity：
// 同构的行同时存在于探索域 activity 表与会话域 conversation_activities 表，
// 拦截审批的执行导航也按它扫描。
//
// 分层约束：本包只能是纯类型+纯函数，禁止 import database/sql 与任何 finduai 子包，
// 保证 core/domain 都能引用。
package model

import (
	"encoding/json"
	"time"
)

// Activity is one recorded execution step (tool call, result, text, …). The same
// struct serves the exploration graph (activity) and the conversation transcript
// (conversation_activities); NodeID/MainSeg are ignored by the conversation side.
type Activity struct {
	ID        int64           `json:"id"`
	NodeID    *int64          `json:"node_id,omitempty"`
	Worker    string          `json:"worker,omitempty"`
	Kind      string          `json:"kind,omitempty"`
	Tool      string          `json:"tool,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	IsError   bool            `json:"is_error"`
	Summary   string          `json:"summary,omitempty"`
	Detail    string          `json:"-"` // full blob; lazy-loaded, omitted from list payloads
	Metadata  json.RawMessage `json:"metadata,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	// Token usage — set only on the terminal kind='result' record; nil elsewhere.
	InputTokens      *int  `json:"input_tokens,omitempty"`
	OutputTokens     *int  `json:"output_tokens,omitempty"`
	CacheReadTokens  *int  `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens *int  `json:"cache_write_tokens,omitempty"`
	SourceTaskID     int64 `json:"source_task_id,omitempty"`
	Inherited        bool  `json:"inherited,omitempty"`
	// MainSeg is the main-agent conversation segment (nil for non-mainagent rows;
	// nil/0 == the original session). Lets the UI route a mainagent row to its segment.
	MainSeg *int `json:"main_seg,omitempty"`
}
