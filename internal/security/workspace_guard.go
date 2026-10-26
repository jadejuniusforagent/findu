// workspace_guard.go —— APK 分析域工作区守卫。
//
// security 包薄转发 internal/workspace（随 APK 域一并移植，见 internal/workspace）。
// ChatUploadsRootDir 由 database.DB 注入的 chat_uploads 根（db.SetChatUploadsDir）替代
// storage 配置单读——dbChatUploadsRoot 由 app 装配时（setupAndroidAnalysis 前）
// 调 SetWorkspaceChatUploadsRoot 注入。
package security

import (
	"errors"
	"sync"

	"finduai/internal/workspace"
)

var (
	chatUploadsRootMu sync.RWMutex
	chatUploadsRoot   string
)

// SetWorkspaceChatUploadsRoot 注入 chat_uploads 根目录（app 装配时调用）。
func SetWorkspaceChatUploadsRoot(root string) {
	chatUploadsRootMu.Lock()
	defer chatUploadsRootMu.Unlock()
	chatUploadsRoot = root
}

// ChatUploadsRootDir 返回 chat_uploads 根目录绝对路径（未配置返回错误）。
func ChatUploadsRootDir() (string, error) {
	chatUploadsRootMu.RLock()
	defer chatUploadsRootMu.RUnlock()
	if chatUploadsRoot == "" {
		return "", ErrChatUploadsRootUnset
	}
	return chatUploadsRoot, nil
}

// ErrChatUploadsRootUnset chat_uploads 根尚未注入（装配顺序问题或未配置）。
var ErrChatUploadsRootUnset = errors.New("chat_uploads 根目录未配置")

// PathWithin 判定 p 是否位于 root 之内（词法判定，防目录逃逸）。
func PathWithin(root, p string) bool { return workspace.PathWithin(root, p) }

// ConversationWorkspaceDir 会话工作目录（项目绑定 → projects/<pid>，否则 conversations/<cid>）。
func ConversationWorkspaceDir(base, projectID, conversationID string) string {
	return workspace.ConversationWorkspaceDir(base, projectID, conversationID)
}

// WorkspaceRootDir 工作区根目录绝对路径（空 root 用全局配置/默认）。
func WorkspaceRootDir(root string) (string, error) { return workspace.WorkspaceRootDir(root) }

// ConversationArtifactsDir 会话产物目录（root/<会话ID>）。
func ConversationArtifactsDir(conversationID string) (string, error) {
	return workspace.ConversationArtifactsDir(conversationID)
}

// WorkspaceCrossProjectGuard 项目间工作区硬隔离（转发 workspace 包实现）。
func WorkspaceCrossProjectGuard(root, path, allowedProjectID string) error {
	return workspace.WorkspaceCrossProjectGuard(root, path, allowedProjectID)
}
