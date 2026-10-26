package apkanalysis

// hardening_test —— 阶段0 识别逻辑的纯函数级测试(不依赖 DB):跨框架条目
// 识别、通道决策树组装。service 级集成依赖 PG,由既有验收测试覆盖。

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

// writeZipEntries 造一个含指定条目的 zip(条目名即壳/框架特征)。
func writeZipEntries(t *testing.T, dir, name string, entries []string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	zw := zip.NewWriter(f)
	all := append([]string{"AndroidManifest.xml"}, entries...)
	for _, e := range all {
		w, err := zw.Create(e)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte("stub")); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDetectFrameworks(t *testing.T) {
	apk := writeZipEntries(t, t.TempDir(), "fw.apk", []string{
		"lib/arm64-v8a/libflutter.so",
		"lib/arm64-v8a/libapp.so",
		"assets/index.android.bundle",
		"lib/arm64-v8a/libil2cpp.so",
		"assets/bin/Data/Managed/Metadata/global-metadata.dat",
	})
	fws := detectFrameworks(apk)
	got := map[string]bool{}
	for _, fw := range fws {
		got[fw.Framework] = true
		if fw.Guidance == "" {
			t.Errorf("%s: guidance 为空", fw.Framework)
		}
		if len(fw.Indicators) == 0 {
			t.Errorf("%s: indicators 为空", fw.Framework)
		}
	}
	for _, want := range []string{"Flutter", "React Native", "Unity"} {
		if !got[want] {
			t.Errorf("未检出 %s; got %v", want, fws)
		}
	}
}

func TestDetectFrameworksNone(t *testing.T) {
	apk := writeZipEntries(t, t.TempDir(), "plain.apk", nil)
	if fws := detectFrameworks(apk); len(fws) != 0 {
		t.Errorf("无框架样本不该命中: %v", fws)
	}
}

func TestBuildDecisionNoPacker(t *testing.T) {
	d := buildDecision(&HardeningReport{Packers: []HardeningProduct{}})
	if len(d.Routes) == 0 {
		t.Fatal("无壳也应有常规通道")
	}
	if d.Routes[0].Key != "standard" {
		t.Errorf("无壳首路线应为 standard, got %s", d.Routes[0].Key)
	}
	last := d.Routes[len(d.Routes)-1]
	if last.Key != "api_surface" {
		t.Errorf("接口面应始终在列, got %s", last.Key)
	}
}

func TestBuildDecisionHardened(t *testing.T) {
	d := buildDecision(&HardeningReport{Packers: []HardeningProduct{
		{Product: "爱加密", Confidence: "high", Indicators: []string{"ZIP 条目: libexecmain.so"}},
	}})
	if len(d.Routes) < 4 {
		t.Fatalf("加固场景至少 4 条路线, got %d", len(d.Routes))
	}
	keys := make([]string, 0, len(d.Routes))
	for _, r := range d.Routes {
		keys = append(keys, r.Key)
	}
	want := []string{"dump_timing", "channel", "repack", "api_surface"}
	for i, k := range want {
		if keys[i] != k {
			t.Errorf("路线 %d 应为 %s, got %s(全部: %v)", i+1, k, keys[i], keys)
		}
	}
	// dump_timing 路线必须落到平台工具
	if len(d.Routes[0].Tools) == 0 || d.Routes[0].Tools[0] != "android_device_frida" {
		t.Errorf("dump_timing 路线工具应含 android_device_frida, got %v", d.Routes[0].Tools)
	}
}

func TestConfRank(t *testing.T) {
	if confRank("high") <= confRank("medium") || confRank("medium") <= confRank("low") {
		t.Error("置信度排序错误")
	}
}
