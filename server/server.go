package server

import (
	"context"
	"encoding/json"
	"finduai/core/log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"finduai/agent"
	"finduai/core/pluginhost"
	"finduai/core/provider"
	"finduai/internal/config"
	"finduai/llmpool"
	actool "finduai/tools"
	"github.com/cloudwego/eino/components/model"
)

// BuildVersion is the backend application version, injected from cmd/findu at
// startup (which in turn gets it from -ldflags "-X main.version=<tag>").
// Defaults to "dev" for local builds. Exposed to the frontend via GET /api/health.
var BuildVersion = "dev"

// Server exposes the FINDU backend over a JSON HTTP API for the shadcn/ui
// frontend.
type Server struct {
	m      *Manager
	engine *Engine
	ctx    context.Context

	// enrichMu guards enrichJobs: in-flight AI finding-enrichment jobs keyed by
	// exploration node id (re-attach instead of double-running).
	enrichMu   sync.Mutex
	enrichJobs map[int64]*enrichJob

	// reportMu guards reportJobs + the report generation workdir. dataDir is
	// where generated .docx artifacts land before download.
	reportMu   sync.Mutex
	reportJobs map[string]*reportJob
	dataDir    string

	skillDir string // root directory for skill subdirectories
	jwtKey   []byte // HS256 signing key loaded from / generated into dataDir/jwt.key

	// apiMux 是 Handler() 里注册的 /api 路由表。HandlerWithFallback 用它判断
	// 「宿主到底有没有这条路由」，没有就交给 findu 兜底，从而把两套 API
	// 收敛成同一个 /api 命名空间。
	apiMux *http.ServeMux

	// multiAgentAPI 是 deep / plan_execute / supervisor 的兼容运行时入口。
	// 路由与鉴权由宿主接管，内部仍复用现有多代理实现，便于后续逐步替换。
	multiAgentAPI http.Handler

	// finduCompatAPI 是 Step 6d 的资源兼容入口：宿主拥有 /api/assets、/api/audit
	// 等路由，内部暂时复用 findu 子系统 handler，直到对应业务/数据完全迁出。
	// 与 multiAgentAPI 一样，这是显式收口的兼容边界，不是整段 findu 兜底。
	finduCompatAPI http.Handler

	// Step 6d Phase R（运行时收口桥）：closure 域（hitl decision/dismiss、
	// agent-loop、batch-tasks、external-mcp、webshell/exec）的 findu 运行态
	// 实例句柄 + 懒构建的同链 gin 子引擎。
	rtHandles runtimeHandles
	rtOnce    sync.Once
	rtGin     http.Handler

	// finduCfg 缓存 findu config.yaml 反序列化结果（打通 config 访问）。宿主
	// server 自身不持有 findu 的 config.Config；此处从 RuntimeHandles.ConfigPath
	// 懒加载一次，供宿主自建依赖 config 的运行时（knowledge/ExternalMCP 等）复用
	// 同一配置对象，解决 config.yaml 对宿主不可达的缺口。FINDU_ENABLE=0 或
	// ConfigPath 为空/加载失败时 finduConfig() 返回 false。
	finduCfg     *config.Config
	finduCfgOnce sync.Once

	// knowledgeRT 缓存宿主懒构建的 knowledge 运行态（manager/retriever/indexer）。
	// 由 finduConfig() 提供 Knowledge/OpenAI 配置 + knowledge.db SQLite 构建；
	// 未启用 / 构建失败时为 nil（对应 4 条索引/检索路由返回禁用形状）。
	knowledgeRT     *knowledgeRuntime
	knowledgeRTOnce sync.Once

	// plugins / providers 是 Step 4 的宿主注册表：批 2 只做只读展示与装配位，
	// 批 3 起由真实插件填充；两者为空都是正常状态。
	plugins   *pluginhost.Registry
	providers *provider.Registry

	// recipeTools / recipeNames：findu 的 YAML 工具配方（configs/toolrecipes）
	// 适配出的 CoreTool 与对应名字（P3 工具层桥接）。由 cmd/findu 启动时经
	// SetRecipeTools 注入，hostTools() 把工具本体加进每个 agent 的 base，
	// 名字进延迟披露块（schema 由 SearchExtraTools 按需取）。
	recipeTools []actool.CoreTool
	recipeNames []string

	// concMu serializes concurrency-cap decisions (admission + reconcile) so a
	// scheduler tick and an HTTP settings change / task creation can't both count
	// free slots off the same snapshot and over-promote past the limit.
	concMu sync.Mutex

	cfgMu     sync.Mutex
	chatAgent *agent.ChatAgent // conversational runner for the chat page; nil w/o LLM
	// llmProv is the fully-decorated global provider (recorder + failover chain)
	// installed by applyLLM. Task routers use it after an explicit chain is cleared.
	llmProv   model.ToolCallingChatModel
	llmDirect model.ToolCallingChatModel // concrete global provider, before any failover pool
	llmCfg    agent.Config               // current LLM config (key not exposed)
	llmOn     bool
	llmProf   string // active LLM profile name (for llmrec tagging)

	// chatBusy guards the per-task main-agent run: the chat handler launches the
	// agent on the server's background ctx (not the request ctx) and returns
	// immediately, so a page reload / proxy timeout can't abort a live run. This
	// map serializes turns per task — one main-agent run at a time per task, so
	// concurrent messages don't corrupt the shared exp<id>-main transcript.
	chatMu   sync.Mutex
	chatBusy map[string]bool
	// chatCancel holds the cancel func for each in-flight conversation run (keyed by
	// convBusyKey), so a manual stop can abort JUST that session's agent run. Set/
	// cleared alongside chatBusy under chatMu. Aborting a run does NOT touch the P3
	// trigger queue — the drain goroutine simply proceeds to the next queued fire.
	chatCancel map[string]context.CancelCauseFunc

	// triggerQ buffers P3 trigger fires PER AGENT. A per-agent "pump" launches runs up
	// to a concurrency limit derived from the agent's策略: serial → limit 1 (+ optional
	// merge); parallel → limit = trigger_max_parallel (0=∞), no merge. triggerActive
	// counts in-flight runs per agent (replaces a boolean drain flag); a run's
	// completion decrements it and re-pumps to fill the freed slot. triggerCfg caches
	// the agent's last-read策略 so the pump never queries the DB while holding queueMu.
	// Distinct agents always run concurrently. Queue is in-memory (matches chatBusy); a
	// restart drops pending fires — the scheduler re-fires from watermarks next tick.
	queueMu       sync.Mutex
	triggerQ      map[string][]triggeredRun
	triggerActive map[string]int
	triggerCfg    map[string]triggerBehavior

	// profChatAgents caches a per-profile ChatAgent (chat page), keyed by profile id.
	// Built lazily on first use; invalidated when any profile is saved/activated/deleted
	// so edits take effect.
	profMu         sync.Mutex
	profChatAgents map[int64]*agent.ChatAgent // per-profile ChatAgent cache (chat page)

	// provByProfile caches ONE provider per LLM profile id so every agent bound or
	// pinned to the same profile shares a single provider instance — hence one rate
	// limiter. applyLLM also resolves the persisted global-active profile through this
	// cache, so global fallback and task chains do not accidentally double the
	// configured request rate. Cleared on profile edits, then repopulated by active reapply.
	provCacheMu   sync.Mutex
	provByProfile map[int64]*provEntry
	provCacheGen  uint64

	// llmHealth is the process-wide circuit-breaker state for LLM failover (轮询).
	// It deliberately lives OUTSIDE the provider caches: rebuilding the chain
	// (saving an unrelated profile, flipping a setting) must not erase what we
	// learned about which backends are out of credit / rate-limited.
	llmHealth *llmpool.Registry

	// Explicit task chains use one task-scoped dynamic provider shared by goals,
	// planner, workers, and the main agent. Bundles are stable; their runtime reads
	// the persisted current profile at every new LLM call.
	taskAgentMu sync.Mutex
	taskAgents  map[string]*taskAgentBundle

	// Cold task archives run through one persistent FIFO worker. The buffered wake
	// channel coalesces enqueue bursts; the database remains the source of truth.
	archiveWake chan struct{}
	archiveWG   sync.WaitGroup
	side        *sideQuestionState
}

