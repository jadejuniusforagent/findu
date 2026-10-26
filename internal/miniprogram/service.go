package miniprogram

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"finduai/internal/database"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Sample 小程序样本
type Sample struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	// Workspace 相对工作目录的落库路径;WorkspaceDisplay 为绝对路径且主目录折叠成 $HOME,
	// 便于页面直接复制到终端打开(如 $HOME/FindU/data/miniprogram_analysis/<hash>)
	Workspace        string `json:"workspace_path"`
	WorkspaceDisplay string `json:"workspace_display,omitempty"`
	// Overwritten 仅在多包合并注册的响应里返回:被后传分包覆盖的文件清单(未持久化)
	Overwritten []string  `json:"overwritten,omitempty"`
	AppID       string    `json:"appid"`
	FileCount   int       `json:"file_count"`
	ProjectID   string    `json:"project_id"`
	OwnerUserID string    `json:"owner_user_id"`
	Source      string    `json:"source"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Finding 敏感信息扫描发现
type Finding struct {
	ID          string    `json:"id"`
	SampleID    string    `json:"sample_id"`
	Category    string    `json:"category"`
	Severity    string    `json:"severity"`
	FilePath    string    `json:"file_path"`
	LineNumber  int       `json:"line_number"`
	Content     string    `json:"content"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

// Service 小程序分析服务
type Service struct {
	db        *database.DB
	log       *zap.Logger
	workspace string
}

// NewService 创建小程序分析服务
func NewService(db *database.DB, log *zap.Logger, workspace string) (*Service, error) {
	if workspace == "" {
		return nil, fmt.Errorf("workspace required")
	}
	if err := os.MkdirAll(workspace, 0o750); err != nil {
		return nil, fmt.Errorf("workspace mkdir: %w", err)
	}
	return &Service{db: db, log: log, workspace: workspace}, nil
}

// workspaceDir 样本工作目录：workspace/<sha256前12位>/
func (s *Service) workspaceDir(sha256Hex string) string {
	return filepath.Join(s.workspace, sha256Hex[:12])
}

// UploadPart 一个待注册的包文件（PC 微信 4.x 常拆成 __APP__ + _subpackages_* 多个包）
type UploadPart struct {
	Path string
	Name string
}

// RegisterFromFile 单文件注册（兼容旧调用）
func (s *Service) RegisterFromFile(srcPath, originalName, projectID, ownerID, source string) (*Sample, error) {
	return s.RegisterFromFiles([]UploadPart{{Path: srcPath, Name: originalName}}, projectID, ownerID, source, "")
}

