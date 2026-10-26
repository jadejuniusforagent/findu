package server

// Step 6d Phase B8：chat-uploads 十条路由的原生实现（findu
// internal/handler/chat_uploads.go 的整文件移植，net/http + 宿主 audit）。
//
// 行为对齐要点：
//   - 四根目录均为进程 cwd 相对：chat_uploads / tmp/reduction /
//     tmp/workspace / data/conversation_artifacts（与 findu 部署一致；config
//     里 reduction_root_dir / workspace_root_dir 目前为空 = 默认根）；
//   - 三个虚拟前缀 __reduction__/ / __workspace__/ / __conversation_artifact__/
//     语义与 findu 相同（列表/下载/resolve 均按前缀路由）；
//   - 路径穿越校验（Clean + 前缀断言）与 findu 一致；
//   - chat_upload_artifacts 元数据经 db.ChatUploadStore 同表维持；
//   - audit 写宿主 audit_logs（category=file，findu audit.Service 同表）。
//
// 已知差异（同 B1/B6 约定）：RBAC 细粒度豁免——宿主 Bearer 语义等价
// scope=all 管理员（findu pathAllowed 对 scope=all 直接放行）。

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"finduai/db"
)

const (
	cuUploadsRootDirName     = "chat_uploads"
	cuReductionRootDirName   = "tmp/reduction"
	cuWorkspaceRootDirName   = "tmp/workspace"
	cuArtifactsRootDirName   = "data/conversation_artifacts"
	cuReductionVirtualPrefix = "__reduction__/"
	cuWorkspaceVirtualPrefix = "__workspace__/"
	cuArtifactVirtualPrefix  = "__conversation_artifact__/"
	cuSourceUpload           = "upload"
	cuSourceReduction        = "reduction"
	cuSourceWorkspace        = "workspace"
	cuSourceConversation     = "conversation_artifact"
	cuMaxEditBytes           = 2 * 1024 * 1024 // 文本编辑上限
)

// ChatUploadFileItem 列表项（与 findu 同形 camelCase）。
type ChatUploadFileItem struct {
	RelativePath      string `json:"relativePath"`
	AbsolutePath      string `json:"absolutePath"`
	Name              string `json:"name"`
	Source            string `json:"source,omitempty"`
	Size              int64  `json:"size"`
	ModifiedUnix      int64  `json:"modifiedUnix"`
	Date              string `json:"date"`
	ConversationID    string `json:"conversationId"`
	ConversationTitle string `json:"conversationTitle,omitempty"`
	ProjectID         string `json:"projectId,omitempty"`
	ProjectName       string `json:"projectName,omitempty"`
	SubPath           string `json:"subPath"`
}

func (s *Server) cuStore() *db.ChatUploadStore {
	if s.m == nil || s.m.pg == nil {
		return nil
	}
	return s.m.pg.ChatUploads()
}

// ---- roots（与 findu absRoot/absReductionRoot/absWorkspaceRoot/absConversationArtifactsRoot 一致）----

func (s *Server) cuAbsRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Abs(filepath.Join(cwd, cuUploadsRootDirName))
}

func (s *Server) cuAbsReductionRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Abs(filepath.Join(cwd, cuReductionRootDirName))
}

func (s *Server) cuAbsWorkspaceRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Abs(filepath.Join(cwd, cuWorkspaceRootDirName))
}

func (s *Server) cuAbsConversationArtifactsRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Abs(filepath.Join(cwd, cuArtifactsRootDirName))
}

// ---- 名称缓存（findu conversationProjectID/title/projectName 同义词法）----

func (s *Server) cuNameCaches() (projectCache, projectNameCache, conversationTitleCache map[string]string) {
	return map[string]string{}, map[string]string{}, map[string]string{}
}

func (s *Server) cuConversationProjectID(cs *db.ChatUploadStore, ctx context.Context, convID string, cache map[string]string) string {
	convID = strings.TrimSpace(convID)
	if convID == "" || convID == "_manual" || convID == "_new" || cs == nil {
		return ""
	}
	if v, ok := cache[convID]; ok {
		return v
	}
	v := cs.ConversationProjectID(ctx, convID)
	cache[convID] = v
	return v
}

func (s *Server) cuConversationTitle(cs *db.ChatUploadStore, ctx context.Context, convID string, cache map[string]string) string {
	convID = strings.TrimSpace(convID)
	if convID == "" || convID == "_manual" || convID == "_new" || cs == nil {
		return ""
	}
	if v, ok := cache[convID]; ok {
		return v
	}
	v := cs.ConversationTitle(ctx, convID)
	cache[convID] = v
	return v
}

