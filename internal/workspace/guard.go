// Package workspace 平台工作区与共享目录的单一来源：越界防护、目录注册表、
// 生命周期策略（TTL/容量/引用保护）与回收执行。立项背景见
// docs/archive/工作区与目录治理方案.md（W1）。
//
// 本文件由 internal/security/workspace_guard.go 原样平移（零行为变化），
// security 包保留薄转发，既有调用点零改动。
package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// DefaultWorkspaceRoot 未配置时的会话工作区根目录（相对服务进程工作目录）。
const DefaultWorkspaceRoot = "var/tmp/workspace"

var (
	workspaceRootMu       sync.RWMutex
	workspaceRootOverride string
)

// SetDefaultWorkspaceRoot 记录全局工作区根（app 启动时随 agent.workspace_root_dir
// 配置调用一次），供无法拿到 Executor 实例的调用方（如文件系统 Backend 包装）
// 与 WorkspaceCrossProjectGuard 的 root 参数为空时使用。
func SetDefaultWorkspaceRoot(root string) {
	workspaceRootMu.Lock()
	defer workspaceRootMu.Unlock()
	workspaceRootOverride = strings.TrimSpace(root)
}

func defaultWorkspaceRoot() string {
	workspaceRootMu.RLock()
	defer workspaceRootMu.RUnlock()
	if workspaceRootOverride != "" {
		return workspaceRootOverride
	}
	return DefaultWorkspaceRoot
}

// WorkspaceCrossProjectGuard 校验 path 未落入「其他项目」的工作区目录——项目间
// 工作区硬隔离边界，exec workdir 与文件系统工具（ls/read_file/write_file/edit_file/
// glob/grep/multimodal_read）共用。
//   - root: 工作区根目录；空 = 全局配置（agent.workspace_root_dir），再退默认 var/tmp/workspace
//   - allowedProjectID: 当前会话绑定的项目 ID；空（未绑定会话）时任何 projects/* 均视为越界
//
// 相对路径按服务进程工作目录解析（与 exec cmd.Dir、文件系统后端 OS 语义一致）。
// 仅做词法判定（Abs+Clean+前缀比对），不解析符号链接——本防线针对 agent 误操作与
// 跨项目串写，不是对抗性沙箱（exec shell 本身具备整机文件访问能力，属平台固有边界）。
func WorkspaceCrossProjectGuard(root, path, allowedProjectID string) error {
	p := strings.TrimSpace(path)
	if p == "" {
		return nil
	}
	if strings.TrimSpace(root) == "" {
		root = defaultWorkspaceRoot()
	}
	rootAbs, err := filepath.Abs(strings.TrimSpace(root))
	if err != nil {
		return nil
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return nil
	}
	projectsPrefix := filepath.Join(rootAbs, "projects") + string(os.PathSeparator)
	if !strings.HasPrefix(abs, projectsPrefix) {
		return nil
	}
	seg := strings.TrimPrefix(abs, projectsPrefix)
	if i := strings.IndexByte(seg, os.PathSeparator); i >= 0 {
		seg = seg[:i]
	}
	allowed := strings.TrimSpace(allowedProjectID)
	if allowed != "" && sanitizeWorkspaceSegment(allowed) == seg {
		return nil
	}
	return fmt.Errorf("跨项目工作区访问被拒绝：%s 位于 projects/%s 工作区，当前会话不属于该项目（工作区按项目硬隔离）", path, seg)
}

// ── 平台共享目录根：对话附件 / 会话产物 ──
// 目录名在此唯一定义，附件管理页（handler/chat_uploads.go）、安卓样本收编
// （apkanalysis）与构建物投递（app/artifact_tools.go）共用同一份解析，
// 避免各处自行拼根目录导致「附件找不到」。
const (
	// ChatUploadsDirName 对话上传附件目录（相对进程工作目录；目录收拢后位于 var/ 下）。
	ChatUploadsDirName = "var/chat_uploads"
	// ConversationArtifactsDirName 会话产物目录（相对进程工作目录）。
	ConversationArtifactsDirName = "data/conversation_artifacts"
)

// ChatUploadsRootDir 对话附件根目录绝对路径。
func ChatUploadsRootDir() (string, error) { return filepath.Abs(ChatUploadsDirName) }

// ConversationArtifactsRootDir 会话产物根目录绝对路径。
func ConversationArtifactsRootDir() (string, error) {
	return filepath.Abs(ConversationArtifactsDirName)
}

// ConversationArtifactsDir 本会话产物目录绝对路径（产物按会话分目录：root/<会话ID>）。
// 会话 ID 缺失返回错误；非法段名退化为 default（不逃逸出产物根）。
func ConversationArtifactsDir(conversationID string) (string, error) {
	cid := strings.TrimSpace(conversationID)
	if cid == "" {
		return "", fmt.Errorf("缺少会话上下文，无法定位会话产物目录")
	}
	root, err := ConversationArtifactsRootDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, sanitizeWorkspaceSegment(cid)), nil
}

// WorkspaceRootDir 会话工作区根目录绝对路径；root 空 = 全局配置
// （SetDefaultWorkspaceRoot），再退默认 var/tmp/workspace。
func WorkspaceRootDir(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		root = defaultWorkspaceRoot()
	}
	return filepath.Abs(strings.TrimSpace(root))
}

// ConversationWorkspaceDir 计算会话工作目录（与 exec cmd.Dir 同源）：
// base 空 = 全局配置/默认；绑定项目用 projects/<项目ID>，否则 conversations/<会话ID>；
// 项目与会话都为空时返回 ""（无会话上下文，调用方自行决定是否放行）。
func ConversationWorkspaceDir(base, projectID, conversationID string) string {
	pid, cid := strings.TrimSpace(projectID), strings.TrimSpace(conversationID)
	if pid == "" && cid == "" {
		return ""
	}
	if strings.TrimSpace(base) == "" {
		base = defaultWorkspaceRoot()
	}
	if pid != "" {
		return filepath.Join(base, "projects", sanitizeWorkspaceSegment(pid))
	}
	return filepath.Join(base, "conversations", sanitizeWorkspaceSegment(cid))
}

// PathWithin 词法判定 p 是否位于 root 内（含 root 自身）。root 或 p 为空返回 false。
func PathWithin(root, p string) bool {
	r, err := filepath.Abs(strings.TrimSpace(root))
	if err != nil || strings.TrimSpace(root) == "" {
		return false
	}
	a, err := filepath.Abs(strings.TrimSpace(p))
	if err != nil || strings.TrimSpace(p) == "" {
		return false
	}
	if a == r {
		return true
	}
	return strings.HasPrefix(a, r+string(os.PathSeparator))
}

// sanitizeWorkspaceSegment 是本语义的唯一实现：历史上 internal/security/executor.go
// 曾有逐字一致的同名函数（当时因 E5 会话占用 executor.go 而复制），该函数已随尾批
// executor 死代码清理删除，「待收敛」目标不复存在。
// 注意：与 strutil.SanitizePathSegment（分隔符替换 + 180 字节截断的清洗语义）不等价
// ——本函数是拒绝式：空/./../含分隔符一律回退 "default"，其余原样保留、不改写不截断。
// 语义不同，不可并入 strutil 版。
func sanitizeWorkspaceSegment(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || s == "." || s == ".." || strings.ContainsAny(s, "/\\") {
		return "default"
	}
	return s
}
