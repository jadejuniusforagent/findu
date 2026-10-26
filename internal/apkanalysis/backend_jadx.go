package apkanalysis

// backend_jadx — jadx 反编译后端：经 java -jar 子进程调用 jadx。
// 支持两种形态：
//   - jar:    java -jar <jadx.jar> --single-class ...
//   - 可执行:  <bin/jadx> --single-class ...（jadx 发行包的 bin/jadx 脚本）
// 每个后端持有独立信号量（workers 限并发），避免 JVM 冷启动打爆内存。

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"finduai/internal/apkengine/dex"
	"finduai/internal/apkengine/zipprobe"
)

// JadxBackend jadx 反编译后端。
type JadxBackend struct {
	id      string
	cmd     []string // 命令前缀: [java, -jar, jarPath] 或 [binPath]
	workers int
	timeout time.Duration

	mu      sync.Mutex
	name    string
	lastErr error
	sem     chan struct{}
}

// NewJadxBackend 构建 jadx 后端。
// path 为 jar 文件或可执行文件;javaPath 仅对 jar 形态生效(默认 java)。
func NewJadxBackend(id, path, javaPath string, workers int) *JadxBackend {
	if workers <= 0 {
		workers = 2
	}
	var cmd []string
	if strings.HasSuffix(strings.ToLower(path), ".jar") {
		cmd = []string{javaOrDefault(javaPath), "-jar", path}
	} else {
		cmd = []string{path}
	}
	return &JadxBackend{
		id:      id,
		cmd:     cmd,
		workers: workers,
		timeout: 180 * time.Second,
		sem:     make(chan struct{}, workers),
	}
}

func javaOrDefault(p string) string {
	if strings.TrimSpace(p) != "" {
		return strings.TrimSpace(p)
	}
	return "java"
}

func (b *JadxBackend) Kind() string { return "jadx" }

func (b *JadxBackend) Name() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.name != "" {
		return b.name
	}
	return "jadx"
}

func (b *JadxBackend) ID() string { return b.id }

// setVersion 探测 jadx 版本写入 name(如 "jadx 1.5.0")。
func (b *JadxBackend) setVersion(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, b.cmd[0], append(b.cmd[1:], "--version")...)
	out, err := cmd.Output()
	if err != nil {
		return
	}
	ver := strings.TrimSpace(string(out))
	// jadx --version 输出形如 "jadx 1.5.0\n" 或 "1.5.0"
	for _, line := range strings.Split(ver, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.Contains(line, "jadx") {
			line = "jadx " + line
		}
		b.mu.Lock()
		b.name = line
		b.mu.Unlock()
		return
	}
}

// Health 检查 jadx 可用性:命令前缀存在 + --version 可执行。
func (b *JadxBackend) Health(ctx context.Context) error {
	b.mu.Lock()
	err := b.lastErr
	b.mu.Unlock()
	if err != nil {
		return err
	}
	if b == nil || len(b.cmd) == 0 {
		return fmt.Errorf("jadx backend not configured")
	}
	// 命令前缀首元素(java 或 bin/jadx)必须存在
	if _, err := exec.LookPath(b.cmd[0]); err != nil {
		b.mu.Lock()
		b.lastErr = fmt.Errorf("jadx command not found: %s", b.cmd[0])
		b.mu.Unlock()
		return b.lastErr
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, b.cmd[0], append(b.cmd[1:], "--version")...).Run(); err != nil {
		b.mu.Lock()
		b.lastErr = fmt.Errorf("jadx --version failed: %v", err)
		b.mu.Unlock()
		return b.lastErr
	}
	return nil
}

// Decompile 反编译单类:优先抽取目标类为 mini-dex(单文件,jadx 只加载
// 一个类,热路径提速);抽取不支持(类定位失败/引用 call site 等)时
// 自动回退全量 APK,行为与旧版一致。
// jadx --single-class <class> <输入> -d <tmpdir>,读取 sources 下 .java。
func (b *JadxBackend) Decompile(ctx context.Context, apkPath, class string) (string, error) {
	select {
	case b.sem <- struct{}{}:
		defer func() { <-b.sem }()
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(2 * time.Minute):
		return "", fmt.Errorf("jadx queue timeout")
	}
	runCtx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()

	tmpDir, err := os.MkdirTemp("", "jadx_*")
	if err != nil {
		return "", fmt.Errorf("jadx tmpdir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	input := apkPath
	if miniPath, merr := buildMiniDex(apkPath, class, tmpDir); merr == nil {
		input = miniPath
	}

	// --single-class 接收 dalvik 描述符与点号形态均可;--no-res 跳过资源处理提速
	args := append(append([]string{}, b.cmd...),
		"--single-class", class, "--no-res", "-d", tmpDir, input)
	cmd := exec.CommandContext(runCtx, args[0], args[1:]...)
	cmd.Env = append(os.Environ(), "JADX_DISABLE_XML_SECURITY=1")
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			msg = outBuf.String()
		}
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("jadx failed: %s", msg)
	}

	rel, err := classSourceRel(class)
	if err != nil {
		return "", err
	}
	srcPath := filepath.Join(tmpDir, "sources", filepath.FromSlash(rel))
	data, err := os.ReadFile(srcPath)
	if err != nil {
		// 兜底:jadx 可能把类写在子包不同的目录,全量扫 sources 找类名
		found, perr := findClassSource(filepath.Join(tmpDir, "sources"), class)
		if perr != nil {
			return "", fmt.Errorf("jadx output missing class source %s: %v", rel, err)
		}
		data = found
	}
	return strings.TrimRight(string(data), "\n") + "\n", nil
}

