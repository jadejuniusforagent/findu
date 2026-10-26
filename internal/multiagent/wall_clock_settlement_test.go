package multiagent

import (
	"strings"
	"testing"
	"time"

	"finduai/internal/config"
)

func TestWallClockBudgetDisabledWhenZero(t *testing.T) {
	if b := newWallClockBudgetFromAgent(nil); b != nil {
		t.Fatalf("nil 配置应返回 nil，实际 %+v", b)
	}
	if b := newWallClockBudgetFromAgent(&config.Config{}); b != nil {
		t.Fatalf("max_run_minutes=0 应返回 nil（默认关闭），实际 %+v", b)
	}
}

func TestWallClockBudgetDefaultsAndPhaseTransitions(t *testing.T) {
	cfg := &config.Config{}
	cfg.Agent.MaxRunMinutes = 10
	b := newWallClockBudgetFromAgent(cfg)
	if b == nil {
		t.Fatal("max_run_minutes=10 应启用")
	}
	if b.grace != 30*time.Second {
		t.Fatalf("grace 默认应为 30s，实际 %v", b.grace)
	}
	if b.wrapTurns != 2 {
		t.Fatalf("wrapTurns 默认应为 2，实际 %d", b.wrapTurns)
	}

	base := time.Now()
	b.started = base
	cases := []struct {
		at   time.Time
		want wallClockPhase
	}{
		{base.Add(9 * time.Minute), wallClockPhaseNormal},
		{base.Add(10*time.Minute + 5*time.Second), wallClockPhaseGrace},
		{base.Add(10*time.Minute + 30*time.Second), wallClockPhaseSettle},
		{base.Add(2 * time.Hour), wallClockPhaseSettle},
	}
	for _, c := range cases {
		if got := b.phaseLocked(c.at); got != c.want {
			t.Fatalf("at=%v 期望 %v 实际 %v", c.at.Sub(base), c.want, got)
		}
	}

	// 未 start 时视为 normal（避免误伤）
	fresh := &wallClockBudget{limit: time.Minute}
	if got := fresh.phase(); got != wallClockPhaseNormal {
		t.Fatalf("未 start 应为 normal，实际 %v", got)
	}
}

func TestWallClockToolAllowlistOnlyDuringSettle(t *testing.T) {
	cfg := &config.Config{}
	cfg.Agent.MaxRunMinutes = 1
	b := newWallClockBudgetFromAgent(cfg)

	// normal：全部放行
	b.started = time.Now()
	for _, n := range []string{"exec", "nmap", "upsert_project_fact"} {
		if !b.allowTool(n) {
			t.Fatalf("normal 阶段 %q 应放行", n)
		}
	}

	// settle：只放行落库类
	b.started = time.Now().Add(-time.Hour)
	if !b.settleActive() {
		t.Fatal("应处于 settle 阶段")
	}
	if !b.allowTool("upsert_project_fact") {
		t.Error("upsert_project_fact 在 settle 应放行")
	}
	if !b.allowTool("RECORD_VULNERABILITY") {
		t.Error("record_vulnerability 大小写不敏感，应放行")
	}
	if b.allowTool("exec") {
		t.Error("exec 在 settle 应被拒")
	}
	if b.allowTool("nmap") {
		t.Error("nmap 在 settle 应被拒")
	}

	// nil budget：恒放行（未启用场景）
	var nilBudget *wallClockBudget
	if !nilBudget.allowTool("exec") {
		t.Error("nil budget 应恒放行")
	}
}

