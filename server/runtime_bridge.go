package server

// Step 6d Phase R1：运行时收口桥（runtime closure bridge）。
//
// closure 域——/api/hitl/{decision,dismiss}、/api/agent-loop/*（4 条）、
// /api/batch-tasks/*（14 条）、/api/external-mcp/*（7 条）、/api/webshell/exec
// —— 的真源是 findu 组装期创建的进程内运行态（HITLManager.pending[id].decideCh
// 由 agent 运行循环 waitDecision 阻塞消费；BatchTaskManager 内存队列 + 自持
// executor goroutine + DB 混合；AgentTaskManager 任务表/事件总线 SSE；
// ExternalMCPManager 运行状态 + config.yaml）。这些无法以纯 DB 投影原生化，
// 收口方式：
//
//   - 路由所有权：宿主 Go mux 显式注册 closure 前缀（findu 兼容名单减去
//     /api/agent-loop、/api/batch-tasks、/api/external-mcp 三项；hitl 与
//     webshell 走更精确的方法路由，互不冲突）；
//   - 行为等价：同进程 gin 子引擎 + findu protected 组同链中间件
//    （AuthMiddleware(h.Auth) + RBACMiddlewareWithDenyHook(h.DB, h.RBACDeny)
//     —— 拒绝同写 rbac 审计）+ 原 handler 函数（同一对象同一方法指针），
//     与 compat 时代逐字节一致；
//   - Bearer→findu_token cookie 提升与 serveFinduCompat 相同；
//   - FINDU_ENABLE=0（未挂载）时桥为 nil → 503，与 compat 不可用形状一致。
//
// 后续演进（Phase R2+，可选）：把各 manager 迁出 internal/handler 独立成宿主
// 运行时包（实例倒转为宿主构建/findu 消费），桥仅剩路由挂载。届时再评估。

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"finduai/internal/app"
	"finduai/internal/config"
	"finduai/internal/security"
)

// runtimeHandles 化名（避免 server.go 直接暴露 findu app 包导入）。
type runtimeHandles = app.RuntimeHandles

// SetFinduRuntime 安装 findu 运行态句柄（main.go 在 MountWithModel 成功后调用）。
func (s *Server) SetFinduRuntime(h runtimeHandles) {
	s.rtHandles = h
}

// finduConfig 返回 findu config.yaml 反序列化结果（懒加载一次）。宿主通过
// RuntimeHandles.ConfigPath 加载，打通 config.yaml 对宿主的不可达缺口。
// FINDU_ENABLE=0 / ConfigPath 为空 / 加载失败时返回 (nil, false)。
func (s *Server) finduConfig() (*config.Config, bool) {
	s.finduCfgOnce.Do(func() {
		h := s.rtHandles
		if h.ConfigPath == "" {
			return
		}
		c, err := config.Load(h.ConfigPath)
		if err != nil {
			return
		}
		s.finduCfg = c
	})
	if s.finduCfg == nil {
		return nil, false
	}
	return s.finduCfg, true
}

// finduRuntimeBridge 是所有 closure 域的宿主 mux 入口。
func (s *Server) finduRuntimeBridge(w http.ResponseWriter, r *http.Request) {
	api := s.finduRuntimeAPI()
	if api == nil {
		writeErr(w, http.StatusServiceUnavailable, "findu 兼容运行时不可用")
		return
	}
	// 与 serveFinduCompat 相同的提升：findu 会话桥读取 findu_token cookie，
	// 而 /api 调用方使用 Bearer。
	if _, err := r.Cookie("findu_token"); err != nil {
		if h := strings.TrimSpace(r.Header.Get("Authorization")); strings.HasPrefix(h, "Bearer ") {
			rr := r.Clone(r.Context())
			rr.AddCookie(&http.Cookie{
				Name:  "findu_token",
				Value: strings.TrimSpace(strings.TrimPrefix(h, "Bearer ")),
			})
			r = rr
		}
	}
	api.ServeHTTP(w, r)
}

