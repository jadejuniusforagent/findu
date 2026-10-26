package apkanalysis

// repack —— 重打包/签名链(P4 立项落地):apktool 解包 → smali 注入 frida-gadget
// → 重打包 → 重签名 → 装机实测。这是绕反注入壳的另一条主线通道:壳场景下
// apktool 解出的 smali 是壳的,业务代码不存在,所以只改壳的入口/检测逻辑——
// 把 gadget 加载点插进 Application 构造,让壳进程一启动就带上 frida-gadget
// (无 frida-server 进程,进程名层检测直接失效)。
//
// 外部依赖按可用性降级:签名优先 uber-apk-signer(免装 Android build-tools),
// 否则 zipalign + apksigner + keytool;注入需要 apktool。依赖缺失时返回带
// scripts/setup-mobile-tools.sh 指引的错误,而不是模糊失败。
//
// 授权声明:重打包/动态注入只用于授权测试的验证,不植入持久后门。

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// RepackTools 重打包/签名链外部工具的可用性清单(空串 = 未安装)。
type RepackTools struct {
	Apktool       string `json:"apktool"`
	Java          string `json:"java"`
	Keytool       string `json:"keytool"`
	Zipalign      string `json:"zipalign"`
	Apksigner     string `json:"apksigner"`
	UberApkSigner string `json:"uber_apk_signer"`
}

// Ready 签名链是否可用(uber-apk-signer 或 zipalign+apksigner 任一路径)。
func (t RepackTools) Ready() bool {
	if t.Java != "" && t.UberApkSigner != "" {
		return true
	}
	return t.Java != "" && t.Keytool != "" && t.Zipalign != "" && t.Apksigner != ""
}

// InjectReady gadget 注入链是否可用。
func (t RepackTools) InjectReady() bool {
	return t.Ready() && t.Apktool != ""
}

// DetectRepackTools 逐项探测外部工具。extraDirs 里找 uber-apk-signer jar
// (setup 脚本默认放 ~/.local/share/FindU-mobile/apksign)。
func DetectRepackTools(extraDirs ...string) RepackTools {
	look := func(bin string) string {
		if p, err := exec.LookPath(bin); err == nil {
			return p
		}
		return ""
	}
	t := RepackTools{
		Apktool:   look("apktool"),
		Java:      look("java"),
		Keytool:   look("keytool"),
		Zipalign:  look("zipalign"),
		Apksigner: look("apksigner"),
	}
	t.UberApkSigner = findUberApkSigner(extraDirs...)
	return t
}

func findUberApkSigner(extraDirs ...string) string {
	for _, dir := range extraDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := strings.ToLower(e.Name())
			if !e.IsDir() && strings.HasSuffix(name, ".jar") && strings.Contains(name, "uber-apk-signer") {
				return filepath.Join(dir, e.Name())
			}
		}
	}
	return ""
}

// DebugKeystorePass 默认调试 keystore 口令(ANDROID_KEYSTORE_PASS 可覆盖)。
const DebugKeystorePass = "FindU-debug"

// EnsureDebugKeystore 保证重签名用的调试 keystore 存在(keytool 生成)。
func EnsureDebugKeystore(keystorePath, storePass string) error {
	if st, err := os.Stat(keystorePath); err == nil && st.Size() > 0 {
		return nil
	}
	keytool := ""
	if p, lerr := exec.LookPath("keytool"); lerr == nil {
		keytool = p
	}
	if keytool == "" {
		return fmt.Errorf("keytool 未安装(需 JDK;scripts/setup-mobile-tools.sh 检查 java 环境)")
	}
	if err := os.MkdirAll(filepath.Dir(keystorePath), 0o755); err != nil {
		return err
	}
	cmd := exec.Command(keytool, "-genkeypair", "-v",
		"-keystore", keystorePath, "-alias", "z4reco",
		"-keyalg", "RSA", "-keysize", "2048", "-validity", "10000",
		"-storepass", storePass, "-keypass", storePass,
		"-dname", "CN=FindU Repack, O=FindU, C=CN")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("生成 keystore 失败: %w: %s", err, truncate(string(out), 400))
	}
	return nil
}

// ResignResult 重签名结果。
type ResignResult struct {
	SignedAPK string `json:"signed_apk"`
	Method    string `json:"method"` // uber-apk-signer | zipalign+apksigner
	Verify    string `json:"verify,omitempty"`
}

