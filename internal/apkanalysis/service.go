// Package apkanalysis — 安卓分析服务层:桥接 internal/apkengine(Go 移植的
// ASC 引擎)与 HTTP/Agent 两个消费面。职责:样本归档(sha256 去重)、
// 项目绑定、检索执行、任务落库。反编译链就绪后补充 GetSource/Manifest/Profile。
package apkanalysis

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"finduai/internal/apkengine/dex"
	"finduai/internal/apkengine/zipprobe"
	"finduai/internal/database"
	ws "finduai/internal/workspace"
)

const (
	// MaxRows 硬上限(对齐 ASC GUI 的 _MAX_UI_RESULTS)。
	MaxRows = 5000
	// DefaultRows 默认返回行数。
	DefaultRows = 1000
	// maxRecordRows 任务落库 result_json 里保留的最大行数(防止大结果撑爆任务表)。
	maxRecordRows = 50
)

type Service struct {
	db        *database.DB
	log       *zap.Logger
	workspace string
	registry  *BackendRegistry
}

func NewService(db *database.DB, log *zap.Logger, workspace string) (*Service, error) {
	if workspace == "" {
		return nil, fmt.Errorf("workspace required")
	}
	if err := os.MkdirAll(workspace, 0o750); err != nil {
		return nil, fmt.Errorf("workspace mkdir: %w", err)
	}
	s := &Service{db: db, log: log, workspace: workspace, registry: NewBackendRegistry()}
	// 目录登记（治理 W2）：工作区根与两个派生目录入注册表（观测/GC 可见）；
	// 方法内既有 s.workspace 拼装保持不变（单根源已在此登记）。
	for _, e := range []ws.Entry{
		{Name: "apkanalysis-workspace", Base: func() (string, error) { return workspace, nil }, Scope: ws.ScopeGlobal},
		{Name: "apkanalysis-backends", Base: ws.JoinBase(workspace, "backends"), Scope: ws.ScopeGlobal},
		{Name: "apkanalysis-source-cache", Base: ws.JoinBase(workspace, ".source_cache"), Scope: ws.ScopeGlobal},
	} {
		if _, err := ws.RegisterOrResolve(e); err != nil {
			return nil, err
		}
	}
	// 注册顺序 = 降级优先级(先高后低)。默认纯 Go 链:
	// go-dad(内置,默认首选)→ dad(Python androguard,兼容保留)→ smali(兜底,永远可用)。
	// B 方案(2026-09-14):DAD 不再自动提为首选——仅当显式 SetPrefer("dad")
	//(管理 API/配置)时才提升;未部署 ASC 的环境行为完全一致。
	s.registry.Register(NewGoDadBackend())
	if dad := NewDadBackend(); dad != nil {
		s.registry.Register(dad)
	}
	s.registry.Register(NewSmaliBackend(s.smaliForApkPath))
	return s, nil
}

// ────────────────────────── 样本 ──────────────────────────

type Sample struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	SHA256      string    `json:"sha256"`
	Size        int64     `json:"size"`
	Workspace   string    `json:"workspace_path"`
	PackageName string    `json:"package_name,omitempty"`
	VersionName string    `json:"version_name,omitempty"`
	DexCount    int       `json:"dex_count"`
	ClassCount  int       `json:"class_count"`
	ProjectID   string    `json:"project_id,omitempty"`
	Source      string    `json:"source"`
	CreatedAt   time.Time `json:"created_at"`
}