// RegisterFromFiles 注册样本并解包扫描。多个包解到同一工作目录（分包合并成一个样本）。
// appid 用于解密 PC 微信 4.x 的 V1MWWX 加密包；留空时若包已加密则报错提示。
func (s *Service) RegisterFromFiles(parts []UploadPart, projectID, ownerID, source, appid string) (*Sample, error) {
	if len(parts) == 0 {
		return nil, fmt.Errorf("没有可注册的包文件")
	}
	// 计算整体 SHA256（内容 + 文件名），同一组包重复上传可去重
	h := sha256.New()
	for _, p := range parts {
		content, err := os.ReadFile(p.Path)
		if err != nil {
			return nil, fmt.Errorf("读取文件失败: %w", err)
		}
		fmt.Fprintf(h, "%s\x00%d\x00", p.Name, len(content))
		h.Write(content)
	}
	sha256Hex := hex.EncodeToString(h.Sum(nil))

	if existing, err := s.findBySHA256(sha256Hex); err == nil && existing != nil {
		if projectID != "" && existing.ProjectID == "" {
			_ = s.BindProject(existing.ID, projectID, ownerID)
			existing.ProjectID = projectID
		}
		return existing, nil
	}

	name := parts[0].Name
	if len(parts) > 1 {
		name = fmt.Sprintf("%s 等 %d 个包", parts[0].Name, len(parts))
	}

	wsDir := s.workspaceDir(sha256Hex)
	if err := os.MkdirAll(wsDir, 0o750); err != nil {
		return nil, fmt.Errorf("创建工作目录失败: %w", err)
	}

	totalSize := int64(0)
	fileCount := 0
	var overwrittenAll []string

	// 任一包为加密格式且未给 AppID 时整组挂起:原始包原样落 raw/,样本登记为
	// file_count=0 的待补 AppID 状态,补填后经 FillAppID 解密解包;
	// 明文包或已给 AppID 时照常就地解密解包,不让「先拿 AppID」堵住上传
	if strings.TrimSpace(appid) == "" && hasEncryptedPart(parts) {
		rawDir := filepath.Join(wsDir, "raw")
		if err := os.MkdirAll(rawDir, 0o750); err != nil {
			return nil, fmt.Errorf("创建原始包目录失败: %w", err)
		}
		for _, part := range parts {
			data, err := os.ReadFile(part.Path)
			if err != nil {
				return nil, fmt.Errorf("读取 %s 失败: %w", part.Name, err)
			}
			dst := filepath.Join(rawDir, filepath.Base(part.Name))
			if err := os.WriteFile(dst, data, 0o600); err != nil {
				return nil, fmt.Errorf("保存原始包 %s 失败: %w", part.Name, err)
			}
			totalSize += int64(len(data))
		}
	} else {
		for _, part := range parts {
			data, err := os.ReadFile(part.Path)
			if err != nil {
				return nil, fmt.Errorf("读取 %s 失败: %w", part.Name, err)
			}
			totalSize += int64(len(data))
			if IsEncrypted(data) {
				data, err = DecryptWxapkg(data, appid)
				if err != nil {
					return nil, fmt.Errorf("%s: %w", part.Name, err)
				}
			}
			pkg, err := ParseWxapkg(data, part.Name)
			if err != nil {
				return nil, fmt.Errorf("%s 解析失败: %w", part.Name, err)
			}
			overwritten, err := ExtractFilesWithReport(pkg, wsDir)
			if err != nil {
				return nil, fmt.Errorf("%s 解压失败: %w", part.Name, err)
			}
			for _, name := range overwritten {
				overwrittenAll = append(overwrittenAll, part.Name+" → "+name)
			}
			fileCount += len(pkg.Files)
		}
	}

	// appid:显式传入优先(加密包必须),否则从包内 app.json 提取
	appID := strings.TrimSpace(appid)
	if appID == "" {
		appID = extractAppID(wsDir)
	}

	id := uuid.New().String()
	now := time.Now()
	// 项目/属主为空时必须落 NULL:project_id 有外键,空字符串不是合法项目
	var projectArg, ownerArg interface{}
	if strings.TrimSpace(projectID) != "" {
		projectArg = projectID
	}
	if strings.TrimSpace(ownerID) != "" {
		ownerArg = ownerID
	}
	_, err := s.db.Exec(`
		INSERT INTO miniprogram_samples (id, name, sha256, size, workspace_path, appid, file_count, project_id, owner_user_id, source, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		id, name, sha256Hex, totalSize, wsDir, appID, fileCount,
		projectArg, ownerArg, source, now, now)
	if err != nil {
		return nil, fmt.Errorf("写入数据库失败: %w", err)
	}

	sample := &Sample{
		ID:               id,
		Name:             name,
		SHA256:           sha256Hex,
		Size:             totalSize,
		Workspace:        wsDir,
		WorkspaceDisplay: displayWorkspacePath(wsDir),
		AppID:            appID,
		FileCount:        fileCount,
		ProjectID:        projectID,
		OwnerUserID:      ownerID,
		Source:           source,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if len(overwrittenAll) > 0 {
		// 分包合并覆盖提示:响应里带回,前端上传后提示
		sample.Overwritten = overwrittenAll
	}
	// 分析记录:上传解包(+首扫)落一条 upload;待补 AppID 的挂起样本同样留痕
	req := map[string]any{"name": name, "parts": len(parts), "appid": appID, "source": source}
	if fileCount > 0 {
		start := time.Now()
		scanErr := s.RunScan(sample)
		status, errText := "done", ""
		if scanErr != nil {
			status, errText = "error", scanErr.Error()
		}
		s.recordTask(sample, "upload", req, scanResultSummary(s, sample.ID), status, errText, time.Since(start).Milliseconds())
	} else {
		s.recordTask(sample, "upload", req, map[string]any{"pending_appid": true, "file_count": 0}, "done", "", 0)
	}
	return sample, nil
}

// hasEncryptedPart 任一包为 V1MMWX 加密格式
func hasEncryptedPart(parts []UploadPart) bool {
	for _, part := range parts {
		data, err := os.ReadFile(part.Path)
		if err != nil {
			continue
		}
		if IsEncrypted(data) {
			return true
		}
	}
	return false
}

// FillAppID 为待补 AppID 的挂起样本补填 AppID:解密 raw/ 下的原始包并解包、重扫,
// 并落一条 appid_fill 分析记录(失败同样留痕,便于回溯错误原因)。
// AppID 不正确时解密/解析会失败,原样报错返回,样本保持挂起可重试。
func (s *Service) FillAppID(sampleID, appid string) (*Sample, error) {
	start := time.Now()
	sampleRef, _ := s.GetSample(sampleID)
	updated, err := s.fillAppID(sampleID, appid)
	status, errText := "done", ""
	if err != nil {
		status, errText = "error", err.Error()
	}
	ref := updated
	if ref == nil {
		ref = sampleRef
	}
	result := map[string]any{"appid": appid}
	if err == nil {
		result["file_count"] = updated.FileCount
	}
	s.recordTask(ref, "appid_fill", map[string]any{"appid": appid}, result, status, errText, time.Since(start).Milliseconds())
	return updated, err
}

func (s *Service) fillAppID(sampleID, appid string) (*Sample, error) {
	appid = strings.TrimSpace(appid)
	if appid == "" {
		return nil, fmt.Errorf("appid 不能为空")
	}
	sample, err := s.GetSample(sampleID)
	if err != nil {
		return nil, fmt.Errorf("样本不存在: %w", err)
	}
	rawDir := filepath.Join(sample.Workspace, "raw")
	entries, err := os.ReadDir(rawDir)
	if err != nil || len(entries) == 0 {
		return nil, fmt.Errorf("该样本没有待解密的原始包(明文包无需 AppID)")
	}
	fileCount := 0
	var overwrittenAll []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".wxapkg") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(rawDir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("读取 %s 失败: %w", e.Name(), err)
		}
		plain, err := DecryptWxapkg(data, appid)
		if err != nil {
			return nil, fmt.Errorf("%s 解密失败,请确认 AppID 是否正确: %w", e.Name(), err)
		}
		pkg, err := ParseWxapkg(plain, e.Name())
		if err != nil {
			return nil, fmt.Errorf("%s 解析失败: %w", e.Name(), err)
		}
		overwritten, err := ExtractFilesWithReport(pkg, sample.Workspace)
		if err != nil {
			return nil, fmt.Errorf("%s 解压失败: %w", e.Name(), err)
		}
		for _, name := range overwritten {
			overwrittenAll = append(overwrittenAll, e.Name()+" → "+name)
		}
		fileCount += len(pkg.Files)
	}
	if fileCount == 0 {
		return nil, fmt.Errorf("解密后没有提取到文件,请确认 AppID 后重试")
	}
	if _, err := s.db.Exec(`UPDATE miniprogram_samples SET appid=$2, file_count=$3, updated_at=now() WHERE id=$1`,
		sample.ID, appid, fileCount); err != nil {
		return nil, fmt.Errorf("更新数据库失败: %w", err)
	}
	updated, err := s.GetSample(sample.ID)
	if err != nil {
		return nil, err
	}
	if len(overwrittenAll) > 0 {
		updated.Overwritten = overwrittenAll
	}
	s.RunScan(updated)
	return updated, nil
}

// findBySHA256 按 SHA256 查找已有样本
func (s *Service) findBySHA256(sha256Hex string) (*Sample, error) {
	var sample Sample
	err := s.db.QueryRow(`
		SELECT id, name, sha256, size, workspace_path, COALESCE(appid,''), file_count,
		       COALESCE(project_id,''), COALESCE(owner_user_id,''), source, created_at, updated_at
		FROM miniprogram_samples WHERE sha256 = $1`, sha256Hex).
		Scan(&sample.ID, &sample.Name, &sample.SHA256, &sample.Size, &sample.Workspace,
			&sample.AppID, &sample.FileCount, &sample.ProjectID, &sample.OwnerUserID,
			&sample.Source, &sample.CreatedAt, &sample.UpdatedAt)
	if err != nil {
		return nil, err
	}
	sample.WorkspaceDisplay = displayWorkspacePath(sample.Workspace)
	return &sample, nil
}

// GetSample 按 ID 或名称查找样本（同名多版本时取最新）
func (s *Service) GetSample(idOrName string) (*Sample, error) {
	var sample Sample
	err := s.db.QueryRow(`
		SELECT id, name, sha256, size, workspace_path, COALESCE(appid,''), file_count,
		       COALESCE(project_id,''), COALESCE(owner_user_id,''), source, created_at, updated_at
		FROM miniprogram_samples WHERE id = $1 OR name = $1
		ORDER BY created_at DESC LIMIT 1`, idOrName).
		Scan(&sample.ID, &sample.Name, &sample.SHA256, &sample.Size, &sample.Workspace,
			&sample.AppID, &sample.FileCount, &sample.ProjectID, &sample.OwnerUserID,
			&sample.Source, &sample.CreatedAt, &sample.UpdatedAt)
	if err != nil {
		return nil, err
	}
	sample.WorkspaceDisplay = displayWorkspacePath(sample.Workspace)
	return &sample, nil
}

// ListSamples 列出样本
func (s *Service) ListSamples(projectID string) ([]*Sample, error) {
	query := `SELECT id, name, sha256, size, workspace_path, COALESCE(appid,''), file_count,
	                 COALESCE(project_id,''), COALESCE(owner_user_id,''), source, created_at, updated_at FROM miniprogram_samples`
	args := []interface{}{}
	if projectID != "" {
		query += ` WHERE project_id = $1`
		args = append(args, projectID)
	}
	query += ` ORDER BY created_at DESC`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var samples []*Sample
	for rows.Next() {
		var sample Sample
		if err := rows.Scan(&sample.ID, &sample.Name, &sample.SHA256, &sample.Size, &sample.Workspace,
			&sample.AppID, &sample.FileCount, &sample.ProjectID, &sample.OwnerUserID,
			&sample.Source, &sample.CreatedAt, &sample.UpdatedAt); err != nil {
			return nil, err
		}
		sample.WorkspaceDisplay = displayWorkspacePath(sample.Workspace)
		samples = append(samples, &sample)
	}
	return samples, nil
}

// displayWorkspacePath 绝对化工作目录并把用户主目录折叠为 $HOME:
// 页面「小程序存储路径」弹窗直接复制即可 cd 进去(工作目录通常就在部署目录下)。
func displayWorkspacePath(p string) string {
	if strings.TrimSpace(p) == "" {
		return ""
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.ToSlash(p)
	}
	home, herr := os.UserHomeDir()
	if herr == nil && home != "" {
		home = filepath.Clean(home)
		if abs == home {
			return "$HOME"
		}
		if strings.HasPrefix(abs, home+string(os.PathSeparator)) {
			return "$HOME/" + filepath.ToSlash(strings.TrimPrefix(abs, home+string(os.PathSeparator)))
		}
	}
	return filepath.ToSlash(abs)
}

// DeleteSample 删除样本及其工作目录
func (s *Service) DeleteSample(id string) error {
	var wsPath string
	err := s.db.QueryRow(`SELECT workspace_path FROM miniprogram_samples WHERE id = $1`, id).Scan(&wsPath)
	if err != nil {
		return fmt.Errorf("样本不存在: %w", err)
	}
	_, err = s.db.Exec(`DELETE FROM miniprogram_samples WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if wsPath != "" {
		if err := os.RemoveAll(wsPath); err != nil {
			s.log.Warn("清理样本工作目录失败(记录已删)", zap.String("sample", id), zap.String("path", wsPath), zap.Error(err))
		}
	}
	return nil
}

// BindProject 绑定/解绑项目（projectID 空 = 解绑）。ownerID 非空时同步属主；
// 为空则保留原属主——补绑项目不应改动样本归属。
func (s *Service) BindProject(sampleID, projectID, ownerID string) error {
	if strings.TrimSpace(ownerID) == "" {
		_, err := s.db.Exec(`UPDATE miniprogram_samples SET project_id = NULLIF($1,''), updated_at = NOW() WHERE id = $2`, projectID, sampleID)
		return err
	}
	_, err := s.db.Exec(`UPDATE miniprogram_samples SET project_id = NULLIF($1,''), owner_user_id = NULLIF($2,''), updated_at = NOW() WHERE id = $3`, projectID, ownerID, sampleID)
	return err
}

// GetSourceFiles 获取样本的源码文件列表
func (s *Service) GetSourceFiles(sampleID string) ([]string, error) {
	sample, err := s.GetSample(sampleID)
	if err != nil {
		return nil, err
	}

	var files []string
	err = filepath.Walk(sample.Workspace, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(sample.Workspace, path)
		files = append(files, rel)
		return nil
	})
	return files, err
}