func (s *Server) cuProjectName(cs *db.ChatUploadStore, ctx context.Context, projectID string, cache map[string]string) string {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" || cs == nil {
		return ""
	}
	if v, ok := cache[projectID]; ok {
		return v
	}
	v := cs.ProjectName(ctx, projectID)
	cache[projectID] = v
	return v
}

// ---- 路径解析（对齐 findu resolveUnderChatUploads / cuJoinSecure 语义）----

func cuJoinSecure(root, relSlash, errPrefix string) (string, error) {
	rel := filepath.Clean(filepath.FromSlash(relSlash))
	if rel == "." || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("invalid path")
	}
	full, err := filepath.Abs(filepath.Join(root, rel))
	if err != nil {
		return "", err
	}
	rootAbs, _ := filepath.Abs(root)
	if full != rootAbs && !strings.HasPrefix(full, rootAbs+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes %s root", errPrefix)
	}
	return full, nil
}

func (s *Server) cuResolveUnderChatUploads(relativePath string) (string, error) {
	rel := strings.TrimSpace(relativePath)
	if rel == "" {
		return "", fmt.Errorf("empty path")
	}
	root, err := s.cuAbsRoot()
	if err != nil {
		return "", err
	}
	return cuJoinSecure(root, rel, "chat_uploads")
}

// ---- collectors（对齐 findu collect*）----

// cuCollectScopedFiles 共享 reduction/workspace 两源的采集：两轮 scope
// （conversations / projects）+ conversation/project 双过滤。
func (s *Server) cuCollectScopedFiles(ctx context.Context, root, virtualPrefix, source string, extensionlessTxt bool, conversationFilter, projectFilter string) ([]ChatUploadFileItem, error) {
	cs := s.cuStore()
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return nil, nil
	}
	files := make([]ChatUploadFileItem, 0)
	projectCache, projectNameCache, conversationTitleCache := s.cuNameCaches()
	for _, scope := range []string{"conversations", "projects"} {
		scopeRoot := filepath.Join(root, scope)
		_ = filepath.WalkDir(scopeRoot, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil || d == nil || d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return nil
			}
			relSlash := filepath.ToSlash(rel)
			parts := strings.Split(relSlash, "/")
			if len(parts) < 3 {
				return nil
			}
			ownerID := parts[1]
			var convID, projectID string
			if scope == "conversations" {
				convID = ownerID
				projectID = s.cuConversationProjectID(cs, ctx, convID, projectCache)
			} else {
				projectID = ownerID
			}
			if conversationFilter != "" && convID != conversationFilter {
				// findu：projects 源仅在「会话筛选所代表的 conversation 的项目
				// 等于该文件项目」时透传，同时把会话 id 回填。
				if scope != "projects" || s.cuConversationProjectID(cs, ctx, conversationFilter, projectCache) != projectID {
					return nil
				}
				convID = conversationFilter
			}
			if projectFilter != "" && projectID != projectFilter {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			name := d.Name()
			if extensionlessTxt && filepath.Ext(name) == "" {
				name += ".txt"
			}
			abs, _ := filepath.Abs(path)
			files = append(files, ChatUploadFileItem{
				RelativePath:      virtualPrefix + relSlash,
				AbsolutePath:      abs,
				Name:              name,
				Source:            source,
				Size:              info.Size(),
				ModifiedUnix:      info.ModTime().Unix(),
				Date:              info.ModTime().Format("2006-01-02"),
				ConversationID:    convID,
				ConversationTitle: s.cuConversationTitle(cs, ctx, convID, conversationTitleCache),
				ProjectID:         projectID,
				ProjectName:       s.cuProjectName(cs, ctx, projectID, projectNameCache),
				SubPath:           strings.Join(parts[2:len(parts)-1], "/"),
			})
			return nil
		})
	}
	return files, nil
}

