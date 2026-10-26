package apkanalysis

// report_scan 单测:纯函数与 fixture 路径,不依赖 DB。
// fixture APK 来自 findrefs/testdata(与引擎差分测试同源)。

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"
)

func TestMatchSecretRule(t *testing.T) {
	cases := []struct {
		input string
		want  string // 期望命中的规则名,空 = 不应命中
	}{
		{"AKIAIOSFODNN7EXAMPLE", "AWS AccessKey"},
		{"ASIAABCDEFGHIJKLMNOP", "AWS AccessKey"},
		{"LTAI5tABCdefGHIjklMn", "阿里云 AccessKey"},
		{"ghp_1234567890abcdefghijklmnopqrstuvwxyz", "GitHub Token"},
		{"xoxb-123456789-XXXXXXXXXXXX", "Slack Token"},
		{"AIzaSyD-1234567890abcdefghijklmnopqrstu", "GCP API Key"},
		{"-----BEGIN RSA PRIVATE KEY-----", "私钥/证书 PEM"},
		{"sk-proj-abcdefghij1234567890ABCDEFGH", "OpenAI/LLM Key"},
		{"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U", "JWT"},
		{"my-app-abc123.firebaseio.com", "Firebase 数据库"},
		{"hello world", ""},
		{"password123", ""},
		{"LTAI_short", ""},
	}
	for _, c := range cases {
		ri, ok := matchSecretRule(c.input)
		if c.want == "" {
			if ok {
				t.Errorf("%q: unexpected hit rule %s", c.input, secretRules[ri].name)
			}
			continue
		}
		if !ok {
			t.Errorf("%q: expected rule %s, no hit", c.input, c.want)
			continue
		}
		if got := secretRules[ri].name; got != c.want {
			t.Errorf("%q: rule = %s, want %s", c.input, got, c.want)
		}
	}
}

func TestScanPackerEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "packed.apk")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for _, name := range []string{
		"AndroidManifest.xml",
		"classes.dex",
		"lib/armeabi-v7a/libjiagu.so",
		"lib/arm64-v8a/libjiagu_64.so",
		"assets/ijiami.dat",
		"lib/armeabi-v7a/libnonexistent.so",
	} {
		fw, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte("junk")); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	hit, err := scanPackerEntries(path)
	if err != nil {
		t.Fatalf("scanPackerEntries: %v", err)
	}
	if _, ok := hit["360 加固"]; !ok {
		t.Errorf("expected 360 加固 hit, got %v", hit)
	}
	if _, ok := hit["爱加密"]; !ok {
		t.Errorf("expected 爱加密 hit (ijiami.dat), got %v", hit)
	}
	if len(hit) != 2 {
		t.Errorf("unexpected extra products: %v", hit)
	}
}

func TestScanDexContentFixture(t *testing.T) {
	apk := "../apkengine/findrefs/testdata/ByteStreamReader.apk"
	res, err := scanDexContent(context.Background(), zap.NewNop(), apk, nil)
	if err != nil {
		t.Fatalf("scanDexContent: %v", err)
	}
	// 预设项齐全在最前(凭据规则仅命中才追加;排序由报告组装层 sortFindings 负责)
	if len(res.findings) < len(autoPresets()) {
		t.Fatalf("findings = %d, want >= %d", len(res.findings), len(autoPresets()))
	}
	for i, f := range res.findings[:len(autoPresets())] {
		if f.Category == "硬编码凭据" {
			t.Errorf("findings[%d] 应为预设项,得到 %s", i, f.Name)
		}
	}
	// 命中的凭据规则项(若有)应追加在预设段之后
	for _, f := range res.findings[len(autoPresets()):] {
		if f.Category != "硬编码凭据" {
			t.Errorf("追加段出现非凭据项: %s", f.Name)
		}
	}
}

func TestHostOfAndTopAggregation(t *testing.T) {
	if h := hostOf("https://api.example.com/v1/login?x=1"); h != "api.example.com" {
		t.Errorf("hostOf = %q", h)
	}
	if h := hostOf("not a url"); h != "" {
		t.Errorf("hostOf junk = %q", h)
	}
	urls := map[string]int{
		"https://b.com/x": 1,
		"https://a.com/y": 5,
		"https://a.com/z": 2,
	}
	top := topEndpoints(urls, 10)
	if len(top) != 3 {
		t.Fatalf("topEndpoints len = %d", len(top))
	}
	if top[0].URL != "https://a.com/y" || top[0].Hits != 5 {
		t.Errorf("top[0] = %+v", top[0])
	}
	limited := topEndpoints(urls, 2)
	if len(limited) != 2 {
		t.Errorf("limit not applied: %d", len(limited))
	}
}
