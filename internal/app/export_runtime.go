package app

import (
	"finduai/internal/apkanalysis"
	"finduai/internal/database"
	"finduai/internal/handler"
	"finduai/internal/mcp"
	"finduai/internal/miniprogram"
	"finduai/internal/security"
)

// RuntimeHandles 是宿主进行 Step 6d Phase R 运行时收口所需的
// findu 运行态实例集合。
//
// 背景：B1–B8 各数据面批次已完成 DB 投影原生化；剩余 closure 域（HITL 决策/
// 撤销、agent-loop、batch-tasks、external-mcp、webshell exec）的真源是 findu
// 组装期创建并持有 goroutine/内存状态的 handler 实例（decideCh 等待者在
// agent 运行循环里阻塞，批队 executor、任务事件总线同源），宿主不可能用纯
// DB 投影替代。收口方式为：
//
//	路由所有权 —— 宿主 Go mux 显式持有 closure 前缀（findu 兼容名单随之减项）；
//	行为等价   —— 同进程 gin 子引擎挂同一批 handler 函数 + findu protected 组
//	              同链中间件（AuthMiddleware / RBACMiddlewareWithDenyHook），
//	              与 compat 时代逐字节一致（同一对象、同一方法）；
//	后续演进   —— findu 移除时这些 manager 会随 internal/*/handler/engine
//	              包归宿主所有（同 internal/workflow 既定处置），本桥届时仅
//	              剩路由挂载。
type RuntimeHandles struct {
	Auth        *security.AuthManager
	DB          *database.DB
	RBACDeny    security.RBACDenyHook
	Agent       *handler.AgentHandler
	ExternalMCP *handler.ExternalMCPHandler
	WebShell    *handler.WebShellHandler
	// Phase R2a：兼容尾巴/未编号前缀桥。
	Knowledge   *handler.KnowledgeHandler
	Workflow    *handler.WorkflowHandler
	AttackChain *handler.AttackChainHandler
	Fofa        *handler.FofaHandler
	// Phase R2b：移动逆向域（路由经 h.Register 重建挂载）。
	MiniProgram *miniprogram.Handler
	Android     *apkanalysis.Handler
	// Phase R3：facts 域桥（fact-edges/fact-graph/promote-attack-chain）。
	Project *handler.ProjectHandler
	// Phase B3：config 脱敏桥。
	Config     *handler.ConfigHandler
	ConfigPath string
}

// RuntimeHandles 返回运行时收口桥所需的实例句柄。
func (a *App) RuntimeHandles() RuntimeHandles {
	if a == nil {
		return RuntimeHandles{}
	}
	return RuntimeHandles{
		Auth:        a.auth,
		DB:          a.db,
		RBACDeny:    a.rbacDenyHook,
		Agent:       a.agentHandler,
		ExternalMCP: a.externalMCPHandler,
		WebShell:    a.webShellHandler,
		Knowledge:   a.knowledgeHandler,
		Workflow:    a.workflowHandler,
		AttackChain: a.attackChainHandler,
		Fofa:        a.fofaHandler,
		MiniProgram: a.miniprogramHandler,
		Android:     a.androidHandler,
		Project:     a.projectHandler,
		Config:      a.configHandler,
		ConfigPath:  a.finduConfigPath,
	}
}

// MCPToolSeeds 导出 findu 子系统当前已注册的全部 MCP 工具定义（按 name 排序）。
// 供宿主装配层把 android_*/miniprogram_* 等运行时注册的工具 seed 进统一
// tools 表（runtime='findu'），补齐角色/技能绑定闭环——工具此前只在
// mcp.Server 运行时注册、无表行，角色页无法绑定。
func (a *App) MCPToolSeeds() []mcp.Tool {
	if a == nil || a.mcpServer == nil {
		return nil
	}
	return a.mcpServer.RegisteredTools()
}