// cuCollectConversationArtifactFiles 对齐 findu collectConversationArtifactFiles。
func (s *Server) cuCollectConversationArtifactFiles(ctx context.Context, conversationFilter, projectFilter string) ([]ChatUploadFileItem, error) {
	cs := s.cuStore()
	root, err := s.cuAbsConversationArtifactsRoot()
	if err != nil {
		return nil, err
	}
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return nil, nil
	}
	projectCache, projectNameCache, conversationTitleCache := s.cuNameCaches()
	files := make([]ChatUploadFileItem, 0)
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d == nil || d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		parts := strings.Split(relSlash, "/")
		if len(parts) < 2 {
			return nil
		}
		convID := parts[0]
		projectID := s.cuConversationProjectID(cs, ctx, convID, projectCache)
		if conversationFilter != "" && convID != conversationFilter {
			return nil
		}
		if projectFilter != "" && projectID != projectFilter {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		name := d.Name()
		if filepath.Ext(name) == "" {
			name += ".txt"
		}
		abs, _ := filepath.Abs(path)
		files = append(files, ChatUploadFileItem{
			RelativePath:      cuArtifactVirtualPrefix + relSlash,
			AbsolutePath:      abs,
			Name:              name,
			Source:            cuSourceConversation,
			Size:              info.Size(),
			ModifiedUnix:      info.ModTime().Unix(),
			Date:              info.ModTime().Format("2006-01-02"),
			ConversationID:    convID,
			ConversationTitle: s.cuConversationTitle(cs, ctx, convID, conversationTitleCache),
			ProjectID:         projectID,
			ProjectName:       s.cuProjectName(cs, ctx, projectID, projectNameCache),
			SubPath:           strings.Join(parts[1:len(parts)-1], "/"),
		})
		return nil
	})
	return files, nil
}

// cuCollectFiles 对齐 findu collectFiles（walk 4 根 + 三处 internal 源合并）。
func (s *Server) cuCollectFiles(ctx context.Context, conversationFilter, projectFilter string) ([]ChatUploadFileItem, []string, error) {
	cs := s.cuStore()
	root, err := s.cuAbsRoot()
	if err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(root, 0755); err != nil { // 对齐 findu：保证根目录存在
		return nil, nil, err
	}
	var files []ChatUploadFileItem
	var folders []string
	projectCache, projectNameCache, conversationTitleCache := s.cuNameCaches()
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		if d.IsDir() {
			folders = append(folders, relSlash)
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		parts := strings.Split(relSlash, "/")
		var dateStr, convID string
		if len(parts) >= 2 {
			dateStr = parts[0]
		}
		if len(parts) >= 3 {
			convID = parts[1]
		}
		projectID := s.cuConversationProjectID(cs, ctx, convID, projectCache)
		var subPath string
		if len(parts) >= 4 {
			subPath = strings.Join(parts[2:len(parts)-1], "/")
		}
		if conversationFilter != "" && convID != conversationFilter {
			return nil
		}
		if projectFilter != "" && projectID != projectFilter {
			return nil
		}
		absPath, _ := filepath.Abs(path)
		files = append(files, ChatUploadFileItem{
			RelativePath:      relSlash,
			AbsolutePath:      absPath,
			Name:              d.Name(),
			Source:            cuSourceUpload,
			Size:              info.Size(),
			ModifiedUnix:      info.ModTime().Unix(),
			Date:              dateStr,
			ConversationID:    convID,
			ConversationTitle: s.cuConversationTitle(cs, ctx, convID, conversationTitleCache),
			ProjectID:         projectID,
			ProjectName:       s.cuProjectName(cs, ctx, projectID, projectNameCache),
			SubPath:           subPath,
		})
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	if conversationFilter != "" || projectFilter != "" {
		filteredFolders := make([]string, 0, len(folders))
		for _, rel := range folders {
			parts := strings.Split(rel, "/")
			if len(parts) >= 2 && (conversationFilter == "" || parts[1] == conversationFilter) && (projectFilter == "" || s.cuConversationProjectID(cs, ctx, parts[1], projectCache) == projectFilter) {
				filteredFolders = append(filteredFolders, rel)
				continue
			}
			if len(parts) == 1 {
				prefix := rel + "/"
				for _, f := range files {
					if strings.HasPrefix(f.RelativePath, prefix) {
						filteredFolders = append(filteredFolders, rel)
						break
					}
				}
			}
		}
		folders = filteredFolders
	}
	sort.Strings(folders)
	sort.Slice(files, func(i, j int) bool {
		return files[i].ModifiedUnix > files[j].ModifiedUnix
	})

	if redRoot, rerr := s.cuAbsReductionRoot(); rerr == nil {
		if reductionFiles, _ := s.cuCollectScopedFiles(ctx, redRoot, cuReductionVirtualPrefix, cuSourceReduction, true, conversationFilter, projectFilter); len(reductionFiles) > 0 {
			files = append(files, reductionFiles...)
		}
	}
	if wsRoot, werr := s.cuAbsWorkspaceRoot(); werr == nil {
		if workspaceFiles, _ := s.cuCollectScopedFiles(ctx, wsRoot, cuWorkspaceVirtualPrefix, cuSourceWorkspace, false, conversationFilter, projectFilter); len(workspaceFiles) > 0 {
			files = append(files, workspaceFiles...)
		}
	}
	if artifactFiles, _ := s.cuCollectConversationArtifactFiles(ctx, conversationFilter, projectFilter); len(artifactFiles) > 0 {
		files = append(files, artifactFiles...)
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].ModifiedUnix > files[j].ModifiedUnix
	})
	return files, folders, nil
}

