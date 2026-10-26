package db

// Step 6d Phase B8：chat_upload_artifacts 元数据表 + 会话/项目名称投影的
// 原生命中接口。文件本体在进程 cwd 的 chat_uploads / tmp/reduction /
// tmp/workspace / data/conversation_artifacts 四根下，由 server 原生 handler
// 直接操作文件系统；本文件只负责 findu internal/database/chat_upload.go 的
// 元数据 SQL（PG 方言 $n）与三级名称缓存查询。

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"time"
)

// ChatUploadStore operates on the shared-PG chat_upload_artifacts table.
type ChatUploadStore struct{ db *DB }

// ChatUploads returns the chat-upload artifact store.
func (d *DB) ChatUploads() *ChatUploadStore { return &ChatUploadStore{db: d} }

// chatUploadEscapeLike 对齐 findu escapeLikePrefix：转义 LIKE 通配符。
func chatUploadEscapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "%", `\%`)
	return strings.ReplaceAll(s, "_", `\_`)
}

// UpsertChatUploadArtifact registers/refreshes artifact ownership metadata.
func (s *ChatUploadStore) UpsertChatUploadArtifact(ctx context.Context, relativePath, conversationID, ownerUserID string) error {
	relativePath = strings.TrimSpace(relativePath)
	conversationID = strings.TrimSpace(conversationID)
	ownerUserID = strings.TrimSpace(ownerUserID)
	if relativePath == "" || conversationID == "" || ownerUserID == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO chat_upload_artifacts(relative_path, conversation_id, owner_user_id, created_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT(relative_path) DO UPDATE SET conversation_id=excluded.conversation_id, owner_user_id=excluded.owner_user_id`,
		relativePath, conversationID, ownerUserID, time.Now())
	return err
}

// GetChatUploadArtifact resolves (conversation, owner) for a relative path.
func (s *ChatUploadStore) GetChatUploadArtifact(ctx context.Context, relativePath string) (conversationID, ownerUserID string, ok bool) {
	var conv, owner sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT conversation_id, owner_user_id FROM chat_upload_artifacts WHERE relative_path = $1`,
		strings.TrimSpace(relativePath)).Scan(&conv, &owner)
	if err != nil {
		return "", "", false
	}
	return conv.String, owner.String, true
}

// DeleteChatUploadArtifactPath deletes metadata for a path (or its subtree).
func (s *ChatUploadStore) DeleteChatUploadArtifactPath(ctx context.Context, path string) (int64, error) {
	path = strings.Trim(strings.TrimSpace(path), "/")
	if path == "" {
		return 0, nil
	}
	prefix := chatUploadEscapeLike(path) + "/%"
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM chat_upload_artifacts WHERE relative_path = $1 OR relative_path LIKE $2 ESCAPE '\'`,
		path, prefix)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// RenameChatUploadArtifactPath renames metadata rows for path/subtree.
func (s *ChatUploadStore) RenameChatUploadArtifactPath(ctx context.Context, oldPath, newPath string) (int64, error) {
	oldPath = strings.Trim(strings.TrimSpace(oldPath), "/")
	newPath = strings.Trim(strings.TrimSpace(newPath), "/")
	if oldPath == "" || newPath == "" {
		return 0, nil
	}
	prefix := chatUploadEscapeLike(oldPath) + "/%"
	// PG 方言重写 findu 的 substr 拼 HEAD 段：newPath || substr(relative_path, len(oldPath)+1)。
	res, err := s.db.ExecContext(ctx, `
		UPDATE chat_upload_artifacts
		SET relative_path = CASE
			WHEN relative_path = $1 THEN $2
			ELSE $2 || substr(relative_path, $3)
		END
		WHERE relative_path = $1 OR relative_path LIKE $4 ESCAPE '\'`,
		oldPath, newPath, strconv.Itoa(len(oldPath)+1), prefix)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ConversationProjectID 列表填充用：conversation → project_id（缺省空）。
func (s *ChatUploadStore) ConversationProjectID(ctx context.Context, conversationID string) string {
	var pid sql.NullString
	if err := s.db.QueryRowContext(ctx,
		`SELECT project_id FROM conversations WHERE id = $1`, conversationID).Scan(&pid); err != nil {
		return ""
	}
	return strings.TrimSpace(pid.String)
}

// ConversationTitle 列表填充用：conversation 标题（缺省空）。
func (s *ChatUploadStore) ConversationTitle(ctx context.Context, conversationID string) string {
	var title string
	if err := s.db.QueryRowContext(ctx,
		`SELECT title FROM conversations WHERE id = $1`, conversationID).Scan(&title); err != nil {
		return ""
	}
	return title
}

// ProjectName 列表填充用：项目名（缺省空）。
func (s *ChatUploadStore) ProjectName(ctx context.Context, projectID string) string {
	var name string
	if err := s.db.QueryRowContext(ctx,
		`SELECT name FROM projects WHERE id = $1`, projectID).Scan(&name); err != nil {
		return ""
	}
	return strings.TrimSpace(name)
}
