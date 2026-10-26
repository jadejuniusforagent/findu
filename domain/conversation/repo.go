// 会话线程与话步（表 conversations / conversation_activities）的存储层。
//
// 迁移边界（Step 3 批 3 第六刀）：
//   - CreateConversation / UpdateConversationProfile 留在 db 装配层——它们要走
//     lockLLMProfileForReference 的「子行→profile 行」加锁协议，一个事务跨会话域
//     与 LLM 配置域，按规范三-2 不整段搬；
//   - chat_mentions.go 留在 db——跨 findings/companies/assets 三域的 catalog 搜索；
//   - Activity 上提 core/model（同批），本包经 model.Activity 使用。
package conversation

import (
	"database/sql"
	"time"

	"finduai/core/model"
	"finduai/core/store"
)

// ConvTokenSummary is one conversation's token total (sum of its kind='result'
// rows) with its profile + created_at, used to merge conversation usage into the
// dashboard's per-profile / daily token stats (which otherwise cover only tasks).
type ConvTokenSummary struct {
	LLMProfileID     *int64 `json:"llm_profile_id"`
	CreatedAt        string `json:"created_at"`
	InputTokens      int    `json:"input_tokens"`
	OutputTokens     int    `json:"output_tokens"`
	CacheReadTokens  int    `json:"cache_read_tokens"`
	CacheWriteTokens int    `json:"cache_write_tokens"`
}

