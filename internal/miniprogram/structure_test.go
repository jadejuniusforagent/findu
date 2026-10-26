package miniprogram

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 构造一个含全要素声明的小程序工作区,验证结构报告:路由/分包/插件/worker/
// wasm/开放数据域/web-view 静态域名与动态绑定。
func TestAnalyzeStructure(t *testing.T) {
	ws := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(ws, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("app.json", `{
		"appid": "wx1234abcdef567890",
		"pages": ["pages/index/index", "pages/login/login"],
		"subpackages": [{"root": "pkgA", "pages": ["pages/a/a", "pages/b/b"]}],
		"plugins": {"wechatpay": {"provider": "wxaaa111111111111", "version": "1.2.3"}},
		"workers": "workers",
		"requiredBackgroundModes": ["audio"]
	}`)
	write("pkgA/index.json", `{"plugins": {"subPlugin": {"provider": "wxbbb222222222222", "version": "0.9"}}}`)
	write("game.json", `{"openDataContext": "opendata"}`)
	write("workers/parse.js", `onmessage = function(){}`)
	write("libs/crypto.wasm", string([]byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00}))
	write("pages/web/web.wxml", `<web-view src="https://h5.example.com/invite?x=1"></web-view>`)
	write("pages/dyn/dyn.wxml", `<web-view src="{{dynamicUrl}}"></web-view>`)

	r := AnalyzeStructure(ws)
	if r.AppID != "wx1234abcdef567890" {
		t.Fatalf("appid = %q", r.AppID)
	}
	if len(r.Pages) != 2 {
		t.Fatalf("pages = %+v", r.Pages)
	}
	if len(r.Subpackages) != 1 || r.Subpackages[0].Root != "pkgA" || r.Subpackages[0].PageCount != 2 {
		t.Fatalf("subpackages = %+v", r.Subpackages)
	}
	if len(r.Plugins) != 2 {
		t.Fatalf("插件应命中 2 个(主包+分包),得到 %+v", r.Plugins)
	}
	providers := map[string]bool{}
	for _, p := range r.Plugins {
		providers[p.Provider] = true
	}
	if !providers["wxaaa111111111111"] || !providers["wxbbb222222222222"] {
		t.Fatalf("插件 provider 缺失: %+v", providers)
	}
	if r.Workers != "workers" {
		t.Fatalf("workers = %q", r.Workers)
	}
	if r.OpenDataContext != "opendata" {
		t.Fatalf("openDataContext = %q", r.OpenDataContext)
	}
	if len(r.WasmFiles) != 1 || !strings.HasSuffix(r.WasmFiles[0], "crypto.wasm") {
		t.Fatalf("wasm = %+v", r.WasmFiles)
	}
	if len(r.WebViewHosts) != 1 || r.WebViewHosts[0] != "h5.example.com" {
		t.Fatalf("webview hosts = %+v", r.WebViewHosts)
	}
	if len(r.WebViewDynamic) != 1 || !strings.HasSuffix(r.WebViewDynamic[0], "dyn.wxml") {
		t.Fatalf("webview dynamic = %+v", r.WebViewDynamic)
	}
	if len(r.RequiredBackgroundModes) != 1 || r.RequiredBackgroundModes[0] != "audio" {
		t.Fatalf("background modes = %+v", r.RequiredBackgroundModes)
	}
	// WASM 存在必须有覆盖边界提示
	foundNote := false
	for _, n := range r.Notes {
		if strings.Contains(n, "WASM") {
			foundNote = true
		}
	}
	if !foundNote {
		t.Fatalf("应有 WASM 覆盖提示: %+v", r.Notes)
	}
}

func TestAnalyzeStructureEmpty(t *testing.T) {
	r := AnalyzeStructure(t.TempDir())
	if len(r.Pages) != 0 || len(r.Plugins) != 0 {
		t.Fatalf("空目录应得空报告: %+v", r)
	}
	if r.AssetCounts == nil {
		t.Fatal("AssetCounts 不应为 nil")
	}
}

func TestRecordWebViewSrc(t *testing.T) {
	r := &StructureReport{AssetCounts: map[string]int{}}
	seenHost, seenDyn := map[string]bool{}, map[string]bool{}
	recordWebViewSrc(r, "a.wxml", "https://H5.Example.com/x", seenHost, seenDyn)
	recordWebViewSrc(r, "b.wxml", "https://h5.example.com/y", seenHost, seenDyn) // 同域名去重
	recordWebViewSrc(r, "c.wxml", "//cdn.example.com/x", seenHost, seenDyn)
	recordWebViewSrc(r, "d.wxml", "{{url}}", seenHost, seenDyn)
	recordWebViewSrc(r, "e.wxml", "", seenHost, seenDyn)
	if len(r.WebViewHosts) != 2 {
		t.Fatalf("hosts = %+v", r.WebViewHosts)
	}
	if len(r.WebViewDynamic) != 1 {
		t.Fatalf("dynamic = %+v", r.WebViewDynamic)
	}
}

// JSON 串行化形态检查(工具输出契约)。
func TestStructureReportJSONShape(t *testing.T) {
	b, err := json.Marshal(AnalyzeStructure(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"pages", "subpackages", "plugins", "wasm_files", "webview_hosts", "asset_counts"} {
		if _, ok := doc[key]; !ok {
			t.Fatalf("输出缺字段 %s", key)
		}
	}
}