func TestWallClockWrapUpNoticeCapped(t *testing.T) {
	cfg := &config.Config{}
	cfg.Agent.MaxRunMinutes = 1
	cfg.Agent.MaxRunWrapUpTurns = 3
	b := newWallClockBudgetFromAgent(cfg)
	b.started = time.Now().Add(-time.Hour)

	first, ok := b.takeWrapUpNotice()
	if !ok {
		t.Fatal("settle 首轮应给出收尾指令")
	}
	if !strings.Contains(first, wallClockWrapUpMarker) {
		t.Fatalf("收尾指令应含标记 %q，实际 %q", wallClockWrapUpMarker, first)
	}
	if !strings.Contains(first, "upsert_project_fact") || !strings.Contains(first, "record_vulnerability") {
		t.Error("收尾指令应点明两个落库工具")
	}

	sent := 1
	for i := 0; i < 10; i++ {
		if _, ok := b.takeWrapUpNotice(); ok {
			sent++
		}
	}
	if sent != 3 {
		t.Fatalf("收尾指令上限应为 wrapTurns=3，实际 %d", sent)
	}

	// normal 阶段不注入
	n := &wallClockBudget{limit: time.Minute, wrapTurns: 2}
	n.started = time.Now()
	if _, ok := n.takeWrapUpNotice(); ok {
		t.Error("normal 阶段不应注入收尾指令")
	}
}

func TestMarkWallClockTimeout(t *testing.T) {
	cfg := &config.Config{}
	cfg.Agent.MaxRunMinutes = 1
	b := newWallClockBudgetFromAgent(cfg)

	// 未越线：不动
	b.started = time.Now()
	r := &RunResult{}
	markWallClockTimeout(r, b)
	if r.Status != "" || r.CompletionReason != "" {
		t.Fatalf("未越线不应改终态，得到 status=%q reason=%q", r.Status, r.CompletionReason)
	}

	// 越线：标 timeout
	b.started = time.Now().Add(-time.Hour)
	b.wrapSent = 0
	r2 := &RunResult{}
	markWallClockTimeout(r2, b)
	if r2.Status != "timeout" || r2.CompletionReason != "timeout" {
		t.Fatalf("越线应标 timeout，得到 status=%q reason=%q", r2.Status, r2.CompletionReason)
	}

	// 已有终态：追加 +timeout，不覆盖原因
	r3 := &RunResult{Status: "completed", CompletionReason: "verified"}
	markWallClockTimeout(r3, b)
	if r3.Status != "completed" {
		t.Errorf("已有 status 不应被覆盖，实际 %q", r3.Status)
	}
	if r3.CompletionReason != "verified+timeout" {
		t.Errorf("原因应追加 +timeout，实际 %q", r3.CompletionReason)
	}

	// nil budget 容错
	markWallClockTimeout(nil, b)
	markWallClockTimeout(r2, nil)
}

func TestWallClockContextRoundTrip(t *testing.T) {
	if wallClockBudgetFromContext(nil) != nil {
		t.Fatal("nil ctx 应返回 nil")
	}
	cfg := &config.Config{}
	cfg.Agent.MaxRunMinutes = 5
	b := newWallClockBudgetFromAgent(cfg)
	ctx := withWallClockBudget(t.Context(), b)
	if got := wallClockBudgetFromContext(ctx); got != b {
		t.Fatalf("ctx 往返应拿到同一实例，实际 %+v", got)
	}
	// 未启用的 run：ctx 里没有预算
	if got := wallClockBudgetFromContext(t.Context()); got != nil {
		t.Fatalf("未挂载时应返回 nil，实际 %+v", got)
	}
}

func TestIsWallClockTimeout(t *testing.T) {
	if IsWallClockTimeout(nil) {
		t.Error("nil 应为 false")
	}
	if IsWallClockTimeout(&RunResult{CompletionReason: "verified"}) {
		t.Error("verified 应为 false")
	}
	if !IsWallClockTimeout(&RunResult{CompletionReason: WallClockTimeoutReason}) {
		t.Error("timeout 应为 true")
	}
	if !IsWallClockTimeout(&RunResult{CompletionReason: "verified+timeout"}) {
		t.Error("追加形式应为 true")
	}
	if !IsWallClockTimeout(&RunResult{Status: WallClockTimeoutReason}) {
		t.Error("仅 status=timeout 也应为 true")
	}
	if IsWallClockTimeout(&RunResult{}) {
		t.Error("空 RunResult 应为 false")
	}
}
