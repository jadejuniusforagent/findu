package multiagent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"finduai/internal/skillpackage"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"
)

// 技能声明式 MCP 解锁（P2，对照 FINDU/norma 的 frontmatter mcps 门控）。
//
// 背景：FindUAI 开启 tool_search 后，未列入常驻白名单的工具（nmap / nuclei / sqlmap /
// ffuf / dirsearch 等）会进入「动态池」——模型必须先调用 tool_search 才能看到并调用它们，
// 这依赖模型「记得」主动去搜。技能本身往往已经写明了该用哪些工具。
//
// 做法：技能在 SKILL.md frontmatter 里声明 mcps: [nmap, sqlmap]；当该技能被 skill 工具
// 加载后，本中间件把对应工具从动态池「自动解锁」回 state.ToolInfos（模型可见工具面），
// 模型无需再猜工具名去 tool_search。
//
// 放置次序：必须位于 toolsearch 中间件「之后」（handlers 切片中更靠后）。
// Eino 的 toolsearch 在首次调用时 stripDynamicTools 摘掉动态工具，之后仅在会话中发现
// tool_search 结果时「追加」；把解锁放在其后，追加结果不会被再次摘掉，并随 state 持久化到
// 本 run 后续的每次模型调用。
//
// 安全性：仅向 state.ToolInfos 追加 ToolInfo（可见性），不触碰工具执行链——动态工具本就已
// 在 runCtx.Tools 中可执行。命中的工具若本已可见、或该名字在本轮工具集中不存在，则为 no-op。

type skillMCPUnlockMiddleware struct {
	*adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]
	skillToolName string
	skillsDir     string
	toolInfos     map[string]*schema.ToolInfo // 运行时工具名（小写） -> ToolInfo
	logger        *zap.Logger

	mu    sync.Mutex
	index map[string][]string // 技能名（小写） -> 声明的 mcps
}

// newSkillMCPUnlockMiddleware 构建解锁中间件；enabled 为 false、tools 为空或 skillsDir 为空时返回 nil。
func newSkillMCPUnlockMiddleware(
	ctx context.Context,
	skillToolName string,
	skillsDir string,
	tools []tool.BaseTool,
	enabled bool,
	logger *zap.Logger,
) adk.TypedChatModelAgentMiddleware[*schema.AgenticMessage] {
	if !enabled {
		return nil
	}
	dir := strings.TrimSpace(skillsDir)
	if dir == "" || len(tools) == 0 {
		return nil
	}
	infos := make(map[string]*schema.ToolInfo, len(tools))
	for _, t := range tools {
		if t == nil {
			continue
		}
		info, err := t.Info(ctx)
		if err != nil || info == nil || strings.TrimSpace(info.Name) == "" {
			continue
		}
		infos[strings.ToLower(strings.TrimSpace(info.Name))] = info
	}
	if len(infos) == 0 {
		return nil
	}
	name := strings.TrimSpace(skillToolName)
	if name == "" {
		name = "skill"
	}
	return &skillMCPUnlockMiddleware{
		TypedBaseChatModelAgentMiddleware: &adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]{},
		skillToolName:                     name,
		skillsDir:                         dir,
		toolInfos:                         infos,
		logger:                            logger,
	}
}

func (m *skillMCPUnlockMiddleware) BeforeModelRewriteState(
	ctx context.Context,
	state *adk.TypedChatModelAgentState[*schema.AgenticMessage],
	mc *adk.TypedModelContext[*schema.AgenticMessage],
) (context.Context, *adk.TypedChatModelAgentState[*schema.AgenticMessage], error) {
	_ = mc
	if m == nil || state == nil || len(state.Messages) == 0 {
		return ctx, state, nil
	}

	loaded := m.loadedSkills(state.Messages)
	if len(loaded) == 0 {
		return ctx, state, nil
	}

	visible := make(map[string]struct{}, len(state.ToolInfos))
	for _, ti := range state.ToolInfos {
		if ti != nil && strings.TrimSpace(ti.Name) != "" {
			visible[strings.ToLower(strings.TrimSpace(ti.Name))] = struct{}{}
		}
	}

	var unlocked []string
	for _, skill := range loaded {
		for _, entry := range m.skillMCPs(skill) {
			for name, info := range m.toolInfos {
				if !mcpEntryMatchesTool(entry, name) {
					continue
				}
				if _, ok := visible[name]; ok {
					continue
				}
				state.ToolInfos = append(state.ToolInfos, info)
				visible[name] = struct{}{}
				unlocked = append(unlocked, name)
			}
		}
	}

	if len(unlocked) > 0 && m.logger != nil {
		m.logger.Info("eino skill mcp unlock: 按技能声明解锁动态工具",
			zap.Strings("skills", loaded), zap.Strings("unlocked", unlocked))
	}
	return ctx, state, nil
}

