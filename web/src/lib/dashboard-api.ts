// 总览（仪表盘）增强数据面：findu 仪表盘同源接口的宿主原生视图。
//
// 这些接口在 Phase B/R 迁移中已全部宿主原生（或运行时桥同链），控制台用
// Bearer 直接取；单个面板失败只影响自身，不拖垮整页。
import { http } from "@/lib/api";

export interface VulnStats {
  total: number;
  by_severity: Partial<Record<"critical" | "high" | "medium" | "low" | "info", number>>;
  by_status: Record<string, number>;
}

export interface MonitorSummary {
  totalCalls: number;
  successCalls: number;
  failedCalls: number;
  blockedCalls: number;
  toolCount: number;
  lastCallTime?: string;
}

export interface MonitorTool {
  toolName: string;
  totalCalls: number;
  successCalls: number;
  failedCalls: number;
  blockedCalls: number;
  lastCallTime?: string;
}

export interface MonitorStats {
  summary: MonitorSummary;
  topTools: MonitorTool[];
}

export interface HitlPendingHead {
  items: unknown[];
  total: number;
}

export interface PluginStatus {
  id: string;
  version: string;
  kind: string;
  state: string;
  error?: string;
}

export interface PluginsOverview {
  plugins: PluginStatus[];
  providers: Record<string, string>;
}

export interface ExternalMcpStats {
  connected: number;
  enabled: number;
  disabled: number;
  total: number;
}

export interface KnowledgeStats {
  enabled: boolean;
  total_categories: number;
  total_items: number;
}

export interface RecentFact {
  id: string;
  project_id?: string;
  project_name?: string;
  fact_key?: string;
  category?: string;
  summary?: string;
  created_at?: string;
}

export interface ProjectsSummary {
  recent_facts?: RecentFact[] | null;
}

export interface WebshellConnection {
  id: string;
  url?: string;
  remark?: string;
  type?: string;
  project_id?: string;
  project_name?: string;
  updated_at?: string;
}

export interface C2Listeners {
  listeners?: unknown[] | null;
}

export interface C2Sessions {
  sessions?: unknown[] | null;
}

export interface C2Tasks {
  total: number;
  pending_queued_count: number;
  status_counts?: Record<string, number>;
}

export interface BatchQueues {
  queues?: unknown[] | null;
  total: number;
}

export interface AgentLoopTasks {
  tasks?: unknown[] | null;
}

export interface AgentItem {
  filename?: string;
  id?: string;
  name?: string;
  description?: string;
  is_orchestrator?: boolean;
  kind?: string;
}

export interface MarkdownAgents {
  agents?: AgentItem[] | null;
  dir?: string;
}

export interface ExternalMCPServerConfig {
  type?: string;
  command?: string;
  args?: string[];
  env?: Record<string, string>;
  url?: string;
  headers?: Record<string, string>;
  disabled?: boolean;
  autoApprove?: string[];
  max_retries?: number;
  terminate_duration?: number;
  keep_alive?: number;
  description?: string;
  timeout?: number;
  external_mcp_enable?: boolean;
  tool_enabled?: Record<string, boolean>;
}

export interface ExternalMCPResponse {
  config: ExternalMCPServerConfig;
  status: string;
  tool_count: number;
  error?: string;
}

export interface ExternalMCPList {
  servers: Record<string, ExternalMCPResponse>;
  stats: { connected: number; enabled: number; disabled: number; total: number };
}

export interface KnowledgeItemSummary {
  id: string;
  category: string;
  title: string;
  cn_title?: string; // 后端动态生成的中文标题
  filePath: string;
  content?: string;
  createdAt?: string;
  updatedAt?: string;
}

export interface KnowledgeItem extends KnowledgeItemSummary {
  content: string;
}

export interface KnowledgeCategoryWithItems {
  category: string;
  itemCount: number;
  items: KnowledgeItemSummary[];
}

export interface KnowledgeItemsResp {
  categories: KnowledgeCategoryWithItems[];
  total: number;
  limit?: number;
  offset?: number;
  is_search?: boolean;
  search?: string;
}

export interface KnowledgeRetrievalLog {
  id: string;
  conversationId?: string;
  messageId?: string;
  query: string;
  riskType?: string;
  retrievedItems?: string[];
  createdAt?: string;
}

