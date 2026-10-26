package apkanalysis

// backend_smali_alignment_test — 验收清单对齐测试:覆盖文档 §7 L0/L1/L2
// 的验收条件,明确哪些当前已满足、哪些需要前置条件。
// 无 PG / 无 DAD / 无 jadx 时均可跑。

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// ────────────────────────── L0 验收 ──────────────────────────

// TestL0_SmaliNoFallbackSvc 无 Java 后端时 GetSource 应降级到 smali(不报 503)。
// 对应验收:「DAD 不可用时降级链自动回落(L0 时期是 smali, 现为 go-dad → smali),不报 503」。
func TestL0_GoDadFallbackSvc(t *testing.T) {
	svc, err := NewService(nil, zap.NewNop(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// CI 中未部署 DAD:内置链头应为 go-dad(Java),smali 在链尾兜底
	ctx := context.Background()
	backend := svc.registry.Resolve(ctx)
	if backend == nil {
		t.Fatal("no backend resolved")
	}
	if backend.Kind() != "go-dad" {
		t.Errorf("expected go-dad fallback, got %s", backend.Kind())
	}
	// DecompilerAvailability 报可用(go-dad 是 Java 反编译后端)
	if err := svc.DecompilerAvailability(); err != nil {
		t.Errorf("DecompilerAvailability = %v, want nil", err)
	}
	// go-dad 应产出 Java 形态源码(class 头/super/方法)
	apk := "../apkengine/findrefs/testdata/ByteStreamReader.apk"
	src, err := backend.Decompile(ctx, apk, "LByteStreamReader;")
	if err != nil {
		t.Fatalf("go-dad decompile: %v", err)
	}
	if !strings.Contains(src, "class ByteStreamReader") || !strings.Contains(src, "super()") {
		t.Errorf("go-dad output unexpected:\n%s", src[:min(len(src), 300)])
	}
	// smali 兜底仍可直接产出
	smaliSrc, err := svc.smaliForApkPath(apk, "LByteStreamReader;")
	if err != nil {
		t.Fatalf("smali render: %v", err)
	}
	if !strings.Contains(smaliSrc, ".class") || !strings.Contains(smaliSrc, ".method") {
		t.Errorf("smali output lacks .class or .method")
	}
}

// TestL0_RuntimeEndpointRuntimeVerify Runtime 端点应返回 backends 列表。
// 对应验收:「后端健康检查 GET /api/android/runtime 返回完整列表」。
func TestL0_RuntimeEndpointRuntimeVerify(t *testing.T) {
	svc, err := NewService(nil, zap.NewNop(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	backends := svc.registry.List(context.Background())
	if len(backends) == 0 {
		t.Fatal("registry has no backends (smali should be registered)")
	}
	found := false
	for _, b := range backends {
		if b.Kind == "smali" {
			found = true
			if !b.Healthy {
				t.Error("smali backend not healthy")
			}
		}
	}
	if !found {
		t.Error("smali backend missing from registry list")
	}
}

// ────────────────────────── L0 smali 回汇编 ──────────────────

// 文档 §3.6 验收「输出的 smali 能被 smali.jar 正确汇编回去」——
// 已由 apkengine/smali 包的 StandardClass + smali_roundtrip_test.go 覆盖
// (ANDROID_SMALI_JAR 门控;见该包测试)。此处不再重复前置说明。

// ────────────────────────── L1 验收 ──────────────────

// TestL1_JadxSmokeNoJava 验证 jadx 不健康时降级链回落到下一个健康后端
// (go-dad),而不是空后端报错。jadx 的实际冒烟需要 java 环境——这里只验证
// DB-Free 注册与降级路径。
func TestL1_JadxSmokeNoJava(t *testing.T) {
	svc, err := NewService(nil, zap.NewNop(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// 模拟注册一个不存在的 jadx 后端到 registry(不走 DB)
	jadx := NewJadxBackend("fake-id", "/nonexistent/jadx.jar", "", 2)
	svc.registry.Register(jadx)
	svc.registry.SetPrefer("jadx")
	ctx := context.Background()
	// Health 应失败(jar 不存在)
	if err := jadx.Health(ctx); err == nil {
		t.Error("Health on missing jar should fail")
	}
	// Resolve 应回落到 go-dad(jadx 不健康,go-dad 是链上首个健康 Java 后端)
	resolved := svc.registry.Resolve(ctx)
	if resolved == nil || resolved.Kind() != "go-dad" {
		t.Errorf("expected go-dad fallback, got %v", resolved)
	}
}

// TestL1_GoDadJavaOutput go-dad 应产出 Java 形态输出(构造器/方法/返回)。
func TestL1_GoDadJavaOutput(t *testing.T) {
	b := NewGoDadBackend()
	if b.Kind() != "go-dad" || b.Health(context.Background()) != nil {
		t.Fatalf("go-dad backend not healthy")
	}
	apk := "../apkengine/findrefs/testdata/ByteStreamReader.apk"
	src, err := b.Decompile(context.Background(), apk, "LByteStreamReader;")
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	// 基本 Java 结构断言
	for _, want := range []string{
		"class ByteStreamReader",
		"extends java.lang.Object",
		"public void <init>()",
		"super();",
		"class",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("go-dad output missing %q", want)
		}
	}
	// 不存在的类应报错
	if _, err := b.Decompile(context.Background(), apk, "LNo/Such/Class;"); err == nil {
		t.Error("expected error for missing class")
	}
}