// provEntry is a cached provider + its config for one LLM profile id.
type provEntry struct {
	prov model.ToolCallingChatModel
	cfg  agent.Config
}

// triggeredRun is one queued P3 trigger fire awaiting its turn for an agent.
// taskID + mergeable let the drainer coalesce several event triggers (finding/goal)
// from the SAME task into one conversation before it starts (interval fires don't merge).
type triggeredRun struct {
	agentKey  string
	title     string
	message   string // 事件正文(触发语 + 工具/入参/返回等);不含任务描述/目标头
	taskID    int64  // source task for finding/goal triggers; 0 for interval/none
	taskDesc  string // 任务描述(任务级,同任务相同);合并时只渲染一次
	taskGoal  string // 任务目标(任务级,同任务相同);合并时只渲染一次
	mergeable bool   // true for finding/goal event triggers (merge by taskID)
}

func New(ctx context.Context, m *Manager, skillDir string, dataDir string, keyDir string) *Server {
	key, err := loadOrCreateJWTKey(keyDir, dataDir)
	if err != nil {
		log.Fatalf("[auth] JWT key: %v", err)
	}
	s := &Server{m: m, engine: NewEngine(m), ctx: ctx, skillDir: skillDir, jwtKey: key, dataDir: dataDir, enrichJobs: map[int64]*enrichJob{}, reportJobs: map[string]*reportJob{}, chatBusy: map[string]bool{},
		chatCancel: map[string]context.CancelCauseFunc{}, triggerQ: map[string][]triggeredRun{},
		triggerActive: map[string]int{}, triggerCfg: map[string]triggerBehavior{},
		profChatAgents: map[int64]*agent.ChatAgent{},
		provByProfile:  map[int64]*provEntry{}, llmHealth: newLLMHealthRegistry(m.pg),
		taskAgents: map[string]*taskAgentBundle{}, archiveWake: make(chan struct{}, 1),
		plugins:   pluginhost.NewRegistry(pluginhost.APIVersion),
		providers: provider.NewRegistry()}
	s.initSideQuestions()
	// 熔断阈值/冷却是失败路径上的热参数，启动时把全局重试策略推给 Registry 一次；
	// 之后每次保存策略再推一次（saveLLMRetryPolicy）。
	s.applyRetryPolicy()
	// Every task uses a stable task router. An empty explicit chain is resolved by
	// that router through Agent bindings and then the global provider, so adding a
	// first chain to a running task takes effect on its very next LLM call.
	s.engine.SetAuthoritativeAgentResolver(func(t *Task) (*agent.Planner, *agent.Worker) {
		if !s.taskRuntimeAvailable(t, "planner", "worker") {
			return nil, nil
		}
		b := s.agentsForTask(t)
		return b.pl, b.wk
	})
	// Global readiness (Ready()/llm_configured): a global active LLM provider is
	// installed. Task-level runnability is separate (ReadyFor → the resolver above).
	s.engine.SetReadiness(func() bool {
		s.cfgMu.Lock()
		defer s.cfgMu.Unlock()
		return s.llmOn
	})
	// Wire DB-stored prompt templates into the agents (新版方案 §3.3 / §5a). With no
	// override row, agents keep their built-in defaults — behavior is unchanged.
	if m.pg != nil {
		agent.PromptOverride = func(key string) (string, bool) {
			a, err := m.pg.GetAgentByKey(key)
			if err != nil || a == nil {
				return "", false
			}
			t, err := m.pg.CurrentPrompt(a.ID)
			if err != nil || t == "" {
				return "", false
			}
			return t, true
		}
		// Wire DB-stored wrap-up (settlement) prompts. Empty column → built-in default.
		agent.WrapupOverride = func(key string) (string, bool) {
			a, err := m.pg.GetAgentByKey(key)
			if err != nil || a == nil || a.WrapupPrompt == "" {
				return "", false
			}
			return a.WrapupPrompt, true
		}
		// Wire DB-stored wrap-up turn budgets. 0 / missing → built-in per-agent default.
		agent.WrapupMaxTurnsOverride = func(key string) (int, bool) {
			a, err := m.pg.GetAgentByKey(key)
			if err != nil || a == nil || a.WrapupMaxTurns <= 0 {
				return 0, false
			}
			return a.WrapupMaxTurns, true
		}
		// Wire DB-stored TASK-TIMEOUT wrap-up prompt/turns (worker/planner). Empty/0 → default.
		agent.WrapupTaskTimeoutOverride = func(key string) (string, bool) {
			a, err := m.pg.GetAgentByKey(key)
			if err != nil || a == nil || a.TaskTimeoutWrapupPrompt == "" {
				return "", false
			}
			return a.TaskTimeoutWrapupPrompt, true
		}
		agent.WrapupTaskTimeoutTurnsOverride = func(key string) (int, bool) {
			a, err := m.pg.GetAgentByKey(key)
			if err != nil || a == nil || a.TaskTimeoutWrapupMaxTurns <= 0 {
				return 0, false
			}
			return a.TaskTimeoutWrapupMaxTurns, true
		}
		wireAgentAugment(m.pg, s.skillDir, s.hostTools) // 可见 skills/MCP + 流量/编排 host 工具装配进 agent 工具集
		domainReg := buildDomainReg(m.Assets())
		wireTools(m.pg, domainReg) // 内置工具表：按 agent 过滤 + 覆盖描述/schema + 注入默认值
		seedPrompts(m.pg)          // 内置 agent 默认提示词正文播种进 agent_prompts(仅空时)
		s.seedOrchestrationTools() // P2 跨任务编排工具 seed 进 tools 表(可按 agent 绑定)
		if err := s.seedFindingRetester(); err != nil {
			log.Printf("[retester] seed: %v", err)
		}
		go s.evidenceStore().RunGC(s.ctx)
		s.seedPythonInterpreter()     // 自定义脚本工具:开机检测 python 解释器入库(仅空时)
		go newScheduler(s).Run(s.ctx) // P3 触发器调度(定时/finding/目标事件),仅自定义 agent
		// 漏洞 IM 推送投递引擎。与 Scheduler 并列但独立：推送的实时性要求(3s)
		// 与触发器的业务节奏不同，且两者失败互不牵连——推送卡住不该影响 agent 触发。
		go newNotifier(s).Run(s.ctx)
		// Fill the tool cache for any enabled MCP that has none yet (notably the
		// seeded browser MCP on first run). Async so it never blocks startup.
		go s.discoverEmptyMCPsOnStartup()
		logSink.SetDB(ctx, m.pg) // restore last 100 log rows and enable async persistence
	}
	// precedence: persisted DB config > env.
	if cfg, ok := s.loadLLMConfig(); ok {
		if err := s.applyLLM(cfg); err != nil {
			log.Printf("[engine] saved LLM config init failed — engine idle: %v", err)
		} else {
			log.Printf("[engine] LLM configured from DB: %s / %s", cfg.Provider(), cfg.Model)
		}
	} else if cfg, ok := agent.FromEnv(); ok {
		if err := s.applyLLM(cfg); err != nil {
			log.Printf("[engine] env provider init failed — engine idle: %v", err)
		} else {
			log.Printf("[engine] LLM configured from env: %s / %s", cfg.Provider(), cfg.Model)
		}
	} else {
		log.Printf("[engine] no LLM provider configured — engine idle until set via /api/llm or env")
	}
	s.restoreTaskRuntimes()
	go s.reconcileConcurrency()
	s.startTaskArchiveWorker()
	s.wireInterceptReviewer() // LLM 兜底审批:未命中拦截规则的命令交给模型判定
	s.wireReport(skillDir)    // 报告子系统:LLM 补全注入 + skill 骨架落盘
	// Step 4 批 3：第一个真实插件（intel）在 New 末尾装配并启动。放在任务恢复之后，
	// 避免插件启动的目录导入与任务恢复抢初始化时间。
	s.startIntelPlugin()
	return s
}

