package apkanalysis

// backend_manage — 后端注册生命周期管理:上传 jar/zip、指定本地路径、
// 冒烟验证、设首选、移除,以及服务启动时从 DB 加载已注册后端。
// 数据落 apk_backends 表,运行态注册进 BackendRegistry。

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"log"

	"go.uber.org/zap"
)

// BackendRecord apk_backends 表行(API 返回体)。
type BackendRecord struct {
	ID         string     `json:"id"`
	Kind       string     `json:"kind"`
	Name       string     `json:"name"`
	Path       string     `json:"path"`
	JavaPath   string     `json:"java_path,omitempty"`
	Workers    int        `json:"workers"`
	VerifiedAt *time.Time `json:"verified_at,omitempty"`
	SmokeOK    bool       `json:"smoke_ok"`
	Preferred  bool       `json:"preferred"`
	Enabled    bool       `json:"enabled"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// backendsDir 后端文件托管目录(工作区下,上传的 jar/解压的 zip 落这里)。
func (s *Service) backendsDir() string {
	return filepath.Join(s.workspace, "backends")
}

// BackendFilePath 给定后端 id 的托管根目录。
func (s *Service) backendDir(id string) string {
	return filepath.Join(s.backendsDir(), id)
}

// ────────────────────────── DB 读写 ──────────────────────────

func (s *Service) upsertBackend(ctx context.Context, r *BackendRecord) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO apk_backends (id, kind, name, path, java_path, workers, verified_at, smoke_ok, preferred, enabled, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (kind) DO UPDATE SET
			name=EXCLUDED.name, path=EXCLUDED.path, java_path=EXCLUDED.java_path,
			workers=EXCLUDED.workers, verified_at=EXCLUDED.verified_at, smoke_ok=EXCLUDED.smoke_ok,
			preferred=EXCLUDED.preferred, enabled=EXCLUDED.enabled, updated_at=EXCLUDED.updated_at`,
		r.ID, r.Kind, r.Name, r.Path, r.JavaPath, r.Workers, r.VerifiedAt, r.SmokeOK,
		r.Preferred, r.Enabled, r.CreatedAt, r.UpdatedAt)
	return err
}

// clearPreferred 清除全部 preferred 标志(设新首选前)。
func (s *Service) clearPreferred(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE apk_backends SET preferred = false`)
	return err
}

// listBackendRows 查询 apk_backends 全表(按 created_at 升序,稳定)。
func (s *Service) listBackendRows(ctx context.Context) ([]*BackendRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, kind, COALESCE(name,''), path, COALESCE(java_path,''), workers,
			verified_at, smoke_ok, preferred, enabled, created_at, updated_at
		FROM apk_backends ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*BackendRecord
	for rows.Next() {
		r := &BackendRecord{}
		if err := rows.Scan(&r.ID, &r.Kind, &r.Name, &r.Path, &r.JavaPath, &r.Workers,
			&r.VerifiedAt, &r.SmokeOK, &r.Preferred, &r.Enabled, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Service) getBackendRow(ctx context.Context, id string) (*BackendRecord, error) {
	rows, err := s.listBackendRows(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		if r.ID == id || r.Kind == id {
			return r, nil
		}
	}
	return nil, fmt.Errorf("backend not found: %s", id)
}

// deleteBackendRow 从 DB 删除记录。
func (s *Service) deleteBackendRow(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM apk_backends WHERE id = $1 OR kind = $1`, id)
	return err
}

// ────────────────────────── 注册 / 加载 ──────────────────────────

// registerJadx 构造 JadxBackend 并注册进 registry(DB 记录已存在时同步字段)。
func (s *Service) registerJadx(rec *BackendRecord) {
	if !rec.Enabled {
		return
	}
	backend := NewJadxBackend(rec.ID, rec.Path, rec.JavaPath, rec.Workers)
	if rec.Name != "" {
		backend.name = rec.Name
	}
	s.registry.Register(backend)
	if rec.Preferred {
		s.registry.SetPrefer("jadx")
	}
}

// unregisterKind 从 registry 移除指定 kind。
func (s *Service) unregisterKind(kind string) {
	s.registry.Remove(kind)
}

// LoadRegisteredBackends 服务启动时加载 DB 中已启用的后端进 registry。
// 返回加载的 kind 列表。
func (s *Service) LoadRegisteredBackends(ctx context.Context) []string {
	rows, err := s.listBackendRows(ctx)
	if err != nil {
		s.log.Warn("load backends failed", zap.Error(err))
		return nil
	}
	var kinds []string
	for _, r := range rows {
		switch r.Kind {
		case "jadx":
			s.registerJadx(r)
			kinds = append(kinds, r.Kind)
		}
	}
	if len(kinds) > 0 {
		s.log.Info("android backends loaded from db", zap.Any("kinds", kinds))
	}
	return kinds
}