// ResignAPK 重签名:zipalign(或 uber-apk-signer 自带 align)+ v1/v2/v3 签名。
// outPath 为空时落到 <apk 目录>/<名>-resigned.apk。
func ResignAPK(ctx context.Context, tools RepackTools, apkPath, outPath, keystorePath, storePass string) (*ResignResult, error) {
	if apkPath == "" {
		return nil, fmt.Errorf("apkPath 必填")
	}
	if outPath == "" {
		outPath = strings.TrimSuffix(apkPath, ".apk") + "-resigned.apk"
	}
	if !strings.HasSuffix(outPath, ".apk") {
		outPath += ".apk"
	}

	// 路径 A:uber-apk-signer(align+sign 一把梭,免 build-tools)
	if tools.UberApkSigner != "" && tools.Java != "" {
		outDir := filepath.Dir(outPath)
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			return nil, err
		}
		cmd := exec.CommandContext(ctx, tools.Java, "-jar", tools.UberApkSigner,
			"--apks", apkPath, "--out", outDir, "--overwrite", "--allowResign")
		out, err := runWithBudget(ctx, cmd, 5*time.Minute)
		if err != nil {
			return nil, fmt.Errorf("uber-apk-signer 失败: %w: %s", err, truncate(out, 400))
		}
		// 产物名:<名>-aligned-debugSigned.apk → 归一到 outPath
		produced := filepath.Join(outDir, strings.TrimSuffix(filepath.Base(apkPath), ".apk")+"-aligned-debugSigned.apk")
		if _, err := os.Stat(produced); err != nil {
			return nil, fmt.Errorf("uber-apk-signer 未产出预期文件(%s);输出: %s", produced, truncate(out, 400))
		}
		if err := os.Rename(produced, outPath); err != nil {
			return nil, err
		}
		return &ResignResult{SignedAPK: outPath, Method: "uber-apk-signer"}, nil
	}

	// 路径 B:zipalign + apksigner(Android build-tools)
	if tools.Zipalign == "" || tools.Apksigner == "" || tools.Keytool == "" {
		return nil, fmt.Errorf("签名工具链不全(需要 uber-apk-signer jar 或 zipalign+apksigner+keytool): %+v;运行 scripts/setup-mobile-tools.sh 预置", tools)
	}
	if err := EnsureDebugKeystore(keystorePath, storePass); err != nil {
		return nil, err
	}
	aligned := strings.TrimSuffix(outPath, ".apk") + "-aligned.apk"
	if out, err := runWithBudget(ctx, exec.CommandContext(ctx, tools.Zipalign, "-f", "-p", "4", apkPath, aligned), 5*time.Minute); err != nil {
		return nil, fmt.Errorf("zipalign 失败: %w: %s", err, truncate(out, 400))
	}
	defer func() {
		if err := os.Remove(aligned); err != nil {
			log.Printf("[apkanalysis] 清理 zipalign 中间产物失败 %s: %v", aligned, err)
		}
	}()
	if out, err := runWithBudget(ctx, exec.CommandContext(ctx, tools.Apksigner, "sign",
		"--ks", keystorePath, "--ks-pass", "pass:"+storePass, "--key-pass", "pass:"+storePass,
		"--out", outPath, aligned), 5*time.Minute); err != nil {
		return nil, fmt.Errorf("apksigner sign 失败: %w: %s", err, truncate(out, 400))
	}
	verify, _ := runWithBudget(ctx, exec.CommandContext(ctx, tools.Apksigner, "verify", "--print-certs", outPath), time.Minute)
	return &ResignResult{SignedAPK: outPath, Method: "zipalign+apksigner", Verify: truncate(strings.TrimSpace(verify), 500)}, nil
}

// InjectGadgetResult gadget 注入结果(含链路步骤,便于 Agent 取证)。
type InjectGadgetResult struct {
	RepackedAPK string   `json:"repacked_apk"`
	SignedAPK   string   `json:"signed_apk"`
	Method      string   `json:"method"`
	Steps       []string `json:"steps"`
}