// ReadSourceFile 读取指定源码文件内容
func (s *Service) ReadSourceFile(sampleID, filePath string) ([]byte, error) {
	sample, err := s.GetSample(sampleID)
	if err != nil {
		return nil, err
	}
	return ReadWorkspaceFile(sample.Workspace, filePath)
}

// AnalyzeTopology 对样本源码做调用链/接口拓扑分析。
func (s *Service) AnalyzeTopology(sampleID string) (*TopologyResult, error) {
	sample, err := s.GetSample(sampleID)
	if err != nil {
		return nil, err
	}
	return AnalyzeTopology(sample.Workspace), nil
}

// AnalyzeInterfaceWorkbench 生成样本的接口工作台（结构化接口清单 + 越权/未授权候选标注）。
func (s *Service) AnalyzeInterfaceWorkbench(sampleID string) (*InterfaceWorkbench, error) {
	sample, err := s.GetSample(sampleID)
	if err != nil {
		return nil, err
	}
	return BuildInterfaceWorkbench(sample.Workspace), nil
}

// SearchHit 跨文件搜索命中行
type SearchHit struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

// SearchWorkspace 在样本工作目录内按关键词（子串，忽略大小写）跨文本文件搜索。
// regexPattern 非空时使用正则匹配。返回命中行，最多 limit 条。
func (s *Service) SearchWorkspace(sample *Sample, keyword string, limit int) ([]SearchHit, error) {
	return SearchWorkspaceWithRegex(sample.Workspace, keyword, nil, limit)
}