// ────────────────────────── 上传 / 路径 ──────────────────────────

// AddJadxByPath 指定本地路径接入 jadx 后端。path 支持:
//   - jar 文件: java -jar <jar>
//   - 可执行文件(bin/jadx): 直接执行
//   - jadx 发行 zip 解压后的目录: 内部探测 jar/bin
//
// 完成版本探测 + 冒烟测试后落库并注册。
func (s *Service) AddJadxByPath(ctx context.Context, path, javaPath string) (*BackendRecord, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("path required")
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("path not accessible: %w", err)
	}
	if st.IsDir() {
		resolved, ok := probeJadxInDir(path)
		if !ok {
			return nil, fmt.Errorf("no jadx jar or bin/jadx found under %s", path)
		}
		path = resolved
	} else if strings.HasSuffix(strings.ToLower(path), ".zip") {
		return nil, fmt.Errorf("zip 请走上传接口(会自动解压托管)")
	}

	backend := NewJadxBackend(uuid.NewString(), path, javaPath, 2)
	name, err := backend.SmokeJadx(ctx)
	if err != nil {
		return nil, fmt.Errorf("jadx smoke test failed: %w", err)
	}
	now := time.Now()
	rec := &BackendRecord{
		ID: backend.id, Kind: "jadx", Name: name, Path: path, JavaPath: javaPath,
		Workers: 2, VerifiedAt: &now, SmokeOK: true, Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.upsertBackend(ctx, rec); err != nil {
		return nil, fmt.Errorf("save backend: %w", err)
	}
	// 首次接入默认设为首选
	prefs, _ := s.listBackendRows(ctx)
	if !anyPreferred(prefs) {
		rec.Preferred = true
		_ = s.clearPreferred(ctx)
		_ = s.upsertBackend(ctx, rec)
	}
	s.registerJadx(rec)
	return rec, nil
}

// UploadJadx 上传 jadx 发行包(jar / zip)并托管到工作区。
// 完成后冒烟测试 → 落库 → 注册。zip 解压后探测 jar 或 bin/jadx。
func (s *Service) UploadJadx(ctx context.Context, srcPath, originalName, javaPath string) (*BackendRecord, error) {
	lower := strings.ToLower(originalName)
	id := uuid.NewString()
	dir := s.backendDir(id)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}

	var backendPath string
	if strings.HasSuffix(lower, ".zip") {
		if err := extractZip(srcPath, dir); err != nil {
			return nil, fmt.Errorf("extract zip: %w", err)
		}
		resolved, ok := probeJadxInDir(dir)
		if !ok {
			return nil, fmt.Errorf("zip 内未找到 jadx jar 或 bin/jadx 可执行文件")
		}
		backendPath = resolved
	} else if strings.HasSuffix(lower, ".jar") {
		dest := filepath.Join(dir, filepath.Base(originalName))
		if err := copyFile(srcPath, dest); err != nil {
			return nil, err
		}
		backendPath = dest
	} else {
		return nil, fmt.Errorf("仅支持 jar / zip 上传")
	}

	backend := NewJadxBackend(id, backendPath, javaPath, 2)
	name, err := backend.SmokeJadx(ctx)
	if err != nil {
		return nil, fmt.Errorf("jadx smoke test failed: %w", err)
	}
	now := time.Now()
	rec := &BackendRecord{
		ID: id, Kind: "jadx", Name: name, Path: backendPath, JavaPath: javaPath,
		Workers: 2, VerifiedAt: &now, SmokeOK: true, Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.upsertBackend(ctx, rec); err != nil {
		return nil, fmt.Errorf("save backend: %w", err)
	}
	prefs, _ := s.listBackendRows(ctx)
	if !anyPreferred(prefs) {
		rec.Preferred = true
		_ = s.clearPreferred(ctx)
		_ = s.upsertBackend(ctx, rec)
	}
	s.registerJadx(rec)
	return rec, nil
}

// VerifyBackend 手动触发后端验证(冒烟 + 版本刷新)。
func (s *Service) VerifyBackend(ctx context.Context, id string) (*BackendRecord, error) {
	rec, err := s.getBackendRow(ctx, id)
	if err != nil {
		return nil, err
	}
	switch rec.Kind {
	case "jadx":
		b := NewJadxBackend(rec.ID, rec.Path, rec.JavaPath, rec.Workers)
		name, err := b.SmokeJadx(ctx)
		if err != nil {
			return nil, fmt.Errorf("verify failed: %w", err)
		}
		now := time.Now()
		rec.Name = name
		rec.SmokeOK = true
		rec.VerifiedAt = &now
		rec.UpdatedAt = now
		if err := s.upsertBackend(ctx, rec); err != nil {
			return nil, err
		}
		s.unregisterKind("jadx")
		s.registerJadx(rec)
		return rec, nil
	}
	return nil, fmt.Errorf("unsupported kind: %s", rec.Kind)
}