// finduDeadPrefix 是 findu 侧从未注册 API 的死前缀的宿主收口（404 JSON，鉴权后）。
// 此前这些前缀 compat 落到 findu gin 的 NoRoute → 返回 SPA HTML（HTTP 200），
// 对 JSON 消费方从未可用；Phase R2a 起宿主显式 404，行为变化记录于交接表。
func (s *Server) finduDeadPrefix(w http.ResponseWriter, r *http.Request) {
	writeErr(w, http.StatusNotFound, "资源不存在（该前缀无 findu API，属死前缀）")
}

// finduRuntimeAPI 懒构建 closure 域的 gin 子引擎（并发安全）。
func (s *Server) finduRuntimeAPI() http.Handler {
	s.rtOnce.Do(func() {
		h := s.rtHandles
		if h.Agent == nil || h.Auth == nil || h.DB == nil || h.ExternalMCP == nil || h.WebShell == nil {
			return // findu 未挂载：桥保持 nil（503，与 compat nil 同形）
		}
		gin.SetMode(gin.ReleaseMode)
		g := gin.New()
		g.Use(gin.Recovery())
		// 与 findu protected 组同链：先会话（auth 中间件回填 session/scope 等
		// gin 键），再「POST 承载查询」适配（miniprogram/android 查询路由依赖，
		// 对白名单外路径为 no-op），后 RBAC（拒绝走同一审计钩子）。
		g.Use(security.AuthMiddleware(h.Auth))
		g.Use(app.PostQueryAdapter())
		g.Use(security.RBACMiddlewareWithDenyHook(h.DB, h.RBACDeny))
		reg := func() {
			// HITL 决策/撤销（B6 运行态收口：决定 wake 决策通道）。
			g.POST("/api/hitl/decision", h.Agent.DecideHITLInterrupt)
			g.POST("/api/hitl/dismiss", h.Agent.DismissHITLInterrupt)
			// Agent loop（B10：任务/取消/完成/SSE 事件总线）。
			g.POST("/api/agent-loop/cancel", h.Agent.CancelAgentLoop)
			g.GET("/api/agent-loop/tasks", h.Agent.ListAgentTasks)
			g.GET("/api/agent-loop/task-events", h.Agent.SubscribeAgentTaskEvents)
			g.GET("/api/agent-loop/tasks/completed", h.Agent.ListCompletedTasks)
			// Batch tasks（B4：队列 CRUD / 调度 / 单任务操作，executor 同源）。
			g.POST("/api/batch-tasks", h.Agent.CreateBatchQueue)
			g.GET("/api/batch-tasks", h.Agent.ListBatchQueues)
			g.GET("/api/batch-tasks/:queueId", h.Agent.GetBatchQueue)
			g.POST("/api/batch-tasks/:queueId/start", h.Agent.StartBatchQueue)
			g.POST("/api/batch-tasks/:queueId/rerun", h.Agent.RerunBatchQueue)
			g.POST("/api/batch-tasks/:queueId/pause", h.Agent.PauseBatchQueue)
			g.PUT("/api/batch-tasks/:queueId/metadata", h.Agent.UpdateBatchQueueMetadata)
			g.PUT("/api/batch-tasks/:queueId/schedule", h.Agent.UpdateBatchQueueSchedule)
			g.PUT("/api/batch-tasks/:queueId/schedule-enabled", h.Agent.SetBatchQueueScheduleEnabled)
			g.DELETE("/api/batch-tasks/:queueId", h.Agent.DeleteBatchQueue)
			g.PUT("/api/batch-tasks/:queueId/tasks/:taskId", h.Agent.UpdateBatchTask)
			g.POST("/api/batch-tasks/:queueId/tasks/:taskId/run", h.Agent.RunSingleBatchTask)
			g.POST("/api/batch-tasks/:queueId/tasks", h.Agent.AddBatchTask)
			g.DELETE("/api/batch-tasks/:queueId/tasks/:taskId", h.Agent.DeleteBatchTask)
			// External MCP（B9）：运行状态 + config.yaml 真源。（Statistics/DELETE/start/stop
			// 无密钥直连；list / 单查 / 写回由 Phase B3 脱敏包装版本承担，见下方。）
			g.GET("/api/external-mcp/stats", h.ExternalMCP.GetExternalMCPStats)
			g.DELETE("/api/external-mcp/:name", h.ExternalMCP.DeleteExternalMCP)
			g.POST("/api/external-mcp/:name/start", h.ExternalMCP.StartExternalMCP)
			g.POST("/api/external-mcp/:name/stop", h.ExternalMCP.StopExternalMCP)
			// WebShell exec（连接凭据读取 + 在线探测执行）。
			g.POST("/api/webshell/exec", h.WebShell.Exec)
			// ── Phase R2a：兼容尾巴 / 未编号前缀 ──
			// WebShell 连接附属（ai 历史/会话、连接状态、文件操作）。
			g.GET("/api/webshell/connections/:id/ai-history", h.WebShell.GetAIHistory)
			g.GET("/api/webshell/connections/:id/ai-conversations", h.WebShell.ListAIConversations)
			g.GET("/api/webshell/connections/:id/state", h.WebShell.GetConnectionState)
			g.PUT("/api/webshell/connections/:id/state", h.WebShell.SaveConnectionState)
			g.POST("/api/webshell/file", h.WebShell.FileOp)
			// Workflows 兼容尾巴（DryRun 进程内执行 / GenerateDraft LLM / Resume agent 续跑）。
			g.POST("/api/workflows/dry-run", h.Workflow.DryRun)
			g.POST("/api/workflows/generate-draft", h.Workflow.GenerateDraft)
			g.POST("/api/workflows/runs/:runId/resume", h.Workflow.ResumeRun)
			// Knowledge 写侧 / 索引 / 检索：宿主已全量接管
			// （create/update → knowledge_native.go；index/scan/search/index-status
			// → knowledge_runtime.go，含未启用时的禁用形状）。此处不再挂 findu
			// handler，避免与宿主 mux 路由重复。
			// Attack chain（PG 真源 + LLM regenerate）。
			g.GET("/api/attack-chain/:conversationId", h.AttackChain.GetAttackChain)
			g.POST("/api/attack-chain/:conversationId/regenerate", h.AttackChain.RegenerateAttackChain)
			// Fofa（外部 API 代理 + 自然语言解析）。
			g.POST("/api/fofa/search", h.Fofa.Search)
			g.POST("/api/fofa/parse", h.Fofa.ParseNaturalLanguage)
			// Phase R2b：移动逆向域全量路由交出（Register 内部自带
			// RequirePermission 与 QueryPOST 白名单注册，与 findu protected 组
			// 的挂载逐路由等价）。
			apiG := g.Group("/api")
			if h.MiniProgram != nil {
				h.MiniProgram.Register(apiG)
			}
			if h.Android != nil {
				h.Android.Register(apiG)
			}
			// Phase R3：facts 域缺口补全（B5 遗留 —— fact-edges CRUD、事实图、
			// 攻击链提升为项目事实）。真源与执行同在 shared PG + project 包，
			// 进程内同链桥（不重写实现）。
			if h.Project != nil {
				g.GET("/api/projects/:id/fact-graph", h.Project.GetFactGraph)
				g.GET("/api/projects/:id/fact-edges", h.Project.ListFactEdges)
				g.POST("/api/projects/:id/fact-edges", h.Project.CreateFactEdge)
				g.DELETE("/api/projects/:id/fact-edges/:edgeId", h.Project.DeleteFactEdge)
				g.POST("/api/projects/:id/promote-attack-chain/:conversationId", h.Project.PromoteAttackChain)
			}
			// ── Phase B3：config 脱敏桥（GET 掩码 / PUT 保真回填 / 写盘 env 引用保全）──
			// cp 供所有「写盘 env 引用保全」包装使用（config/hitl/external-mcp）。
			cp := func() string { return h.ConfigPath }
			if h.Config != nil && h.ConfigPath != "" {
				rebuildSecretRefDisplay(h.ConfigPath)
				g.GET("/api/config", ginWrapCapture(h.Config.GetConfig, maskConfigResponseBytes))
				g.PUT("/api/config", ginWrapBodyTransform(h.Config.UpdateConfig, cp, restoreConfigRequestBytes, true))
				g.POST("/api/config/apply", ginWrapBodyTransform(h.Config.ApplyConfig, cp, nil, true))
				// External MCP（写入 config.yaml 的 env/headers 同受脱敏）。
				g.GET("/api/external-mcp", ginWrapCapture(h.ExternalMCP.GetExternalMCPs, maskConfigResponseBytes))
				g.GET("/api/external-mcp/:name", ginWrapCapture(h.ExternalMCP.GetExternalMCP, maskConfigResponseBytes))
				g.PUT("/api/external-mcp/:name", ginWrapBodyTransform(h.ExternalMCP.AddOrUpdateExternalMCP, cp, restoreConfigRequestBytes, true))
			} else {
				// 无脱敏集成（极老二进制/FINDU_ENABLE=0）时直连同链 handler。
				if h.Config != nil {
					g.GET("/api/config", h.Config.GetConfig)
					g.PUT("/api/config", h.Config.UpdateConfig)
					g.POST("/api/config/apply", h.Config.ApplyConfig)
					g.GET("/api/external-mcp", h.ExternalMCP.GetExternalMCPs)
					g.GET("/api/external-mcp/:name", h.ExternalMCP.GetExternalMCP)
					g.PUT("/api/external-mcp/:name", h.ExternalMCP.AddOrUpdateExternalMCP)
				}
			}
			// config 只读附属（无密钥）。
			if h.Config != nil {
				g.GET("/api/config/tools", h.Config.GetTools)
				g.GET("/api/config/tools/:name/schema", h.Config.GetToolSchema)
				g.POST("/api/config/list-models", h.Config.ListModels)
				g.POST("/api/config/test-openai", h.Config.TestOpenAI)
				g.POST("/api/config/test-typesafe", h.Config.TestTypeSafe)
				g.POST("/api/config/test-vision", h.Config.TestVision)
				// tool-guard 同域路由（此前走 findu 兜底）。
				g.GET("/api/tool-guard", h.Config.GetToolGuard)
				g.PUT("/api/tool-guard", h.Config.UpdateToolGuard)
				g.POST("/api/tool-guard/test", h.Config.TestToolGuard)
			}
			// ── Phase B3：hitl 配置/白名单尾巴（pending/logs×3 为 B6/原生命名空间
			// 既有宿主原生，不重复注册；decision/dismiss 已由 R1 桥）──
			if h.Agent != nil {
				g.GET("/api/hitl/config/:conversationId", h.Agent.GetHITLConversationConfig)
				g.PUT("/api/hitl/config", h.Agent.UpsertHITLConversationConfig)
				g.GET("/api/hitl/tool-whitelist", h.Agent.GetHITLGlobalToolWhitelist)
				g.PUT("/api/hitl/tool-whitelist", h.Agent.SetHITLGlobalToolWhitelist)
				g.POST("/api/hitl/tool-whitelist", h.Agent.MergeHITLGlobalToolWhitelist)
				g.GET("/api/hitl/default-config", h.Agent.GetHITLDefaultConfig)
				g.PUT("/api/hitl/default-config", h.Agent.UpdateHITLDefaultConfig)
				g.GET("/api/hitl/default-reviewer", h.Agent.GetHITLDefaultReviewer)
				g.PUT("/api/hitl/default-reviewer", h.Agent.UpdateHITLDefaultReviewer)
				g.GET("/api/hitl/audit-strategy", h.Agent.GetHITLAuditStrategy)
				// audit-strategy PUT 经 HitlAuditStrategySaver 写 config.yaml
				// → 同受写盘 env 引用保全。
				g.PUT("/api/hitl/audit-strategy", ginWrapBodyTransform(h.Agent.UpdateHITLAuditStrategy, cp, nil, true))
			}
		}
		reg()
		s.rtGin = g
	})
	return s.rtGin
}