export interface KnowledgeRetrievalLogsResp {
  logs: KnowledgeRetrievalLog[] | null;
}

export interface WorkflowDefinition {
  id: string;
  name: string;
  description?: string;
  version: number;
  graph_json: string;
  enabled: boolean;
  created_at?: string;
  updated_at?: string;
}

export interface WorkflowRunsResp {
  runs: unknown[] | null;
}

export interface WorkflowSaveRequest {
  id?: string;
  name: string;
  description?: string;
  version?: number;
  graph?: unknown;
  graph_json?: string;
  enabled?: boolean;
}

export interface ToolGuardRule {
  id: string;
  name: string;
  enabled: boolean;
  pattern: string;
  message: string;
}

export interface ToolGuardConfig {
  enabled: boolean;
  rules: ToolGuardRule[];
  allowPatterns: string[];
}

export interface ToolGuardMatch {
  ruleId?: string;
  ruleName?: string;
  matchedText?: string;
  message?: string;
}

export interface ToolGuardTestResult {
  blocked: boolean;
  match?: ToolGuardMatch | null;
}

export interface ToolGuardUpdatePayload {
  enabled: boolean;
  rules: ToolGuardRule[];
  allowPatterns?: string[];
}

export interface ProjectBrief {
  id: string;
  name: string;
  pinned?: boolean;
  status?: string;
  description?: string;
  created_at?: string;
  updated_at?: string;
}

export interface FactGraphNode {
  id: string;
  fact_key?: string;
  category?: string;
  confidence?: string;
  label?: string;
  summary?: string;
  pinned?: boolean;
  type?: string;
}

export interface FactGraphEdge {
  id: string;
  source: string;
  target: string;
  type?: string;
  confidence?: string;
}

export interface ProjectFactGraph {
  nodes: FactGraphNode[];
  edges: FactGraphEdge[];
}

export interface SystemService {
  id: string;
  name: string;
  description?: string;
  module_id?: string;
  module_name?: string;
  kind?: string;
  running?: boolean;
  state?: string;
  detail?: string;
  start_supported?: boolean;
  stop_supported?: boolean;
  autostart_supported?: boolean;
  autostart_enabled?: boolean;
  expected_autostart?: boolean;
  optional?: boolean;
}

export interface SystemModule {
  id: string;
  name: string;
  description?: string;
  services: SystemService[];
  running?: number;
  stopped?: number;
  total?: number;
  autostart_ok?: number;
}

export interface SystemServicesOverview {
  enabled?: boolean;
  autostart_ok?: number;
  generated_at?: string;
  modules: SystemModule[];
  running?: number;
  stopped?: number;
  total?: number;
}

export interface SkillCandidate {
  id: string;
  skillName: string;
  capability?: string;
  completion?: number;
  conversationId?: string;
  draftModel?: string;
  evidence?: string;
  hasDraft?: boolean;
  occurrences?: number;
  priority?: number;
  projectId?: string;
  score?: number;
  source?: string;
  sourceRef?: string;
  status?: string;
  updatedAt?: string;
}

export interface SkillCandidatesResult {
  candidates: SkillCandidate[];
  count?: number;
}

