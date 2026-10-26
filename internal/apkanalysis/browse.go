package apkanalysis

// browse — 代码工作台后端:样本级 DEX 内存缓存、类枚举(树/搜索)、
// 类的方法与字段清单。所有读取走缓存后的 findrefs.Manager(即 DEX 解析
// 一次,后续请求零 inflate)。

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"finduai/internal/apkengine/dex"
	"finduai/internal/apkengine/findrefs"
	"finduai/internal/apkengine/zipprobe"
)

// ────────────────────────── DEX 缓存 ──────────────────────────

const (
	// dexCacheMaxBytes 缓存字节上限(按解压后 DEX 体积计)。
	dexCacheMaxBytes = 512 << 20
	// dexCacheMaxSamples 缓存样本数上限。
	dexCacheMaxSamples = 4
)

type cachedDex struct {
	name string
	mgr  *findrefs.Manager
}

type cachedSample struct {
	apkPath  string
	dexs     []*cachedDex
	bytes    int64
	lastUsed time.Time
}

type dexCache struct {
	mu      sync.Mutex
	entries map[string]*cachedSample // key: apkPath
	loading map[string]*sync.Once
	cur     int64
}

var sampleDexCache = &dexCache{entries: map[string]*cachedSample{}, loading: map[string]*sync.Once{}}

// getSampleDexs 取样本全部逻辑 DEX 的 Manager(缓存命中或加载)。
func (s *Service) getSampleDexs(apkPath string) ([]*cachedDex, error) {
	for {
		sampleDexCache.mu.Lock()
		if e, ok := sampleDexCache.entries[apkPath]; ok {
			e.lastUsed = time.Now()
			sampleDexCache.mu.Unlock()
			return e.dexs, nil
		}
		once, loading := sampleDexCache.loading[apkPath]
		if !loading {
			once = &sync.Once{}
			sampleDexCache.loading[apkPath] = once
		}
		sampleDexCache.mu.Unlock()

		var loaded []*cachedDex
		var loadErr error
		once.Do(func() { loaded, loadErr = s.loadSampleDexs(apkPath) })

		sampleDexCache.mu.Lock()
		if loadErr != nil {
			delete(sampleDexCache.loading, apkPath)
			sampleDexCache.mu.Unlock()
			return nil, loadErr
		}
		sampleDexCache.entries[apkPath] = &cachedSample{apkPath: apkPath, dexs: loaded, lastUsed: time.Now()}
		for _, d := range loaded {
			sampleDexCache.cur += int64(len(d.mgr.Dex().Buf()))
		}
		delete(sampleDexCache.loading, apkPath)
		sampleDexCache.evictLocked()
		sampleDexCache.mu.Unlock()
		return loaded, nil
	}
}

func (c *dexCache) evictLocked() {
	for c.cur > dexCacheMaxBytes || len(c.entries) > dexCacheMaxSamples {
		if len(c.entries) == 0 {
			return
		}
		var oldestKey string
		var oldest time.Time
		for k, e := range c.entries {
			if oldestKey == "" || e.lastUsed.Before(oldest) {
				oldestKey, oldest = k, e.lastUsed
			}
		}
		e := c.entries[oldestKey]
		for _, d := range e.dexs {
			c.cur -= int64(len(d.mgr.Dex().Buf()))
		}
		delete(c.entries, oldestKey)
	}
}

// loadSampleDexs 打开 APK 并解析全部逻辑 DEX(zip 容忍坏条目,坏 DEX 跳过)。
func (s *Service) loadSampleDexs(apkPath string) ([]*cachedDex, error) {
	r, err := zipprobe.Open(apkPath)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	entries, err := zipprobe.ParseCDDexEntries(r)
	if err != nil {
		return nil, err
	}
	zipprobe.SortByCompSize(entries)
	out := []*cachedDex{}
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
			out = append(out, &cachedDex{name: ld.Name, mgr: findrefs.NewManager(d)})
		}
	}
	return out, nil
}

// ────────────────────────── 类枚举 ──────────────────────────

// ClassEntry 类清单条目。
type ClassEntry struct {
	Name string `json:"name"` // 点号形态 com.foo.Bar
	Dex  string `json:"dex"`
}

