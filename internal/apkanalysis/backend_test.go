package apkanalysis

// backend_test — 反编译后端注册表与降级链单元测试（无需 DB / 无需 ASC 部署）。
// 核心断言:未上传 jadx、未部署 DAD 时,GetSource 降级链最终落到 smali 兜底。

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// fakeBackend 可编程后端(测试双)。
type fakeBackend struct {
	kind    string
	name    string
	healthy bool
	src     string
}

func (f *fakeBackend) Kind() string { return f.kind }
func (f *fakeBackend) Name() string { return f.name }
func (f *fakeBackend) Health(context.Context) error {
	if f.healthy {
		return nil
	}
	return errors.New("unhealthy")
}
func (f *fakeBackend) Decompile(_ context.Context, _, _ string) (string, error) {
	if f.src == "" {
		return "", errors.New("no source")
	}
	return f.src, nil
}
func (f *fakeBackend) Close() error { return nil }

// TestRegistrySmaliOnlyFallback 仅注册 smali 兜底时,Resolve 必须返回 smali。
// 对应手工注册表的最简形态(smali 永远可用,是降级链的最后一环)。
func TestRegistrySmaliOnlyFallback(t *testing.T) {
	reg := NewBackendRegistry()
	render := func(_, _ string) (string, error) { return "smali-source", nil }
	reg.Register(NewSmaliBackend(render))

	ctx := context.Background()
	b := reg.Resolve(ctx)
	if b == nil {
		t.Fatal("Resolve returned nil, want smali fallback")
	}
	if b.Kind() != "smali" {
		t.Fatalf("Resolve kind = %s, want smali", b.Kind())
	}
	src, err := b.Decompile(ctx, "/tmp/x.apk", "Lcom/a/B;")
	if err != nil || src != "smali-source" {
		t.Fatalf("Decompile = %q, %v", src, err)
	}
}

// TestServiceBuiltinChain 未部署 DAD / 未上传 jadx 时,内置降级链应为
// go-dad(Java)→ smali(字节码);DecompilerAvailability 报可用(go-dad 是 Java 后端)。
func TestServiceBuiltinChain(t *testing.T) {
	svc, err := NewService(nil, zap.NewNop(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	b := svc.registry.Resolve(ctx)
	if b == nil {
		t.Fatal("Resolve returned nil")
	}
	if b.Kind() != "go-dad" {
		t.Fatalf("builtin chain head = %s, want go-dad", b.Kind())
	}
	if err := svc.DecompilerAvailability(); err != nil {
		t.Errorf("DecompilerAvailability = %v, want nil (go-dad is a Java backend)", err)
	}
	// smali 仍在链尾(go-dad 掉线时的最终兜底)
	infos := svc.registry.List(ctx)
	last := infos[len(infos)-1]
	if last.Kind != "smali" {
		t.Errorf("chain tail = %s, want smali", last.Kind)
	}
}

// TestRegistryPreferReordersChain 注册 dad + smali 后 SetPrefer(dad) 应把
// dad 提到最前;dad 不健康时仍回落 smali。
func TestRegistryPreferReordersChain(t *testing.T) {
	reg := NewBackendRegistry()
	render := func(_, _ string) (string, error) { return "smali-source", nil }
	reg.Register(NewSmaliBackend(render))
	dad := &fakeBackend{kind: "dad", name: "DAD", healthy: true, src: "java-source"}
	reg.Register(dad)
	reg.SetPrefer("dad")

	ctx := context.Background()
	b := reg.Resolve(ctx)
	if b.Kind() != "dad" {
		t.Fatalf("prefer not honored: got %s", b.Kind())
	}
	if reg.Prefer() != "dad" {
		t.Errorf("Prefer() = %s, want dad", reg.Prefer())
	}

	// dad 掉线 → 回落 smali
	dad.healthy = false
	b = reg.Resolve(ctx)
	if b == nil || b.Kind() != "smali" {
		t.Fatalf("fallback failed: got %v", b)
	}
}

// TestRegistryList 列表应输出健康状态摘要。
func TestRegistryList(t *testing.T) {
	reg := NewBackendRegistry()
	reg.Register(&fakeBackend{kind: "jadx", name: "jadx 1.5.0", healthy: true})
	reg.Register(&fakeBackend{kind: "dad", name: "DAD", healthy: false})
	infos := reg.List(context.Background())
	if len(infos) != 2 {
		t.Fatalf("List len = %d, want 2", len(infos))
	}
	if infos[0].Kind != "jadx" || !infos[0].Healthy {
		t.Errorf("jadx entry = %+v", infos[0])
	}
	if infos[1].Healthy || infos[1].Error == "" {
		t.Errorf("dad entry should be unhealthy with error: %+v", infos[1])
	}
}

// TestSmaliBackendViaService 走 Service 的 smaliForApkPath 真实渲染
// (fixture APK,不依赖 DB / ASC 部署)。证实兜底链路能产出真实字节码。
func TestSmaliBackendViaService(t *testing.T) {
	svc, err := NewService(nil, zap.NewNop(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	src, err := svc.smaliForApkPath("../apkengine/findrefs/testdata/ByteStreamReader.apk", "LByteStreamReader;")
	if err != nil {
		t.Fatalf("smaliForApkPath: %v", err)
	}
	if !strings.Contains(src, ".method") {
		t.Errorf("smali output missing .method block:\n%s", src[:min(len(src), 200)])
	}
	// 不存在的类应报错
	if _, err := svc.smaliForApkPath("../apkengine/findrefs/testdata/ByteStreamReader.apk", "LNo/Such/Class;"); err == nil {
		t.Errorf("expected error for missing class")
	}
}
