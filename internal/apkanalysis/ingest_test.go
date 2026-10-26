package apkanalysis

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeZip 造一个最小的合法 zip（APK/XAPK/APKS 同族）。
func writeZip(t *testing.T, dir, name string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	w, err := zw.Create("AndroidManifest.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("stub")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeText(t *testing.T, dir, name string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("not an apk"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestResolveIngestFile(t *testing.T) {
	base := t.TempDir()
	chatDir := filepath.Join(base, "chat_uploads")
	wsDir := filepath.Join(base, "tmp", "workspace", "projects", "p1")
	artifactsDir := filepath.Join(base, "data", "conversation_artifacts")

	wsApk := writeZip(t, wsDir, "downloaded.apk")
	chatApk := writeZip(t, chatDir, "uploaded.apk")
	artifactApk := writeZip(t, artifactsDir, "from-pipeline.apk")
	textFile := writeText(t, wsDir, "notes.txt")
	dirPath := filepath.Join(wsDir, "sub")
	if err := os.MkdirAll(dirPath, 0o755); err != nil {
		t.Fatal(err)
	}

	roots := []IngestRoot{
		{Dir: chatDir, Source: "chat", Label: "对话附件"},
		{Dir: wsDir, Source: "workspace", Label: "本会话工作区"},
		{Dir: artifactsDir, Source: "conversation_artifact", Label: "会话产物"},
	}

	cases := []struct {
		name       string
		raw        string
		bases      []string
		wantAbs    string
		wantSource string
		wantErr    string
	}{
		{"工作区绝对路径", wsApk, nil, wsApk, "workspace", ""},
		{"对话附件绝对路径", chatApk, nil, chatApk, "chat", ""},
		{"会话产物绝对路径", artifactApk, nil, artifactApk, "conversation_artifact", ""},
		{"相对路径按会话工作区解析", "downloaded.apk", []string{wsDir}, wsApk, "workspace", ""},
		{"根目录外拒绝", filepath.Join(base, "elsewhere", "x.apk"), nil, "", "", "不在可收编目录内"},
		{"非 ZIP 拒绝", textFile, nil, "", "", "不是 APK/ZIP"},
		{"目录拒绝", dirPath, nil, "", "", "不是常规文件"},
		{"不存在拒绝", filepath.Join(wsDir, "missing.apk"), nil, "", "", "不存在或不是常规文件"},
		{"空路径拒绝", "  ", nil, "", "", "路径为空"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			abs, source, err := ResolveIngestFile(c.raw, c.bases, roots)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("期望错误包含 %q, got err=%v", c.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("期望成功, got err=%v", err)
			}
			if abs != c.wantAbs || source != c.wantSource {
				t.Fatalf("got (%q, %q), want (%q, %q)", abs, source, c.wantAbs, c.wantSource)
			}
		})
	}
}

// 嵌套根：命中更具体的那个（避免外层的通用根吃掉来源标记）。
func TestResolveIngestFileLongestRootWins(t *testing.T) {
	base := t.TempDir()
	wsRoot := filepath.Join(base, "tmp", "workspace")
	p1 := filepath.Join(wsRoot, "projects", "p1")
	apk := writeZip(t, p1, "a.apk")

	roots := []IngestRoot{
		{Dir: wsRoot, Source: "workspace-any", Label: "工作区根"},
		{Dir: p1, Source: "workspace", Label: "本项目工作区"},
	}
	abs, source, err := ResolveIngestFile(apk, nil, roots)
	if err != nil {
		t.Fatalf("期望成功: %v", err)
	}
	if abs != apk || source != "workspace" {
		t.Fatalf("应命中更具体的项目根: got (%q, %q)", abs, source)
	}
}
