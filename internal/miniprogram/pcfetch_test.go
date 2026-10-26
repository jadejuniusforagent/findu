package miniprogram

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 按微信 4.x radium / 3.x Applet 两种真实布局构造假缓存目录,验证扫描聚合与加密标记。
func TestScanPCPackages(t *testing.T) {
	root := t.TempDir()

	// 4.x radium: AppData/Roaming/Tencent/xwechat/radium/users/<账号>/applet/packages/<appid>/<版本>/
	radiumVer := filepath.Join(root, "AppData", "Roaming", "Tencent", "xwechat", "radium",
		"users", "oIOZL5Q0tXbG8E3HB8S4xU123456", "applet", "packages", "wx1234abcdef567890", "1.0.0")
	if err := os.MkdirAll(radiumVer, 0o755); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(radiumVer, "__APP__.wxapkg")
	if err := os.WriteFile(main, append([]byte("V1MMWX"), make([]byte, 32)...), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(radiumVer, "_subpackages_1768123456_1.wxapkg")
	if err := os.WriteFile(sub, []byte("plaintext-not-a-package"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 干扰项:非 appid 目录(账号段)不应聚合
	noise := filepath.Join(root, "AppData", "Roaming", "Tencent", "xwechat",
		"radium", "users", "oIOZL5Q0tXbG8E3HB8S4xU123456", "applet", "packages", "notappid", "1.0.0")
	if err := os.MkdirAll(noise, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(noise, "x.wxapkg"), []byte("V1MMWXnoise"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 3.x Applet: Documents/WeChat Files/Applet/<appid>/<hash>/
	appletDir := filepath.Join(root, "Documents", "WeChat Files", "Applet", "wxabcdef1234567890", "5b6f")
	if err := os.MkdirAll(appletDir, 0o755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(appletDir, "page-frame.html.wxapkg")
	if err := os.WriteFile(old, []byte("plain"), 0o644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}

	results := ScanPCPackages([]string{root, filepath.Join(root, "missing")})
	byApp := map[string]PCAppPackage{}
	for _, r := range results {
		byApp[r.AppID] = r
	}
	if len(results) != 2 {
		t.Fatalf("应聚合出 2 个 appid,得到 %d: %+v", len(results), results)
	}
	radium := byApp["wx1234abcdef567890"]
	if len(radium.Files) != 2 {
		t.Fatalf("radium 包应命中 2 个文件,得到 %d", len(radium.Files))
	}
	if !radium.Files[0].Encrypted {
		t.Fatalf("__APP__.wxapkg 应识别为加密")
	}
	if radium.Files[1].Encrypted {
		t.Fatalf("分包应识别为明文")
	}
	applet := byApp["wxabcdef1234567890"]
	if len(applet.Files) != 1 || applet.Files[0].Encrypted {
		t.Fatalf("3.x Applet 布局未正确命中: %+v", applet.Files)
	}
}

func TestPCDefaultScanRoots(t *testing.T) {
	t.Setenv("MINIPROGRAM_PC_SCAN_ROOTS", string(os.PathListSeparator)+"/data/wechat "+string(os.PathListSeparator)+"/opt/roots")
	roots := PCDefaultScanRoots()
	found := false
	for _, r := range roots {
		if r == "/data/wechat" || r == "/opt/roots" {
			found = true
		}
	}
	if !found {
		t.Fatalf("环境变量扫描根未生效: %v", roots)
	}
}

// 非法 appid 目录名绝不能进入聚合结果（appid 形态校验兜底）。
func TestScanPCPackagesRejectsBadAppIDShape(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Documents", "WeChat Files", "Applet", "../../etc", "x")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.wxapkg"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	results := ScanPCPackages([]string{root})
	if len(results) != 0 {
		t.Fatalf("非法 appid 不应被聚合: %+v", results)
	}
	if strings.Contains(strings.Join(func() []string {
		var ps []string
		for _, r := range results {
			ps = append(ps, r.AppID)
		}
		return ps
	}(), ","), "..") {
		t.Fatal("路径穿越目录名不得出现")
	}
}