// Restored deadline and worker loops must inherit the same context as new tasks,
// including the side-question checkpoint publisher installed during startup.
func (s *Server) restoreTaskRuntimes() {
	m := s.m
	// reload tasks persisted on disk so the task list survives a restart, and
	// restore persisted paused state (so a task paused before restart stays paused).
	for _, t := range m.LoadExisting() {
		lifecycle := t.lifecycleSnapshot()
		// clear stale 'running' intents from a prior crash/restart (no live worker
		// owns them) so they re-claim instead of spinning forever in the UI.
		if n, _ := t.Store.ResetRunningIntents(); n > 0 {
			log.Printf("[engine] task %s 重置 %d 个残留 running 意图为 open", t.ID, n)
		}
		if lifecycle.Paused {
			s.engine.Pause(t.ID, agent.AbortPausedOnReload)
		}
		// 任务级超时:为每个未终态、带 timeout 的任务起 deadline 协调器,独立于 planner/worker
		// loop——非活跃任务重启后也能在到点后被收尾(deadline 已过则立即走收尾时序)。
		if !isTerminalStatus(lifecycle.Status) {
			s.engine.startDeadlineCoordinator(s.ctx, t)
		}
	}
	// Restore every task that had already been admitted before shutdown. Starting
	// only the active UI task left other non-queued tasks counted as concurrency
	// occupants without live loops, which could permanently block the persistent
	// FIFO. Paused loops remain idle; queued tasks are admitted below as slots allow.
	for _, t := range m.List() {
		lifecycle := t.lifecycleSnapshot()
		if !lifecycle.Queued && !isTerminalStatus(lifecycle.Status) {
			s.engine.Run(s.ctx, t)
		}
	}
}

