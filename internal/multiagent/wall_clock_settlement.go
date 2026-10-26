package multiagent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"finduai/internal/config"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"
)

// 墙钟超时 + 就地收尾（P1）。
//
// 背景：FindUAI 原有超时只有「局部」三件套 —— 单次工具 tool_timeout_minutes、
// 工具本轮等待 tool_wait_timeout_seconds、shell 无输出 shell_no_output_timeout_seconds；
// 另有 max_iterations（回合数，默认 3000）。**缺整轮的墙钟上限**：一轮 run 可以一直跑下去，
// 到点通常只能靠外部硬 ctx 直接杀，成果（已识别的事实/漏洞）来不及写回。
//
// 本文件补这一层，语义对齐 norma harness 的「墙钟超时就地收尾」：
//
//	阶段一 normal ：t < limit，一切照旧
//	阶段二 grace  ：limit <= t < limit+grace，仍允许工具（给正在跑的工具留收尾时间）
//	阶段三 settle ：t >= limit+grace，进入收尾 ——
//	               ① 除落库类工具（upsert_project_fact / record_vulnerability）外，新调用一律拒绝
//	               ② 每轮注入一条收尾指令，最多 wrapTurns 轮
//	               ③ 模型无法再发起新动作 → 自然产出「已写回内容 + 阶段性总结」后收场
//
// 与最初设计的一处差异（如实记录）：设计里「打断正在跑的工具」未做硬打断。
// 执行中的工具由既有 tool_timeout_minutes 兜底，settle 只约束**新**的工具调用；
// 硬打断需要动 Eino 的 cancel 链路，风险高，留作后续。
//
// 默认关闭：agent.max_run_minutes <= 0 时 newWallClockBudgetFromAgent 返回 nil，
// 所有接入点均透传，行为与改造前完全一致。
//
// 预算实例的传递走两条通道：
//   - 显式：装配点直接构造中间件时传入（newWallClockSettleMiddlewareWithBudget）
//   - 回落：run 入口把它挂进 ctx，任何拿不到实例的装配点（如 plan_execute 执行器）从 ctx 取
const (
	defaultWallClockGraceSeconds = 30
	defaultWallClockWrapUpTurns  = 2
	wallClockWrapUpMarker        = "【时间预算已用尽 / Wall-clock budget exhausted】"
)

// wallClockSettleAllowlist 收尾期仍放行的工具：只写回，不发起新动作。
var wallClockSettleAllowlist = map[string]struct{}{
	"upsert_project_fact":  {},
	"record_vulnerability": {},
}

type wallClockPhase int

const (
	wallClockPhaseNormal wallClockPhase = iota
	wallClockPhaseGrace
	wallClockPhaseSettle
)

func (p wallClockPhase) String() string {
	switch p {
	case wallClockPhaseGrace:
		return "grace"
	case wallClockPhaseSettle:
		return "settle"
	default:
		return "normal"
	}
}

// wallClockBudget 单轮 run 的墙钟预算。并发安全：模型中间件与工具门控可能在不同 goroutine 访问。
type wallClockBudget struct {
	mu        sync.Mutex
	limit     time.Duration
	grace     time.Duration
	wrapTurns int
	started   time.Time
	wrapSent  int
}

// newWallClockBudgetFromAgent 从 agent 配置构造；未启用（max_run_minutes<=0）返回 nil。
func newWallClockBudgetFromAgent(cfg *config.Config) *wallClockBudget {
	if cfg == nil || cfg.Agent.MaxRunMinutes <= 0 {
		return nil
	}
	graceSec := cfg.Agent.MaxRunGraceSeconds
	if graceSec < 0 {
		graceSec = 0
	} else if graceSec == 0 {
		graceSec = defaultWallClockGraceSeconds
	}
	wrapTurns := cfg.Agent.MaxRunWrapUpTurns
	if wrapTurns < 0 {
		wrapTurns = 0
	} else if wrapTurns == 0 {
		wrapTurns = defaultWallClockWrapUpTurns
	}
	return &wallClockBudget{
		limit:     time.Duration(cfg.Agent.MaxRunMinutes) * time.Minute,
		grace:     time.Duration(graceSec) * time.Second,
		wrapTurns: wrapTurns,
	}
}

// start 记本轮 run 的墙钟起点；重复调用只认第一次（幂等）。
func (b *wallClockBudget) start() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.started.IsZero() {
		b.started = time.Now()
	}
}