// RegisterFromFile 把一个 APK 收编进工作区:sha256 去重、统计 DEX/类数、落库。
// srcPath 必须由调用方先行校验(chat 上传路径校验复用 handler 侧现成函数)。
func (s *Service) RegisterFromFile(ctx context.Context, srcPath, originalName, projectID, source string) (*Sample, error) {
	if originalName == "" {
		originalName = filepath.Base(srcPath)
	}
	fp, err := os.Open(srcPath)
	if err != nil {
		return nil, fmt.Errorf("open src: %w", err)
	}

	hasher := sha256.New()
	size, err := io.Copy(hasher, fp)
	if err != nil {
		fp.Close()
		return nil, fmt.Errorf("hash: %w", err)
	}
	sum := hex.EncodeToString(hasher.Sum(nil))

	if existing, err := s.findBySHA256(sum); err == nil && existing != nil {
		fp.Close()
		if projectID != "" && existing.ProjectID == "" {
			_ = s.BindProject(existing.ID, projectID)
			existing.ProjectID = projectID
		}
		return existing, nil
	}

	dest := filepath.Join(s.workspace, sum[:12]+"_"+filepath.Base(originalName))
	if err := copyFile(srcPath, dest); err != nil {
		fp.Close()
		return nil, fmt.Errorf("archive: %w", err)
	}
	fp.Close()

	dexCount, classCount, err := countDexClasses(dest)
	if err != nil {
		s.log.Warn("count dex/classes failed", zap.String("apk", dest), zap.Error(err))
	}

	sample := &Sample{
		ID:         uuid.NewString(),
		Name:       filepath.Base(originalName),
		SHA256:     sum,
		Size:       size,
		Workspace:  dest,
		DexCount:   dexCount,
		ClassCount: classCount,
		ProjectID:  projectID,
		Source:     source,
		CreatedAt:  time.Now(),
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO apk_samples (id, name, sha256, size, workspace_path, dex_count, class_count, project_id, source, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,NULLIF($8,'')::text,$9,$10)`,
		sample.ID, sample.Name, sample.SHA256, sample.Size, sample.Workspace,
		sample.DexCount, sample.ClassCount, sample.ProjectID, sample.Source, sample.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert sample: %w", err)
	}
	s.log.Info("android sample registered",
		zap.String("id", sample.ID), zap.String("name", sample.Name),
		zap.String("sha256", sum), zap.Int("dex", dexCount), zap.Int("classes", classCount))
	return sample, nil
}

// RegisterDexSet 收编脱壳产物:dex 集合 zip(FRIDA-DEXDump/blackdex 等 dump
// 结果,条目名任意),规范化重命名为 classesN.dex 后走既有收编/索引管道。
// 脱壳产物通常没有 AndroidManifest.xml,manifest 相关画像会降级,代码检索
// (android_findrefs / android_source / android_extract_apis)全部可用。
func (s *Service) RegisterDexSet(ctx context.Context, srcPath, originalName, projectID, source string) (*Sample, error) {
	zr, err := zip.OpenReader(srcPath)
	if err != nil {
		return nil, fmt.Errorf("打开 dex 集合失败: %w", err)
	}
	defer zr.Close()

	// 收集全部 .dex 条目(按解压顺序;跳过目录与极小条目)
	type dexEntry struct {
		name string
		rc   io.ReadCloser
	}
	dexFiles := make([]dexEntry, 0, 8)
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || f.UncompressedSize64 < 112 {
			continue
		}
		if !strings.EqualFold(filepath.Ext(f.Name), ".dex") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		dexFiles = append(dexFiles, dexEntry{name: f.Name, rc: rc})
	}
	if len(dexFiles) == 0 {
		return nil, fmt.Errorf("zip 中没有 .dex 条目,不是有效的脱壳产物")
	}

	// 合成规范化 zip:classes.dex / classes2.dex / ...(下游按 classes* 索引)
	tmp, err := os.CreateTemp("", "dexset_*.apk")
	if err != nil {
		for _, d := range dexFiles {
			d.rc.Close()
		}
		return nil, err
	}
	defer tmp.Close()
	defer os.Remove(tmp.Name())
	tw := zip.NewWriter(tmp)
	for i, d := range dexFiles {
		name := "classes.dex"
		if i > 0 {
			name = fmt.Sprintf("classes%d.dex", i+1)
		}
		w, werr := tw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
		if werr == nil {
			_, _ = io.Copy(w, d.rc)
		}
		d.rc.Close()
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := tmp.Sync(); err != nil {
		return nil, err
	}

	if originalName == "" {
		originalName = filepath.Base(srcPath)
	}
	originalName = strings.TrimSuffix(originalName, filepath.Ext(originalName)) + "_dexset.apk"
	return s.RegisterFromFile(ctx, tmp.Name(), originalName, projectID, source)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

func (s *Service) findBySHA256(sum string) (*Sample, error) {
	rows, err := s.querySamples(`WHERE sha256 = $1 ORDER BY created_at DESC LIMIT 1`, sum)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// where 传入完整后缀(WHERE/ORDER/LIMIT 组合)。
func (s *Service) querySamples(where string, args ...any) ([]*Sample, error) {
	rows, err := s.db.Query(`SELECT id, name, sha256, size, workspace_path,
		COALESCE(package_name,''), COALESCE(version_name,''), dex_count, class_count,
		COALESCE(project_id,''), source, created_at
		FROM apk_samples `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Sample
	for rows.Next() {
		sample := &Sample{}
		if err := rows.Scan(&sample.ID, &sample.Name, &sample.SHA256, &sample.Size, &sample.Workspace,
			&sample.PackageName, &sample.VersionName, &sample.DexCount, &sample.ClassCount,
			&sample.ProjectID, &sample.Source, &sample.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, sample)
	}
	return out, rows.Err()
}

// ListSamples 样本列表;projectID 为空返回全部。
func (s *Service) ListSamples(projectID string) ([]*Sample, error) {
	if projectID != "" {
		return s.querySamples(`WHERE project_id = $1`, projectID)
	}
	return s.querySamples(``)
}

// GetSample 按 ID 或工作区文件名解析样本。
func (s *Service) GetSample(idOrName string) (*Sample, error) {
	rows, err := s.querySamples(`WHERE id = $1 OR name = $1 ORDER BY created_at DESC LIMIT 1`, idOrName)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("sample not found: %s", idOrName)
	}
	return rows[0], nil
}

// BindProject 绑定/解绑项目(projectID 空 = 解绑)。
func (s *Service) BindProject(sampleID, projectID string) error {
	_, err := s.db.Exec(`UPDATE apk_samples SET project_id = NULLIF($2,'')::text WHERE id = $1`, sampleID, projectID)
	return err
}

// DeleteSample 删除样本记录与工作区文件。
func (s *Service) DeleteSample(id string) error {
	sample, err := s.GetSample(id)
	if err != nil {
		return err
	}
	if sample.Workspace != "" {
		if err := os.Remove(sample.Workspace); err != nil {
			s.log.Warn("清理样本工作区文件失败(记录将照常删除)", zap.String("workspace", sample.Workspace), zap.Error(err))
		}
	}
	_, err = s.db.Exec(`DELETE FROM apk_samples WHERE id = $1`, id)
	return err
}

// countDexClasses 统计 DEX 与类总数(仅读 header,不做检索)。
func countDexClasses(apkPath string) (dexCount int, classCount int, err error) {
	r, err := zipprobe.Open(apkPath)
	if err != nil {
		return 0, 0, err
	}
	defer r.Close()
	entries, err := zipprobe.ParseCDDexEntries(r)
	if err != nil {
		return 0, 0, err
	}
	for _, e := range entries {
		data, err := zipprobe.InflateDex(r, e)
		if err != nil {
			continue
		}
		for _, ld := range zipprobe.LogicalDexBuffers(e.Name, data) {
			d, err := dex.Parse(ld.Buf, ld.Name)
			if err != nil {
				continue
			}
			dexCount++
			classCount += int(d.NumClasses())
		}
	}
	return dexCount, classCount, nil
}

// ────────────────────────── 任务 ──────────────────────────

type ScanTask struct {
	ID         string    `json:"id"`
	SampleID   string    `json:"sample_id"`
	ProjectID  string    `json:"project_id,omitempty"`
	Action     string    `json:"action"`
	QueryJSON  string    `json:"query_json,omitempty"` // 检索参数(前端"最近查询"消费)
	Status     string    `json:"status"`
	ResultJSON string    `json:"-"`
	Error      string    `json:"error,omitempty"`
	DurationMS int64     `json:"duration_ms"`
	CreatedAt  time.Time `json:"created_at"`
}

func (s *Service) recordTask(sample *Sample, action string, req any, result any, status, errText string, durationMS int64) {
	qj, _ := json.Marshal(req)
	rj, _ := json.Marshal(result)
	projectID := ""
	if sample != nil {
		projectID = sample.ProjectID
	}
	_, err := s.db.Exec(`INSERT INTO apk_scan_tasks
		(id, sample_id, project_id, action, query_json, status, result_json, error, duration_ms, created_at)
		VALUES ($1,$2,NULLIF($3,'')::text,$4,$5,$6,$7,NULLIF($8,'')::text,$9,$10)`,
		uuid.NewString(), sampleIDOf(sample), projectID, action, string(qj), status, string(rj), errText, durationMS, time.Now())
	if err != nil {
		s.log.Warn("record scan task failed", zap.Error(err))
	}
}

// RecordTask 供外部(MCP 工具等)落一条扫描任务历史到 apk_scan_tasks。
// action 取值与 apk_scan_tasks.action 一致；前端 taskActionLabel 需同步。
func (s *Service) RecordTask(sample *Sample, action string, req any, result any, status, errText string, durationMS int64) {
	s.recordTask(sample, action, req, result, status, errText, durationMS)
}

func sampleIDOf(sample *Sample) string {
	if sample == nil {
		return ""
	}
	return sample.ID
}

// ListTasks 任务历史;sampleID 为空返回全部(上限 200)。
func (s *Service) ListTasks(sampleID string) ([]*ScanTask, error) {
	where := ``
	args := []any{}
	if sampleID != "" {
		where = `WHERE sample_id = $1`
		args = append(args, sampleID)
	}
	rows, err := s.db.Query(`SELECT id, sample_id, COALESCE(project_id,''), action, COALESCE(query_json,''), status,
		COALESCE(error,''), duration_ms, created_at
		FROM apk_scan_tasks `+where+` ORDER BY created_at DESC LIMIT 200`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ScanTask
	for rows.Next() {
		task := &ScanTask{}
		if err := rows.Scan(&task.ID, &task.SampleID, &task.ProjectID, &task.Action, &task.QueryJSON,
			&task.Status, &task.Error, &task.DurationMS, &task.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, task)
	}
	return out, rows.Err()
}
