// Command layercheck 校验分层依赖：core 不得依赖 domain/plugins/cmd，domain 不得依赖 plugins，
// 插件不得依赖 server/db/cmd，也不得直接 import 其他插件。
//
// 用法：go run ./cmd/layercheck
// 违规时打印明细并以退出码 1 结束，可直接作为 CI 闸门。
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func main() {
	type rule struct {
		target string
		forbid []string
	}

	rules := []rule{
		{target: "finduai/core/", forbid: []string{"finduai/domain/", "finduai/plugins/", "finduai/cmd/"}},
		{target: "finduai/domain/", forbid: []string{"finduai/plugins/"}},
		{target: "finduai/plugins/", forbid: []string{"finduai/server", "finduai/db", "finduai/cmd/"}},
	}

	// 模板里不放引号，避免 shell 与转义问题：输出 包路径|导入1|导入2...
	fmtSpec := "{{.ImportPath}}{{range .Imports}}|{{.}}{{end}}"
	targets := []string{"./core/...", "./domain/..."}
	if st, err := os.Stat("plugins"); err == nil && st.IsDir() {
		targets = append(targets, "./plugins/...")
	}
	args := append([]string{"list", "-f", fmtSpec}, targets...)
	cmd := exec.Command("go", args...)
	out, err := cmd.Output()
	if err != nil {
		fmt.Println("go list 失败:", err)
		os.Exit(2)
	}

	violations := 0
	checked := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), string(rune(10))) {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "|")
		pkg := fields[0]
		imports := fields[1:]
		checked++
		for _, r := range rules {
			if !strings.HasPrefix(pkg, r.target) {
				continue
			}
			for _, imp := range imports {
				for _, bad := range r.forbid {
					if strings.HasPrefix(imp, bad) {
						fmt.Println("VIOLATION:", pkg, "->", imp)
						violations++
					}
				}
				// 插件之间不得直接 import：只在同属 plugins 前缀时比较插件名。
				if strings.HasPrefix(pkg, "finduai/plugins/") && strings.HasPrefix(imp, "finduai/plugins/") {
					if pluginName(pkg) != pluginName(imp) {
						fmt.Println("VIOLATION:", pkg, "->", imp, "(跨插件直接依赖)")
						violations++
					}
				}
			}
		}
	}

	if violations > 0 {
		fmt.Println("分层检查失败：违规", violations, "处；检查包数", checked)
		os.Exit(1)
	}
	fmt.Println("分层检查通过：检查包数", checked, "；core 不依赖 domain/plugins/cmd，domain 不依赖 plugins，plugins 不依赖 server/db/cmd 且互不直连")
}

// pluginName 取 finduai/plugins/<name> 里的 <name>。
func pluginName(pkg string) string {
	rest := strings.TrimPrefix(pkg, "finduai/plugins/")
	if i := strings.Index(rest, "/"); i >= 0 {
		return rest[:i]
	}
	return rest
}