// SearchWorkspaceWithRegex 在指定工作目录内搜索；re 非 nil 时优先使用正则。
func SearchWorkspaceWithRegex(workspace, keyword string, re *regexp.Regexp, limit int) ([]SearchHit, error) {
	if limit <= 0 {
		limit = 100
	}
	textExts := map[string]bool{
		".js": true, ".json": true, ".wxml": true, ".wxss": true,
		".ts": true, ".css": true, ".html": true, ".vue": true,
	}
	var hits []SearchHit
	err := filepath.Walk(workspace, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || len(hits) >= limit {
			return nil
		}
		if !textExts[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		rel, _ := filepath.Rel(workspace, path)
		for i, line := range strings.Split(string(data), "\n") {
			if len(hits) >= limit {
				break
			}
			match := false
			if re != nil {
				match = re.MatchString(line)
			} else if keyword != "" {
				match = strings.Contains(strings.ToLower(line), strings.ToLower(keyword))
			}
			if match {
				hits = append(hits, SearchHit{File: rel, Line: i + 1, Text: strings.TrimSpace(line)})
			}
		}
		return nil
	})
	return hits, err
}

// ReadWorkspaceFile 读取工作目录内的文件（防路径穿越：解析后的绝对路径必须
// 落在工作目录之内）。
func ReadWorkspaceFile(workspace, filePath string) ([]byte, error) {
	if filePath == "" {
		return nil, fmt.Errorf("文件路径为空")
	}
	wsAbs, err := filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}
	fullPath := filepath.Join(wsAbs, filePath)
	abs, err := filepath.Abs(fullPath)
	if err != nil {
		return nil, err
	}
	// 必须在工作目录内（含工作目录本身）
	if abs != wsAbs && !strings.HasPrefix(abs, wsAbs+string(os.PathSeparator)) {
		return nil, fmt.Errorf("非法路径：超出样本工作目录")
	}
	return os.ReadFile(abs)
}

