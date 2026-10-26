package apkanalysis

// repack_test —— 重打包链纯函数测试:smali gadget 注入、manifest 属性补齐、
// Application 定位、smali 文件查找、工具链就绪判定。外部二进制链路(apktool/
// apksigner 实跑)不在此覆盖。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testSmaliAttach = `.class public Lcom/shell/StubApp;
.super Landroid/app/Application;

# direct methods
.method public constructor <init>()V
    .locals 0

    invoke-direct {p0}, Landroid/app/Application;-><init>()V

    return-void
.end method

# virtual methods
.method protected attachBaseContext(Landroid/content/Context;)V
    .locals 1

    invoke-super {p0, p1}, Landroid/app/Application;->attachBaseContext(Landroid/content/Context;)V

    return-void
.end method
`

func TestInjectGadgetSmaliAttachContext(t *testing.T) {
	patched, err := injectGadgetSmali(testSmaliAttach)
	if err != nil {
		t.Fatalf("injectGadgetSmali: %v", err)
	}
	// attachBaseContext 优先注入(加载最早)
	attachIdx := strings.Index(patched, ".method protected attachBaseContext")
	injectIdx := strings.Index(patched, `loadLibrary(Ljava/lang/String;)V`)
	if attachIdx < 0 || injectIdx < 0 || injectIdx < attachIdx {
		t.Fatalf("loadLibrary 应注入在 attachBaseContext 内:\n%s", patched)
	}
	if !strings.Contains(patched, `const-string v1, "gadget"`) {
		t.Errorf("应使用 .locals 1 腾出的 v1:\n%s", patched)
	}
	if !strings.Contains(patched, ".locals 2") {
		t.Errorf(".locals 计数应 +1:\n%s", patched)
	}
	// <init> 不应被改(只在 attachBaseContext 注入)
	initSection := patched[strings.Index(patched, ".method public constructor"):strings.Index(patched, ".end method")]
	if strings.Contains(initSection, "gadget") {
		t.Errorf("<init> 不该被注入:\n%s", initSection)
	}
}

func TestInjectGadgetSmaliCtorFallback(t *testing.T) {
	smali := `.class public Lcom/app/Main;
.super Landroid/app/Application;

.method public constructor <init>()V
    .locals 2

    invoke-direct {p0}, Landroid/app/Application;-><init>()V

    return-void
.end method
`
	patched, err := injectGadgetSmali(smali)
	if err != nil {
		t.Fatalf("injectGadgetSmali: %v", err)
	}
	if !strings.Contains(patched, `const-string v2, "gadget"`) || !strings.Contains(patched, ".locals 3") {
		t.Errorf("<init> 兜底注入应 bump .locals 2→3 并用 v2:\n%s", patched)
	}
}

func TestInjectGadgetSmaliIdempotent(t *testing.T) {
	once, err := injectGadgetSmali(testSmaliAttach)
	if err != nil {
		t.Fatal(err)
	}
	twice, err := injectGadgetSmali(once)
	if err != nil {
		t.Fatal(err)
	}
	if once != twice {
		t.Error("重复注入应幂等")
	}
}

func TestInjectGadgetSmaliNoMethod(t *testing.T) {
	if _, err := injectGadgetSmali(".class public Lcom/A;\n.super Ljava/lang/Object;\n"); err == nil {
		t.Error("无 attachBaseContext/<init> 应报错")
	}
}

func TestFindApplicationClass(t *testing.T) {
	if got := findApplicationClass(`<application android:name="com.stub.StubApp" android:label="x">`); got != "com.stub.StubApp" {
		t.Errorf("android:name 解析: %q", got)
	}
	if got := findApplicationClass(`<application ns0:name="com.secneo.apkwrapper.ApplicationWrapper">`); got != "com.secneo.apkwrapper.ApplicationWrapper" {
		t.Errorf("apktool ns 前缀解析: %q", got)
	}
	if got := findApplicationClass(`<application android:label="x">`); got != "" {
		t.Errorf("无 name 应为空: %q", got)
	}
}

func TestEnsureExtractNativeLibs(t *testing.T) {
	in := `<manifest><application android:name="com.a.B" ></application></manifest>`
	out := ensureExtractNativeLibs(in)
	if !strings.Contains(out, `android:extractNativeLibs="true"`) {
		t.Errorf("应补 extractNativeLibs: %s", out)
	}
	if out != ensureExtractNativeLibs(out) {
		t.Error("已有时应幂等")
	}
	selfClosed := `<application android:name="com.a.B"/>`
	if got := ensureExtractNativeLibs(selfClosed); !strings.Contains(got, `extractNativeLibs="true"`) || !strings.Contains(got, "</application>") {
		t.Errorf("自闭合标签应展开: %s", got)
	}
}

func TestFindSmaliFile(t *testing.T) {
	decoded := t.TempDir()
	clsDir := filepath.Join(decoded, "smali", "com", "shell")
	if err := os.MkdirAll(clsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clsDir, "StubApp.smali"), []byte(testSmaliAttach), 0o644); err != nil {
		t.Fatal(err)
	}
	// 干扰项:smali_classes2 里的同名类不应优先
	alt := filepath.Join(decoded, "smali_classes2", "com", "shell")
	_ = os.MkdirAll(alt, 0o755)
	_ = os.WriteFile(filepath.Join(alt, "StubApp.smali"), []byte("alt"), 0o644)

	got, err := findSmaliFile(decoded, "com.shell.StubApp")
	if err != nil {
		t.Fatalf("findSmaliFile: %v", err)
	}
	if got != filepath.Join(clsDir, "StubApp.smali") {
		t.Errorf("应优先主 smali 目录: %s", got)
	}
	if _, err := findSmaliFile(decoded, "com.missing.Cls"); err == nil {
		t.Error("不存在的类应报错")
	}
}

func TestRepackToolsReady(t *testing.T) {
	if (&RepackTools{}).Ready() {
		t.Error("空工具链不应 Ready")
	}
	if (&RepackTools{Java: "java"}).Ready() {
		t.Error("缺签名器不应 Ready")
	}
	full := RepackTools{Java: "java", Keytool: "keytool", Zipalign: "zipalign", Apksigner: "apksigner"}
	if !full.Ready() {
		t.Error("zipalign+apksigner 路径应 Ready")
	}
	if full.InjectReady() {
		t.Error("缺 apktool 不应 InjectReady")
	}
	full.Apktool = "apktool"
	if !full.InjectReady() {
		t.Error("补 apktool 后应 InjectReady")
	}
	if !(RepackTools{Java: "java", Apktool: "apktool", UberApkSigner: "uber.jar"}).InjectReady() {
		t.Error("uber-apk-signer 路径应 InjectReady")
	}
}

func TestDetectRepackToolsNoPanic(t *testing.T) {
	tools := DetectRepackTools(t.TempDir()) // 空目录:uber jar 找不到,其余看环境
	_ = tools
}