// loadedSkills 从会话历史里收集被 skill 工具加载过的技能名（按出现顺序去重）。
// 依据：assistant 消息中的 FunctionToolCall，其 Name == skill 工具名，Arguments 形如 {"skill":"<name>"}。
func (m *skillMCPUnlockMiddleware) loadedSkills(msgs []*schema.AgenticMessage) []string {
	target := strings.TrimSpace(m.skillToolName)
	if target == "" {
		target = "skill"
	}
	seen := make(map[string]struct{})
	out := make([]string, 0, 4)
	for _, msg := range msgs {
		for _, call := range agenticFunctionToolCalls(msg) {
			if call == nil || !strings.EqualFold(strings.TrimSpace(call.Name), target) {
				continue
			}
			skill := parseSkillToolArgument(call.Arguments)
			if skill == "" {
				continue
			}
			k := strings.ToLower(skill)
			if _, ok := seen[k]; ok {
				continue
			}
			seen[k] = struct{}{}
			out = append(out, skill)
		}
	}
	return out
}

func parseSkillToolArgument(arguments string) string {
	raw := strings.TrimSpace(arguments)
	if raw == "" || raw == "{}" || raw == "null" {
		return ""
	}
	var args struct {
		Skill string `json:"skill"`
	}
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return ""
	}
	return strings.TrimSpace(args.Skill)
}

// skillMCPs 返回某技能声明的 mcps；索引未命中时重建一次（覆盖运行时新建的技能）。
func (m *skillMCPUnlockMiddleware) skillMCPs(skill string) []string {
	key := strings.ToLower(strings.TrimSpace(skill))
	if key == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.index == nil {
		m.index = m.scanIndex()
	}
	if v, ok := m.index[key]; ok {
		return v
	}
	m.index = m.scanIndex()
	return m.index[key]
}

// scanIndex 扫描 skillsDir 一级子目录的 SKILL.md，建立 技能名 -> mcps 索引。
func (m *skillMCPUnlockMiddleware) scanIndex() map[string][]string {
	idx := make(map[string][]string)
	entries, err := os.ReadDir(m.skillsDir)
	if err != nil {
		if m.logger != nil {
			m.logger.Warn("eino skill mcp unlock: 读取技能目录失败",
				zap.String("dir", m.skillsDir), zap.Error(err))
		}
		return idx
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sub := filepath.Join(m.skillsDir, e.Name())
		raw, readErr := os.ReadFile(filepath.Join(sub, "SKILL.md"))
		if readErr != nil {
			continue
		}
		mcps := skillpackage.ParseSkillMCPs(raw)
		if len(mcps) == 0 {
			continue
		}
		name := e.Name()
		if manifest, _, perr := skillpackage.ParseSkillMD(raw); perr == nil && manifest != nil && strings.TrimSpace(manifest.Name) != "" {
			name = strings.TrimSpace(manifest.Name)
		}
		idx[strings.ToLower(name)] = mcps
	}
	return idx
}

// mcpEntryMatchesTool 判断技能声明的 mcps 条目是否命中某运行时工具名。
// 命中规则：
//   - 完全相同（大小写不敏感）；
//   - 条目为外部 MCP 服务名时，命中其命名空间下的工具：<entry>__<tool>；
//   - 条目写成配置态 "server::tool" 时，等价于运行时 "server__tool"。
func mcpEntryMatchesTool(entry, toolName string) bool {
	e := strings.TrimSpace(entry)
	n := strings.TrimSpace(toolName)
	if e == "" || n == "" {
		return false
	}
	eLower := strings.ToLower(e)
	nLower := strings.ToLower(n)
	if eLower == nLower {
		return true
	}
	if strings.Contains(eLower, "::") {
		if strings.ToLower(strings.ReplaceAll(eLower, "::", "__")) == nLower {
			return true
		}
	}
	if strings.HasPrefix(nLower, eLower+"__") {
		return true
	}
	return false
}