// ListFindings 获取扫描发现
func (s *Service) ListFindings(sampleID, category string) ([]*Finding, error) {
	query := `SELECT id, sample_id, category, severity, file_path, line_number, content, description, created_at FROM miniprogram_findings WHERE sample_id = $1`
	args := []interface{}{sampleID}
	if category != "" {
		query += ` AND category = $2`
		args = append(args, category)
	}
	query += ` ORDER BY CASE severity WHEN 'critical' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2 WHEN 'low' THEN 3 ELSE 4 END, created_at DESC`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// 工作目录用于把命中文件路径规范成前端可直接读取的相对路径;查不到样本时不阻断列表
	var wsPath string
	if err := s.db.QueryRow(`SELECT workspace_path FROM miniprogram_samples WHERE id = $1`, sampleID).Scan(&wsPath); err != nil && !errors.Is(err, sql.ErrNoRows) {
		s.log.Warn("查询样本工作目录失败(文件路径不规范化)", zap.String("sample", sampleID), zap.Error(err))
	}

	var findings []*Finding
	for rows.Next() {
		var f Finding
		if err := rows.Scan(&f.ID, &f.SampleID, &f.Category, &f.Severity, &f.FilePath, &f.LineNumber, &f.Content, &f.Description, &f.CreatedAt); err != nil {
			return nil, err
		}
		f.FilePath = NormalizeFindingPath(wsPath, f.FilePath)
		findings = append(findings, &f)
	}
	return findings, nil
}