// InjectGadget apktool 解包 → 注入 gadget 加载点到 Application → 重打包 → 重签名。
// gadgetSo 为 libfrida-gadget.so 的本地路径(abi 对应,setup 脚本预置);
// gadget 默认 listen 模式(设备侧 127.0.0.1:27042,adb forward 后 frida -H 连接)。
func InjectGadget(ctx context.Context, tools RepackTools, apkPath, gadgetSo, abi, outPath, workDir, keystorePath, storePass string) (*InjectGadgetResult, error) {
	if !tools.InjectReady() {
		return nil, fmt.Errorf("注入工具链不全(需要 apktool + 签名链): %+v;运行 scripts/setup-mobile-tools.sh 预置", tools)
	}
	if st, err := os.Stat(gadgetSo); err != nil || st.IsDir() {
		return nil, fmt.Errorf("gadget so 不存在: %s(scripts/setup-mobile-tools.sh 预置 libfrida-gadget.so,或从 github.com/frida/frida/releases 下载 frida-gadget-<版本>-android-<abi>.so.xz 解压)", gadgetSo)
	}
	if abi == "" {
		abi = "arm64-v8a"
	}
	if workDir == "" {
		workDir, _ = os.MkdirTemp("", "z4reco-repack-")
		defer func() {
			if err := os.RemoveAll(workDir); err != nil {
				log.Printf("[apkanalysis] 清理 gadget 注入工作目录失败 %s: %v", workDir, err)
			}
		}()
	}
	res := &InjectGadgetResult{Steps: []string{}}
	step := func(s string) { res.Steps = append(res.Steps, s) }

	// 1) apktool d
	decoded := filepath.Join(workDir, "decoded")
	if out, err := runWithBudget(ctx, exec.CommandContext(ctx, tools.Apktool, "d", "-f", "-o", decoded, apkPath), 10*time.Minute); err != nil {
		return nil, fmt.Errorf("apktool d 失败: %w: %s", err, truncate(out, 400))
	}
	step("apktool d 完成(壳场景下解出的是壳的 smali,业务代码不存在——只改壳入口)")

	// 2) manifest:Application 类定位 + extractNativeLibs
	manifestPath := filepath.Join(decoded, "AndroidManifest.xml")
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("读取解码 manifest 失败: %w", err)
	}
	appClass := findApplicationClass(string(manifest))
	if appClass == "" {
		return nil, fmt.Errorf("manifest 没有声明 android:name 的 Application:改用 Zygisk/LSPosed 通道注入")
	}
	patchedManifest := ensureExtractNativeLibs(string(manifest))
	if patchedManifest != string(manifest) {
		if err := os.WriteFile(manifestPath, []byte(patchedManifest), 0o644); err != nil {
			return nil, err
		}
		step("manifest 已加 android:extractNativeLibs=true(保证 gadget so 被解压加载)")
	}
	step("Application 类: " + appClass)

	// 3) smali 注入 System.loadLibrary("gadget")
	smaliPath, err := findSmaliFile(decoded, appClass)
	if err != nil {
		return nil, err
	}
	content, err := os.ReadFile(smaliPath)
	if err != nil {
		return nil, err
	}
	patched, err := injectGadgetSmali(string(content))
	if err != nil {
		return nil, fmt.Errorf("注入 %s 失败: %w", smaliPath, err)
	}
	if err := os.WriteFile(smaliPath, []byte(patched), 0o644); err != nil {
		return nil, err
	}
	step("smali 已注入 System.loadLibrary(\"gadget\"): " + filepath.Base(smaliPath))

	// 4) gadget so 进 lib/<abi>/
	libDir := filepath.Join(decoded, "lib", abi)
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		return nil, err
	}
	gadgetDst := filepath.Join(libDir, "libgadget.so")
	if err := copyFile(gadgetSo, gadgetDst); err != nil {
		return nil, err
	}
	step("gadget 已就位: " + abi + "/libgadget.so(默认 listen 127.0.0.1:27042;装机后 adb forward tcp:27042 tcp:27042 再 frida -H 127.0.0.1:27042)")

	// 5) apktool b + 重签名
	unsigned := filepath.Join(workDir, "repacked-unsigned.apk")
	if out, err := runWithBudget(ctx, exec.CommandContext(ctx, tools.Apktool, "b", "-o", unsigned, decoded), 10*time.Minute); err != nil {
		return nil, fmt.Errorf("apktool b 失败: %w: %s", err, truncate(out, 400))
	}
	step("apktool b 完成")
	signed, err := ResignAPK(ctx, tools, unsigned, outPath, keystorePath, storePass)
	if err != nil {
		return nil, err
	}
	res.RepackedAPK = unsigned
	res.SignedAPK = signed.SignedAPK
	res.Method = signed.Method
	step("重签名完成(" + signed.Method + "): " + signed.SignedAPK)
	return res, nil
}

// ────────────────────────── 纯函数(可单测) ──────────────────────────

var (
	reApplicationTag = regexp.MustCompile(`(?s)<application\b[^>]*>`)
	reApplicationNS  = regexp.MustCompile(`(?s)<application\b[^>]*?(?:android:|ns0:|ns:)name="([^"]+)"`)
	reExtractNative  = regexp.MustCompile(`android:extractNativeLibs`)
)

// findApplicationClass 从解码 manifest 抓 android:name(兼容 apktool 的 ns 前缀)。
func findApplicationClass(manifestXML string) string {
	tag := reApplicationTag.FindString(manifestXML)
	if tag == "" {
		return ""
	}
	if m := reApplicationNS.FindStringSubmatch(tag); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}

