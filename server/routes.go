package server

import "net/http"

// routeRegistrar 是 registerRoutes 需要的最小 mux 能力。生产路径传 *http.ServeMux，
// 契约测试传一个只记录 pattern 的实现，这样归属检查不需要启动真实服务或解析源码。
type routeRegistrar interface {
	HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request))
}

// 路由注册集中处（批 5 第一刀自 server.go 的 Handler 抽出）。
//
// 每条路由只做「模式 → handler」绑定；鉴权、CORS 与前端回退留在 Handler，
// 这样 HandlerWithFallback 可以复用同一份 s.apiMux 而不重复注册。
func (s *Server) registerRoutes(mux routeRegistrar) {
	s.registerSideRoutes(mux)

	// Auth routes — exempt from JWT check (handled in requireAuth)
	mux.HandleFunc("GET /api/auth/status", s.authStatus)
	mux.HandleFunc("POST /api/auth/init", s.authInit)
	mux.HandleFunc("POST /api/auth/login", s.authLogin)
	mux.HandleFunc("POST /api/auth/change-password", s.authChangePassword)

	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/stats", s.stats)

	// Deep / plan_execute / supervisor：宿主接管路由与鉴权，运行时由 findu
	// 多代理实现注入，避免迁移期丢失深挖能力。
	mux.HandleFunc("/api/multi-agent", s.serveMultiAgent)
	mux.HandleFunc("/api/multi-agent/", s.serveMultiAgent)
	mux.HandleFunc("GET /api/plugins", s.getPlugins)
	mux.HandleFunc("GET /api/logs", s.getLogs)
	mux.HandleFunc("GET /api/logs/history", s.getLogsHistory)
	mux.HandleFunc("GET /api/logs/stream", s.streamLogs)

	// 页面一键更新。走的是默认的 JWT 鉴权（auth.go 只放行 /api/auth/* 和
	// /api/health），所以这几个改动程序自身的接口天然需要登录。
	mux.HandleFunc("GET /api/update/check", s.updateCheck)
	mux.HandleFunc("POST /api/update/apply", s.updateApply)
	mux.HandleFunc("POST /api/update/rollback", s.updateRollback)
	mux.HandleFunc("GET /api/update/stream", s.updateStream)

	mux.HandleFunc("GET /api/tasks", s.listTasks)
	mux.HandleFunc("POST /api/tasks", s.createTask)
	mux.HandleFunc("GET /api/task-categories", s.pgListTaskCategories)
	mux.HandleFunc("POST /api/task-categories", s.pgCreateTaskCategory)
	mux.HandleFunc("PATCH /api/task-categories/{id}", s.pgRenameTaskCategory)
	mux.HandleFunc("DELETE /api/task-categories/{id}", s.pgDeleteTaskCategory)
	mux.HandleFunc("POST /api/tasks/category/batch", s.updateTasksCategoryBatch)
	mux.HandleFunc("GET /api/task-templates", s.pgListTaskTemplates)
	mux.HandleFunc("POST /api/task-templates", s.pgCreateTaskTemplate)
	mux.HandleFunc("PATCH /api/task-templates/{id}", s.pgUpdateTaskTemplate)
	mux.HandleFunc("DELETE /api/task-templates/{id}", s.pgDeleteTaskTemplate)
	mux.HandleFunc("GET /api/tasks/{id}", s.getTask)
	mux.HandleFunc("PATCH /api/tasks/{id}", s.updateTaskMetadata)
	mux.HandleFunc("PATCH /api/tasks/{id}/category", s.updateTaskCategory)
	// 任务级资产拦截/允许规则
	mux.HandleFunc("GET /api/tasks/{id}/intercept-rules", s.taskInterceptListRules)
	mux.HandleFunc("POST /api/tasks/{id}/intercept-rules", s.taskInterceptCreateRule)
	mux.HandleFunc("PUT /api/tasks/{id}/intercept-rules/{rid}", s.taskInterceptUpdateRule)
	mux.HandleFunc("DELETE /api/tasks/{id}/intercept-rules/{rid}", s.taskInterceptDeleteRule)
	mux.HandleFunc("POST /api/tasks/{id}/intercept-rules/{rid}/toggle", s.taskInterceptToggleRule)
	mux.HandleFunc("POST /api/tasks/control/batch", s.controlTasksBatch)
	mux.HandleFunc("GET /api/task-archives", s.listTaskArchives)
	mux.HandleFunc("GET /api/task-archives/{id}", s.getTaskArchive)
	mux.HandleFunc("POST /api/tasks/{id}/archive", s.queueTaskArchive)
	mux.HandleFunc("POST /api/tasks/archive/batch", s.queueTaskArchivesBatch)
	mux.HandleFunc("POST /api/task-archives/{id}/restore", s.queueTaskArchiveRestore)
	mux.HandleFunc("POST /api/task-archives/restore/batch", s.restoreTaskArchivesBatch)
	mux.HandleFunc("DELETE /api/task-archives/{id}", s.queueTaskArchiveDelete)
	mux.HandleFunc("POST /api/task-archives/delete/batch", s.deleteTaskArchivesBatch)
	mux.HandleFunc("GET /api/tasks/{id}/coverage", s.taskCoverage)
	mux.HandleFunc("GET /api/tasks/{id}/coverage-graph", s.taskCoverageGraph)
	mux.HandleFunc("GET /api/tasks/{id}/asset-refs", s.taskAssetRefs)
	mux.HandleFunc("POST /api/tasks/{id}/assets", s.attachTaskAssets)
	mux.HandleFunc("DELETE /api/tasks/{id}/assets/{assetID}", s.detachTaskAsset)
	mux.HandleFunc("GET /api/tasks/{id}/intent-assets", s.taskIntentAssets)

	// 工作空间文件管理器（针对 workDir）
	mux.HandleFunc("GET /api/workspace/list", s.wsList)
	mux.HandleFunc("GET /api/workspace/read", s.wsRead)
	mux.HandleFunc("POST /api/workspace/write", s.wsWrite)
	mux.HandleFunc("POST /api/workspace/mkdir", s.wsMkdir)
	mux.HandleFunc("DELETE /api/workspace/delete", s.wsDelete)
	mux.HandleFunc("GET /api/workspace/download", s.wsDownload)
	mux.HandleFunc("POST /api/workspace/upload", s.wsUpload)
	mux.HandleFunc("GET /api/tasks/{id}/scope", s.taskScopeList)
	mux.HandleFunc("POST /api/tasks/{id}/scope", s.taskScopeAdd)
	mux.HandleFunc("DELETE /api/tasks/{id}/scope/{sid}", s.taskScopeDelete)
	mux.HandleFunc("GET /api/tasks/{id}/goals", s.listGoals)                       // 目标管理:列出本任务全部目标
	mux.HandleFunc("POST /api/tasks/{id}/goals", s.addGoal)                        // 目标管理:人工新增目标(复活任务)
	mux.HandleFunc("PATCH /api/tasks/{id}/goals/{gid}", s.editGoal)                // 目标管理:修改目标(复活任务)
	mux.HandleFunc("DELETE /api/tasks/{id}/goals/{gid}", s.deleteGoal)             // 目标管理:硬删除目标(不复活)
	mux.HandleFunc("GET /api/tasks/{id}/constraints", s.listConstraints)           // 约束管理:列出本任务操作约束
	mux.HandleFunc("POST /api/tasks/{id}/constraints", s.addConstraint)            // 约束管理:新增约束(不通知 planner)
	mux.HandleFunc("PATCH /api/tasks/{id}/constraints/{cid}", s.editConstraint)    // 约束管理:修改约束
	mux.HandleFunc("DELETE /api/tasks/{id}/constraints/{cid}", s.deleteConstraint) // 约束管理:删除约束
	mux.HandleFunc("POST /api/tasks/{id}/control", s.control)
	mux.HandleFunc("PUT /api/tasks/{id}/llm", s.updateTaskLLMProfiles)
	mux.HandleFunc("GET /api/tasks/{id}/llm/resolution", s.taskLLMResolutionHandler)
	mux.HandleFunc("POST /api/tasks/{id}/intents/{iid}/control", s.controlIntent)
	mux.HandleFunc("POST /api/tasks/{id}/intents/{iid}/messages", s.sendWorkerMessage)
	mux.HandleFunc("POST /api/tasks/{id}/intents/{iid}/rerun", s.rerunIntent)    // 重跑单条 blocked/exhausted/stopped 意图
	mux.HandleFunc("POST /api/tasks/{id}/intents/rerun-blocked", s.rerunBlocked) // 批量重跑本任务全部 blocked 意图
	mux.HandleFunc("POST /api/active", s.setActive)

	mux.HandleFunc("GET /api/llm", s.getLLM)
	mux.HandleFunc("POST /api/llm", s.setLLM)
	mux.HandleFunc("POST /api/llm/test", s.testLLM)

	// asset system：宿主保留已有 list/counts/insert/delete；其余子路由在 Step 6d
	// 期间复用 findu handler，补齐 selection/stats/import/bulk/binding/merge 等能力。
	mux.HandleFunc("GET /api/assets", s.listAssets)
	mux.HandleFunc("GET /api/assets/counts", s.assetCounts)
	mux.HandleFunc("GET /api/assets/stats", s.assetStatsNative)
	mux.HandleFunc("GET /api/assets/findu", s.listAssetsFinduNative)
	mux.HandleFunc("POST /api/assets", s.insertAssets)
	mux.HandleFunc("DELETE /api/assets", s.deleteAssets)
	mux.HandleFunc("DELETE /api/assets/{id}", s.deleteAssetNative)
	mux.HandleFunc("POST /api/assets/batch-delete", s.batchDeleteAssetsNative)
	mux.HandleFunc("PUT /api/assets/project-binding", s.updateAssetsProjectNative)
	mux.HandleFunc("PUT /api/assets/bulk", s.bulkUpdateAssetsNative)
	mux.HandleFunc("PUT /api/assets/{id}", s.updateAssetNative)
	mux.HandleFunc("GET /api/assets/selection", s.assetSelectionNative)
	mux.HandleFunc("POST /api/assets/import", s.importAssetsNative)
	mux.HandleFunc("POST /api/assets/scan-links", s.recordAssetScansNative)
	mux.HandleFunc("POST /api/assets/merge", s.mergeAssetsNative)
	mux.HandleFunc("/api/assets/", s.serveFinduCompat)

	// company system
	mux.HandleFunc("GET /api/companies", s.listCompanies)
	mux.HandleFunc("POST /api/companies", s.createCompany)
	mux.HandleFunc("GET /api/companies/{id}", s.getCompany)
	mux.HandleFunc("DELETE /api/companies/{id}", s.deleteCompany)
	mux.HandleFunc("POST /api/companies/{id}/scope", s.addCompanyScope)
	mux.HandleFunc("POST /api/companies/reattribute", s.reattribute)

	mux.HandleFunc("GET /api/exploration/frontier", s.frontier)
	mux.HandleFunc("GET /api/exploration/findings", s.findings)
	mux.HandleFunc("GET /api/exploration/findings/groups", s.findingGroups)
	mux.HandleFunc("GET /api/exploration/findings/asset-tree", s.findingAssetTree)
	mux.HandleFunc("GET /api/exploration/findings/stats", s.findingStats)
	mux.HandleFunc("GET /api/exploration/findings/export", s.findingsExport)
	s.registerFindingTraffic(mux)
	mux.HandleFunc("GET /api/exploration/findings/{id}", s.getFinding)
	mux.HandleFunc("GET /api/exploration/findings/{id}/lineage", s.findingLineage)
	mux.HandleFunc("POST /api/exploration/findings/{id}/deepen", s.deepenFinding)
	mux.HandleFunc("GET /api/exploration/findings/{id}/retests", s.listFindingRetests)
	mux.HandleFunc("GET /api/exploration/findings/retests/active", s.listActiveFindingRetests)
	mux.HandleFunc("POST /api/exploration/findings/{id}/retests", s.startFindingRetest)
	mux.HandleFunc("PATCH /api/exploration/findings/{id}", s.patchFinding)
	mux.HandleFunc("DELETE /api/exploration/findings/{id}", s.deleteFinding)
	mux.HandleFunc("GET /api/exploration/intents", s.intents)
	mux.HandleFunc("GET /api/exploration/graph", s.explorationGraph)
	mux.HandleFunc("GET /api/exploration/nodes", s.explorationNodes)
	mux.HandleFunc("GET /api/exploration/activity", s.activity)
	mux.HandleFunc("GET /api/exploration/activity/history", s.activityHistory)
	mux.HandleFunc("GET /api/exploration/main-sessions", s.mainSessions)
	mux.HandleFunc("POST /api/exploration/main-session/new", s.newMainSession)
	mux.HandleFunc("GET /api/exploration/activity/stream", s.streamActivity)
	mux.HandleFunc("GET /api/exploration/activity/{seq}", s.activityDetail)
	mux.HandleFunc("GET /api/exploration/tokens", s.tokenStats)
	mux.HandleFunc("GET /api/tokens/daily", s.tokenDailyStats)
	mux.HandleFunc("GET /api/tokens/conversations", s.conversationTokens)
	mux.HandleFunc("GET /api/tokens/usage", s.pgUsageStats) // 全局 llm_usage 聚合（仪表盘新版视图）

	// audit：宿主保留 GET /api/audit 总览；meta/summary/logs/export 等子路由
	// 在 Step 6d 期间复用 findu handler。
	mux.HandleFunc("GET /api/audit", s.getAudit)
	mux.HandleFunc("GET /api/audit/meta", s.auditMetaNative)
	mux.HandleFunc("GET /api/audit/summary", s.auditSummaryNative)
	mux.HandleFunc("GET /api/audit/logs", s.auditLogsNative)
	mux.HandleFunc("GET /api/audit/logs/export", s.auditExportNative)
	mux.HandleFunc("GET /api/audit/logs/{id}", s.auditLogNative)
	mux.HandleFunc("/api/audit/", s.serveFinduCompat)

	// projects / webshell：宿主拥有 /api 命名空间，Step 6d 期间复用 findu handler；
	// 业务迁出后再替换为原生 store。
	mux.HandleFunc("GET /api/projects", s.listProjectsNative)
	mux.HandleFunc("POST /api/projects", s.createProjectNative)
	mux.HandleFunc("GET /api/projects/dashboard-summary", s.projectDashboardSummaryNative)
	mux.HandleFunc("GET /api/projects/{id}", s.getProjectNative)
	mux.HandleFunc("PUT /api/projects/{id}", s.updateProjectNative)
	mux.HandleFunc("DELETE /api/projects/{id}", s.deleteProjectNative)
	mux.HandleFunc("GET /api/projects/{id}/conversations", s.listProjectConversationsNative)
	mux.HandleFunc("GET /api/projects/{id}/stats", s.getProjectStatsNative)
	mux.HandleFunc("GET /api/projects/{id}/facts", s.listProjectFactsNative)
	mux.HandleFunc("POST /api/projects/{id}/facts", s.upsertProjectFactNative)
	mux.HandleFunc("PUT /api/projects/{id}/facts/{factId}", s.updateProjectFactNative)
	mux.HandleFunc("DELETE /api/projects/{id}/facts/{factId}", s.deleteProjectFactNative)
	mux.HandleFunc("POST /api/projects/{id}/facts/deprecate", s.deprecateProjectFactNative)
	mux.HandleFunc("POST /api/projects/{id}/facts/restore", s.restoreProjectFactNative)
	mux.HandleFunc("/api/projects/", s.serveFinduCompat)
	mux.HandleFunc("/api/webshell", s.serveFinduCompat)
	mux.HandleFunc("GET /api/webshell/connections", s.listWebshellsNative)
	mux.HandleFunc("POST /api/webshell/connections", s.createWebshellNative)
	mux.HandleFunc("PUT /api/webshell/connections/{id}", s.updateWebshellNative)
	mux.HandleFunc("DELETE /api/webshell/connections/{id}", s.deleteWebshellNative)
	mux.HandleFunc("/api/webshell/", s.serveFinduCompat)

	// 其余 findu 资源前缀：宿主统一持有 /api 路由与鉴权，底层暂时复用 findu
	// handler。C2 仍保持 findu 外部槽位，不在此列表。
	// Phase B3 后 compat 名单已清空（config 已入脱敏桥，见下方运行桥注册）。

	// Phase B3：config 脱敏桥（宿主 mux 显式持有全前缀）+ hitl 配置尾巴。
	mux.HandleFunc("/api/config", s.finduRuntimeBridge)
	mux.HandleFunc("/api/config/", s.finduRuntimeBridge)
	mux.HandleFunc("/api/hitl", s.finduRuntimeBridge)
	mux.HandleFunc("/api/hitl/", s.finduRuntimeBridge)
	// tool-guard 同域（ConfigHandler 持有，此前走 findu 兜底）。
	mux.HandleFunc("/api/tool-guard", s.finduRuntimeBridge)
	mux.HandleFunc("/api/tool-guard/", s.finduRuntimeBridge)

	// Phase R2a：findu 侧从未注册这些前缀的 API（此前 compat 落 findu NoRoute
	// 返回 SPA HTML 200）。宿主显式收口为鉴权后的 404 JSON。
	for _, prefix := range []string{
		"/api/categories",
		"/api/files",
		"/api/events",
		"/api/dingtalk",
		"/api/exchange",
	} {
		mux.HandleFunc(prefix, s.finduDeadPrefix)
		mux.HandleFunc(prefix+"/", s.finduDeadPrefix)
	}

	// vulnerability system（Phase B1 原生）
	mux.HandleFunc("GET /api/vulnerabilities", s.listVulnerabilitiesNative)
	mux.HandleFunc("GET /api/vulnerabilities/export", s.exportVulnerabilitiesNative)
	mux.HandleFunc("DELETE /api/vulnerabilities/batch", s.batchDeleteVulnerabilitiesNative)
	mux.HandleFunc("GET /api/vulnerabilities/filter-options", s.getVulnerabilityFilterOptionsNative)
	mux.HandleFunc("GET /api/vulnerabilities/stats", s.getVulnerabilityStatsNative)
	mux.HandleFunc("GET /api/vulnerabilities/{id}", s.getVulnerabilityNative)
	mux.HandleFunc("POST /api/vulnerabilities", s.createVulnerabilityNative)
	mux.HandleFunc("PUT /api/vulnerabilities/{id}", s.updateVulnerabilityNative)
	mux.HandleFunc("DELETE /api/vulnerabilities/{id}", s.deleteVulnerabilityNative)
	mux.HandleFunc("/api/vulnerabilities/", s.serveFinduCompat)

	// role system（Phase B2 原生：读 agents 真源，写收口控制台）
	mux.HandleFunc("GET /api/roles", s.listRolesNative)
	mux.HandleFunc("GET /api/roles/{name}", s.getRoleNative)
	mux.HandleFunc("POST /api/roles", s.createRoleNative)
	mux.HandleFunc("PUT /api/roles/{name}", s.updateRoleNative)
	mux.HandleFunc("DELETE /api/roles/{name}", s.deleteRoleNative)

	// hitl（Phase B6 部分：4 条纯 DB 投影/清理原生；Phase R1 桥 decision/dismiss；
	// Phase B3 桥 config/tool-whitelist/default-* 尾巴，见上方 registrar）
	mux.HandleFunc("GET /api/hitl/pending", s.listHitlPendingNative)
	mux.HandleFunc("GET /api/hitl/logs", s.listHitlLogsNative)
	mux.HandleFunc("DELETE /api/hitl/logs", s.deleteHitlLogsNative)
	mux.HandleFunc("GET /api/hitl/logs/{id}", s.getHitlLogNative)

	// chat-uploads（Phase B8 原生：10 条文件管理路由整件移植）
	mux.HandleFunc("GET /api/chat-uploads", s.listChatUploadsNative)
	mux.HandleFunc("GET /api/chat-uploads/export", s.exportChatUploadsNative)
	mux.HandleFunc("GET /api/chat-uploads/download", s.downloadChatUploadsNative)
	mux.HandleFunc("GET /api/chat-uploads/path", s.resolveChatUploadsPathNative)
	mux.HandleFunc("GET /api/chat-uploads/content", s.getChatUploadContentNative)
	mux.HandleFunc("POST /api/chat-uploads", s.uploadChatUploadNative)
	mux.HandleFunc("POST /api/chat-uploads/mkdir", s.mkdirChatUploadNative)
	mux.HandleFunc("DELETE /api/chat-uploads", s.deleteChatUploadNative)
	mux.HandleFunc("PUT /api/chat-uploads/rename", s.renameChatUploadNative)
	mux.HandleFunc("PUT /api/chat-uploads/content", s.putChatUploadContentNative)
	mux.HandleFunc("/api/chat-uploads/", s.serveFinduCompat)

	// knowledge（Phase B7 原生：读侧 7 条；创建/更新/scan/index/search/index-status 仍走兼容）
	mux.HandleFunc("GET /api/knowledge/categories", s.listKnowledgeCategoriesNative)
	mux.HandleFunc("GET /api/knowledge/items", s.listKnowledgeItemsNative)
	mux.HandleFunc("GET /api/knowledge/items/{id}", s.getKnowledgeItemNative)
	mux.HandleFunc("DELETE /api/knowledge/items/{id}", s.deleteKnowledgeItemNative)
	mux.HandleFunc("GET /api/knowledge/retrieval-logs", s.listKnowledgeRetrievalLogsNative)
	mux.HandleFunc("DELETE /api/knowledge/retrieval-logs/{id}", s.deleteKnowledgeRetrievalLogNative)
	mux.HandleFunc("GET /api/knowledge/stats", s.getKnowledgeStatsNative)
	mux.HandleFunc("/api/knowledge/", s.serveFinduCompat)

	// workflows（Phase B5 原生：定义 CRUD 5 条 + validate/export + 运行读侧 3 条；
	// DryRun/GenerateDraft/ResumeRun 及包检查/导入仍走兼容）
	mux.HandleFunc("GET /api/workflows", s.listWorkflowsNative)
	mux.HandleFunc("GET /api/workflows/{id}", s.getWorkflowNative)
	mux.HandleFunc("POST /api/workflows", s.workflowCreateNative)
	mux.HandleFunc("PUT /api/workflows/{id}", s.workflowUpdateNative)
	mux.HandleFunc("DELETE /api/workflows/{id}", s.deleteWorkflowNative)
	mux.HandleFunc("POST /api/workflows/validate", s.validateWorkflowNative)
	mux.HandleFunc("GET /api/workflows/runs/pending", s.listWorkflowPendingRunsNative)
	mux.HandleFunc("GET /api/workflows/runs/package", s.workflowRunPackageNative)
	mux.HandleFunc("GET /api/workflows/runs/{runId}", s.getWorkflowRunNative)
	mux.HandleFunc("GET /api/workflows/runs/{runId}/replay", s.replayWorkflowRunNative)
	// GET 托底分发：/api/workflows/{id}/package（Go 1.22 不允许 {id}/package 与
	// runs/{runId} 双通配注册——gin 静态树同理按"非 runs 前缀"解析）；其余 GET
	// 未注册路径仍走 findu 兼容（与原列表兜底一致）。
	mux.HandleFunc("GET /api/workflows/", s.workflowGetDispatchNative)
	// 非 GET 的未注册路径（POST dry-run / generate-draft、POST runs/{id}/resume、
	// 包检查/导入）走 findu 兼容兜底。
	mux.HandleFunc("/api/workflows/", s.serveFinduCompat)

	// Step 6d Phase R1 运行时收口桥：closure 域由宿主 Go mux 显式持有，进程内
	// 挂 findu 同链中间件 + 原 handler 函数（见 server/runtime_bridge.go）。
	mux.HandleFunc("POST /api/hitl/decision", s.finduRuntimeBridge)
	mux.HandleFunc("POST /api/hitl/dismiss", s.finduRuntimeBridge)
	mux.HandleFunc("/api/agent-loop", s.finduRuntimeBridge)
	mux.HandleFunc("/api/agent-loop/", s.finduRuntimeBridge)
	mux.HandleFunc("/api/batch-tasks", s.finduRuntimeBridge)
	mux.HandleFunc("/api/batch-tasks/", s.finduRuntimeBridge)
	mux.HandleFunc("/api/external-mcp", s.finduRuntimeBridge)
	mux.HandleFunc("/api/external-mcp/", s.finduRuntimeBridge)
	mux.HandleFunc("POST /api/webshell/exec", s.execWebshellNative)
	// ── Phase R2a：兼容尾巴 / 未编号前缀路由交出 ──
	// attack-chain / fofa 全前缀经桥（宿主 mux 显式持有，findu gin 越过）。
	mux.HandleFunc("/api/attack-chain", s.finduRuntimeBridge)
	mux.HandleFunc("/api/attack-chain/", s.finduRuntimeBridge)
	mux.HandleFunc("/api/fofa", s.finduRuntimeBridge)
	mux.HandleFunc("/api/fofa/", s.finduRuntimeBridge)
	// workflows 兼容尾巴：dry-run / generate-draft / resume（package 检查导入
	// 维持 compat——findu 无对应路由，compat 保持 404 对照语义）。
	mux.HandleFunc("POST /api/workflows/dry-run", s.workflowDryRunNative)
	mux.HandleFunc("POST /api/workflows/generate-draft", s.workflowGenerateDraftNative)
	mux.HandleFunc("POST /api/workflows/runs/{runId}/resume", s.finduRuntimeBridge)
	// WebShell 连接附属尾巴（ai 历史/会话、连接状态、文件操作）。
	mux.HandleFunc("GET /api/webshell/connections/{id}/ai-history", s.finduRuntimeBridge)
	mux.HandleFunc("GET /api/webshell/connections/{id}/ai-conversations", s.finduRuntimeBridge)
	mux.HandleFunc("GET /api/webshell/connections/{id}/state", s.getWebshellStateNative)
	mux.HandleFunc("PUT /api/webshell/connections/{id}/state", s.saveWebshellStateNative)
	mux.HandleFunc("POST /api/webshell/file", s.fileOpWebshellNative)
	// Knowledge 写侧 / 索引 / 检索尾巴（读侧 7 条 B7 已原生）。
	mux.HandleFunc("POST /api/knowledge/items", s.createKnowledgeItemNative)
	mux.HandleFunc("PUT /api/knowledge/items/{id}", s.updateKnowledgeItemNative)
	mux.HandleFunc("GET /api/knowledge/index-status", s.knowledgeIndexStatusNative)
	mux.HandleFunc("POST /api/knowledge/index", s.knowledgeStartIndexNative)
	mux.HandleFunc("POST /api/knowledge/scan", s.knowledgeScanNative)
	mux.HandleFunc("POST /api/knowledge/search", s.knowledgeSearchNative)
	// 移动逆向域（Phase R2b）：miniprogram / android 全前缀经桥。
	mux.HandleFunc("/api/miniprogram", s.finduRuntimeBridge)
	mux.HandleFunc("/api/miniprogram/", s.finduRuntimeBridge)
	mux.HandleFunc("/api/android", s.finduRuntimeBridge)
	mux.HandleFunc("/api/android/", s.finduRuntimeBridge)
	// Project facts / edges / graph / promote（Phase R3 缺口补全：进程内同链桥；
	// 由 findu 兜底改为宿主显式持有 → 未授权时同样过 requireAuth）。
	mux.HandleFunc("GET /api/projects/{id}/fact-graph", s.finduRuntimeBridge)
	mux.HandleFunc("GET /api/projects/{id}/fact-edges", s.finduRuntimeBridge)
	mux.HandleFunc("POST /api/projects/{id}/fact-edges", s.finduRuntimeBridge)
	mux.HandleFunc("DELETE /api/projects/{id}/fact-edges/{edgeId}", s.finduRuntimeBridge)
	mux.HandleFunc("POST /api/projects/{id}/promote-attack-chain/{conversationId}", s.finduRuntimeBridge)

	mux.HandleFunc("POST /api/gc", s.gc)
	mux.HandleFunc("GET /api/traffic", s.getTraffic)
	mux.HandleFunc("GET /api/traffic/hosts", s.getTrafficHosts)
	mux.HandleFunc("DELETE /api/traffic", s.deleteTraffic)
	mux.HandleFunc("DELETE /api/traffic/hosts", s.deleteTrafficHosts)
	mux.HandleFunc("DELETE /api/traffic/all", s.deleteAllTraffic)
	mux.HandleFunc("GET /api/traffic/exchange", s.getTrafficExchange)
	mux.HandleFunc("GET /api/traffic/blob", s.getTrafficBlob)
	mux.HandleFunc("GET /api/commands", s.pgListCommands)
	mux.HandleFunc("GET /api/commands/stats", s.pgToolStats) // 按工具聚合调用次数

	// 插件路由：当前由 plugins/intel 提供 /api/vulnkb* 与 /api/vulnintel*。
	// 每个 handler 外层包状态检查，插件停止后返回 503。
	s.registerPluginRoutes(mux)

	// ---- 报告生成(AI 叙述 + 确定性渲染;GET /api/report 是基线 markdown 报告,已在上方注册)----
	mux.HandleFunc("POST /api/report/generate", s.generateReport)
	mux.HandleFunc("GET /api/report/status", s.reportStatus)
	mux.HandleFunc("GET /api/report/download", s.downloadReport)
	mux.HandleFunc("GET /api/report-settings", s.getReportSettings)
	mux.HandleFunc("PUT /api/report-settings", s.putReportSettings)
	mux.HandleFunc("POST /api/report-assets/logo", s.uploadReportLogo)
	mux.HandleFunc("GET /api/report-assets/{file}", s.serveReportAsset)
	mux.HandleFunc("GET /api/reports", s.listReports)
	mux.HandleFunc("GET /api/reports/{id}/download", s.downloadReportByID)
	mux.HandleFunc("DELETE /api/reports/{id}", s.deleteReportByID)
	mux.HandleFunc("GET /api/report-templates", s.listReportTemplates)
	mux.HandleFunc("POST /api/report-templates", s.createReportTemplate)
	mux.HandleFunc("PUT /api/report-templates/{id}", s.updateReportTemplate)
	mux.HandleFunc("DELETE /api/report-templates/{id}", s.deleteReportTemplate)
	mux.HandleFunc("POST /api/tasks/{id}/extract-findings", s.extractFindings)
	mux.HandleFunc("POST /api/findings/{id}/enrich", s.findingEnrich)
	mux.HandleFunc("GET /api/findings/{id}/enrich/status", s.findingEnrichStatus)
	mux.HandleFunc("GET /api/llm/records", s.pgListLLMRecords)
	mux.HandleFunc("DELETE /api/llm/records", s.pgDeleteLLMRecords)
	mux.HandleFunc("GET /api/llm/records/tasks", s.pgLLMTasks)
	mux.HandleFunc("GET /api/llm/records/by-model", s.pgTokenByModel) // 按模型聚合本任务 token 用量
	mux.HandleFunc("GET /api/llm/records/{id}", s.pgGetLLMRecord)
	mux.HandleFunc("GET /api/settings", s.getSettings)
	mux.HandleFunc("PUT /api/settings", s.putSettings)
	// 漏洞 IM 推送。渠道是「多实例 + 各自过滤规则」的资源，因此独立成一组
	// REST 接口，而不是塞进扁平的 /api/settings 键值里。
	mux.HandleFunc("GET /api/notify/meta", s.notifyMeta)
	mux.HandleFunc("GET /api/notify/channels", s.notifyListChannels)
	mux.HandleFunc("POST /api/notify/channels", s.notifyCreateChannel)
	mux.HandleFunc("PATCH /api/notify/channels/{id}", s.notifyUpdateChannel)
	mux.HandleFunc("DELETE /api/notify/channels/{id}", s.notifyDeleteChannel)
	mux.HandleFunc("POST /api/notify/channels/{id}/test", s.notifyTestChannel)
	mux.HandleFunc("GET /api/notify/deliveries", s.notifyListDeliveries)
	mux.HandleFunc("POST /api/notify/deliveries/{id}/retry", s.notifyRetryDelivery)
	mux.HandleFunc("POST /api/settings/web-search/test", s.testWebSearch)
	mux.HandleFunc("GET /api/report", s.getReport)
	mux.HandleFunc("GET /api/chat/mentions", s.searchChatMentions)
	mux.HandleFunc("POST /api/chat", s.chat)
	mux.HandleFunc("POST /api/chat/upload", s.chatUpload) // 方式1 文件上传:落到会话/任务工作目录 uploads/
	mux.HandleFunc("GET /api/tasks/{id}/chat/status", s.taskChatStatus)
	mux.HandleFunc("POST /api/tasks/{id}/chat/stop", s.stopChat)

	// --- 管理后台 API (PostgreSQL 数据源; 新版数据库与管理后台方案) ---
	mux.HandleFunc("DELETE /api/tasks/{id}", s.pgDeleteTask)
	// Agents
	// conversations (chat page)
	mux.HandleFunc("GET /api/conversations", s.FinduListConversations)
	mux.HandleFunc("POST /api/conversations", s.FinduCreateConversation)
	mux.HandleFunc("GET /api/conversations/{id}", s.FinduGetConversation)
	mux.HandleFunc("PUT /api/conversations/{id}", s.FinduUpdateConversation)
	mux.HandleFunc("PUT /api/conversations/{id}/pinned", s.FinduUpdateConversationPinned)
	mux.HandleFunc("PUT /api/conversations/{id}/project", s.FinduUpdateConversationProject)
	mux.HandleFunc("POST /api/conversations/{id}/delete-turn", s.FinduDeleteConversationTurn)
	mux.HandleFunc("GET /api/conversations/{id}/plan-tasks", s.FinduConversationPlanTasks)
	mux.HandleFunc("GET /api/conversations/{id}/token-usage", s.FinduConversationTokenUsage)
	mux.HandleFunc("GET /api/conversations/{id}/results", s.FinduConversationResults)
	mux.HandleFunc("POST /api/conversations/delete/batch", s.pgDeleteConversationsBatch)
	mux.HandleFunc("PATCH /api/conversations/{id}", s.pgRenameConversation)
	mux.HandleFunc("PATCH /api/conversations/{id}/profile", s.pgUpdateConversation)
	mux.HandleFunc("DELETE /api/conversations/{id}", s.FinduDeleteConversation)
	mux.HandleFunc("GET /api/conversations/{id}/messages", s.pgConversationMessages)
	mux.HandleFunc("POST /api/conversations/{id}/messages", s.pgSendConversationMessage)
	mux.HandleFunc("POST /api/conversations/{id}/stop", s.pgStopConversation)
	mux.HandleFunc("GET /api/conversations/{id}/messages/{seq}", s.pgConversationMsgDetail)

	// findu-compatible chat entry: actual findu UI posts here. The bridge keeps
	// findu messages/process_details and executes the turn on findu ChatAgent.
	mux.HandleFunc("POST /api/eino-agent", s.EinoAgentBridge(false))
	mux.HandleFunc("POST /api/eino-agent/stream", s.EinoAgentBridge(true))

	mux.HandleFunc("GET /api/agents", s.pgListAgents)
	mux.HandleFunc("POST /api/agents", s.pgCreateAgent)
	mux.HandleFunc("GET /api/agents/{key}", s.pgGetAgent)
	mux.HandleFunc("PATCH /api/agents/{key}", s.pgUpdateAgent)
	mux.HandleFunc("DELETE /api/agents/{key}", s.pgDeleteAgent)
	mux.HandleFunc("PUT /api/agents/{key}/config", s.pgSaveAgentConfig)
	mux.HandleFunc("PUT /api/agents/{key}/prompt", s.pgSavePrompt)
	mux.HandleFunc("POST /api/agents/{key}/prompt/reset", s.pgResetPrompt)
	mux.HandleFunc("PUT /api/agents/{key}/wrapup", s.pgSaveWrapup)
	mux.HandleFunc("POST /api/agents/{key}/wrapup/reset", s.pgResetWrapup)
	mux.HandleFunc("PUT /api/agents/{key}/wrapup/task-timeout", s.pgSaveTaskTimeoutWrapup)
	mux.HandleFunc("POST /api/agents/{key}/wrapup/task-timeout/reset", s.pgResetTaskTimeoutWrapup)
	mux.HandleFunc("GET /api/agents/{key}/triggers", s.pgListTriggers)
	mux.HandleFunc("POST /api/agents/{key}/triggers", s.pgCreateTrigger)
	mux.HandleFunc("PATCH /api/triggers/{id}", s.pgUpdateTrigger)
	mux.HandleFunc("DELETE /api/triggers/{id}", s.pgDeleteTrigger)
	mux.HandleFunc("GET /api/agents/{key}/prompts", s.pgListPromptVersions)
	mux.HandleFunc("GET /api/agents/{key}/variables", s.pgPromptVars)
	mux.HandleFunc("POST /api/agents/{key}/prompt/preview", s.pgPreviewPrompt)
	mux.HandleFunc("GET /api/agents/{key}/visibility", s.pgGetAgentVisibility)
	mux.HandleFunc("PUT /api/agents/{key}/visibility", s.pgSetAgentVisibility)
	// 内置工具目录（描述/参数默认值可改、按 agent 绑定；key 与 handler 在代码层）
	mux.HandleFunc("GET /api/tools", s.pgListTools)
	mux.HandleFunc("PUT /api/tools/{key}", s.pgUpdateTool)
	mux.HandleFunc("POST /api/tools/custom", s.pgCreateCustomTool)
	mux.HandleFunc("POST /api/tools/custom/test", s.pgTestCustomTool)
	mux.HandleFunc("PUT /api/tools/custom/{key}", s.pgUpdateCustomTool)
	mux.HandleFunc("DELETE /api/tools/custom/{key}", s.pgDeleteCustomTool)
	mux.HandleFunc("POST /api/settings/python/detect", s.pgDetectPython)
	mux.HandleFunc("POST /api/tools/{key}/reset", s.pgResetTool)
	// MCP CRUD
	mux.HandleFunc("GET /api/mcp", s.pgListMCP)
	mux.HandleFunc("POST /api/mcp", s.pgSaveMCP)
	mux.HandleFunc("DELETE /api/mcp/{id}", s.pgDeleteMCP)
	mux.HandleFunc("GET /api/mcp/{id}/tools", s.pgMCPTools)
	mux.HandleFunc("POST /api/mcp/{id}/refresh", s.pgRefreshMCP)
	// 资产同步 — ScopeSentry 数据源
	mux.HandleFunc("GET /api/sync/scopesentry/status", s.syncSSStatus)
	mux.HandleFunc("POST /api/sync/scopesentry/datasource", s.syncSSDatasource)
	mux.HandleFunc("GET /api/sync/scopesentry/projects", s.syncSSProjects)
	mux.HandleFunc("GET /api/sync/scopesentry/tasks", s.syncSSTasks)
	mux.HandleFunc("POST /api/sync/scopesentry/sync", s.syncSSRun)
	// Skill CRUD (文件系统)
	mux.HandleFunc("GET /api/skills", s.fsListSkills)
	mux.HandleFunc("GET /api/skills/findu", s.listSkillsFinduNative)
	mux.HandleFunc("POST /api/skills", s.fsCreateSkill)
	mux.HandleFunc("POST /api/skills/upload", s.fsUploadSkill)
	mux.HandleFunc("DELETE /api/skills/{name}", s.fsDeleteSkill)
	mux.HandleFunc("GET /api/skills/missing", s.fsMissingSkills)   // 未命中(想调但不存在)的 skill 名
	mux.HandleFunc("GET /api/skills/{name}/usage", s.fsSkillUsage) // 单个 skill 的最近调用
	mux.HandleFunc("PUT /api/skills/{name}/meta", s.fsUpdateSkillMeta)
	mux.HandleFunc("POST /api/skills/{name}/dirs", s.fsCreateDir)
	mux.HandleFunc("GET /api/skills/{name}/files", s.fsListFiles)
	// {file...} captures path segments including slashes (e.g. scripts/extract.py)
	mux.HandleFunc("GET /api/skills/{name}/files/{file...}", s.fsReadFile)
	mux.HandleFunc("PUT /api/skills/{name}/files/{file...}", s.fsWriteFile)
	mux.HandleFunc("DELETE /api/skills/{name}/files/{file...}", s.fsDeletePath)
	// MCP 资源侧可见性（更具体的 skill 路由会优先匹配）
	mux.HandleFunc("GET /api/visibility/{kind}/{id}", s.pgResourceVisibility)
	mux.HandleFunc("POST /api/visibility/toggle", s.pgToggleVisibility)
	// Skill 可见性（按名称，更具体，优先于上面的通配路由）
	mux.HandleFunc("GET /api/visibility/skill/{name}", s.pgSkillVisibility)
	mux.HandleFunc("POST /api/visibility/skill/toggle", s.pgToggleSkillVisibility)
	// LLM 多 profile
	mux.HandleFunc("GET /api/llm/profiles", s.pgListProfiles)
	mux.HandleFunc("POST /api/llm/profiles", s.pgSaveProfile)
	mux.HandleFunc("DELETE /api/llm/profiles/{id}", s.pgDeleteProfile)
	mux.HandleFunc("POST /api/llm/profiles/active", s.pgActivateProfile)
	mux.HandleFunc("GET /api/llm/retry-policy", s.pgGetLLMRetryPolicy)
	mux.HandleFunc("POST /api/llm/retry-policy", s.pgSaveLLMRetryPolicy)
	mux.HandleFunc("GET /api/llm/pool", s.pgLLMPoolStatus)
	mux.HandleFunc("POST /api/llm/pool/reset", s.pgLLMPoolReset)
	mux.HandleFunc("POST /api/llm/models", s.pgListModels)

	// 拦截规则管理
	mux.HandleFunc("GET /api/intercept/rules", s.interceptListRules)
	mux.HandleFunc("POST /api/intercept/rules", s.interceptCreateRule)
	mux.HandleFunc("PUT /api/intercept/rules/{id}", s.interceptUpdateRule)
	mux.HandleFunc("DELETE /api/intercept/rules/{id}", s.interceptDeleteRule)
	mux.HandleFunc("POST /api/intercept/rules/{id}/toggle", s.interceptToggleRule)

	// 资产拦截规则管理（全局黑名单：域名/IP/URL/CIDR）
	mux.HandleFunc("GET /api/asset-intercept/rules", s.assetInterceptListRules)
	mux.HandleFunc("POST /api/asset-intercept/rules", s.assetInterceptCreateRule)
	mux.HandleFunc("PUT /api/asset-intercept/rules/{id}", s.assetInterceptUpdateRule)
	mux.HandleFunc("DELETE /api/asset-intercept/rules/{id}", s.assetInterceptDeleteRule)
	mux.HandleFunc("POST /api/asset-intercept/rules/{id}/toggle", s.assetInterceptToggleRule)

	mux.HandleFunc("GET /api/intercept/pending", s.interceptListPending)
	mux.HandleFunc("GET /api/intercept/pending/{id}", s.interceptGetOne)
	mux.HandleFunc("POST /api/intercept/pending/{id}/decide", s.interceptDecide)
	mux.HandleFunc("GET /api/intercept/history", s.interceptHistory)
	mux.HandleFunc("GET /api/intercept/history/{id}", s.interceptDetail)
	mux.HandleFunc("GET /api/intercept/history/{id}/execution", s.interceptExecution)
	mux.HandleFunc("GET /api/intercept/task/{taskID}", s.interceptListTaskItems)
	mux.HandleFunc("GET /api/intercept/tool-config", s.interceptGetToolConfig)
	mux.HandleFunc("PUT /api/intercept/tool-config", s.interceptSetToolConfig)
	mux.HandleFunc("GET /api/intercept/judge", s.interceptGetJudgeConfig)
	mux.HandleFunc("PUT /api/intercept/judge", s.interceptSetJudgeConfig)
	mux.HandleFunc("GET /api/intercept/judge/usage", s.interceptJudgeUsage) // 兜底审批累计 token 用量
}