// ---- 虚拟路径解析 + 过滤辅助 ----

func (s *Server) cuResolveReductionVirtualPath(relativePath string) (string, error) {
	rel := strings.TrimPrefix(strings.TrimSpace(relativePath), cuReductionVirtualPrefix)
	root, err := s.cuAbsReductionRoot()
	if err != nil {
		return "", err
	}
	return cuJoinSecure(root, rel, "reduction")
}

func (s *Server) cuResolveWorkspaceVirtualPath(relativePath string) (string, error) {
	rel := strings.TrimPrefix(strings.TrimSpace(relativePath), cuWorkspaceVirtualPrefix)
	root, err := s.cuAbsWorkspaceRoot()
	if err != nil {
		return "", err
	}
	return cuJoinSecure(root, rel, "workspace")
}

func (s *Server) cuResolveConversationArtifactVirtualPath(relativePath string) (string, error) {
	rel := strings.TrimPrefix(strings.TrimSpace(relativePath), cuArtifactVirtualPrefix)
	root, err := s.cuAbsConversationArtifactsRoot()
	if err != nil {
		return "", err
	}
	return cuJoinSecure(root, rel, "conversation artifacts")
}

func cuSourceMatches(item ChatUploadFileItem, sourceFilter string) bool {
	sourceFilter = strings.TrimSpace(sourceFilter)
	if sourceFilter == "" || sourceFilter == "all" {
		return true
	}
	source := strings.TrimSpace(item.Source)
	if source == "" {
		source = cuSourceUpload
	}
	return source == sourceFilter
}

func cuSearchMatches(item ChatUploadFileItem, search string) bool {
	search = strings.ToLower(strings.TrimSpace(search))
	if search == "" {
		return true
	}
	for _, value := range []string{item.RelativePath, item.Name, item.Source, item.Date, item.ConversationID, item.ProjectID, item.SubPath} {
		if strings.Contains(strings.ToLower(value), search) {
			return true
		}
	}
	return false
}

func cuFilterItems(files []ChatUploadFileItem, sourceFilter, search string) []ChatUploadFileItem {
	if strings.TrimSpace(sourceFilter) == "" && strings.TrimSpace(search) == "" {
		return files
	}
	out := make([]ChatUploadFileItem, 0, len(files))
	for _, item := range files {
		if cuSourceMatches(item, sourceFilter) && cuSearchMatches(item, search) {
			out = append(out, item)
		}
	}
	return out
}

func cuPositiveQuery(r *http.Request, key string, def, maxV int) int {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return def
	}
	if strings.EqualFold(raw, "all") {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return def
	}
	if maxV > 0 && n > maxV {
		return maxV
	}
	return n
}

func cuPaginate(items []ChatUploadFileItem, page, pageSize int) []ChatUploadFileItem {
	if pageSize <= 0 {
		return items
	}
	if page <= 0 {
		page = 1
	}
	start := (page - 1) * pageSize
	if start >= len(items) {
		return []ChatUploadFileItem{}
	}
	end := start + pageSize
	if end > len(items) {
		end = len(items)
	}
	return items[start:end]
}

// cuResolveListedFilePath 对齐 findu resolveListedFilePath。
func (s *Server) cuResolveListedFilePath(item ChatUploadFileItem) (string, error) {
	switch {
	case item.Source == cuSourceReduction || strings.HasPrefix(item.RelativePath, cuReductionVirtualPrefix):
		return s.cuResolveReductionVirtualPath(item.RelativePath)
	case item.Source == cuSourceWorkspace || strings.HasPrefix(item.RelativePath, cuWorkspaceVirtualPrefix):
		return s.cuResolveWorkspaceVirtualPath(item.RelativePath)
	case item.Source == cuSourceConversation || strings.HasPrefix(item.RelativePath, cuArtifactVirtualPrefix):
		return s.cuResolveConversationArtifactVirtualPath(item.RelativePath)
	default:
		return s.cuResolveUnderChatUploads(item.RelativePath)
	}
}

// ---- HTTP handlers ----