// agentMaxTurns returns the configured max_turns for an agent key (0 = unlimited,
// also the fallback when no DB or no row).
func (s *Server) agentMaxTurns(key string) int {
	if s.m.pg == nil {
		return 0
	}
	a, err := s.m.pg.GetAgentByKey(key)
	if err != nil || a == nil {
		return 0
	}
	return a.MaxTurns
}

// agentRunSeconds returns the configured wall-clock run budget (seconds) for an
// agent key (0 = unlimited; 1200 fallback when no DB or no row, matching schema).
func (s *Server) agentRunSeconds(key string) int {
	if s.m.pg == nil {
		return 1200
	}
	a, err := s.m.pg.GetAgentByKey(key)
	if err != nil || a == nil {
		return 1200
	}
	return a.RunSecs
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	// /api/* goes through CORS + JWT; everything else is served by the embedded
	// frontend (public — auth is enforced client-side and on the API). With the
	// no-embed build the webui handler just 404s (run `next dev` separately).
	s.apiMux = mux
	api := cors(s.requireAuth(mux))
	root := http.NewServeMux()
	root.Handle("/api/", api)
	root.Handle("/", s.webuiHandler())
	return root
}

// PathOwnedBy 判断路径是否落在给定前缀列表里（前缀按路径段匹配，/api/c2/ 这种带尾斜杠的按裸前缀匹配）。
func PathOwnedBy(p string, prefixes []string) bool {
	for _, pre := range prefixes {
		if pre == "" {
			continue
		}
		if p == pre || strings.HasPrefix(p, pre+"/") {
			return true
		}
		if strings.HasSuffix(pre, "/") && strings.HasPrefix(p, pre) {
			return true
		}
	}
	return false
}