// ClassListing 类浏览结果:tree 模式返回直接子包 + 前缀下的直接类;
// list 模式返回前缀(可含关键词)下的全部类(分页)。
type ClassListing struct {
	Prefix   string       `json:"prefix"`
	Packages []string     `json:"packages,omitempty"`
	Classes  []ClassEntry `json:"classes"`
	Total    int          `json:"total"`
}

// ListClasses 类浏览(树形一级展开或前缀搜索)。
func (s *Service) ListClasses(sampleID, prefix, keyword string, offset, limit int) (*ClassListing, error) {
	sample, err := s.GetSample(sampleID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 2000 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}
	// 归一:点号形态 → "com.foo." 前缀(允许 Lcom/foo/ 描述符形态)
	prefix = normalizeBrowsePrefix(prefix)

	dexs, err := s.getSampleDexs(sample.Workspace)
	if err != nil {
		return nil, err
	}
	seen := map[string]ClassEntry{}
	pkgSet := map[string]bool{}
	keyword = strings.ToLower(keyword)

	for _, cd := range dexs {
		d := cd.mgr.Dex()
		n := int(d.NumClasses())
		for i := 0; i < n; i++ {
			cls := d.GetClassByIdx(uint32(i))
			name := cls.Fullname()
			dot := dotClassName(name)
			if !strings.HasPrefix(dot, prefix) {
				continue
			}
			rest := dot[len(prefix):]
			switch {
			case rest == "":
				// 类名恰好等于前缀
			case strings.HasPrefix(rest, "."):
				inner := rest[1:]
				if idx := strings.Index(inner, "."); idx >= 0 {
					pkgSet[inner[:idx]] = true // 子包 → tree 展开
					continue
				}
			default:
				if idx := strings.Index(rest, "."); idx >= 0 {
					pkgSet[rest[:idx]] = true
					continue
				}
			}
			if keyword != "" && !strings.Contains(strings.ToLower(dot), keyword) {
				continue
			}
			if _, dup := seen[dot]; dup {
				continue
			}
			seen[dot] = ClassEntry{Name: dot, Dex: cd.name}
		}
	}

	out := &ClassListing{Prefix: prefix, Classes: []ClassEntry{}}
	if keyword == "" {
		for p := range pkgSet {
			out.Packages = append(out.Packages, p)
		}
		sort.Strings(out.Packages)
	} else {
		// 搜索模式:子包匹配关键词的直接类也纳入(包名整体匹配已含)
	}
	for _, e := range seen {
		out.Classes = append(out.Classes, e)
	}
	sort.Slice(out.Classes, func(i, j int) bool { return out.Classes[i].Name < out.Classes[j].Name })
	out.Total = len(out.Classes)
	if offset < len(out.Classes) {
		out.Classes = out.Classes[offset:]
		if len(out.Classes) > limit {
			out.Classes = out.Classes[:limit]
		}
	} else {
		out.Classes = []ClassEntry{}
	}
	return out, nil
}

// normalizeBrowsePrefix 类浏览前缀归一:空 → "";描述符/斜杠形态 → 点号形态。
// 不强制尾点:顶级类(ByteStreamReader)与包前缀(com.foo)同为合法输入。
func normalizeBrowsePrefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return ""
	}
	if strings.HasPrefix(prefix, "L") && strings.HasSuffix(prefix, ";") {
		prefix = prefix[1 : len(prefix)-1]
	}
	return strings.ReplaceAll(prefix, "/", ".")
}

// ────────────────────────── 类详情(方法/字段大纲) ──────────────────────────

// MethodOutline 方法大纲条目。
type MethodOutline struct {
	Name    string `json:"name"`
	Proto   string `json:"proto"`    // (params)return
	Access  string `json:"access"`   // public/private/static/synchronized… 简化拼接
	HasCode bool   `json:"has_code"` // 有方法体(abstract/native 为 false)
	CodeOff uint32 `json:"code_off"`
}

// FieldOutline 字段大纲条目。
type FieldOutline struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Access string `json:"access"`
}

// ClassOutline 类的方法/字段大纲(源码查看器侧栏数据源)。
type ClassOutline struct {
	Class  string          `json:"class"`
	Dex    string          `json:"dex"`
	Exists bool            `json:"exists"`
	Method []MethodOutline `json:"methods"`
	Field  []FieldOutline  `json:"fields"`
}

