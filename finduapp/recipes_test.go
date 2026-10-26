package finduapp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	actool "finduai/tools"
)

// TestMain 让配方加载能找到配置文件：`go test ./finduapp` 的 cwd 是包目录，
// 而 configPath() 默认取 ./config-findu.yaml（仓库根）。容器内跑测试时
// FINDU_CONFIG 已被显式指到 /app/config-findu.yaml，这里不覆盖。
func TestMain(m *testing.M) {
	if os.Getenv("FINDU_CONFIG") == "" {
		if _, err := os.Stat("../config-findu.yaml"); err == nil {
			if abs, err := filepath.Abs("../config-findu.yaml"); err == nil {
				os.Setenv("FINDU_CONFIG", abs)
			}
		}
	}
	os.Exit(m.Run())
}

// TestRecipeToolsRegistered 验证配方桥接的结构正确性：tools 与 names 一一对应、
// 每个工具都有名字与 schema。不依赖外部二进制是否装全（装不全只影响数量）。
func TestRecipeToolsRegistered(t *testing.T) {
	tools, names, err := RecipeTools()
	if err != nil {
		t.Fatalf("RecipeTools 失败: %v", err)
	}
	if len(tools) == 0 {
		t.Fatal("没有注册任何配方工具")
	}
	if len(tools) != len(names) {
		t.Fatalf("tools 与 names 长度不一致: %d vs %d", len(tools), len(names))
	}
	for i := range tools {
		if tools[i].Name() != names[i] {
			t.Fatalf("第 %d 项不对齐: %q vs %q", i, tools[i].Name(), names[i])
		}
		if tools[i].Name() == "" || tools[i].Description() == "" {
			t.Fatalf("第 %d 项缺名字或描述", i)
		}
		if s := tools[i].InputSchema(); s == nil || len(s) == 0 {
			t.Fatalf("%s 的 schema 为空", tools[i].Name())
		}
	}
	t.Logf("注册 %d 个配方: %s", len(names), strings.Join(names, ","))
}

// recipeByName 在注册结果里按名字取工具。
func recipeByName(t *testing.T, name string) actool.CoreTool {
	t.Helper()
	tools, names, err := RecipeTools()
	if err != nil {
		t.Fatalf("RecipeTools 失败: %v", err)
	}
	for i, n := range names {
		if n == name {
			return tools[i]
		}
	}
	t.Skipf("配方 %s 未注册（二进制未安装？）", name)
	return nil
}

// TestRecipeInvokeNmap 真跑一次 nmap（扫 127.0.0.1 的 22 端口），验证从
// CoreTool 入参 → 配方参数规则 → 进程执行 → 结果回填这条链路是通的。
// 默认跳过，设 RECIPE_INVOKE=1 才执行。
func TestRecipeInvokeNmap(t *testing.T) {
	if os.Getenv("RECIPE_INVOKE") == "" {
		t.Skip("设 RECIPE_INVOKE=1 才真实执行 nmap")
	}
	tool := recipeByName(t, "nmap")
	in, _ := json.Marshal(map[string]any{"target": "127.0.0.1", "ports": "22"})
	res, err := tool.Call(context.Background(), in, &actool.ToolContext{})
	if err != nil {
		t.Fatalf("调用失败: %v", err)
	}
	out := res.Flatten()
	t.Logf("nmap 输出前 300 字:\n%.300s", out)
	if strings.TrimSpace(out) == "" {
		t.Fatal("nmap 没有任何输出")
	}
	if res.IsError && !strings.Contains(out, "nmap") {
		t.Fatalf("nmap 报错且输出不可用: %s", out)
	}
}
