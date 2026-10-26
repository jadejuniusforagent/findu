package multiagent

import (
	"context"
	"strings"
	"sync"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"
)

// 压缩后技能重注入（P2，对照 norma/compaction 的 skills_reinject）。
//
// 问题：技能内容是以「skill 工具结果」的形式进入上下文的。FindUAI 的 summarization
// 中间件在超阈值时会丢弃最旧的完整轮次，技能流程说明会随之消失 —— 于是模型跑到中途
// 就"忘了"它加载过的作业流程。
//
// 做法：
//   1. 后端记录：本轮 run 里被实际 Get 过的技能（名字 + 正文 + 目录）记进 run 级缓存；
//   2. 每轮模型调用前（在 summarization 之后）检查该技能的内容是否还在上下文里
//      （判据：其 BaseDirectory 路径是否仍出现在消息中，Eino 的 skill 结果形如
//       「此 Skill 的目录：<dir>」）；
//   3. 已丢失且未注入过的技能 → 以 user 消息重新注入（带标记，防重复；单轮上限与
//      单技能长度上限，避免反噬上下文预算）。
//
// 缓存经 ctx 传递（不引入签名变更），未启用时整条链路是 no-op。

const (
	skillReinjectMarkerPrefix = "【技能重注入 / Skill re-injected"
	skillReinjectMaxPerCall   = 3
	skillReinjectMaxRunes     = 12000
)

// skillReinjectCache 记录本轮 run 里被加载过的技能。
type skillReinjectCache struct {
	mu     sync.Mutex
	order  []string
	byName map[string]skill.Skill
}

func newSkillReinjectCache() *skillReinjectCache {
	return &skillReinjectCache{byName: make(map[string]skill.Skill)}
}

func (c *skillReinjectCache) record(s skill.Skill) {
	if c == nil {
		return
	}
	name := strings.TrimSpace(s.Name)
	if name == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.byName[name]; !ok {
		c.order = append(c.order, name)
	}
	c.byName[name] = s
}

func (c *skillReinjectCache) snapshot() []skill.Skill {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]skill.Skill, 0, len(c.order))
	for _, n := range c.order {
		out = append(out, c.byName[n])
	}
	return out
}

type skillReinjectCacheCtxKey struct{}

// withSkillReinjectCache 把缓存挂进 run ctx；c 为 nil 时原样返回。
func withSkillReinjectCache(ctx context.Context, c *skillReinjectCache) context.Context {
	if ctx == nil || c == nil {
		return ctx
	}
	return context.WithValue(ctx, skillReinjectCacheCtxKey{}, c)
}

func skillReinjectCacheFromContext(ctx context.Context) *skillReinjectCache {
	if ctx == nil {
		return nil
	}
	c, _ := ctx.Value(skillReinjectCacheCtxKey{}).(*skillReinjectCache)
	return c
}

type agenticSkillReinjectMiddleware struct {
	*adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]
	toolName string
	logger   *zap.Logger
	phase    string
}

func newAgenticSkillReinjectMiddleware(toolName string, logger *zap.Logger, phase string) adk.TypedChatModelAgentMiddleware[*schema.AgenticMessage] {
	name := strings.TrimSpace(toolName)
	if name == "" {
		name = "skill"
	}
	return &agenticSkillReinjectMiddleware{
		TypedBaseChatModelAgentMiddleware: &adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]{},
		toolName:                          name,
		logger:                            logger,
		phase:                             phase,
	}
}

