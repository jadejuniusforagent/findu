package db

// 会话线程与话步的存储层已迁到 domain/conversation（Step 3 批 3 第六刀）。
// 本文件保留：
//  1. 类型别名 —— 调用点零改动；
//  2. 方法转发 —— 纯会话表 SQL 委托 domain/conversation；
//  3. 两个跨域函数留在装配层 —— CreateConversation / UpdateConversationProfile
//     要执行 lockLLMProfileForReference 的「子行→profile 行」加锁协议（会话域
//     ×LLM 配置域，一个事务两个域），不整段搬。
//
// 另：chat_mentions.go（会话引用搜索）跨 findings/companies/assets 三域，
// 同属装配层跨域函数，留在 db。

import (
	"database/sql"
	"finduai/core/model"

	dconv "finduai/domain/conversation"
)

// ---- 会话线程（表 conversations）----

func (d *DB) ConversationTokenSummaries() ([]dconv.ConvTokenSummary, error) {
	return dconv.ConversationTokenSummaries(d)
}

func (d *DB) ListConversations() ([]*dconv.Conversation, error) {
	return dconv.ListConversations(d)
}

func (d *DB) GetConversation(id string) (*dconv.Conversation, error) {
	return dconv.GetConversation(d, id)
}

func (d *DB) UpdateConversation(id string, patch dconv.ConversationPatch) (*dconv.Conversation, error) {
	return dconv.UpdateConversation(d, id, patch)
}

func (d *DB) RenameConversation(id string, title string) error {
	return dconv.RenameConversation(d, id, title)
}

func (d *DB) TouchConversation(id string) error {
	return dconv.TouchConversation(d, id)
}

func (d *DB) DeleteConversation(id string) error {
	return dconv.DeleteConversation(d, id)
}

func (d *DB) DeleteConversations(ids []string) ([]string, error) {
	return dconv.DeleteConversations(d, ids)
}

// ---- 话步（表 conversation_activities）----

func (d *DB) AppendConvActivity(convID string, a model.Activity) (int64, error) {
	return dconv.AppendConvActivity(d, convID, a)
}

func (d *DB) ConvActivityList(convID string, sinceID int64, limit int) ([]model.Activity, int64, error) {
	return dconv.ConvActivityList(d, convID, sinceID, limit)
}

func (d *DB) ConvActivityPage(convID string, before int64, limit int) ([]model.Activity, bool, error) {
	return dconv.ConvActivityPage(d, convID, before, limit)
}

func (d *DB) ConvActivityDetail(convID string, id int64) (string, error) {
	return dconv.ConvActivityDetail(d, convID, id)
}

// ---- 以下两个跨域函数留在装配层（见文件头第 3 点）----

// CreateConversation opens a new chat thread for agentKey with an initial title.
// llmProfileID may be nil to use the globally active profile.
//
// 事务里执行「先插子行（无 profile）→ 锁 profile → 再绑 profile」的既有协议：
// 新子行在拿到 profile 锁之前只归本事务所有，与 DeleteProfile 的删除协议互斥。
func (d *DB) CreateConversation(agentKey, title string, llmProfileID *int64) (*dconv.Conversation, error) {
	tx, err := d.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	c, err := dconv.ScanConversation(tx.QueryRow(`
INSERT INTO conversations(agent_key, title, llm_profile_id) VALUES ($1, $2, NULL)
RETURNING `+dconv.ConversationCols, agentKey, title))
	if err != nil {
		return nil, err
	}
	if err := lockLLMProfileForReference(tx, llmProfileID); err != nil {
		return nil, err
	}
	if llmProfileID != nil {
		c, err = dconv.ScanConversation(tx.QueryRow(`UPDATE conversations SET llm_profile_id=$2
WHERE id=$1 RETURNING `+dconv.ConversationCols, c.ID, llmProfileID))
		if err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return c, nil
}

// UpdateConversationProfile sets (or clears) the LLM profile override for a conversation.
// 同一加锁协议：先 FOR UPDATE 锁住会话行，未知的 id 保持旧的 UPDATE 语义（no-op）。
func (d *DB) UpdateConversationProfile(id string, llmProfileID *int64) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var lockedID string
	if err := tx.QueryRow(`SELECT id FROM conversations WHERE id=$1 FOR UPDATE`, id).Scan(&lockedID); err != nil {
		if err == sql.ErrNoRows {
			// Preserve the previous UPDATE semantics: an unknown id is a no-op.
			return tx.Commit()
		}
		return err
	}
	if err := lockLLMProfileForReference(tx, llmProfileID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE conversations SET llm_profile_id=$2 WHERE id=$1`, id, llmProfileID); err != nil {
		return err
	}
	return tx.Commit()
}