export const dashboardApi = {
  vulnStats: () => http<VulnStats>("/vulnerabilities/stats"),
  monitorStats: (top = 8) => http<MonitorStats>(`/monitor/stats?top=${top}`),
  hitlPendingHead: () => http<HitlPendingHead>("/hitl/pending?page=1&pageSize=1"),
  externalMcpStats: () => http<ExternalMcpStats>("/external-mcp/stats"),
  knowledgeStats: () => http<KnowledgeStats>("/knowledge/stats"),
  projectsSummary: (factLimit = 5) => http<ProjectsSummary>(`/projects/dashboard-summary?fact_limit=${factLimit}`),
  webshellConnections: () => http<WebshellConnection[]>("/webshell/connections"),
  c2Listeners: () => http<C2Listeners>("/c2/listeners"),
  c2Sessions: (limit = 5) => http<C2Sessions>(`/c2/sessions?limit=${limit}`),
  c2Tasks: () => http<C2Tasks>("/c2/tasks?page=1&page_size=1"),
  batchQueues: (limit = 5) => http<BatchQueues>(`/batch-tasks?limit=${limit}&page=1`),
  agentLoopTasks: () => http<AgentLoopTasks>("/agent-loop/tasks"),
  markdownAgents: () => http<MarkdownAgents>("/multi-agent/markdown-agents"),
  externalMcpList: () => http<ExternalMCPList>("/external-mcp"),
  externalMcpUpsert: (name: string, config: ExternalMCPServerConfig) =>
    http<{ message: string }>(`/external-mcp/${encodeURIComponent(name)}`, {
      method: "PUT",
      body: JSON.stringify({ config }),
    }),
  externalMcpDelete: (name: string) =>
    http<{ message: string }>(`/external-mcp/${encodeURIComponent(name)}`, { method: "DELETE" }),
  externalMcpStart: (name: string) =>
    http<{ message: string; status?: string }>(`/external-mcp/${encodeURIComponent(name)}/start`, {
      method: "POST",
    }),
  externalMcpStop: (name: string) =>
    http<{ message: string }>(`/external-mcp/${encodeURIComponent(name)}/stop`, { method: "POST" }),
  knowledgeCategories: () => http<{ categories: string[] }>("/knowledge/categories"),
  knowledgeItems: (limit = 50, offset = 0) =>
    http<KnowledgeItemsResp>(`/knowledge/items?limit=${limit}&offset=${offset}`),
  knowledgeItem: (id: string) => http<KnowledgeItem>(`/knowledge/items/${encodeURIComponent(id)}`),
  knowledgeDeleteItem: (id: string) =>
    http<{ message: string }>(`/knowledge/items/${encodeURIComponent(id)}`, { method: "DELETE" }),
  knowledgeRetrievalLogs: (limit = 50) => http<KnowledgeRetrievalLogsResp>(`/knowledge/retrieval-logs?limit=${limit}`),
  knowledgeDeleteLog: (id: string) =>
    http<{ message: string }>(`/knowledge/retrieval-logs/${encodeURIComponent(id)}`, { method: "DELETE" }),
  workflowsList: (includeDisabled = true) =>
    http<{ workflows: WorkflowDefinition[] | null }>(`/workflows${includeDisabled ? "?includeDisabled=true" : ""}`),
  workflowGet: (id: string) => http<{ workflow: WorkflowDefinition }>(`/workflows/${encodeURIComponent(id)}`),
  workflowSave: (body: WorkflowSaveRequest, id?: string) => {
    const path = id ? `/workflows/${encodeURIComponent(id)}` : "/workflows";
    return http<{ message: string; workflow?: WorkflowDefinition | null }>(path, {
      method: id ? "PUT" : "POST",
      body: JSON.stringify(body),
    });
  },
  workflowDelete: (id: string) =>
    http<{ message: string }>(`/workflows/${encodeURIComponent(id)}`, { method: "DELETE" }),
  workflowValidate: (graph: unknown) =>
    http<{ ok: boolean; error?: string }>("/workflows/validate", {
      method: "POST",
      body: JSON.stringify({ graph }),
    }),
  workflowRunsPending: () => http<WorkflowRunsResp>("/workflows/runs/pending"),
  toolGuardGet: () => http<ToolGuardConfig>("/tool-guard"),
  toolGuardUpdate: (payload: ToolGuardUpdatePayload) =>
    http<ToolGuardConfig>("/tool-guard", {
      method: "PUT",
      body: JSON.stringify(payload),
    }),
  toolGuardTest: (body: { config: ToolGuardConfig; toolName: string; arguments: Record<string, unknown> }) =>
    http<ToolGuardTestResult>("/tool-guard/test", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  projectsList: () => http<{ projects: ProjectBrief[]; total: number }>("/projects"),
  projectFactGraph: (id: string) => http<ProjectFactGraph>(`/projects/${encodeURIComponent(id)}/fact-graph`),
  systemServices: () => http<SystemServicesOverview>("/system-services"),
  systemServiceAction: (id: string, action: "start" | "stop" | "restart" | "autostart") =>
    http<{ message?: string }>(`/system-services/${encodeURIComponent(id)}/${action}`, {
      method: "POST",
    }),
  skillCandidates: () => http<SkillCandidatesResult>("/skills/candidates"),
  skillCandidateAction: (id: string, action: "draft" | "approve" | "reject") =>
    http<{ message?: string }>(`/skills/candidates/${encodeURIComponent(id)}/${action}`, {
      method: "POST",
    }),
  plugins: () => http<PluginsOverview>("/plugins"),
};
