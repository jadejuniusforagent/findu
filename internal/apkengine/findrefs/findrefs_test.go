package findrefs

// 差分测试:同一批真实 APK(drozer 模块样本,1–3KB)上,Go 引擎与
// Python ASC 产出逐行一致。golden_real.json 由 testdata/gen_golden.py
// 用原版 ASC 生成(一次性,已提交,测试不依赖 Python)。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"finduai/internal/apkengine/dex"
	"finduai/internal/apkengine/internal/fixture"
	"finduai/internal/apkengine/zipprobe"
)

// engineLines 对 AscHandler 消费路径:遍历 APK 的全部逻辑 DEX,聚合行输出。
func engineLines(t *testing.T, apkPath string, q Query) []string {
	t.Helper()
	r, err := zipprobe.Open(apkPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if closer, ok := r.(interface{ Close() error }); ok {
		defer closer.Close()
	}
	entries, err := zipprobe.ParseCDDexEntries(r)
	if err != nil {
		t.Fatalf("cd: %v", err)
	}
	zipprobe.SortByCompSize(entries)
	var lines []string
	for _, e := range entries {
		data, err := zipprobe.InflateDex(r, e)
		if err != nil {
			t.Fatalf("inflate %s: %v", e.Name, err)
		}
		for _, ld := range zipprobe.LogicalDexBuffers(e.Name, data) {
			d, err := dex.Parse(ld.Buf, ld.Name)
			if err != nil {
				t.Fatalf("dex parse %s: %v", ld.Name, err)
			}
			mgr := NewManager(d)
			res := mgr.FindRef(&q)
			for _, row := range mgr.AggregateRows(res) {
				lines = append(lines, row.Line)
			}
		}
	}
	return lines
}

func TestDifferentialAgainstPythonASC(t *testing.T) {
	goldenRaw, err := os.ReadFile(filepath.Join("testdata", "golden_real.json"))
	if err != nil {
		t.Fatalf("golden_real.json: %v", err)
	}
	var golden map[string][]string
	if err := json.Unmarshal(goldenRaw, &golden); err != nil {
		t.Fatalf("golden parse: %v", err)
	}

	queries := map[string]Query{
		"q01_string_read":  {String: "read"},
		"q02_string_value": {String: "value"},
		"q03_string_miss":  {String: "zzz_not_exist"},
		"q04_string_desc":  {String: "Ljava/io/IOException;"},
		"q05_string_short": {String: "a"},
		"q06_type_exact":   {Type: "Ljava/io/InputStream;"},
		"q07_type_fuzzy":   {Type: "Ljava/io/"},
		"q09_type_miss":    {Type: "Lno/such/Type;"},
		"q10_method_name":  {Method: &MemberFind{Name: "read"}},
		"q11_method_short": {Method: &MemberFind{Name: "write"}},
		"q12_method_miss":  {Method: &MemberFind{Name: "zzz"}},
		"q13_field_name":   {Field: &MemberFind{Name: "value"}},
		"q14_field_miss":   {Field: &MemberFind{Name: "zzz"}},
	}

	apks, err := filepath.Glob(filepath.Join("testdata", "*.apk"))
	if err != nil {
		t.Fatal(err)
	}
	if len(apks) < 5 {
		t.Fatalf("test corpus missing: %d apks", len(apks))
	}

	for qid, q := range queries {
		want, ok := golden[qid]
		if !ok {
			t.Fatalf("golden missing query %s", qid)
		}
		var got []string
		for _, apk := range apks {
			for _, line := range engineLines(t, apk, q) {
				got = append(got, filepath.Base(apk)+"::"+line)
			}
		}
		sort.Strings(got)
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("query %s diverged:\n got %d lines, want %d lines\nfirst diff:\n got: %v\nwant: %v",
				qid, len(got), len(want), head(got, 3), head(want, 3))
		}
	}
}

func head(list []string, n int) []string {
	if len(list) > n {
		return list[:n]
	}
	return list
}

// ───────────────────────── 合成夹具单测(手工推演的断言)─────────────────────────

func fixtureDEXManager(t *testing.T) *Manager {
	t.Helper()
	d, err := dex.Parse(fixture.BuildTestDEX(), "classes.dex")
	if err != nil {
		t.Fatal(err)
	}
	return NewManager(d)
}

