package findrefs

// findrefs 性能基准 —— 对齐文档 §3.6 L0 验收:
//   "352MB APK 全局字符串搜索 < 3s" / "内存 < 200MB"
//
// 小 fixture 基准(CI 常跑):回归门禁,量级不应随改动恶化。
// 大 APK 基准(ANDROID_PERF_APK 指定):真实压测,对照验收阈值。

import (
	"os"
	"path/filepath"
	"testing"

	"finduai/internal/apkengine/dex"
	"finduai/internal/apkengine/zipprobe"
)

// loadManagers 打开 APK,返回全部逻辑 DEX 的 Manager(检索前的一次性解析成本)。
func loadManagers(tb testing.TB, apkPath string) []*Manager {
	tb.Helper()
	r, err := zipprobe.Open(apkPath)
	if err != nil {
		tb.Fatalf("open %s: %v", apkPath, err)
	}
	defer r.Close()
	entries, err := zipprobe.ParseCDDexEntries(r)
	if err != nil {
		tb.Fatalf("cd: %v", err)
	}
	zipprobe.SortByCompSize(entries)
	var out []*Manager
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
			out = append(out, NewManager(d))
		}
	}
	if len(out) == 0 {
		tb.Fatalf("no dex in %s", apkPath)
	}
	return out
}

// BenchmarkFindRefsString_AllFixtures 全 fixture 字符串检索总耗时。
// 单次迭代 = 每个 fixture 全部 DEX 跑一次 string 检索。
func BenchmarkFindRefsString_AllFixtures(b *testing.B) {
	matches, err := filepath.Glob(filepath.Join("testdata", "*.apk"))
	if err != nil || len(matches) == 0 {
		b.Skip("no fixtures")
	}
	all := map[string][]*Manager{}
	for _, apk := range matches {
		all[apk] = loadManagers(b, apk)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, mgrs := range all {
			for _, m := range mgrs {
				m.FindRef(&Query{String: "data"})
			}
		}
	}
}

// BenchmarkFindRefsType_AllFixtures 全 fixture 类型检索总耗时。
func BenchmarkFindRefsType_AllFixtures(b *testing.B) {
	matches, err := filepath.Glob(filepath.Join("testdata", "*.apk"))
	if err != nil || len(matches) == 0 {
		b.Skip("no fixtures")
	}
	all := map[string][]*Manager{}
	for _, apk := range matches {
		all[apk] = loadManagers(b, apk)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, mgrs := range all {
			for _, m := range mgrs {
				m.FindRef(&Query{Type: "java/io"})
			}
		}
	}
}

// BenchmarkFindRefsMethod_AllFixtures 全 fixture 方法检索总耗时。
func BenchmarkFindRefsMethod_AllFixtures(b *testing.B) {
	matches, err := filepath.Glob(filepath.Join("testdata", "*.apk"))
	if err != nil || len(matches) == 0 {
		b.Skip("no fixtures")
	}
	all := map[string][]*Manager{}
	for _, apk := range matches {
		all[apk] = loadManagers(b, apk)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, mgrs := range all {
			for _, m := range mgrs {
				m.FindRef(&Query{Method: &MemberFind{Name: "read"}})
			}
		}
	}
}

// BenchmarkParseInflateLargeAPK 大 APK 全量解析基准(ANDROID_PERF_APK 指定)。
func BenchmarkParseInflateLargeAPK(b *testing.B) {
	apk := os.Getenv("ANDROID_PERF_APK")
	if apk == "" {
		b.Skip("set ANDROID_PERF_APK=/path/to/large.apk to run large-APK benchmark")
	}
	r, err := zipprobe.Open(apk)
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	defer r.Close()
	entries, err := zipprobe.ParseCDDexEntries(r)
	if err != nil {
		b.Fatalf("cd: %v", err)
	}
	zipprobe.SortByCompSize(entries)
	var totalBytes int64
	for _, e := range entries {
		totalBytes += int64(e.UncompSize)
	}
	b.SetBytes(totalBytes)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, e := range entries {
			if _, err := zipprobe.InflateDex(r, e); err != nil {
				b.Fatalf("inflate %s: %v", e.Name, err)
			}
		}
	}
}

// BenchmarkFindRefsString_LargeAPK 大 APK 字符串检索(ANDROID_PERF_APK 指定)。
func BenchmarkFindRefsString_LargeAPK(b *testing.B) {
	apk := os.Getenv("ANDROID_PERF_APK")
	if apk == "" {
		b.Skip("set ANDROID_PERF_APK=/path/to/large.apk to run large-APK benchmark")
	}
	mgrs := loadManagers(b, apk)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, m := range mgrs {
			m.FindRef(&Query{String: "token"})
		}
	}
}
