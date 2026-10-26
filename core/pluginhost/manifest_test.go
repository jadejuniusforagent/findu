package pluginhost

import (
	"errors"
	"strings"
	"testing"
)

func TestParseManifestValid(t *testing.T) {
	data := []byte(`{
		"id": "intel",
		"name": "威胁情报",
		"version": "1.2.3",
		"kind": "dataset",
		"api_version": "1.0",
		"description": "指纹与漏洞库",
		"requires": {"api": "1.0", "plugins": ["c2"], "binaries": ["nmap"]},
		"provides": {"tools": ["fingerprint"], "events": ["intel.updated"]},
		"owns_tables": ["vuln_kb", "fingerprint_rules"],
		"migrations": {"dir": "migrations/intel", "dialect": "postgres"},
		"permissions": ["db:write"],
		"lifecycle": {"health": "intel-health"},
		"compat": {"min_core": "p8.9", "adapters": ["native"]}
	}`)
	m, err := ParseManifest(data)
	if err != nil {
		t.Fatalf("合法清单解析失败: %v", err)
	}
	if m.ID != "intel" || m.Kind != KindDataset || m.Provides == nil {
		t.Fatalf("解析结果不对: %+v", m)
	}
	if len(m.Requires.Plugins) != 1 || m.Requires.Plugins[0] != "c2" {
		t.Fatalf("requires.plugins 解析不对: %+v", m.Requires)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate 对合法清单报错: %v", err)
	}
}

func TestParseManifestRejects(t *testing.T) {
	valid := `"id":"intel","version":"1.0.0","kind":"dataset","api_version":"1.0","provides":{}`
	cases := []struct {
		name string
		json string
		want string
	}{
		{"未知字段", `{` + valid + `,"extra":1}`, "unknown field"},
		{"缺 provides", `{"id":"intel","version":"1.0.0","kind":"dataset","api_version":"1.0"}`, "provides"},
		{"id 大写", `{"id":"Intel","version":"1.0.0","kind":"dataset","api_version":"1.0","provides":{}}`, "id"},
		{"id 太短", `{"id":"a","version":"1.0.0","kind":"dataset","api_version":"1.0","provides":{}}`, "id"},
		{"version 两段", `{"id":"intel","version":"1.0","kind":"dataset","api_version":"1.0","provides":{}}`, "version"},
		{"kind 越界", `{"id":"intel","version":"1.0.0","kind":"crawler","api_version":"1.0","provides":{}}`, "kind"},
		{"api 三段", `{"id":"intel","version":"1.0.0","kind":"dataset","api_version":"1.0.0","provides":{}}`, "api_version"},
		{"自依赖", `{"id":"intel","version":"1.0.0","kind":"dataset","api_version":"1.0","provides":{},"requires":{"plugins":["intel"]}}`, "自己"},
		{"依赖 id 非法", `{"id":"intel","version":"1.0.0","kind":"dataset","api_version":"1.0","provides":{},"requires":{"plugins":["C2"]}}`, "requires.plugins"},
		{"表名非法", `{"id":"intel","version":"1.0.0","kind":"dataset","api_version":"1.0","provides":{},"owns_tables":["Bad-Table"]}`, "owns_tables"},
		{"表重复", `{"id":"intel","version":"1.0.0","kind":"dataset","api_version":"1.0","provides":{},"owns_tables":["vuln_kb","vuln_kb"]}`, "重复"},
		{"方言越界", `{"id":"intel","version":"1.0.0","kind":"dataset","api_version":"1.0","provides":{},"migrations":{"dialect":"mysql"}}`, "dialect"},
		{"尾随内容", `{` + valid + `} {}`, "多余内容"},
		{"非对象", `[]`, "解析失败"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseManifest([]byte(c.json))
			if err == nil {
				t.Fatalf("应当拒绝，实际通过")
			}
			if !errors.Is(err, ErrInvalidManifest) {
				t.Fatalf("错误未包装 ErrInvalidManifest: %v", err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("错误信息 %q 不含 %q", err.Error(), c.want)
			}
		})
	}
}

func TestParseManifestAllowsShortID(t *testing.T) {
	// c2 是两字符插件 id，schema 允许 2..64 字符（原 {2,63} 会误杀 c2）。
	m, err := ParseManifest([]byte(`{"id":"c2","version":"1.0.0","kind":"provider-c2","api_version":"1.0","provides":{},"owns_tables":["c2_events"]}`))
	if err != nil {
		t.Fatalf("c2 应合法: %v", err)
	}
	if m.ID != "c2" || m.Kind != KindProviderC2 {
		t.Fatalf("解析结果不对: %+v", m)
	}
}

func TestLoadManifestMissingFile(t *testing.T) {
	_, err := LoadManifest(t.TempDir() + "/nope.json")
	if !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("缺文件应包装 ErrInvalidManifest，得到 %v", err)
	}
}