// cuAudit 写宿主 audit_logs（findu audit.RecordOK 同表列名）。
func (s *Server) cuAudit(r *http.Request, action, message, resourceID string, detail map[string]interface{}) {
	if s.m == nil || s.m.pg == nil {
		return
	}
	_ = s.m.pg.AuditLogs().Append(&db.AuditLog{
		Category:     "file",
		Action:       action,
		Result:       "success",
		Actor:        loginUsername,
		ResourceType: "chat_upload",
		ResourceID:   resourceID,
		Message:      message,
		Detail:       detail,
	})
}

// listChatUploadsNative serves GET /api/chat-uploads。
func (s *Server) listChatUploadsNative(w http.ResponseWriter, r *http.Request) {
	conversationFilter := strings.TrimSpace(r.URL.Query().Get("conversation"))
	projectFilter := strings.TrimSpace(r.URL.Query().Get("project"))
	sourceFilter := strings.TrimSpace(r.URL.Query().Get("source"))
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	page := cuPositiveQuery(r, "page", 1, 0)
	pageSize := cuPositiveQuery(r, "pageSize", 20, 200)
	files, folders, err := s.cuCollectFiles(r.Context(), conversationFilter, projectFilter)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	files = cuFilterItems(files, sourceFilter, search)
	total := len(files)
	writeJSON(w, http.StatusOK, map[string]any{
		"files":    cuPaginate(files, page, pageSize),
		"folders":  folders,
		"total":    total,
		"page":     page,
		"pageSize": pageSize,
	})
}

// cuServeFileAttachment 对齐 findu FileAttachment（可按需补 .txt 扩展名）。
func cuServeFileAttachment(w http.ResponseWriter, r *http.Request, abs string, ensureExt bool) {
	name := filepath.Base(abs)
	if ensureExt && filepath.Ext(name) == "" {
		name += ".txt"
	}
	f, err := os.Open(abs)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	http.ServeContent(w, r, "", st.ModTime(), f)
}

// cuStatExisting 非 nil 判断 file 存在且为文件，否则写 404 并返回 false。
func (s *Server) cuStatExisting(w http.ResponseWriter, abs string) (os.FileInfo, bool) {
	st, err := os.Stat(abs)
	if err != nil || st.IsDir() {
		writeErr(w, http.StatusNotFound, "file not found")
		return nil, false
	}
	return st, true
}

// downloadChatUploadsNative serves GET /api/chat-uploads/download?path=...。
func (s *Server) downloadChatUploadsNative(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	switch {
	case strings.HasPrefix(strings.TrimSpace(p), cuReductionVirtualPrefix):
		abs, err := s.cuResolveReductionVirtualPath(p)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if _, ok := s.cuStatExisting(w, abs); !ok {
			return
		}
		cuServeFileAttachment(w, r, abs, true)
	case strings.HasPrefix(strings.TrimSpace(p), cuWorkspaceVirtualPrefix):
		abs, err := s.cuResolveWorkspaceVirtualPath(p)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if _, ok := s.cuStatExisting(w, abs); !ok {
			return
		}
		cuServeFileAttachment(w, r, abs, false)
	case strings.HasPrefix(strings.TrimSpace(p), cuArtifactVirtualPrefix):
		abs, err := s.cuResolveConversationArtifactVirtualPath(p)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if _, ok := s.cuStatExisting(w, abs); !ok {
			return
		}
		cuServeFileAttachment(w, r, abs, true)
	default:
		abs, err := s.cuResolveUnderChatUploads(p)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if _, ok := s.cuStatExisting(w, abs); !ok {
			return
		}
		cuServeFileAttachment(w, r, abs, false)
	}
}

// resolveChatUploadsPathNative serves GET /api/chat-uploads/path。
func (s *Server) resolveChatUploadsPathNative(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimSpace(r.URL.Query().Get("path"))
	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	if kind == "" {
		kind = "file"
	}
	var abs string
	var err error
	switch {
	case strings.HasPrefix(p, cuReductionVirtualPrefix):
		if strings.Trim(strings.TrimPrefix(p, cuReductionVirtualPrefix), "/") == "" {
			abs, err = s.cuAbsReductionRoot()
		} else {
			abs, err = s.cuResolveReductionVirtualPath(p)
		}
	case strings.HasPrefix(p, cuWorkspaceVirtualPrefix):
		if strings.Trim(strings.TrimPrefix(p, cuWorkspaceVirtualPrefix), "/") == "" {
			abs, err = s.cuAbsWorkspaceRoot()
		} else {
			abs, err = s.cuResolveWorkspaceVirtualPath(p)
		}
	case strings.HasPrefix(p, cuArtifactVirtualPrefix):
		if strings.Trim(strings.TrimPrefix(p, cuArtifactVirtualPrefix), "/") == "" {
			abs, err = s.cuAbsConversationArtifactsRoot()
		} else {
			abs, err = s.cuResolveConversationArtifactVirtualPath(p)
		}
	default:
		if p == "" || p == "." {
			abs, err = s.cuAbsRoot()
		} else {
			abs, err = s.cuResolveUnderChatUploads(p)
		}
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	st, err := os.Stat(abs)
	if err != nil {
		writeErr(w, http.StatusNotFound, "path not found")
		return
	}
	if kind == "directory" && !st.IsDir() {
		writeErr(w, http.StatusBadRequest, "not a directory")
		return
	}
	if kind != "directory" && st.IsDir() {
		writeErr(w, http.StatusBadRequest, "not a file")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"absolutePath": abs, "isDir": st.IsDir()})
}

