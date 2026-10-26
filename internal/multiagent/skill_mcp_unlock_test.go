package multiagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"finduai/internal/config"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

func writeTestSkill(t *testing.T, root, dir, frontmatter string) {
	t.Helper()
	sub := filepath.Join(root, dir)
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", sub, err)
	}
	content := "---\n" + frontmatter + "\n---\n\n# body\n"
	if err := os.WriteFile(filepath.Join(sub, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
}

func visibleToolNames(infos []*schema.ToolInfo) map[string]bool {
	out := make(map[string]bool, len(infos))
	for _, ti := range infos {
		if ti != nil {
			out[ti.Name] = true
		}
	}
	return out
}

func TestSkillMCPUnlockUnlocksDeclaredTools(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeTestSkill(t, root, "recon", "name: attack-surface-recon\ndescription: 侦察\nmcps: [nmap, sqlmap]")

	tools := []tool.BaseTool{
		stubTool{name: "exec"},
		stubTool{name: "nmap"},
		stubTool{name: "sqlmap"},
		stubTool{name: "nuclei"},
	}
	mw := newSkillMCPUnlockMiddleware(ctx, "skill", root, tools, true, nil)
	if mw == nil {
		t.Fatal("middleware should be built when enabled with tools+skillsDir")
	}

	state := &adk.TypedChatModelAgentState[*schema.AgenticMessage]{
		Messages: []*schema.AgenticMessage{
			agenticAssistantToolCall("c1", "skill", `{"skill":"attack-surface-recon"}`),
		},
		ToolInfos: []*schema.ToolInfo{{Name: "exec"}},
	}
	_, out, err := mw.BeforeModelRewriteState(ctx, state, nil)
	if err != nil {
		t.Fatalf("BeforeModelRewriteState: %v", err)
	}
	got := visibleToolNames(out.ToolInfos)
	if !got["nmap"] || !got["sqlmap"] {
		t.Fatalf("declared tools should be unlocked, got %v", got)
	}
	if got["nuclei"] {
		t.Fatalf("undeclared tool nuclei must stay hidden, got %v", got)
	}
	if !got["exec"] {
		t.Fatalf("pre-existing tool must remain visible, got %v", got)
	}
}

func TestSkillMCPUnlockIsIdempotent(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeTestSkill(t, root, "recon", "name: recon\ndescription: 侦察\nmcps: [nmap]")

	mw := newSkillMCPUnlockMiddleware(ctx, "skill", root, []tool.BaseTool{stubTool{name: "nmap"}}, true, nil)
	state := &adk.TypedChatModelAgentState[*schema.AgenticMessage]{
		Messages:  []*schema.AgenticMessage{agenticAssistantToolCall("c1", "skill", `{"skill":"recon"}`)},
		ToolInfos: []*schema.ToolInfo{{Name: "exec"}},
	}
	for i := 0; i < 3; i++ {
		_, out, err := mw.BeforeModelRewriteState(ctx, state, nil)
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		n := 0
		for _, ti := range out.ToolInfos {
			if ti.Name == "nmap" {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("nmap should appear exactly once after %d calls, got %d", i+1, n)
		}
	}
}

func TestSkillMCPUnlockNoopWithoutSkillCall(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeTestSkill(t, root, "recon", "name: recon\ndescription: 侦察\nmcps: [nmap]")

	mw := newSkillMCPUnlockMiddleware(ctx, "skill", root, []tool.BaseTool{stubTool{name: "nmap"}}, true, nil)
	state := &adk.TypedChatModelAgentState[*schema.AgenticMessage]{
		Messages:  []*schema.AgenticMessage{agenticAssistantToolCall("c1", "exec", `{"command":"id"}`)},
		ToolInfos: []*schema.ToolInfo{{Name: "exec"}},
	}
	_, out, err := mw.BeforeModelRewriteState(ctx, state, nil)
	if err != nil {
		t.Fatalf("BeforeModelRewriteState: %v", err)
	}
	if len(out.ToolInfos) != 1 || out.ToolInfos[0].Name != "exec" {
		t.Fatalf("no skill call => no unlock, got %v", visibleToolNames(out.ToolInfos))
	}
}

func TestSkillMCPUnlockDisabledReturnsNil(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeTestSkill(t, root, "recon", "name: recon\ndescription: 侦察\nmcps: [nmap]")
	if mw := newSkillMCPUnlockMiddleware(ctx, "skill", root, []tool.BaseTool{stubTool{name: "nmap"}}, false, nil); mw != nil {
		t.Fatal("disabled gating must return nil middleware")
	}
	if mw := newSkillMCPUnlockMiddleware(ctx, "skill", "", []tool.BaseTool{stubTool{name: "nmap"}}, true, nil); mw != nil {
		t.Fatal("empty skills dir must return nil middleware")
	}
	if mw := newSkillMCPUnlockMiddleware(ctx, "skill", root, nil, true, nil); mw != nil {
		t.Fatal("no tools must return nil middleware")
	}
}

func TestSkillMCPUnlockExternalMCPNamespace(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeTestSkill(t, root, "ext", "name: ext-recon\ndescription: 外部 MCP\nmcps: [myrecon]")

	tools := []tool.BaseTool{
		stubTool{name: "myrecon__scan"},
		stubTool{name: "myrecon__report"},
		stubTool{name: "other__scan"},
	}
	mw := newSkillMCPUnlockMiddleware(ctx, "skill", root, tools, true, nil)
	state := &adk.TypedChatModelAgentState[*schema.AgenticMessage]{
		Messages: []*schema.AgenticMessage{agenticAssistantToolCall("c1", "skill", `{"skill":"ext-recon"}`)},
	}
	_, out, err := mw.BeforeModelRewriteState(ctx, state, nil)
	if err != nil {
		t.Fatalf("BeforeModelRewriteState: %v", err)
	}
	got := visibleToolNames(out.ToolInfos)
	if !got["myrecon__scan"] || !got["myrecon__report"] {
		t.Fatalf("namespace tools of declared MCP should unlock, got %v", got)
	}
	if got["other__scan"] {
		t.Fatalf("unrelated namespace must stay hidden, got %v", got)
	}
}

func TestMCPSEntryMatchesTool(t *testing.T) {
	cases := []struct {
		entry string
		tool  string
		want  bool
	}{
		{"nmap", "nmap", true},
		{"NMAP", "nmap", true},
		{"myrecon", "myrecon__scan", true},
		{"myrecon", "myreconx__scan", false},
		{"myrecon::scan", "myrecon__scan", true},
		{"myrecon", "other__scan", false},
		{"", "nmap", false},
		{"nmap", "", false},
	}
	for _, c := range cases {
		if got := mcpEntryMatchesTool(c.entry, c.tool); got != c.want {
			t.Errorf("mcpEntryMatchesTool(%q,%q)=%v want %v", c.entry, c.tool, got, c.want)
		}
	}
}

// 装配级：skillsRoot 非空且 toolsearch 激活时，prependEinoAgenticMiddlewares 必须把
// 技能 MCP 解锁中间件追加到 handler 链「末尾」（保证排在 toolsearch 之后）。
func TestPrependEinoAgenticMiddlewaresMountsSkillMCPUnlock(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeTestSkill(t, root, "recon", "name: recon\ndescription: d\nmcps: [nmap]")

	mw := &config.MultiAgentEinoMiddlewareConfig{
		ToolSearchEnable:        true,
		ToolSearchMinTools:      20,
		ToolSearchAlwaysVisible: 5,
	}
	_, handlers, toolSearchActive, err := prependEinoAgenticMiddlewares(ctx, mw, einoMWMain, stubTools(25), nil, root, "skill", "conv-x", "", nil)
	if err != nil {
		t.Fatalf("prependEinoAgenticMiddlewares: %v", err)
	}
	if !toolSearchActive {
		t.Fatal("tool_search should be active")
	}
	last := handlers[len(handlers)-1]
	if _, ok := last.(*skillMCPUnlockMiddleware); !ok {
		t.Fatalf("last handler should be *skillMCPUnlockMiddleware, got %T", last)
	}

	// 关闭开关 → 不挂载
	off := false
	mw2 := &config.MultiAgentEinoMiddlewareConfig{
		ToolSearchEnable:        true,
		ToolSearchMinTools:      20,
		ToolSearchAlwaysVisible: 5,
		SkillMCPGating:          &off,
	}
	_, handlers2, _, err := prependEinoAgenticMiddlewares(ctx, mw2, einoMWMain, stubTools(25), nil, root, "skill", "conv-x", "", nil)
	if err != nil {
		t.Fatalf("prepend (gating off): %v", err)
	}
	if _, ok := handlers2[len(handlers2)-1].(*skillMCPUnlockMiddleware); ok {
		t.Fatal("gating off must not mount unlock middleware")
	}
}

// 真实数据验证：用仓库内的 skills/ 与 tools/ 目录，验证标注后的技能确实能解锁对应工具。
// 工作目录为包目录（internal/multiagent），故仓库根为 ../../。
func TestSkillMCPUnlockAgainstRealSkills(t *testing.T) {
	ctx := context.Background()
	skillsDir := filepath.Join("..", "..", "skills")
	toolsDir := filepath.Join("..", "..", "configs", "toolrecipes")
	if st, err := os.Stat(skillsDir); err != nil || !st.IsDir() {
		t.Skipf("skills dir not available: %v", err)
	}
	entries, err := os.ReadDir(toolsDir)
	if err != nil {
		t.Skipf("tools dir not available: %v", err)
	}
	var tools []tool.BaseTool
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := strings.TrimSuffix(strings.TrimSuffix(e.Name(), ".yaml"), ".yml")
		if name == "" || name == e.Name() {
			continue
		}
		tools = append(tools, stubTool{name: name})
	}
	if len(tools) < 50 {
		t.Fatalf("expected >=50 real tools, got %d", len(tools))
	}

	unlock := newSkillMCPUnlockMiddleware(ctx, "skill", skillsDir, tools, true, nil)
	if unlock == nil {
		t.Fatal("unlock middleware should build against real skills dir")
	}

	// 1) 侦察技能已标注 → 应解锁 nmap/nuclei/subfinder 等（这些都不在常驻白名单里，属动态池）
	state := &adk.TypedChatModelAgentState[*schema.AgenticMessage]{
		Messages:  []*schema.AgenticMessage{agenticAssistantToolCall("c1", "skill", `{"skill":"attack-surface-recon"}`)},
		ToolInfos: []*schema.ToolInfo{{Name: "exec"}},
	}
	_, out, err := unlock.BeforeModelRewriteState(ctx, state, nil)
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	got := visibleToolNames(out.ToolInfos)
	for _, want := range []string{"nmap", "nuclei", "subfinder", "amass", "katana"} {
		if !got[want] {
			t.Errorf("attack-surface-recon 应解锁 %q，实际可见=%v", want, got)
		}
	}

	// 2) 未标注 mcps 的技能（redteam-opsec）→ 不应解锁任何工具
	state2 := &adk.TypedChatModelAgentState[*schema.AgenticMessage]{
		Messages:  []*schema.AgenticMessage{agenticAssistantToolCall("c2", "skill", `{"skill":"redteam-opsec"}`)},
		ToolInfos: []*schema.ToolInfo{{Name: "exec"}},
	}
	_, out2, err := unlock.BeforeModelRewriteState(ctx, state2, nil)
	if err != nil {
		t.Fatalf("rewrite2: %v", err)
	}
	if len(out2.ToolInfos) != 1 {
		t.Errorf("未标注技能不应解锁工具，实际=%v", visibleToolNames(out2.ToolInfos))
	}
}