// ensureExtractNativeLibs application 标签缺 extractNativeLibs 时补 true。
func ensureExtractNativeLibs(manifestXML string) string {
	if reExtractNative.MatchString(manifestXML) {
		return manifestXML
	}
	return reApplicationTag.ReplaceAllStringFunc(manifestXML, func(tag string) string {
		if strings.HasSuffix(tag, "/>") { // 自闭合(空 application):展开为完整标签
			return strings.TrimSuffix(tag, "/>") + ` android:extractNativeLibs="true"></application>`
		}
		return strings.TrimSuffix(tag, ">") + ` android:extractNativeLibs="true">`
	})
}

var (
	reMethodCtor   = regexp.MustCompile(`(?m)^\.method\s+(?:public\s+)?constructor\s+<init>\(\)V\s*$`)
	reMethodAttach = regexp.MustCompile(`(?m)^\.method\s+(?:public\s+|protected\s+)?attachBaseContext\(Landroid/content/Context;\)V\s*$`)
	reLocals       = regexp.MustCompile(`(?m)^\s*\.locals\s+(\d+)\s*$`)
	reRegisters    = regexp.MustCompile(`(?m)^\s*\.registers\s+(\d+)\s*$`)
)

// injectGadgetSmali 在 smali 的 attachBaseContext(优先,加载最早)或 <init>
// 里插入 System.loadLibrary("gadget")。做法:寄存器计数 +1,用腾出的新寄存器
// 传 so 名——标准 gadget 注入手法,不碰壳的既有寄存器分配。
func injectGadgetSmali(smali string) (string, error) {
	if strings.Contains(smali, `"gadget"`) {
		return smali, nil // 幂等:已注入过
	}
	methodRe := reMethodAttach
	if methodRe.FindStringIndex(smali) == nil {
		methodRe = reMethodCtor
	}
	loc := methodRe.FindStringIndex(smali)
	if loc == nil {
		return "", fmt.Errorf("没找到 attachBaseContext 或 <init> 方法(壳 smali 混淆形态?手工定位壳入口后再注入)")
	}
	// 方法头之后的第一个 .locals / .registers 行
	head := smali[loc[1]:]
	regs := reLocals.FindStringSubmatchIndex(head)
	if regs == nil {
		regs = reRegisters.FindStringSubmatchIndex(head)
	}
	if regs == nil {
		return "", fmt.Errorf("方法体内没有 .locals/.registers 指令(smali 形态异常)")
	}
	n, err := parseDigits(head[regs[2]:regs[3]])
	if err != nil {
		return "", err
	}
	// 新寄存器编号 = 原 counts(0..n-1 已占用,n 即空闲位);计数行 +1
	line := head[regs[0]:regs[1]]
	bumped := strings.Replace(line, fmt.Sprintf("%d", n), fmt.Sprintf("%d", n+1), 1)
	reg := fmt.Sprintf("v%d", n)
	inject := fmt.Sprintf("\n    const-string %s, \"gadget\"\n    invoke-static {%s}, Ljava/lang/System;->loadLibrary(Ljava/lang/String;)V\n", reg, reg)
	return smali[:loc[1]] + head[:regs[0]] + bumped + inject + head[regs[1]:], nil
}

func parseDigits(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("非法寄存器计数: %q", s)
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

// findSmaliFile 在 apktool 解码目录(smali / smali_classes2..N)定位类 smali。
func findSmaliFile(decodedDir, classFQN string) (string, error) {
	rel := strings.ReplaceAll(strings.TrimPrefix(strings.TrimSpace(classFQN), "L"), ".", "/") + ".smali"
	entries, err := os.ReadDir(decodedDir)
	if err != nil {
		return "", err
	}
	dirs := []string{}
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "smali") {
			dirs = append(dirs, filepath.Join(decodedDir, e.Name()))
		}
	}
	// 优先 smali(壳入口通常在主 dex)
	for _, d := range dirs {
		p := filepath.Join(d, rel)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("application smali 未找到: %s(smali*/%s)", classFQN, rel)
}

// ────────────────────────── 小工具 ──────────────────────────

func runWithBudget(ctx context.Context, cmd *exec.Cmd, timeout time.Duration) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	done := make(chan error, 1)
	var combined strings.Builder
	cmd.Stdout = &combined
	cmd.Stderr = &combined
	go func() { done <- cmd.Run() }()
	select {
	case err := <-done:
		return combined.String(), err
	case <-cctx.Done():
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return combined.String(), fmt.Errorf("超时(%s)", timeout)
	}
}