// getChatUploadContentNative serves GET /api/chat-uploads/content?path=...。
func (s *Server) getChatUploadContentNative(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	abs, err := s.cuResolveUnderChatUploads(p)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	st, err := os.Stat(abs)
	if err != nil || st.IsDir() {
		writeErr(w, http.StatusNotFound, "file not found")
		return
	}
	if st.Size() > cuMaxEditBytes {
		writeErr(w, http.StatusRequestEntityTooLarge, "file too large for editor")
		return
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !utf8.Valid(b) {
		writeErr(w, http.StatusBadRequest, "binary file not editable in UI")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"content": string(b)})
}

// putChatUploadContentNative serves PUT /api/chat-uploads/content。
func (s *Server) putChatUploadContentNative(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if !utf8.ValidString(body.Content) {
		writeErr(w, http.StatusBadRequest, "content must be valid UTF-8")
		return
	}
	if len(body.Content) > cuMaxEditBytes {
		writeErr(w, http.StatusRequestEntityTooLarge, "content too large")
		return
	}
	abs, err := s.cuResolveUnderChatUploads(body.Path)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := os.WriteFile(abs, []byte(body.Content), 0644); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// deleteChatUploadNative serves DELETE /api/chat-uploads。
func (s *Server) deleteChatUploadNative(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Path) == "" {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	abs, err := s.cuResolveUnderChatUploads(body.Path)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	st, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			writeErr(w, http.StatusNotFound, "file not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if st.IsDir() {
		if err := os.RemoveAll(abs); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	} else if err := os.Remove(abs); err != nil {
		if os.IsNotExist(err) {
			writeErr(w, http.StatusNotFound, "file not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if cs := s.cuStore(); cs != nil {
		_, _ = cs.DeleteChatUploadArtifactPath(context.Background(), filepath.ToSlash(filepath.Clean(filepath.FromSlash(body.Path))))
	}
	s.cuAudit(r, "delete", "删除对话附件", body.Path, nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// mkdirChatUploadNative serves POST /api/chat-uploads/mkdir。
func (s *Server) mkdirChatUploadNative(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Parent string `json:"parent"`
		Name   string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		writeErr(w, http.StatusBadRequest, "invalid name")
		return
	}
	if utf8.RuneCountInString(name) > 200 {
		writeErr(w, http.StatusBadRequest, "name too long")
		return
	}
	parent := strings.TrimSpace(body.Parent)
	parent = filepath.ToSlash(filepath.Clean(filepath.FromSlash(parent)))
	parent = strings.Trim(parent, "/")
	if parent == "." {
		parent = ""
	}
	root, err := s.cuAbsRoot()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if parent != "" {
		absParent, perr := s.cuResolveUnderChatUploads(parent)
		if perr != nil {
			writeErr(w, http.StatusBadRequest, perr.Error())
			return
		}
		st, serr := os.Stat(absParent)
		if serr != nil || !st.IsDir() {
			writeErr(w, http.StatusBadRequest, "parent not found")
			return
		}
	}
	var rel string
	if parent == "" {
		rel = name
	} else {
		rel = parent + "/" + name
	}
	absNew, err := s.cuResolveUnderChatUploads(rel)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := os.Stat(absNew); err == nil {
		writeErr(w, http.StatusConflict, "already exists")
		return
	}
	if err := os.Mkdir(absNew, 0755); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	relOut, _ := filepath.Rel(root, absNew)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "relativePath": filepath.ToSlash(relOut)})
}

// renameChatUploadNative serves PUT /api/chat-uploads/rename。
func (s *Server) renameChatUploadNative(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path    string `json:"path"`
		NewName string `json:"newName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	newName := strings.TrimSpace(body.NewName)
	if newName == "" || strings.ContainsAny(newName, `/\`) {
		writeErr(w, http.StatusBadRequest, "invalid newName")
		return
	}
	abs, err := s.cuResolveUnderChatUploads(body.Path)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	dir := filepath.Dir(abs)
	newAbs := filepath.Join(dir, filepath.Base(newName))
	root, _ := s.cuAbsRoot()
	newAbs, _ = filepath.Abs(newAbs)
	rootAbs, _ := filepath.Abs(root)
	if newAbs != rootAbs && !strings.HasPrefix(newAbs, rootAbs+string(filepath.Separator)) {
		writeErr(w, http.StatusBadRequest, "invalid target path")
		return
	}
	if err := os.Rename(abs, newAbs); err != nil {
		if os.IsNotExist(err) {
			writeErr(w, http.StatusNotFound, "file not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	newRel, _ := filepath.Rel(rootAbs, newAbs)
	oldRel := filepath.ToSlash(filepath.Clean(filepath.FromSlash(body.Path)))
	if cs := s.cuStore(); cs != nil {
		_, _ = cs.RenameChatUploadArtifactPath(context.Background(), oldRel, filepath.ToSlash(newRel))
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "relativePath": filepath.ToSlash(newRel)})
}

// uploadChatUploadNative serves POST /api/chat-uploads (multipart file)。
func (s *Server) uploadChatUploadNative(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeErr(w, http.StatusBadRequest, "missing file")
		return
	}
	fh, hdr, err := r.FormFile("file")
	if err != nil || fh == nil || hdr == nil {
		writeErr(w, http.StatusBadRequest, "missing file")
		return
	}
	defer fh.Close()
	root, err := s.cuAbsRoot()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var targetDir string
	targetRel := strings.TrimSpace(r.FormValue("relativeDir"))
	if targetRel != "" {
		absDir, rerr := s.cuResolveUnderChatUploads(targetRel)
		if rerr != nil {
			writeErr(w, http.StatusBadRequest, rerr.Error())
			return
		}
		st, serr := os.Stat(absDir)
		if serr != nil {
			if os.IsNotExist(serr) {
				if err := os.MkdirAll(absDir, 0755); err != nil {
					writeErr(w, http.StatusInternalServerError, err.Error())
					return
				}
			} else {
				writeErr(w, http.StatusInternalServerError, serr.Error())
				return
			}
		} else if !st.IsDir() {
			writeErr(w, http.StatusBadRequest, "relativeDir is not a directory")
			return
		}
		targetDir = absDir
	} else {
		convID := strings.TrimSpace(r.FormValue("conversationId"))
		dateStr := time.Now().Format("2006-01-02")
		convDir := convID
		if convDir == "" {
			convDir = "_manual"
		} else {
			convDir = strings.ReplaceAll(convID, string(filepath.Separator), "_")
		}
		targetDir = filepath.Join(root, dateStr, convDir)
		if err := os.MkdirAll(targetDir, 0755); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	baseName := filepath.Base(hdr.Filename)
	if baseName == "" || baseName == "." || baseName == "/" {
		baseName = "file"
	}
	baseName = strings.ReplaceAll(baseName, string(filepath.Separator), "_")
	ext := filepath.Ext(baseName)
	nameNoExt := strings.TrimSuffix(baseName, ext)
	suffix := fmt.Sprintf("_%s_%s", time.Now().Format("150405"), cuShortRand(6))
	var unique string
	if ext != "" {
		unique = nameNoExt + suffix + ext
	} else {
		unique = baseName + suffix
	}
	fullPath := filepath.Join(targetDir, unique)
	dst, err := os.Create(fullPath)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer dst.Close()
	if _, err := io.Copy(dst, fh); err != nil {
		_ = os.Remove(fullPath)
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	rel, _ := filepath.Rel(root, fullPath)
	absSaved, _ := filepath.Abs(fullPath)
	conversationID := strings.TrimSpace(r.FormValue("conversationId"))
	if conversationID == "" {
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) >= 2 {
			conversationID = parts[1]
		}
	}
	if cs := s.cuStore(); cs != nil {
		_ = cs.UpsertChatUploadArtifact(context.Background(), filepath.ToSlash(rel), conversationID, loginUsername)
	}
	s.cuAudit(r, "upload", "上传对话附件", filepath.ToSlash(rel), map[string]interface{}{"name": unique})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"relativePath": filepath.ToSlash(rel),
		"absolutePath": absSaved,
		"name":         unique,
	})
}

// cuShortRand 对齐 findu chatUploadShortRand。
func cuShortRand(n int) string {
	const letters = "0123456789abcdef"
	b := make([]byte, n)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = letters[int(b[i])%len(letters)]
	}
	return string(b)
}

// exportChatUploadsNative serves GET /api/chat-uploads/export。
func (s *Server) exportChatUploadsNative(w http.ResponseWriter, r *http.Request) {
	conversationFilter := strings.TrimSpace(r.URL.Query().Get("conversation"))
	projectFilter := strings.TrimSpace(r.URL.Query().Get("project"))
	sourceFilter := strings.TrimSpace(r.URL.Query().Get("source"))
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	files, _, err := s.cuCollectFiles(r.Context(), conversationFilter, projectFilter)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	files = cuFilterItems(files, sourceFilter, search)
	if len(files) == 0 {
		writeErr(w, http.StatusNotFound, "no files to export")
		return
	}
	nameParts := []string{"chat-files"}
	if projectFilter != "" {
		nameParts = append(nameParts, "project-"+projectFilter)
	}
	if conversationFilter != "" {
		nameParts = append(nameParts, "conversation-"+conversationFilter)
	}
	nameParts = append(nameParts, time.Now().Format("20060102-150405"))
	filename := strings.Join(nameParts, "-") + ".zip"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))

	zw := zip.NewWriter(w)
	defer zw.Close()
	manifest := map[string]any{
		"exportedAt":      time.Now().UTC().Format(time.RFC3339),
		"conversationId":  conversationFilter,
		"projectId":       projectFilter,
		"source":          sourceFilter,
		"search":          search,
		"fileCount":       len(files),
		"files":           files,
		"layout":          "chat uploads are stored under conversations/<conversationId>/; internal outputs are stored under internal/<source>/",
		"sourceDirectory": []string{cuUploadsRootDirName, cuReductionRootDirName, cuWorkspaceRootDirName, cuArtifactsRootDirName},
	}
	manifestBytes, _ := json.MarshalIndent(manifest, "", "  ")
	if mw, err := zw.Create("manifest.json"); err == nil {
		_, _ = mw.Write(manifestBytes)
	}
	used := make(map[string]int)
	for _, item := range files {
		abs, err := s.cuResolveListedFilePath(item)
		if err != nil {
			continue
		}
		st, err := os.Stat(abs)
		if err != nil || st.IsDir() {
			continue
		}
		var zipName string
		switch {
		case item.Source == cuSourceReduction || strings.HasPrefix(item.RelativePath, cuReductionVirtualPrefix):
			rel := strings.TrimPrefix(item.RelativePath, cuReductionVirtualPrefix)
			zipName = filepath.ToSlash(filepath.Join("internal", "reduction", rel))
			if filepath.Ext(zipName) == "" {
				zipName += ".txt"
			}
		case item.Source == cuSourceWorkspace || strings.HasPrefix(item.RelativePath, cuWorkspaceVirtualPrefix):
			rel := strings.TrimPrefix(item.RelativePath, cuWorkspaceVirtualPrefix)
			zipName = filepath.ToSlash(filepath.Join("internal", "workspace", rel))
		case item.Source == cuSourceConversation || strings.HasPrefix(item.RelativePath, cuArtifactVirtualPrefix):
			rel := strings.TrimPrefix(item.RelativePath, cuArtifactVirtualPrefix)
			zipName = filepath.ToSlash(filepath.Join("internal", "conversation_artifacts", rel))
			if filepath.Ext(zipName) == "" {
				zipName += ".txt"
			}
		default:
			conv := strings.TrimSpace(item.ConversationID)
			if conv == "" || conv == "_manual" || conv == "_new" {
				conv = "manual"
			}
			zipName = filepath.ToSlash(filepath.Join("conversations", conv, strings.TrimSpace(item.SubPath), item.Name))
		}
		if used[zipName] > 0 {
			ext := filepath.Ext(zipName)
			zipName = strings.TrimSuffix(zipName, ext) + fmt.Sprintf("-%d", used[zipName]+1) + ext
		}
		used[zipName]++
		fw, err := zw.Create(zipName)
		if err != nil {
			continue
		}
		src, err := os.Open(abs)
		if err != nil {
			continue
		}
		_, copyErr := io.Copy(fw, src)
		_ = src.Close()
		if copyErr != nil {
			return
		}
	}
	s.cuAudit(r, "export", "导出对话附件", filename, map[string]interface{}{
		"conversation_id": conversationFilter,
		"project_id":      projectFilter,
	})
}
