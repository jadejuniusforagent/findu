package apkanalysis

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"

	"finduai/internal/apkengine/axml"
	"finduai/internal/apkengine/smali"
	"finduai/internal/apkengine/zipprobe"
)

// ────────────────────────── 阶段2占位 ──────────────────────────

// ErrDecompilerUnavailable — 无可用的 Java 反编译后端（DAD / jadx 均未部署）。
// 降级链中的 smali 兜底不受此错误影响（smali 是字节码级视图）。
var ErrDecompilerUnavailable = errors.New("no decompiler backend available (set ANDROID_ASC_PATH for DAD, or upload a jadx jar; smali view always available as fallback)")

// GetSource 反编译单类。检索与 DEX 定位走原生 Go 引擎;反编译经
// BackendRegistry 降级链选择后端（jadx → dad → smali）。
// 结果按 (sha256, class) 落盘缓存(样本不可变 → 缓存永久有效),
// 并发子进程由各后端内部信号量限幅,防止多请求并行拉满内存。
func (s *Service) GetSource(ctx context.Context, sampleID, dalvikClass string) (dexName, source string, err error) {
	start := time.Now()
	sample, err := s.GetSample(sampleID)
	if err != nil {
		return "", "", err
	}
	class := normalizeClassQuery(dalvikClass, false)
	taskReq := map[string]string{"apk": sample.Name, "class": class}
	fail := func(format string, args ...any) (string, string, error) {
		e := fmt.Errorf(format, args...)
		s.recordTask(sample, "getclass", taskReq, nil, "error", e.Error(), time.Since(start).Milliseconds())
		return "", "", e
	}

	// 原生 Go 定位定义该类的 DEX(顺带早失败:类不存在立即返回)
	dexName, err = s.locateClassDex(sample.Workspace, class)
	if err != nil {
		return fail("locate dex: %s", err.Error())
	}
	if dexName == "" {
		return fail("class %s not found in APK(该类在样本 DEX 中不存在:可能是从字符串命中推测的类名已被混淆/替换,或位于 split APK/动态加载 dex;用 android_findrefs find_type=type 检索该类名确认实际存在的类,或改用命中行里真实出现的类)", class)
	}

	// 缓存命中:零子进程
	if src, ok := s.readSourceCache(sample.SHA256, class); ok {
		s.recordTask(sample, "getclass",
			taskReq, map[string]any{"dex": dexName, "bytes": len(src), "cached": true},
			"done", "", time.Since(start).Milliseconds())
		return dexName, src, nil
	}

	// 降级链:依次取健康后端(优先 jadx/dad,最终落到 smali 兜底)
	backend := s.registry.Resolve(ctx)
	if backend == nil {
		return dexName, "", ErrDecompilerUnavailable
	}
	source, err = backend.Decompile(ctx, sample.Workspace, class)
	if err != nil {
		return fail("decompile (%s) failed: %s", backend.Kind(), err.Error())
	}
	s.writeSourceCache(sample.SHA256, class, source)
	elapsed := time.Since(start)
	s.recordTask(sample, "getclass",
		taskReq,
		map[string]any{"dex": dexName, "backend": backend.Kind(), "bytes": len(source)},
		"done", "", elapsed.Milliseconds())
	return dexName, source, nil
}

// sha256Class 类名 → 稳定缓存文件名(避免 dalvik 描述符里的 /; 进文件名)。
func sha256Class(class string) []byte {
	h := sha256.New()
	h.Write([]byte(class))
	return h.Sum(nil)
}

// sourceCacheDir 反编译缓存目录(工作区下,随样本库生命周期)。
func (s *Service) sourceCacheDir() string {
	return filepath.Join(s.workspace, ".source_cache")
}

func (s *Service) readSourceCache(sha256, class string) (string, bool) {
	path := s.sourceCachePath(sha256, class)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(data), true
}

func (s *Service) writeSourceCache(sha256, class, source string) {
	dir := filepath.Join(s.sourceCacheDir(), sha256[:12])
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return
	}
	name := fmt.Sprintf("%x.java", sha256Class(class))
	tmp := filepath.Join(dir, name+".tmp")
	if err := os.WriteFile(tmp, []byte(source), 0o600); err != nil {
		return
	}
	if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
		s.log.Warn("源码缓存原子替换失败(下次解析重新生成)", zap.String("file", name), zap.Error(err))
	}
}

func (s *Service) sourceCachePath(sha256, class string) string {
	name := fmt.Sprintf("%x.java", sha256Class(class))
	return filepath.Join(s.sourceCacheDir(), sha256[:12], name)
}