func TestFixtureStringRef(t *testing.T) {
	mgr := fixtureDEXManager(t)
	res := mgr.FindRef(&Query{String: "secret_token"})
	rows := mgr.AggregateRows(res)
	// main 的 const-string 指向 secret_token
	if len(rows) != 1 || rows[0].Method != "main" || strings.Join(rows[0].Matched, ",") != "secret_token" {
		t.Errorf("rows = %+v", rows)
	}
}

func TestFixtureStringRefMiss(t *testing.T) {
	mgr := fixtureDEXManager(t)
	res := mgr.FindRef(&Query{String: "hello world"})
	if rows := mgr.AggregateRows(res); len(rows) != 0 {
		t.Errorf("hello world 无 const-string 引用,应为空: %+v", rows)
	}
}

func TestFixtureTypeExactAndFuzzy(t *testing.T) {
	mgr := fixtureDEXManager(t)
	res := mgr.FindRef(&Query{Type: "Lcom/poc/Main;"})
	rows := mgr.AggregateRows(res)
	if len(rows) != 1 || rows[0].Method != "main" {
		t.Errorf("exact: %+v", rows)
	}
	res = mgr.FindRef(&Query{Type: "poc"})
	rows = mgr.AggregateRows(res)
	if len(rows) != 1 || rows[0].Method != "main" {
		t.Errorf("fuzzy: %+v", rows)
	}
}

func TestFixtureMethodPaths(t *testing.T) {
	mgr := fixtureDEXManager(t)
	// 无类名,按方法名:main 被 getCount invoke-static 引用
	rows := mgr.AggregateRows(mgr.FindRef(&Query{Method: &MemberFind{Name: "main"}}))
	if len(rows) != 1 || rows[0].Method != "getCount" || strings.Join(rows[0].Matched, ",") != "Lcom/poc/Main;->main" {
		t.Errorf("name only: %+v", rows)
	}
	// 精确类名,无方法名 → 类下全部;指令引用只命中 main(idx 1)
	rows = mgr.AggregateRows(mgr.FindRef(&Query{Method: &MemberFind{Class: "Lcom/poc/Main;", ClassExact: true}}))
	if len(rows) != 1 || rows[0].Method != "getCount" {
		t.Errorf("clz all: %+v", rows)
	}
	// 精确类名 + 子串 "get" → getCount 本身;无指令引用 getCount → 空
	rows = mgr.AggregateRows(mgr.FindRef(&Query{Method: &MemberFind{Class: "Lcom/poc/Main;", ClassExact: true, Name: "get"}}))
	if len(rows) != 0 {
		t.Errorf("clz+get: %+v", rows)
	}
	// 模糊类名 + 方法名
	rows = mgr.AggregateRows(mgr.FindRef(&Query{Method: &MemberFind{Class: "poc", ClassExact: false, Name: "main"}}))
	if len(rows) != 1 || rows[0].Method != "getCount" {
		t.Errorf("fuzzy clz: %+v", rows)
	}
}

func TestFixtureFieldRef(t *testing.T) {
	mgr := fixtureDEXManager(t)
	rows := mgr.AggregateRows(mgr.FindRef(&Query{Field: &MemberFind{Name: "value"}}))
	// getCount 的 sget 引用 value
	if len(rows) != 1 || rows[0].Method != "getCount" || strings.Join(rows[0].Matched, ",") != "Lcom/poc/Main;->value" {
		t.Errorf("rows = %+v", rows)
	}
	// 模糊类名 + 子串
	rows = mgr.AggregateRows(mgr.FindRef(&Query{Field: &MemberFind{Class: "poc", Name: "alu"}}))
	if len(rows) != 1 || rows[0].Method != "getCount" {
		t.Errorf("fuzzy: %+v", rows)
	}
}

func TestFixtureMultiDimQuery(t *testing.T) {
	mgr := fixtureDEXManager(t)
	// 字符串 + 类型双维度同时检索
	res := mgr.FindRef(&Query{String: "secret_token", Type: "Lcom/poc/Main;"})
	rows := mgr.AggregateRows(res)
	// 两个维度的命中都归位到 main,聚合为一行,命中项按索引排序
	if len(rows) != 1 || rows[0].Method != "main" || len(rows[0].Matched) != 2 {
		t.Errorf("rows = %+v", rows)
	}
}