// NormalizeFindingPath 把落库的命中文件路径转成相对样本工作目录的路径,
// 供前端 file 接口直接读取。历史数据落库的是相对进程 cwd 的 walk 路径
// (如 data/miniprogram_analysis/<hash12>/app.js):优先按工作目录求相对,
// 失败再启发式剥掉 miniprogram_analysis/<hash>/ 前缀。
func NormalizeFindingPath(workspace, p string) string {
	if p == "" {
		return p
	}
	p = filepath.ToSlash(p)
	if ws := filepath.ToSlash(workspace); ws != "" {
		if rel, err := filepath.Rel(ws, p); err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, "../") {
			return rel
		}
	}
	const marker = "miniprogram_analysis/"
	if idx := strings.LastIndex(p, marker); idx >= 0 {
		rest := p[idx+len(marker):]
		if slash := strings.Index(rest, "/"); slash >= 0 {
			return rest[slash+1:]
		}
	}
	return p
}

// RunScan 对样本执行敏感信息扫描并写入数据库
func (s *Service) RunScan(sample *Sample) error {
	// 清除旧扫描结果(失败仅记日志:继续扫描,用户重扫可去重)
	if _, err := s.db.Exec(`DELETE FROM miniprogram_findings WHERE sample_id = $1`, sample.ID); err != nil {
		s.log.Warn("清除旧扫描结果失败(可能叠加旧记录)", zap.String("sample", sample.ID), zap.Error(err))
	}

	scanner := NewScanner()
	findings, err := scanner.ScanDirectory(sample.Workspace)
	if err != nil {
		s.log.Warn("扫描失败", zap.String("sample", sample.ID), zap.Error(err))
		return err
	}

	for i := range findings {
		findings[i].ID = uuid.New().String()
		findings[i].SampleID = sample.ID
		_, err := s.db.Exec(`
			INSERT INTO miniprogram_findings (id, sample_id, category, severity, file_path, line_number, content, description, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			findings[i].ID, findings[i].SampleID, findings[i].Category, findings[i].Severity,
			findings[i].FilePath, findings[i].LineNumber, findings[i].Content, findings[i].Description, time.Now())
		if err != nil {
			s.log.Warn("写入发现记录失败", zap.Error(err))
		}
	}

	// 更新 updated_at
	if _, err := s.db.Exec(`UPDATE miniprogram_samples SET updated_at = NOW() WHERE id = $1`, sample.ID); err != nil {
		s.log.Warn("刷新样本扫描时间失败", zap.String("sample", sample.ID), zap.Error(err))
	}
	s.log.Info("扫描完成", zap.String("sample", sample.ID), zap.Int("findings", len(findings)))

	// 扫描发现变化后重建测试项账本(幂等;失败不阻断扫描结果)
	if _, terr := s.RegenerateTestItems(sample.ID); terr != nil {
		s.log.Warn("重建测试项失败", zap.String("sample", sample.ID), zap.Error(terr))
	}
	return nil
}

// extractAppID 从解包目录中读取 app.json 的 appid（app.json 可能在根目录或
// 子目录——分包/多包结构下取第一个命中）。
func extractAppID(dir string) string {
	var appID string
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if appID != "" {
			return filepath.SkipAll
		}
		if filepath.Base(path) != "app.json" {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		appID = parseAppIDFromJSON(data)
		return nil
	})
	return appID
}

// parseAppIDFromJSON 从 app.json 的 JSON 中提取 appid 值。
func parseAppIDFromJSON(data []byte) string {
	var doc struct {
		AppID string `json:"appid"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return ""
	}
	return strings.TrimSpace(doc.AppID)
}