// classSourceRel dalvik 描述符 → sources 下的相对路径。
// Lcom/foo/Bar; → com/foo/Bar.java
func classSourceRel(class string) (string, error) {
	c := strings.TrimSpace(class)
	if strings.HasPrefix(c, "L") && strings.HasSuffix(c, ";") {
		c = c[1 : len(c)-1]
	}
	c = strings.ReplaceAll(c, "/", ".")
	c = strings.TrimPrefix(c, ".")
	if c == "" {
		return "", fmt.Errorf("invalid class: %s", class)
	}
	return strings.ReplaceAll(c, ".", "/") + ".java", nil
}

// findClassSource 在 sources 树下按类名(含内部类)模糊找 java 文件。
func findClassSource(sourcesDir, class string) ([]byte, error) {
	c := strings.TrimSpace(class)
	if strings.HasPrefix(c, "L") && strings.HasSuffix(c, ";") {
		c = c[1 : len(c)-1]
	}
	// 简单名:取最后一个 / 或 . 之后;忽略内部类 $ 后的部分作匹配键
	simple := c[strings.LastIndexAny(c, "/.")+1:]
	simple = strings.SplitN(simple, "$", 2)[0]
	var found []byte
	err := filepath.WalkDir(sourcesDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".java") {
			return nil
		}
		name := filepath.Base(path)
		key := strings.SplitN(strings.TrimSuffix(name, ".java"), "$", 2)[0]
		if key == simple {
			data, rerr := os.ReadFile(path)
			if rerr == nil {
				found = data
				return filepath.SkipAll
			}
		}
		return nil
	})
	if err != nil || found == nil {
		return nil, fmt.Errorf("class %s source not found under %s", class, sourcesDir)
	}
	return found, nil
}

func (b *JadxBackend) Close() error { return nil }

// SmokeJadx 冒烟测试:内嵌真实 APK 跑一次 --single-class。
// 返回后端显示名(含版本)与错误。
func (b *JadxBackend) SmokeJadx(ctx context.Context) (name string, err error) {
	apkPath := filepath.Join(os.TempDir(), fmt.Sprintf("jadx_smoke_%d.apk", time.Now().UnixNano()))
	if werr := os.WriteFile(apkPath, smokeAPK, 0o600); werr != nil {
		return "", fmt.Errorf("write smoke apk: %w", werr)
	}
	defer os.Remove(apkPath)
	if _, derr := b.Decompile(ctx, apkPath, smokeClass); derr != nil {
		return "", derr
	}
	b.setVersion(ctx)
	return b.Name(), nil
}

// buildMiniDex 从 APK 中定位目标类所在 DEX,重组为仅含该类的 mini.dex
// 写入 tmpDir,返回文件路径。任何失败(类定位失败、call site 引用、
// 畸形结构)返回 error,调用方回退全量 APK 输入。
func buildMiniDex(apkPath, class, tmpDir string) (string, error) {
	dalvik := normalizeClassQuery(class, false)
	r, err := zipprobe.Open(apkPath)
	if err != nil {
		return "", fmt.Errorf("open apk: %w", err)
	}
	defer r.Close()
	entries, err := zipprobe.ParseCDDexEntries(r)
	if err != nil {
		return "", fmt.Errorf("cd entries: %w", err)
	}
	zipprobe.SortByCompSize(entries)
	for _, e := range entries {
		data, err := zipprobe.InflateDex(r, e)
		if err != nil {
			continue
		}
		for _, ld := range zipprobe.LogicalDexBuffers(e.Name, data) {
			d, err := dex.Parse(ld.Buf, ld.Name)
			if err != nil {
				continue
			}
			if d.GetClassByName(dalvik) == nil {
				continue
			}
			mini, err := d.ExtractClasses(dalvik)
			if err != nil {
				return "", fmt.Errorf("extract %s: %w", dalvik, err)
			}
			path := filepath.Join(tmpDir, "mini.dex")
			if err := os.WriteFile(path, mini, 0o600); err != nil {
				return "", fmt.Errorf("write mini dex: %w", err)
			}
			return path, nil
		}
	}
	return "", fmt.Errorf("class %s not found in APK", dalvik)
}