// GetClassOutline 输出指定类的方法/字段清单(跨 DEX 找定义处)。
func (s *Service) GetClassOutline(sampleID, class string) (*ClassOutline, error) {
	sample, err := s.GetSample(sampleID)
	if err != nil {
		return nil, err
	}
	dalvik := normalizeClassQuery(class, false)
	dexs, err := s.getSampleDexs(sample.Workspace)
	if err != nil {
		return nil, err
	}
	out := &ClassOutline{Class: dalvik, Exists: false, Method: []MethodOutline{}, Field: []FieldOutline{}}
	for _, cd := range dexs {
		d := cd.mgr.Dex()
		n := int(d.NumClasses())
		for i := 0; i < n; i++ {
			cls := d.GetClassByIdx(uint32(i))
			if cls.Fullname() != dalvik {
				continue
			}
			out.Exists = true
			out.Dex = cd.name
			for _, m := range cls.Methods() {
				out.Method = append(out.Method, MethodOutline{
					Name:    m.Name(),
					Proto:   renderProto(m),
					Access:  accessNames(m.AccessFlags),
					HasCode: m.HasCode(),
					CodeOff: m.CodeOffset(),
				})
			}
			for _, f := range cls.Fields() {
				out.Field = append(out.Field, FieldOutline{
					Name:   f.Name(),
					Type:   f.Type().Descriptor,
					Access: accessNames(f.AccessFlags),
				})
			}
			return out, nil
		}
	}
	return out, nil
}

// renderProto 渲染 (p1,p2)ret(类型点号化,可读性优先)。
func renderProto(m *dex.Method) string {
	p := m.Prototype()
	if p == nil {
		return "()?"
	}
	params := p.Parameters()
	names := make([]string, 0, len(params))
	for _, pt := range params {
		names = append(names, dotClassName(pt.Descriptor))
	}
	return "(" + strings.Join(names, ",") + ")" + dotClassName(p.ReturnType().Descriptor)
}

// accessNames access flags → 可读串(常见位;顺序稳定)。
func accessNames(flags uint32) string {
	var parts []string
	bits := []struct {
		v    uint32
		name string
	}{
		{0x1, "public"}, {0x2, "private"}, {0x4, "protected"}, {0x8, "static"},
		{0x10, "final"}, {0x20, "synchronized"}, {0x100, "interface"}, {0x400, "abstract"},
		{0x800, "strict"}, {0x1000, "synthetic"}, {0x40, "enum"}, {0x80, "annotation"},
	}
	for _, b := range bits {
		if flags&b.v != 0 {
			parts = append(parts, b.name)
		}
	}
	return strings.Join(parts, " ")
}

// LocateClassDexCached 定位类所在 DEX(缓存路径,供 CodeTab 打开时展示)。
func (s *Service) LocateClassDexCached(sampleID, class string) (string, error) {
	sample, err := s.GetSample(sampleID)
	if err != nil {
		return "", err
	}
	dalvik := normalizeClassQuery(class, false)
	dexs, err := s.getSampleDexs(sample.Workspace)
	if err != nil {
		return "", err
	}
	for _, cd := range dexs {
		d := cd.mgr.Dex()
		n := int(d.NumClasses())
		for i := 0; i < n; i++ {
			if d.GetClassByIdx(uint32(i)).Fullname() == dalvik {
				return cd.name, nil
			}
		}
	}
	return "", fmt.Errorf("class %s not found", class)
}

// ────────────────────────── Smali 兜底视图 ──────────────────────────

// maxSmaliBytes smali 视图输出上限(类异常大时截断)。
const maxSmaliBytes = 256 * 1024

// GetClassSmali 输出类的 smali 风格字节码(纯 Go,反编译链未部署时可用)。
func (s *Service) GetClassSmali(sampleID, class string) (string, error) {
	sample, err := s.GetSample(sampleID)
	if err != nil {
		return "", err
	}
	return s.smaliForApkPath(sample.Workspace, class)
}

// GetClassSmaliStandard 输出标准 smali(可被 smali.jar 汇编)。
func (s *Service) GetClassSmaliStandard(sampleID, class string) (string, error) {
	sample, err := s.GetSample(sampleID)
	if err != nil {
		return "", err
	}
	return s.smaliStandardForApkPath(sample.Workspace, class)
}