func (b *wallClockBudget) phase() wallClockPhase {
	if b == nil {
		return wallClockPhaseNormal
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.phaseLocked(time.Now())
}

func (b *wallClockBudget) phaseLocked(now time.Time) wallClockPhase {
	if b.started.IsZero() {
		return wallClockPhaseNormal
	}
	elapsed := now.Sub(b.started)
	if elapsed < b.limit {
		return wallClockPhaseNormal
	}
	if elapsed < b.limit+b.grace {
		return wallClockPhaseGrace
	}
	return wallClockPhaseSettle
}

// settleActive 是否处于收尾期（禁工具 + 注入收尾指令）。
func (b *wallClockBudget) settleActive() bool {
	return b.phase() == wallClockPhaseSettle
}

// timedOut 是否已越过墙钟（含 grace），用于给终态打标。
func (b *wallClockBudget) timedOut() bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.phaseLocked(time.Now()) != wallClockPhaseNormal
}

// allowTool 收尾期只放行落库类工具；非收尾期一律放行。
func (b *wallClockBudget) allowTool(name string) bool {
	if b == nil || !b.settleActive() {
		return true
	}
	_, ok := wallClockSettleAllowlist[strings.ToLower(strings.TrimSpace(name))]
	return ok
}

// takeWrapUpNotice 取一条收尾指令；每轮最多消费一次，总量上限 wrapTurns。
func (b *wallClockBudget) takeWrapUpNotice() (string, bool) {
	if b == nil {
		return "", false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.phaseLocked(time.Now()) != wallClockPhaseSettle {
		return "", false
	}
	if b.wrapSent >= b.wrapTurns {
		return "", false
	}
	b.wrapSent++
	remaining := b.wrapTurns - b.wrapSent
	return formatWallClockWrapUpNotice(b.limit, remaining), true
}

func formatWallClockWrapUpNotice(limit time.Duration, remaining int) string {
	var sb strings.Builder
	sb.WriteString(wallClockWrapUpMarker)
	sb.WriteString("\n")
	fmt.Fprintf(&sb, "本轮运行已超过 max_run_minutes=%d 的墙钟预算，系统进入收尾阶段（剩余收尾轮次 %d）。\n", int(limit.Minutes()), remaining)
	sb.WriteString("现在必须按顺序做两件事：\n")
	sb.WriteString("1) 立即用 upsert_project_fact / record_vulnerability 把已经拿到的事实与漏洞写回，别丢证据；\n")
	sb.WriteString("2) 然后输出阶段性总结：已完成的验证、已确认的结论、尚未覆盖的攻击面与下一步建议。\n")
	sb.WriteString("除上述两个落库工具外，其余工具在收尾阶段已被禁用——不要再尝试发起新的扫描、利用或信息收集动作。\n")
	return sb.String()
}

// ---------- ctx 传递 ----------

type wallClockBudgetCtxKey struct{}

// withWallClockBudget 把预算挂进 run ctx；b 为 nil 时原样返回。
func withWallClockBudget(ctx context.Context, b *wallClockBudget) context.Context {
	if ctx == nil || b == nil {
		return ctx
	}
	return context.WithValue(ctx, wallClockBudgetCtxKey{}, b)
}

// wallClockBudgetFromContext 取回预算；没有则 nil（表示本 run 未启用墙钟）。
func wallClockBudgetFromContext(ctx context.Context) *wallClockBudget {
	if ctx == nil {
		return nil
	}
	b, _ := ctx.Value(wallClockBudgetCtxKey{}).(*wallClockBudget)
	return b
}

// ---------- 模型侧中间件：注入收尾指令 ----------

type wallClockSettleMiddleware struct {
	*adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]
	budget *wallClockBudget // 显式传入；nil 时回落到 ctx
	logger *zap.Logger
	phase  string
}

func newWallClockSettleMiddleware(logger *zap.Logger, phase string) adk.TypedChatModelAgentMiddleware[*schema.AgenticMessage] {
	return &wallClockSettleMiddleware{
		TypedBaseChatModelAgentMiddleware: &adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]{},
		logger:                            logger,
		phase:                             phase,
	}
}

// newWallClockSettleMiddlewareWithBudget 供已持有预算实例的装配点直接使用。
func newWallClockSettleMiddlewareWithBudget(budget *wallClockBudget, logger *zap.Logger, phase string) adk.TypedChatModelAgentMiddleware[*schema.AgenticMessage] {
	return &wallClockSettleMiddleware{
		TypedBaseChatModelAgentMiddleware: &adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]{},
		budget:                            budget,
		logger:                            logger,
		phase:                             phase,
	}
}

