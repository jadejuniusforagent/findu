package apkanalysis

// backend_jadx_stub_test — 用契约等价的 jadx 替身验证可插拔链路:
// Health(--version) → SmokeJadx(内嵌 smoke.apk 走 mini-dex) →
// Decompile(真实 fixture APK + buildMiniDex 热路径 + 输出解析)。
// 真实 jadx 的行为契约:--version 打印版本;--single-class <cls> --no-res
// -d <dir> <input> 在 <dir>/sources/ 下产出 <类名>.java。

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const jadxStubScript = `#!/bin/sh
# 契约替身:实现 JadxBackend 依赖的 jadx CLI 子集
if [ "$1" = "--version" ]; then
  echo "jadx 1.5.2-stub"
  exit 0
fi
cls=""; dir=""; input=""
while [ $# -gt 0 ]; do
  case "$1" in
    --single-class) cls="$2"; shift 2 ;;
    --no-res) shift ;;
    -d) dir="$2"; shift 2 ;;
    *) input="$1"; shift ;;
  esac
done
name=$(printf '%s' "$cls" | sed 's/^L//; s/;$//')
magic=$(head -c 3 "$input")
size=$(wc -c < "$input")
base=$(basename "$input")
mkdir -p "$dir/sources"
cat > "$dir/sources/$name.java" <<EOF2
// stub-jadx marker input=$base magic=$magic size=$size
public class Stub { }
EOF2
exit 0
`

func TestJadxBackendStubContract(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("替身脚本是 POSIX shell")
	}
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stub := filepath.Join(binDir, "jadx")
	if err := os.WriteFile(stub, []byte(jadxStubScript), 0o755); err != nil {
		t.Fatal(err)
	}

	b := NewJadxBackend("jadx-stub", stub, "", 1)
	ctx := context.Background()

	// 1) Health:命令存在 + --version 契约
	if err := b.Health(ctx); err != nil {
		t.Fatalf("health: %v", err)
	}

	// 2) SmokeJadx:内嵌 smoke.apk 全链(含 mini-dex 抽取)
	name, err := b.SmokeJadx(ctx)
	if err != nil {
		t.Fatalf("smoke: %v", err)
	}
	if !strings.Contains(name, "jadx") {
		t.Errorf("smoke name = %q", name)
	}

	// 3) Decompile:真实 fixture APK + buildMiniDex 热路径
	apk := filepath.Join("..", "apkengine", "findrefs", "testdata", "ByteStreamReader.apk")
	out, err := b.Decompile(ctx, apk, "LByteStreamReader;")
	if err != nil {
		t.Fatalf("decompile: %v", err)
	}
	// 替身把收到的输入注入输出:证明 jadx 收到的是 mini.dex(DEX 魔数)
	for _, want := range []string{"stub-jadx marker", "input=mini.dex", "magic=dex"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	// 类源码落在 sources/<类名>.java
	if !strings.Contains(out, "public class Stub") {
		t.Errorf("stub source body missing:\n%s", out)
	}
	_ = fmt.Sprint()
}
