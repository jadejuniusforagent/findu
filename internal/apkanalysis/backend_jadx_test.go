package apkanalysis

// backend_jadx_test — jadx 后端纯函数单测(不依赖真实 jadx / java)。
// 覆盖:dalvik 类名 → sources 相对路径、模糊找类源码、zip 解压防穿越与去壳。

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClassSourceRel(t *testing.T) {
	cases := map[string]string{
		"Lcom/poc/Main;":           "com/poc/Main.java",
		"com.foo.Bar":              "com/foo/Bar.java",
		"Lcom/poc/Outer$Inner;":    "com/poc/Outer$Inner.java",
		"Lkotlin/coroutines/Cont;": "kotlin/coroutines/Cont.java",
		"Ljava/lang/String;":       "java/lang/String.java",
	}
	for in, want := range cases {
		got, err := classSourceRel(in)
		if err != nil {
			t.Errorf("classSourceRel(%q) error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("classSourceRel(%q) = %q, want %q", in, got, want)
		}
	}
	if _, err := classSourceRel(""); err == nil {
		t.Errorf("empty class should error")
	}
}

func TestFindClassSource(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, "com", "poc")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "Main.java"), []byte("class Main {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 内部类形态:Lcom/poc/Outer$Inner; → 匹配同名前缀文件
	if err := os.WriteFile(filepath.Join(pkg, "Outer$Inner.java"), []byte("class Inner {}"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := findClassSource(dir, "Lcom/poc/Main;")
	if err != nil || !strings.Contains(string(got), "Main") {
		t.Fatalf("findClassSource Main = %q, %v", got, err)
	}
	if _, err := findClassSource(dir, "Lcom/absent/Foo;"); err == nil {
		t.Errorf("missing class should error")
	}
}

func TestProbeJadxInDir(t *testing.T) {
	// 1) bin/jadx 优先
	dir1 := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir1, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir1, "bin", "jadx"), []byte("#!/bin/sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if p, ok := probeJadxInDir(dir1); !ok || !strings.HasSuffix(p, "bin/jadx") {
		t.Errorf("bin/jadx probe = %q, %v", p, ok)
	}

	// 2) lib/jadx-cli*.jar 兜底
	dir2 := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir2, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir2, "lib", "jadx-cli-1.5.0.jar"), []byte("jar"), 0o755); err != nil {
		t.Fatal(err)
	}
	if p, ok := probeJadxInDir(dir2); !ok || !strings.Contains(p, "jadx-cli") {
		t.Errorf("lib jar probe = %q, %v", p, ok)
	}

	// 3) 空目录不可探测
	if _, ok := probeJadxInDir(t.TempDir()); ok {
		t.Errorf("empty dir should fail probe")
	}
}

func TestExtractZipAndStripTopDir(t *testing.T) {
	// 构造 jadx 风格 zip:顶层单目录 jadx-1.5.0/{bin/jadx, lib/jadx-cli.jar}
	zpath := filepath.Join(t.TempDir(), "jadx.zip")
	fz, err := os.Create(zpath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(fz)
	for _, name := range []string{
		"jadx-1.5.0/bin/jadx",
		"jadx-1.5.0/lib/jadx-cli-1.5.0.jar",
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	fz.Close()

	dest := t.TempDir()
	if err := extractZip(zpath, dest); err != nil {
		t.Fatalf("extract: %v", err)
	}
	// 去壳(顶层单目录提升)
	bin := filepath.Join(dest, "bin", "jadx")
	jar := filepath.Join(dest, "lib", "jadx-cli-1.5.0.jar")
	if _, err := os.Stat(bin); err != nil {
		t.Errorf("bin/jadx not promoted: %v", err)
	}
	if _, err := os.Stat(jar); err != nil {
		t.Errorf("lib jar not promoted: %v", err)
	}
	if p, ok := probeJadxInDir(dest); !ok {
		t.Errorf("probe after extract failed: %q %v", p, ok)
	}
}

func TestExtractZipTraversalBlocked(t *testing.T) {
	zpath := filepath.Join(t.TempDir(), "evil.zip")
	fz, err := os.Create(zpath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(fz)
	w, _ := zw.Create("../../escape.jar")
	w.Write([]byte("x"))
	zw.Close()
	fz.Close()

	if err := extractZip(zpath, t.TempDir()); err == nil {
		t.Errorf("path traversal should be blocked")
	}
}

func TestJadxBackendKindAndHealth(t *testing.T) {
	// 未配置(cmd 存在但空校验)应报错;kind 恒为 jadx
	b := &JadxBackend{}
	if b.Kind() != "jadx" {
		t.Errorf("Kind = %s", b.Kind())
	}
	// 构造路径不存在的后端,Health 应非 nil
	missing := NewJadxBackend("x", "/nonexistent/jadx-cli.jar", "", 2)
	if err := missing.Health(t.Context()); err == nil {
		t.Errorf("Health on missing jar should fail")
	}
	_ = missing.Close()
}

func TestNewJadxBackendFormsCmd(t *testing.T) {
	// jar 形态 → java -jar
	b1 := NewJadxBackend("id1", "/opt/jadx.jar", "", 3)
	if len(b1.cmd) != 3 || b1.cmd[0] != "java" || b1.cmd[1] != "-jar" {
		t.Errorf("jar cmd = %v", b1.cmd)
	}
	if b1.workers != 3 {
		t.Errorf("workers = %d", b1.workers)
	}
	// 可执行形态 → 直接路径
	b2 := NewJadxBackend("id2", "/opt/jadx/bin/jadx", "", 0)
	if len(b2.cmd) != 1 || b2.cmd[0] != "/opt/jadx/bin/jadx" {
		t.Errorf("bin cmd = %v", b2.cmd)
	}
	if b2.workers != 2 {
		t.Errorf("default workers = %d", b2.workers)
	}
}
