package smali

// smali_roundtrip_test — 标准 smali roundtrip 门禁:用 StandardClass 输出写入
// .smali 文件,调 smali.jar 汇编为 DEX,验证汇编成功。
// 门控:ANDROID_SMALI_JAR 指定 smali.jar 路径;未设置时跳过。
// 安装: go install github.com/google/smali@latest  或从 GitHub Releases 下载。

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"finduai/internal/apkengine/dex"
	"finduai/internal/apkengine/zipprobe"
)

func TestStandardSmaliRoundtrip(t *testing.T) {
	jarPath := strings.TrimSpace(os.Getenv("ANDROID_SMALI_JAR"))
	if jarPath == "" {
		t.Skip("ANDROID_SMALI_JAR not set; set to smali.jar path to enable roundtrip test")
	}
	if _, err := os.Stat(jarPath); err != nil {
		t.Fatalf("smali.jar not found: %s", jarPath)
	}
	// 找 java
	javaPath := strings.TrimSpace(os.Getenv("ANDROID_JAVA_PATH"))
	if javaPath == "" {
		javaPath = "java"
	}

	// 遍历全部 fixture APK,对每个类跑 roundtrip
	matches, _ := filepath.Glob(filepath.Join("..", "findrefs", "testdata", "*.apk"))
	if len(matches) == 0 {
		t.Skip("no fixtures")
	}
	for _, apkPath := range matches {
		t.Run(filepath.Base(apkPath), func(t *testing.T) {
			r, err := zipprobe.Open(apkPath)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			entries, err := zipprobe.ParseCDDexEntries(r)
			if err != nil {
				t.Fatal(err)
			}
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
					for i := 0; i < int(d.NumClasses()); i++ {
						cls := d.GetClassByIdx(uint32(i))
						roundtripOneClass(t, d, cls, javaPath, jarPath)
					}
				}
			}
		})
	}
}

func roundtripOneClass(t *testing.T, d *dex.DEX, cls *dex.Class, javaPath, jarPath string) {
	t.Helper()
	clsName := cls.Fullname()
	// Lcom/foo/Bar; → smali/com/foo/Bar.smali
	relPath := strings.TrimPrefix(clsName, "L")
	relPath = strings.TrimSuffix(relPath, ";")
	relPath = strings.ReplaceAll(relPath, "/", string(filepath.Separator))

	tmpDir := t.TempDir()
	smaliDir := filepath.Join(tmpDir, "smali")
	smaliFile := filepath.Join(smaliDir, relPath+".smali")
	if err := os.MkdirAll(filepath.Dir(smaliFile), 0o755); err != nil {
		t.Fatal(err)
	}

	lines := StandardClass(d, cls)
	smaliText := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(smaliFile, []byte(smaliText), 0o644); err != nil {
		t.Fatal(err)
	}

	// smali.jar assemble: java -jar smali.jar a <dir> -o <out.dex>
	outDex := filepath.Join(tmpDir, "out.dex")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, javaPath, "-jar", jarPath, "a", smaliDir, "-o", outDex)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Errorf("smali assemble failed for %s: %v\nstderr: %s\nsmali:\n%s",
			clsName, err, stderr.String(), smaliText[:min(len(smaliText), 500)])
		return
	}
	// 验证输出 DEX 非空
	st, err := os.Stat(outDex)
	if err != nil || st.Size() == 0 {
		t.Errorf("assembled DEX missing or empty for %s", clsName)
	}
}