// locateClassDex 原生定位定义目标类的 DEX(字节层二分,不解码字符串)。
func (s *Service) locateClassDex(apkPath, class string) (string, error) {
	r, err := zipprobe.Open(apkPath)
	if err != nil {
		return "", err
	}
	defer r.Close()
	entries, err := zipprobe.ParseCDDexEntries(r)
	if err != nil {
		return "", err
	}
	zipprobe.SortByCompSize(entries)
	for _, e := range entries {
		data, err := zipprobe.InflateDex(r, e)
		if err != nil {
			return "", err
		}
		for _, ld := range zipprobe.LogicalDexBuffers(e.Name, data) {
			defined, err := zipprobe.DexDefinesClass(ld.Buf, class)
			if err == nil && defined {
				return ld.Name, nil
			}
		}
	}
	return "", nil
}

// DecompilerAvailability 返回 nil 表示有可用的 Java 反编译后端（非 smali 兜底）。
func (s *Service) DecompilerAvailability() error {
	b := s.registry.Resolve(context.Background())
	if b != nil && b.Kind() != "smali" {
		return nil
	}
	return ErrDecompilerUnavailable
}

// DecompilerPaths 反编译运行时路径(asc 目录与解释器)。
func DecompilerPaths() (ascDir, python string, ok bool) {
	python = strings.TrimSpace(os.Getenv("ANDROID_ASC_PYTHON"))
	ascDir = strings.TrimSpace(os.Getenv("ANDROID_ASC_PATH"))
	if ascDir == "" {
		ascDir = filepath.Join("external", "asc")
	}
	ascMain := filepath.Join(ascDir, "main.py")
	if st, err := os.Stat(ascMain); err != nil || st.IsDir() {
		return "", "", false
	}
	// 宿主侧 Python/ASC 自动安装与更新已下线（不再创建 ascenv venv）。
	// 如需 DAD 反编译，请自行提供 ANDROID_ASC_PATH（含 main.py 的 ASC 目录）与
	// ANDROID_ASC_PYTHON（已装 androguard 的解释器）；缺省用裸 python3 探测。
	if python == "" {
		python = "python3"
	}
	return ascDir, python, true
}

// Backends 返回后端注册表引用（供 handler 层查询/管理）。
func (s *Service) Backends() *BackendRegistry { return s.registry }

// smaliStandardForApkPath 输出标准 smali(可被 smali.jar 汇编)。
func (s *Service) smaliStandardForApkPath(apkPath, class string) (string, error) {
	dalvik := normalizeClassQuery(class, false)
	dexs, err := s.getSampleDexs(apkPath)
	if err != nil {
		return "", err
	}
	for _, cd := range dexs {
		d := cd.mgr.Dex()
		n := int(d.NumClasses())
		for i := 0; i < n; i++ {
			cls := d.GetClassByIdx(uint32(i))
			if cls.Fullname() != dalvik {
				continue
			}
			lines := smali.StandardClass(d, cls)
			out := strings.Join(lines, "\n") + "\n"
			if len(out) > maxSmaliBytes {
				out = out[:maxSmaliBytes] + "\n...[截断,超大类]\n"
			}
			return out, nil
		}
	}
	return "", fmt.Errorf("class %s not found", class)
}

// smaliForApkPath 给定 APK 路径 + dalvik 类名，渲染 smali 字节码。
// 供 SmaliBackend 与 GetClassSmali 复用（共享 DEX 缓存）。
func (s *Service) smaliForApkPath(apkPath, class string) (string, error) {
	dalvik := normalizeClassQuery(class, false)
	dexs, err := s.getSampleDexs(apkPath)
	if err != nil {
		return "", err
	}
	for _, cd := range dexs {
		d := cd.mgr.Dex()
		n := int(d.NumClasses())
		for i := 0; i < n; i++ {
			cls := d.GetClassByIdx(uint32(i))
			if cls.Fullname() != dalvik {
				continue
			}
			lines := smali.Class(d, cls)
			out := strings.Join(lines, "\n") + "\n"
			if len(out) > maxSmaliBytes {
				out = out[:maxSmaliBytes] + "\n...[截断,超大类]\n"
			}
			return out, nil
		}
	}
	return "", fmt.Errorf("class %s not found", class)
}

// Manifest 输出 AndroidManifest.xml(原生 Go AXML 解码)。
func (s *Service) Manifest(ctx context.Context, sampleID string) (string, error) {
	sample, err := s.GetSample(sampleID)
	if err != nil {
		return "", err
	}
	return s.manifestXML(sample.Workspace)
}

