package apkanalysis

// backend_dad — DAD 反编译后端：通过 Python 子进程调用 ASC fork 的 getclass。
// 语义与原 service.go 的硬编码子进程完全一致，抽取为 Backend 实现。

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// DadBackend DAD（androguard DAD）反编译后端。
type DadBackend struct {
	ascDir string // ASC fork 目录（含 main.py）
	python string // Python 解释器路径
}

// NewDadBackend 从环境变量构建 DAD 后端；未部署返回 nil。
func NewDadBackend() *DadBackend {
	ascDir, python, ok := DecompilerPaths()
	if !ok {
		return nil
	}
	return &DadBackend{ascDir: ascDir, python: python}
}

func (b *DadBackend) Kind() string { return "dad" }

func (b *DadBackend) Name() string {
	// 尝试从 .asc-lock 读版本信息
	return "DAD (Python)"
}

func (b *DadBackend) Health(_ context.Context) error {
	if b == nil || b.ascDir == "" || b.python == "" {
		return fmt.Errorf("DAD backend not configured")
	}
	mainPy := b.ascDir + "/main.py"
	if st, err := os.Stat(mainPy); err != nil || st.IsDir() {
		return fmt.Errorf("ASC fork main.py not found at %s", mainPy)
	}
	return nil
}

func (b *DadBackend) Decompile(ctx context.Context, samplePath, class string) (string, error) {
	select {
	case decompileSem <- struct{}{}:
		defer func() { <-decompileSem }()
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(60 * time.Second):
		return "", fmt.Errorf("decompile queue timeout")
	}
	runCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, b.python, b.ascDir+"/main.py", "getclass", samplePath, class)
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("decompile failed: %s", msg)
	}
	out := strings.TrimRight(stdout.String(), "\n") + "\n"
	if out == "\n" {
		return "", fmt.Errorf("decompile produced no output")
	}
	return out, nil
}

func (b *DadBackend) Close() error { return nil }

// decompileSem 同时运行的反编译子进程上限（DAD 与 jadx 共用）。
var decompileSem = make(chan struct{}, 2)
