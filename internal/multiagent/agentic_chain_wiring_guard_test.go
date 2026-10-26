package multiagent

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestAgenticTailChainWiringGuard —— 生产链「建了没接线」接线守门（防回潮）。
//
// 背景（2026-10-13 实测）：输入遥测 / 模型输入软预算 / tool_search 结果修复 / 调用参数消毒
// 四个中间件当时只挂在经典链 `eino_chat_model_tail_middleware.go`，而经典链全仓唯一调用点是
// runner.go 的 PlanExecute 重写钩子（实测约占 8% 调用量）⇒ 生产（agentic 链）等于零度量、
// 零软预算、零消毒。修复后本测试钉死「生产链必须挂载」这一契约，语义测试（只测纯函数）
// 测不到装配断线，这里用定点扫源兜底。
//
// 来源：FindU 同上游 fork 的 consumer_wiring_guard_test.go / always_visible_wiring_guard_test.go。
func TestAgenticTailChainWiringGuard(t *testing.T) {
	const file = "eino_agentic_chat_model_tail_middleware.go"
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", file, err)
	}
	body := wiringGuardFunctionBody(t, string(data), "func appendEinoAgenticChatModelTailMiddlewares(")

	required := []string{
		"newAgenticModelInputTelemetryMiddleware", // 模型输入遥测（度量先行）
		"contextmgmt.NewAgenticSoftBudget",        // 上下文分级预算软卫兵
		"newAgenticToolSearchUnlockMiddleware",    // tool_search 粘性解锁上报
		"newAgenticSkillReinjectMiddleware",       // 压缩后技能重注入
		"newWallClockSettleMiddlewareWithBudget",  // 墙钟就地收尾
	}
	for _, sym := range required {
		if !strings.Contains(body, sym) {
			t.Errorf("生产链挂载函数体内缺 %s —— 该能力在生产不生效（历史「建了没接线」复发）", sym)
		}
	}

	// 顺序契约：收尾指令必须最后，才不会被后续中间件重排掉；技能重注入恒在其前一位。
	appends := wiringGuardAppendSequence(body)
	if len(appends) < 2 {
		t.Fatalf("从挂载函数体解析出 %d 个 append(handlers, ...)，期望 >= 2 —— 解析失效或装配被改空", len(appends))
	}
	if last := appends[len(appends)-1]; last != "newWallClockSettleMiddlewareWithBudget" {
		t.Errorf("链末位 = %s，期望 newWallClockSettleMiddlewareWithBudget（收尾必须最后）", last)
	}
	if second := appends[len(appends)-2]; second != "newAgenticSkillReinjectMiddleware" {
		t.Errorf("链倒数第二位 = %s，期望 newAgenticSkillReinjectMiddleware", second)
	}
}

// TestAgenticTailChainStaysInvokedEverywhere —— 生产链装配点不被摘除。
// agentic 链由 eino_single_runner / eino_orchestration / runner 多处调用；若装配点只剩一处
// 或归零，说明生产链被绕过（典型：改回经典链）。至少须有 2 处非测试调用点。
func TestAgenticTailChainStaysInvokedEverywhere(t *testing.T) {
	const sym = "appendEinoAgenticChatModelTailMiddlewares("
	const definitionFile = "eino_agentic_chat_model_tail_middleware.go"

	root := filepath.Join("..", "..")
	refs := 0
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == definitionFile {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		for _, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue // 注释里提到不算接线
			}
			if strings.Contains(line, sym) {
				refs++
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("扫描 internal/ 失败: %v", err)
	}
	if refs < 2 {
		t.Errorf("appendEinoAgenticChatModelTailMiddlewares 非测试调用点仅 %d 处（期望 >= 2）—— 生产链装配被绕过", refs)
	}
}

// wiringGuardFunctionBody 取函数体（剥注释行 + 括号配平截断），避免「注释里提到函数」造成假绿。
func wiringGuardFunctionBody(t *testing.T, src, sig string) string {
	t.Helper()
	idx := strings.Index(src, sig)
	if idx < 0 {
		t.Fatalf("未找到函数签名 %q —— 装配被移走，请同步本守门", sig)
	}
	lines := strings.Split(src[idx:], "\n")
	var b strings.Builder
	depth, started := 0, false
	for _, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(ln), "//") {
			continue
		}
		b.WriteString(ln)
		b.WriteByte('\n')
		depth += strings.Count(ln, "{") - strings.Count(ln, "}")
		if strings.Contains(ln, "{") {
			started = true
		}
		if started && depth <= 0 {
			break
		}
	}
	return b.String()
}

// wiringGuardAppendSequence 按出现顺序提取 append(handlers, X) 的 X 标识符。
func wiringGuardAppendSequence(body string) []string {
	re := regexp.MustCompile(`append\(handlers,\s*([A-Za-z0-9_]+)`)
	ms := re.FindAllStringSubmatch(body, -1)
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m[1])
	}
	return out
}