func (m *wallClockSettleMiddleware) BeforeModelRewriteState(
	ctx context.Context,
	state *adk.TypedChatModelAgentState[*schema.AgenticMessage],
	mc *adk.TypedModelContext[*schema.AgenticMessage],
) (context.Context, *adk.TypedChatModelAgentState[*schema.AgenticMessage], error) {
	_ = mc
	if m == nil || state == nil {
		return ctx, state, nil
	}
	budget := m.budget
	if budget == nil {
		budget = wallClockBudgetFromContext(ctx)
	}
	if budget == nil {
		return ctx, state, nil
	}
	notice, ok := budget.takeWrapUpNotice()
	if !ok {
		return ctx, state, nil
	}
	out := *state
	msgs := make([]*schema.AgenticMessage, 0, len(state.Messages)+1)
	msgs = append(msgs, state.Messages...)
	msgs = append(msgs, schema.UserAgenticMessage(notice))
	out.Messages = msgs
	if m.logger != nil {
		m.logger.Warn("墙钟预算已用尽，注入收尾指令",
			zap.String("phase", m.phase),
			zap.String("stage", "settle"),
			zap.Int("messages", len(msgs)),
		)
	}
	return ctx, &out, nil
}

// ---------- 工具侧门控：收尾期只放行落库类 ----------

// wallClockToolGateMiddleware 收尾期拒绝非落库类工具调用。
// 预算从 ctx 取（run 入口挂入）；ctx 里没有时恒透传 —— 未启用墙钟的 run 行为不变。
func wallClockToolGateMiddleware() compose.ToolMiddleware {
	blocked := func(ctx context.Context, name string) (string, bool) {
		budget := wallClockBudgetFromContext(ctx)
		if budget == nil || budget.allowTool(name) {
			return "", false
		}
		return fmt.Sprintf(
			"Blocked: wall-clock budget exhausted; tool %q is disabled during the wrap-up stage. "+
				"Only upsert_project_fact / record_vulnerability are allowed — write back your findings and summarise.",
			name), true
	}
	return compose.ToolMiddleware{
		Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
				if input != nil {
					if msg, hit := blocked(ctx, input.Name); hit {
						return &compose.ToolOutput{Result: msg}, nil
					}
				}
				return next(ctx, input)
			}
		},
		Streamable: func(next compose.StreamableToolEndpoint) compose.StreamableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.StreamToolOutput, error) {
				if input != nil {
					if msg, hit := blocked(ctx, input.Name); hit {
						return &compose.StreamToolOutput{Result: schema.StreamReaderFromArray([]string{msg})}, nil
					}
				}
				return next(ctx, input)
			}
		},
	}
}

// markWallClockTimeout 若本轮越过墙钟，把终态标成 timeout，与「正常完成」区分。
func markWallClockTimeout(result *RunResult, budget *wallClockBudget) {
	if result == nil || budget == nil || !budget.timedOut() {
		return
	}
	if strings.TrimSpace(result.CompletionReason) == "" {
		result.CompletionReason = "timeout"
	} else {
		result.CompletionReason = result.CompletionReason + "+timeout"
	}
	if strings.TrimSpace(result.Status) == "" {
		result.Status = "timeout"
	}
}

// applyWallClockCompletionReason 同 markWallClockTimeout，但允许从 ctx 取预算。
func applyWallClockCompletionReason(ctx context.Context, result *RunResult, budget *wallClockBudget) {
	if budget == nil {
		budget = wallClockBudgetFromContext(ctx)
	}
	markWallClockTimeout(result, budget)
}

// WallClockTimeoutReason 墙钟就地收尾的终态原因；由上层 finalizer 透出到 API/UI。
const WallClockTimeoutReason = "timeout"

// IsWallClockTimeout 判断该 run 是否因墙钟收尾而结束。
// 供 agentfinalizer 使用：finalizer 会把 CompletionReason 重算成 verified，
// 必须在它之前把这个信号带过去，否则终态会丢。
func IsWallClockTimeout(result *RunResult) bool {
	if result == nil {
		return false
	}
	return strings.Contains(result.CompletionReason, WallClockTimeoutReason) ||
		strings.TrimSpace(result.Status) == WallClockTimeoutReason
}