func (m *agenticSkillReinjectMiddleware) BeforeModelRewriteState(
	ctx context.Context,
	state *adk.TypedChatModelAgentState[*schema.AgenticMessage],
	mc *adk.TypedModelContext[*schema.AgenticMessage],
) (context.Context, *adk.TypedChatModelAgentState[*schema.AgenticMessage], error) {
	_ = mc
	if m == nil || state == nil || len(state.Messages) == 0 {
		return ctx, state, nil
	}
	cache := skillReinjectCacheFromContext(ctx)
	if cache == nil {
		return ctx, state, nil
	}
	loaded := cache.snapshot()
	if len(loaded) == 0 {
		return ctx, state, nil
	}

	blob := agenticMessagesAllText(state.Messages)

	var toInject []skill.Skill
	for _, s := range loaded {
		if strings.Contains(blob, skillReinjectMarkerPrefix+"】技能 "+s.Name) {
			continue // 已重注入过
		}
		if skillContentStillPresent(blob, s) {
			continue // 内容还在上下文里
		}
		toInject = append(toInject, s)
		if len(toInject) >= skillReinjectMaxPerCall {
			break
		}
	}
	if len(toInject) == 0 {
		return ctx, state, nil
	}

	out := *state
	msgs := make([]*schema.AgenticMessage, 0, len(state.Messages)+len(toInject))
	msgs = append(msgs, state.Messages...)
	for _, s := range toInject {
		msgs = append(msgs, schema.UserAgenticMessage(formatSkillReinjectNotice(s)))
	}
	out.Messages = msgs

	if m.logger != nil {
		names := make([]string, 0, len(toInject))
		for _, s := range toInject {
			names = append(names, s.Name)
		}
		m.logger.Warn("上下文压缩后重注入技能",
			zap.String("phase", m.phase),
			zap.Strings("skills", names),
			zap.Int("messages", len(msgs)),
		)
	}
	return ctx, &out, nil
}

// skillContentStillPresent 判断该技能的内容是否仍在上下文中。
// Eino 的 skill 工具结果首行为「此 Skill 的目录：<dir>」/「Base directory for this skill: <dir>」，
// 其中 <dir> 即 BaseDirectory；用它做判据比按技能名更准（技能名会出现在 available_skills 索引里）。
func skillContentStillPresent(blob string, s skill.Skill) bool {
	dir := strings.TrimSpace(s.BaseDirectory)
	if dir == "" {
		return true // 拿不到目录就不冒险重注入
	}
	return strings.Contains(blob, dir)
}

func formatSkillReinjectNotice(s skill.Skill) string {
	var sb strings.Builder
	sb.WriteString(skillReinjectMarkerPrefix)
	sb.WriteString("】技能 ")
	sb.WriteString(s.Name)
	sb.WriteString("\n上下文压缩后该技能的流程说明已被丢弃，现重新注入，请继续遵循（勿重复已完成的步骤）：\n\n")

	body := strings.TrimSpace(s.Content)
	if rs := []rune(body); len(rs) > skillReinjectMaxRunes {
		body = string(rs[:skillReinjectMaxRunes]) + "\n...(已截断 / truncated)"
	}
	sb.WriteString(body)
	return sb.String()
}

// agenticMessagesAllText 抽取消息带里的全部文本（含工具调用参数与工具结果内容）。
func agenticMessagesAllText(msgs []*schema.AgenticMessage) string {
	var b strings.Builder
	for _, msg := range msgs {
		b.WriteString(agenticMessageAllText(msg))
		b.WriteByte('\n')
	}
	return b.String()
}

func agenticMessageAllText(msg *schema.AgenticMessage) string {
	if msg == nil {
		return ""
	}
	var b strings.Builder
	for _, block := range msg.ContentBlocks {
		if block == nil {
			continue
		}
		switch {
		case block.UserInputText != nil:
			b.WriteString(block.UserInputText.Text)
		case block.AssistantGenText != nil:
			b.WriteString(block.AssistantGenText.Text)
		case block.FunctionToolCall != nil:
			b.WriteString(block.FunctionToolCall.Name)
			b.WriteByte(' ')
			b.WriteString(block.FunctionToolCall.Arguments)
		case block.FunctionToolResult != nil:
			for _, c := range block.FunctionToolResult.Content {
				if c != nil && c.Text != nil {
					b.WriteString(c.Text.Text)
				}
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}
