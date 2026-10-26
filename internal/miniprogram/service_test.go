package miniprogram

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestNormalizeFindingPath(t *testing.T) {
	cases := []struct {
		name      string
		workspace string
		in        string
		want      string
	}{
		{"相对工作目录+相对落库路径(历史数据)", "data/miniprogram_analysis", "data/miniprogram_analysis/abc123/app.js", "abc123/app.js"},
		{"绝对工作目录+绝对落库路径", "/tmp/ws/abc123", "/tmp/ws/abc123/utils/config.json", "utils/config.json"},
		{"工作目录不可用时启发式剥前缀", "", "data/miniprogram_analysis/abc123/app.js", "app.js"},
		{"已是相对路径原样返回", "data/miniprogram_analysis", "app.js", "app.js"},
		{"工作目录之外且无标记前缀原样返回", "/tmp/ws/abc123", "/etc/passwd", "/etc/passwd"},
		{"空路径", "data/miniprogram_analysis", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizeFindingPath(tc.workspace, tc.in)
			if got != tc.want {
				t.Fatalf("NormalizeFindingPath(%q, %q) = %q, want %q", tc.workspace, tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalizeFindingPathWindowsSeparators(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("仅 Windows 分隔符形态需要验证")
	}
	p := filepath.ToSlash(`data\miniprogram_analysis\abc123\app.js`)
	if got := NormalizeFindingPath(`data\miniprogram_analysis`, p); got != "abc123/app.js" {
		t.Fatalf("got %q", got)
	}
}
