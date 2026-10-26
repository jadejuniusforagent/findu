package apkanalysis

// buildMiniDex_test — jadx 后端 mini-dex 热路径测试。
// 真实 jadx 运行由 SmokeJadx 覆盖(需 java);此处验证抽取与回退路径。
// invoke-custom 的抽取降级在 dex 包 TestExtractClassesBailOnInvokeCustom 覆盖。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"finduai/internal/apkengine/zipprobe"
)

const miniDexFixtureAPK = "../apkengine/findrefs/testdata/ByteStreamReader.apk"

func TestBuildMiniDexExtracts(t *testing.T) {
	tmpDir := t.TempDir()
	path, err := buildMiniDex(miniDexFixtureAPK, "LByteStreamReader;", tmpDir)
	if err != nil {
		t.Fatalf("buildMiniDex: %v", err)
	}
	if filepath.Dir(path) != tmpDir {
		t.Errorf("mini dex path = %q, want under %q", path, tmpDir)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data[:4]), "dex\n") {
		t.Errorf("mini dex magic = %q", string(data[:4]))
	}
	// mini.dex 显著小于所在源 DEX(热路径价值;APK 经 deflate 压缩不可直接比)
	if r, err := zipprobe.Open(miniDexFixtureAPK); err == nil {
		defer r.Close()
		if entries, err := zipprobe.ParseCDDexEntries(r); err == nil && len(entries) > 0 {
			if src, err := zipprobe.InflateDex(r, entries[0]); err == nil {
				if len(data) >= len(src) {
					t.Errorf("mini dex %d bytes >= source dex %d bytes", len(data), len(src))
				}
			}
		}
	}
	// 点号形态查询同样可定位
	if _, err := buildMiniDex(miniDexFixtureAPK, "ByteStreamReader", tmpDir); err != nil {
		t.Errorf("dotted class query failed: %v", err)
	}
}

func TestBuildMiniDexFallbacks(t *testing.T) {
	tmpDir := t.TempDir()
	// 类不存在 → 报错,调用方回退全量 APK
	if _, err := buildMiniDex(miniDexFixtureAPK, "Lcom/missing/Cls;", tmpDir); err == nil {
		t.Error("expected error for missing class")
	}
	// 非 APK 输入 → 报错回退
	if _, err := buildMiniDex("/nonexistent/file.apk", "LByteStreamReader;", tmpDir); err == nil {
		t.Error("expected error for unreadable apk")
	}
}
