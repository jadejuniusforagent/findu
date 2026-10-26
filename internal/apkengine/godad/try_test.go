package godad

// try_test — try/catch 结构化测试:异常表解析、catch 子句渲染、
// move-exception 参数命名、旧 unreachable 注释消除。

import (
	"path/filepath"
	"strings"
	"testing"

	"finduai/internal/apkengine/dex"
	"finduai/internal/apkengine/zipprobe"
)

// fixtureClassPair DEX 与其类(反编译需要 DEX 上下文)。
type fixtureClassPair struct {
	d   *dex.DEX
	cls *dex.Class
}

// allFixtureClasses 遍历全部 fixture APK 的全部类。
func allFixtureClasses(t *testing.T) []fixtureClassPair {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join("..", "findrefs", "testdata", "*.apk"))
	if err != nil || len(matches) == 0 {
		t.Skip("no fixtures")
	}
	var out []fixtureClassPair
	for _, apkPath := range matches {
		r, err := zipprobe.Open(apkPath)
		if err != nil {
			t.Fatal(err)
		}
		entries, _ := zipprobe.ParseCDDexEntries(r)
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
				for i := 0; i < int(d.NumClasses()); i++ {
					out = append(out, fixtureClassPair{d: d, cls: d.GetClassByIdx(uint32(i))})
				}
			}
		}
		r.Close()
	}
	return out
}

// TestExceptionTableParsed fixture 中至少一个方法带异常表,且解析出
// try 区间与 handler(类型或 catch-all)。
func TestExceptionTableParsed(t *testing.T) {
	found := 0
	for _, p := range allFixtureClasses(t) {
		for _, m := range p.cls.Methods() {
			et := m.ExceptionTable()
			if et == nil {
				continue
			}
			found++
			if len(et.Tries) == 0 {
				t.Fatalf("%s.%s: empty tries", p.cls.Fullname(), m.Name())
			}
			hasHandler := false
			for _, ti := range et.Tries {
				if h := et.Handler(ti.HandlerOff); h != nil && (len(h.Typed) > 0 || h.CatchAllAddr >= 0) {
					hasHandler = true
				}
			}
			if !hasHandler {
				t.Errorf("%s.%s: no handler resolved", p.cls.Fullname(), m.Name())
			}
		}
	}
	if found == 0 {
		t.Fatal("no method with exception table found in fixtures")
	}
	t.Logf("%d methods with exception tables", found)
}

// TestTryCatchStructured 含异常表的方法必须产出真实 try/catch 语句,
// 且旧 "unreachable block (exception handler?)" 注释彻底消失。
func TestTryCatchStructured(t *testing.T) {
	withTable, withTry := 0, 0
	for _, p := range allFixtureClasses(t) {
		src := New(p.d).DecompileClass(p.cls)
		if strings.Contains(src, "aborted") {
			t.Errorf("%s: decompilation aborted:\n%s", p.cls.Fullname(), src)
			continue
		}
		if strings.Contains(src, "exception handler?") {
			t.Errorf("%s: legacy unreachable-handler comment leaked:\n%s", p.cls.Fullname(), src)
		}
		hasTable := false
		for _, m := range p.cls.Methods() {
			if m.ExceptionTable() != nil {
				hasTable = true
				break
			}
		}
		if hasTable {
			withTable++
			if strings.Contains(src, "try {") && strings.Contains(src, "catch (") {
				withTry++
			} else {
				t.Errorf("%s: exception table but no structured try/catch:\n%s", p.cls.Fullname(), src)
			}
		}
	}
	if withTable == 0 {
		t.Fatal("no fixture class with exception table")
	}
	if withTry != withTable {
		t.Errorf("structured try/catch coverage: %d/%d classes with tables", withTry, withTable)
	}
	t.Logf("%d/%d classes render structured try/catch", withTry, withTable)
}