// ConversationTokenSummaries returns one row per conversation with its summed
// result-row token usage (0 for conversations with no completed run yet).
func ConversationTokenSummaries(q store.Querier) ([]ConvTokenSummary, error) {
	rows, err := q.Query(`
SELECT c.llm_profile_id, c.created_at::text,
       COALESCE(sum(ca.input_tokens),0), COALESCE(sum(ca.output_tokens),0),
       COALESCE(sum(ca.cache_read_tokens),0), COALESCE(sum(ca.cache_write_tokens),0)
FROM conversations c
LEFT JOIN conversation_activities ca ON ca.conversation_id = c.id AND ca.kind = 'result'
GROUP BY c.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ConvTokenSummary{}
	for rows.Next() {
		var s ConvTokenSummary
		var pid sql.NullInt64
		if err := rows.Scan(&pid, &s.CreatedAt, &s.InputTokens, &s.OutputTokens, &s.CacheReadTokens, &s.CacheWriteTokens); err != nil {
			return nil, err
		}
		if pid.Valid {
			v := pid.Int64
			s.LLMProfileID = &v
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Conversation is one ChatGPT-style chat thread bound to an agent key. It lives
// independent of the pentest exploration graph — see schema.sql §I.
type Conversation struct {
	ID           string     `json:"id"`
	AgentKey     string     `json:"agent_key"`
	Title        string     `json:"title"`
	LLMProfileID *int64     `json:"llm_profile_id,omitempty"`
	Pinned       bool       `json:"pinned"`
	PinnedAt     *time.Time `json:"pinned_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// ConversationPatch updates only the fields whose pointers are non-nil.
type ConversationPatch struct {
	Title  *string
	Pinned *bool
}

const convCols = `id, COALESCE(agent_key, 'findu_default') AS agent_key, title, llm_profile_id, pinned_at, created_at, updated_at`

// ConversationCols 导出会话列清单：CreateConversation / UpdateConversationProfile
// 的「子行→profile 行」加锁协议仍在 db 装配层（跨会话域与 LLM 配置域），那里要拼
// RETURNING 子句，复用本清单而不是复制字符串。
const ConversationCols = convCols

func scanConv(row interface{ Scan(...any) error }) (*Conversation, error) {
	var c Conversation
	var pinnedAt sql.NullTime
	err := row.Scan(&c.ID, &c.AgentKey, &c.Title, &c.LLMProfileID, &pinnedAt, &c.CreatedAt, &c.UpdatedAt)
	if pinnedAt.Valid {
		c.Pinned = true
		c.PinnedAt = &pinnedAt.Time
	}
	return &c, err
}

// ScanConversation 把一行扫描成 *Conversation，导出给装配层（db.CreateConversation
// 等锁协议函数）复用同一个列序。
func ScanConversation(row interface{ Scan(...any) error }) (*Conversation, error) {
	return scanConv(row)
}

// ListConversations returns all threads, most-recently-updated first.
func ListConversations(q store.Querier) ([]*Conversation, error) {
	rows, err := q.Query(`SELECT ` + convCols + ` FROM conversations
ORDER BY (pinned_at IS NOT NULL) DESC, pinned_at DESC NULLS LAST, updated_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Conversation{}
	for rows.Next() {
		c, err := scanConv(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetConversation returns one thread (nil, nil if absent).
func GetConversation(q store.Querier, id string) (*Conversation, error) {
	c, err := scanConv(q.QueryRow(`SELECT `+convCols+` FROM conversations WHERE id=$1`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return c, nil
}

// UpdateConversation applies a partial title/pin mutation and returns the updated
// row. Pinning an already-pinned conversation preserves its original pin order.
func UpdateConversation(q store.Querier, id string, patch ConversationPatch) (*Conversation, error) {
	c, err := scanConv(q.QueryRow(`UPDATE conversations SET
	title = CASE WHEN $2::boolean THEN $3 ELSE title END,
	pinned_at = CASE
		WHEN $4::boolean IS NULL THEN pinned_at
		WHEN $4::boolean THEN COALESCE(pinned_at, now())
		ELSE NULL
	END
WHERE id=$1
RETURNING `+convCols, id, patch.Title != nil, patch.Title, patch.Pinned))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return c, nil
}

// RenameConversation sets a thread's title. Kept for automatic first-message
// titles and compatibility with existing callers.
func RenameConversation(q store.Querier, id string, title string) error {
	_, err := UpdateConversation(q, id, ConversationPatch{Title: &title})
	return err
}

// TouchConversation bumps updated_at so the thread floats to the top of the list.
func TouchConversation(q store.Querier, id string) error {
	_, err := q.Exec(`UPDATE conversations SET updated_at=now() WHERE id=$1`, id)
	return err
}

// DeleteConversation removes a thread; its activities cascade via FK.
func DeleteConversation(q store.Querier, id string) error {
	_, err := q.Exec(`DELETE FROM conversations WHERE id=$1`, id)
	return err
}

// DeleteConversations removes existing threads in one statement and returns the
// ids that were actually present. Child activities and trigger runs cascade.
func DeleteConversations(q store.Querier, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return []string{}, nil
	}
	rows, err := q.Query(`DELETE FROM conversations WHERE id=ANY($1::text[]) RETURNING id`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	deleted := make([]string, 0, len(ids))
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		deleted = append(deleted, id)
	}
	return deleted, rows.Err()
}

// AppendConvActivity records one step of a conversation (human message or an agent
// execution step) and returns its id. Mirrors the exploration AppendActivity but
// keyed by conversation_id. Reuses model.Activity (NodeID is ignored here).
func AppendConvActivity(q store.Querier, convID string, a model.Activity) (int64, error) {
	var id int64
	err := q.QueryRow(`
INSERT INTO conversation_activities(conversation_id, worker, kind, tool, tool_use_id, is_error, summary, detail, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens)
VALUES ($1,NULLIF($2,''),NULLIF($3,''),NULLIF($4,''),NULLIF($5,''),$6,NULLIF($7,''),NULLIF($8,''),$9,$10,$11,$12)
RETURNING id`, convID, store.UTF8Clean(a.Worker), store.UTF8Clean(a.Kind), store.UTF8Clean(a.Tool), store.UTF8Clean(a.ToolUseID), a.IsError,
		store.UTF8Clean(a.Summary), store.UTF8Clean(a.Detail), a.InputTokens, a.OutputTokens, a.CacheReadTokens, a.CacheWriteTokens).Scan(&id)
	return id, err
}

// convActivityCols is the summary-only column set (detail is lazy-loaded).
const convActivityCols = `id, COALESCE(worker,''), COALESCE(kind,''), COALESCE(tool,''), COALESCE(tool_use_id,''), is_error, COALESCE(summary,''), created_at, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens`

func scanConvActivity(rows interface{ Scan(...any) error }, a *model.Activity) error {
	return rows.Scan(&a.ID, &a.Worker, &a.Kind, &a.Tool, &a.ToolUseID, &a.IsError, &a.Summary, &a.CreatedAt,
		&a.InputTokens, &a.OutputTokens, &a.CacheReadTokens, &a.CacheWriteTokens)
}

// ConvActivityList returns a conversation's steps after sinceID (exclusive), ascending.
func ConvActivityList(q store.Querier, convID string, sinceID int64, limit int) ([]model.Activity, int64, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := q.Query(`SELECT `+convActivityCols+`
FROM conversation_activities WHERE conversation_id=$1 AND id>$2 ORDER BY id LIMIT $3`, convID, sinceID, limit)
	if err != nil {
		return nil, sinceID, err
	}
	defer rows.Close()
	out := []model.Activity{}
	cursor := sinceID
	for rows.Next() {
		var a model.Activity
		if err := scanConvActivity(rows, &a); err != nil {
			return nil, sinceID, err
		}
		if a.ID > cursor {
			cursor = a.ID
		}
		out = append(out, a)
	}
	return out, cursor, rows.Err()
}

// ConvActivityPage returns one page for reverse (newest-first) pagination: up to
// `limit` steps ending before id `before` (exclusive; before<=0 = the latest
// page), returned in ASCENDING id order. hasMore reports whether still-older steps
// exist before the returned window, so the client can stop loading earlier history
// on scroll-up. Summary-only columns (detail is lazy-loaded via ConvActivityDetail).
func ConvActivityPage(q store.Querier, convID string, before int64, limit int) ([]model.Activity, bool, error) {
	if limit <= 0 {
		limit = 200
	}
	// fetch one extra row to detect whether older history remains before this window.
	rows, err := q.Query(`SELECT `+convActivityCols+`
FROM conversation_activities
WHERE conversation_id=$1 AND ($2 <= 0 OR id < $2)
ORDER BY id DESC LIMIT $3`, convID, before, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	desc := []model.Activity{}
	for rows.Next() {
		var a model.Activity
		if err := scanConvActivity(rows, &a); err != nil {
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
	out := make([]model.Activity, len(desc))
	for i, a := range desc {
		out[len(desc)-1-i] = a
	}
	return out, hasMore, nil
}

// ConvActivityDetail lazily returns the full detail blob for one step.
func ConvActivityDetail(q store.Querier, convID string, id int64) (string, error) {
	var s sql.NullString
	err := q.QueryRow(`SELECT detail FROM conversation_activities WHERE id=$1 AND conversation_id=$2`, id, convID).Scan(&s)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return s.String, err
}