func (s *Service) manifestXML(apkPath string) (string, error) {
	r, err := zipprobe.Open(apkPath)
	if err != nil {
		return "", err
	}
	defer r.Close()
	raw, err := zipprobe.ReadFile(r, "AndroidManifest.xml")
	if err != nil {
		return "", fmt.Errorf("read manifest: %w", err)
	}
	root, err := axml.Decode(raw)
	if err != nil {
		return "", fmt.Errorf("decode manifest: %w", err)
	}
	return `<?xml version="1.0" encoding="utf-8"?>` + "\n" + root.XML(), nil
}

// ManifestProfile manifest 结构化画像:包名/版本/权限/组件清单。
func (s *Service) ManifestProfile(ctx context.Context, sampleID string) (map[string]any, error) {
	sample, err := s.GetSample(sampleID)
	if err != nil {
		return nil, err
	}
	r, err := zipprobe.Open(sample.Workspace)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	raw, err := zipprobe.ReadFile(r, "AndroidManifest.xml")
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	root, err := axml.Decode(raw)
	if err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	profile := map[string]any{}
	if v, ok := root.Attr("package"); ok {
		profile["package_name"] = v
	}
	if v, ok := root.Attr("versionName"); ok {
		profile["version_name"] = v
	}
	perms := []string{}
	for _, el := range root.FindAll("uses-permission") {
		if v, ok := el.Attr("name"); ok {
			perms = append(perms, v)
		}
	}
	profile["permissions"] = perms
	for _, kind := range []string{"activity", "service", "receiver", "provider"} {
		var names []string
		for _, el := range root.FindAll(kind) {
			if v, ok := el.Attr("name"); ok {
				names = append(names, v)
			}
		}
		profile[kind+"s"] = names
	}
	// 回填样本表
	if pkg, ok := profile["package_name"].(string); ok && pkg != "" {
		if _, err := s.db.Exec(`UPDATE apk_samples SET package_name = $2, version_name = COALESCE(NULLIF($3,''), version_name) WHERE id = $1`,
			sample.ID, pkg, profile["version_name"]); err != nil {
			s.log.Warn("样本表包名回填失败", zap.String("sampleId", sample.ID), zap.Error(err))
		}
	}
	return profile, nil
}

// ManifestSecurityProfile manifest 安全要点摘要,透出到 android_analyze 工具
// (导出组件/deeplink scheme/危险标志),免 Agent 手写 AXML 二进制解析。
func (s *Service) ManifestSecurityProfile(ctx context.Context, sampleID string) (map[string]any, error) {
	sample, err := s.GetSample(sampleID)
	if err != nil {
		return nil, err
	}
	r, err := zipprobe.Open(sample.Workspace)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	raw, err := zipprobe.ReadFile(r, "AndroidManifest.xml")
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	root, err := axml.Decode(raw)
	if err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	return manifestSecurityDigest(root), nil
}

// manifestSecurityDigest 从解码后的 manifest 树提取安全要点(纯函数,可单测)。
// 属性名在 AXML 字符串池中不带 android: 前缀(exported/scheme/...)。
func manifestSecurityDigest(root *axml.Element) map[string]any {
	explicit := map[string][]string{}
	implied := map[string][]string{}
	schemes := map[string]bool{}
	for _, kind := range []string{"activity", "activity-alias", "service", "receiver", "provider"} {
		for _, el := range root.FindAll(kind) {
			name, _ := el.Attr("name")
			if name == "" {
				continue
			}
			exp, has := el.Attr("exported")
			if exp == "true" {
				explicit[kind] = append(explicit[kind], name)
			} else if !has && len(el.FindAll("intent-filter")) > 0 && kind != "provider" {
				// targetSdk<31:带 intent-filter 且未显式声明 exported 的组件默认导出
				implied[kind] = append(implied[kind], name)
			}
			for _, f := range el.FindAll("intent-filter") {
				for _, d := range f.FindAll("data") {
					if sch, ok := d.Attr("scheme"); ok && sch != "" {
						schemes[sch] = true
					}
				}
			}
		}
	}
	flags := map[string]bool{}
	if apps := root.FindAll("application"); len(apps) > 0 {
		for _, flag := range []string{"debuggable", "allowBackup", "usesClefindutTraffic"} {
			if v, ok := apps[0].Attr(flag); ok && v == "true" {
				flags[flag] = true
			}
		}
	}
	schemeList := make([]string, 0, len(schemes))
	for sch := range schemes {
		schemeList = append(schemeList, sch)
	}
	sort.Strings(schemeList)
	return map[string]any{
		"explicit_exported":   explicit,
		"implicitly_exported": implied,
		"deeplink_schemes":    schemeList,
		"app_flags":           flags,
	}
}

// fileExists 判断路径存在且非目录。
func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}