// promoteBearerCookie 把宿主 /api 调用方的 Bearer 提升为 findu 会话桥读取的
// findu_token cookie。findu 侧 AuthMiddleware 已做账号统一：找不到 findu 令牌
// 时经 SessionFromFinduCookie 用控制台 cookie 建立会话；因此宿主 Bearer 只要
// 提升成 cookie 就能通过 findu 鉴权（system-services / C2 / skills 等走 findu
// 兜底的前缀此前 401，正是因为这条 fallback 路径没做提升）。
func promoteBearerCookie(r *http.Request) *http.Request {
	if _, err := r.Cookie("findu_token"); err == nil {
		return r
	}
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(h, "Bearer ") {
		return r
	}
	rr := r.Clone(r.Context())
	rr.AddCookie(&http.Cookie{Name: "findu_token", Value: strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))})
	return rr
}

// HandlerWithFallback 把两套 API 合成一个 /api 命名空间：
//   - finduOwned 里的资源一律给 fallback（这些资源的数据在 findu schema 里，宿主同名表是空的）；
//   - 其余路径宿主优先，宿主路由表里没有这条路由时才交给 fallback；
//   - fallback 自己负责鉴权（findu 侧校验 findu_token），不经过宿主的 requireAuth，
//     否则没有宿主 token 的旧页面会在到达 findu 之前被 401 拦掉。
//
// 页面与静态资源仍由宿主内嵌前端提供，旧 UI 留在 /findu/ 下（由 fallback 处理）。
func (s *Server) HandlerWithFallback(fallback http.Handler, finduOwned []string) http.Handler {
	if s.apiMux == nil {
		_ = s.Handler() // 幂等：只是把路由注册出来，取到 apiMux
	}
	mux := s.apiMux
	api := cors(s.requireAuth(mux))
	root := http.NewServeMux()
	root.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		if PathOwnedBy(r.URL.Path, finduOwned) {
			fallback.ServeHTTP(w, promoteBearerCookie(r))
			return
		}
		if _, pattern := mux.Handler(r); pattern != "" {
			api.ServeHTTP(w, r)
			return
		}
		fallback.ServeHTTP(w, promoteBearerCookie(r))
	})
	root.Handle("/", s.webuiHandler())
	return root
}