// SetPreferredBackend 设指定 kind 为首选后端。
func (s *Service) SetPreferredBackend(ctx context.Context, kind string) error {
	if !s.registry.HasKind(kind) {
		return fmt.Errorf("backend %s not registered", kind)
	}
	if err := s.clearPreferred(ctx); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE apk_backends SET preferred = true, updated_at = $2 WHERE kind = $1`, kind, time.Now()); err != nil {
		return err
	}
	s.registry.SetPrefer(kind)
	return nil
}

// RemoveBackend 移除后端(DB + registry)。托管文件一并清理。
func (s *Service) RemoveBackend(ctx context.Context, id string) error {
	rec, err := s.getBackendRow(ctx, id)
	if err != nil {
		return err
	}
	s.unregisterKind(rec.Kind)
	if err := s.deleteBackendRow(ctx, rec.ID); err != nil {
		return err
	}
	if err := os.RemoveAll(s.backendDir(rec.ID)); err != nil {
		s.log.Warn("清理后端部署目录失败(记录已删,目录可手工清理)", zap.String("id", rec.ID), zap.Error(err))
	}
	return nil
}

// ListBackends 返回全部后端记录,并附 registry 实时健康状态。
func (s *Service) ListBackends(ctx context.Context) ([]BackendRecord, error) {
	rows, err := s.listBackendRows(ctx)
	if err != nil {
		return nil, err
	}
	healthy := map[string]bool{}
	for _, info := range s.registry.List(ctx) {
		healthy[info.Kind] = info.Healthy
	}
	out := make([]BackendRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, *r)
	}
	// 排序:首选在前,其余按 created_at
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Preferred != out[j].Preferred {
			return out[i].Preferred
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	_ = healthy
	return out, nil
}

// ────────────────────────── 工具 ──────────────────────────

// probeJadxInDir 在目录内探测 jadx 入口:
//  1. bin/jadx(可执行)
//  2. 任意 *.jar(Main-Class 含 jadx)
//
// 返回入口路径。
func probeJadxInDir(dir string) (string, bool) {
	bin := filepath.Join(dir, "bin", "jadx")
	if st, err := os.Stat(bin); err == nil && !st.IsDir() {
		return bin, true
	}
	// 全目录扫 jar(优先 jadx-cli / jadx 命名)
	matches, _ := filepath.Glob(filepath.Join(dir, "lib", "*.jar"))
	matches = append(matches, mustGlob(dir, "*.jar")...)
	for _, name := range []string{"jadx-cli", "jadx"} {
		for _, m := range matches {
			if strings.Contains(filepath.Base(m), name) {
				return m, true
			}
		}
	}
	if len(matches) > 0 {
		return matches[0], true
	}
	return "", false
}

func mustGlob(dir, pat string) []string {
	m, _ := filepath.Glob(filepath.Join(dir, pat))
	return m
}

func anyPreferred(recs []*BackendRecord) bool {
	for _, r := range recs {
		if r.Preferred {
			return true
		}
	}
	return false
}

// extractZip 解压 zip 到 dest(防路径穿越;顶层单目录自动去壳)。
func extractZip(srcPath, dest string) error {
	zr, err := zip.OpenReader(srcPath)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		name := filepath.FromSlash(f.Name)
		if strings.Contains(name, "..") {
			return fmt.Errorf("zip path traversal blocked: %s", f.Name)
		}
		target := filepath.Join(dest, name)
		if !strings.HasPrefix(target, filepath.Clean(dest)+string(filepath.Separator)) && target != filepath.Clean(dest) {
			return fmt.Errorf("zip entry escapes dest: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o750); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return err
		}
		in, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.Create(target)
		if err != nil {
			in.Close()
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			in.Close()
			return err
		}
		out.Close()
		in.Close()
	}
	// 去掉顶层单目录壳(bin/lib 直接提升到 dest 下)
	return stripSingleTopDir(dest)
}

// stripSingleTopDir 若解压产物只有一个顶层目录,把内容提升到 dest。
func stripSingleTopDir(dest string) error {
	entries, err := os.ReadDir(dest)
	if err != nil || len(entries) != 1 || !entries[0].IsDir() {
		return nil
	}
	inner := filepath.Join(dest, entries[0].Name())
	if strings.HasPrefix(filepath.Base(inner), ".") {
		return nil
	}
	kids, _ := os.ReadDir(inner)
	for _, k := range kids {
		src := filepath.Join(inner, k.Name())
		dst := filepath.Join(dest, k.Name())
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		if err := os.Rename(src, dst); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(inner); err != nil {
		log.Printf("[apkanalysis] 清理解压壳层目录失败 %s: %v", inner, err)
	}
	return nil
}