// bridgeConversationExtrasPath reports the findu conversation sub-routes that
// findu now serves directly. Nested shapes are intentionally excluded.
func bridgeConversationExtrasPath(path string) bool {
	if !strings.HasPrefix(path, "/api/conversations/") {
		return false
	}
	for _, suffix := range []string{"/token-usage", "/results"} {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return false
}

// bridgePlanTaskConversationID extracts the id from
// /api/conversations/{id}/plan-tasks. Plan boards are only routed to findu
// while an findu bridge run is active; deep/findu runs keep their own board.
func bridgePlanTaskConversationID(path string) (string, bool) {
	const prefix = "/api/conversations/"
	const suffix = "/plan-tasks"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return "", false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	if id == "" || strings.Contains(id, "/") {
		return "", false
	}
	return id, true
}

// bridgeStopConversationID extracts the id from /api/conversations/{id}/stop.
// Only this exact shape is special-cased; all other owned routes stay findu.
func bridgeConversationGetID(path string) (string, bool) {
	const prefix = "/api/conversations/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	id := strings.TrimPrefix(path, prefix)
	if id == "" || strings.Contains(id, "/") {
		return "", false
	}
	return id, true
}

func bridgeStopConversationID(path string) (string, bool) {
	const prefix = "/api/conversations/"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, "/stop") {
		return "", false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(path, prefix), "/stop")
	if id == "" || strings.Contains(id, "/") {
		return "", false
	}
	return id, true
}

func (s *Server) bridgeRunActive(conversationID string) bool {
	if s == nil || conversationID == "" {
		return false
	}
	s.chatMu.Lock()
	defer s.chatMu.Unlock()
	return s.chatCancel[s.convBusyKey(conversationID)] != nil
}

// --- handlers ---

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"ok": true, "service": "findu", "version": BuildVersion})
}

type createTaskReq struct {
	Name                 string   `json:"name,omitempty"` // 可选任务名称;省略/空=未命名
	CategoryID           *int64   `json:"category_id,omitempty"`
	Description          string   `json:"description"`
	Goal                 string   `json:"goal"`
	LLMProfileID         *int64   `json:"llm_profile_id,omitempty"`    // 指定运行本任务的 LLM 配置;省略/null=用激活配置
	LLMProfileIDs        []int64  `json:"llm_profile_ids,omitempty"`   // 有序任务级配置链;第一项初始生效
	SourceTaskIDs        []string `json:"source_task_ids,omitempty"`   // 仅直接、只读继承的来源任务
	CompanyIDs           []int64  `json:"company_ids,omitempty"`       // 关联企业范围并快照关联当前企业资产;不复制资产或强制生成意图
	TimeoutSeconds       int      `json:"timeout_seconds"`             // 任务级超时(秒);0/省略=不限时
	PlanHeartbeatSeconds int      `json:"plan_heartbeat_seconds"`      // planner 心跳触发间隔(秒);0/省略=默认600(10min);下限=默认=600,低于自动抬到600
	SeedFirstIntent      *bool    `json:"seed_first_intent,omitempty"` // 创建时直接下发一条种子意图(内容=描述+目标),让 worker 免等首轮 planner 直接开跑;省略/null=默认关闭,走标准先规划再执行。显式传 true 才开(CTF 常一 work 解决时可省掉开跑前的 planner 轮)。
	CoverageEnabled      *bool    `json:"coverage_enabled,omitempty"`  // 资产覆盖度功能;省略/null=默认开(true)。false=关闭覆盖度计算/展示/自动累积范围+隐藏 add_task_scope/list_untested_assets。company 关联不受影响。
	// InterceptRules 任务级资产拦截/允许规则(创建时录入,存 task_intercept_rules,不进全局表)。
	InterceptRules []taskInterceptRuleReq `json:"intercept_rules,omitempty"`
}

var (
	reURL    = regexp.MustCompile(`https?://[^\s'"]+`)
	reIPPort = regexp.MustCompile(`\b((?:\d{1,3}\.){3}\d{1,3})(?::(\d{1,5}))?`)
	reDomain = regexp.MustCompile(`\b((?:[a-zA-Z0-9-]+\.)+[a-zA-Z]{2,})(?::(\d{1,5}))?`)
)

// --- utils ---

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": msg})
}

func atoiDefault(s string, d int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return d
}
