// Read-only DTOs and fetchers for the Step 6b console pages.
//
// The console is being migrated page by page. Until a page is fully moved, its
// data still lives in findu: conversations are served by the host's shared PG
// handlers, while assets/skills/roles are read through /findu/api. Keeping the
// shapes here (instead of widening the write-capable lib/api types) makes the
// read-only boundary explicit: these functions never expose a mutation.

import { http } from "@/lib/api";

// ---- conversations (host-owned shared PG) ----

export interface ConversationRow {
  id: string;
  title: string;
  roleName?: string;
  agentMode?: string;
  agent_key?: string;
  projectId?: string;
  pinned?: boolean;
  running?: boolean;
  createdAt?: string;
  updatedAt?: string;
  created_at?: string;
  updated_at?: string;
}

export interface ConversationMessage {
  id: string;
  role: string;
  content: string;
  reasoningContent?: string;
  createdAt?: string;
}

export interface ConversationDetail extends ConversationRow {
  messages?: ConversationMessage[];
}

export interface ConversationPage {
  conversations: ConversationRow[];
  total: number;
  limit: number;
  offset: number;
}

export function listConversations(
  input: { search?: string; limit?: number; offset?: number; sortBy?: "created_at" | "updated_at" } = {},
): Promise<ConversationPage> {
  const q = new URLSearchParams();
  q.set("limit", String(input.limit ?? 50));
  q.set("offset", String(input.offset ?? 0));
  if (input.search?.trim()) q.set("search", input.search.trim());
  if (input.sortBy) q.set("sort_by", input.sortBy);
  return http<ConversationPage>(`/conversations?${q.toString()}`);
}

export function getConversation(id: string): Promise<ConversationDetail> {
  // findu 的 include_process_details 同时控制 messages 是否返回；控制台的
  // 会话 Sheet 和聊天页都需要消息，因此这里必须请求 1。过程详情会一并返回，
  // 由调用方按需展示。
  return http<ConversationDetail>(`/conversations/${encodeURIComponent(id)}?include_process_details=1`);
}

// ---- assets (findu-owned until 6c) ----

export interface FinduAsset {
  id: string;
  host?: string;
  ip?: string;
  port?: number;
  domain?: string;
  protocol?: string;
  title?: string;
  server?: string;
  status?: string;
  source?: string;
  source_query?: string;
  vulnerability_count?: number;
  risk_level?: string;
  last_seen_at?: string;
  first_seen_at?: string;
}

export interface FinduAssetPage {
  assets: FinduAsset[];
  total: number;
  page: number;
  page_size: number;
  total_pages: number;
}

export function listFinduAssets(
  input: { page?: number; pageSize?: number; search?: string; status?: string } = {},
): Promise<FinduAssetPage> {
  const q = new URLSearchParams();
  q.set("page", String(input.page ?? 1));
  q.set("page_size", String(input.pageSize ?? 50));
  if (input.search?.trim()) q.set("q", input.search.trim());
  if (input.status?.trim()) q.set("status", input.status.trim());
  // 已原生化：宿主 GET /api/assets/findu 返回 findu 共表形状，不再走 /findu/api。
  return http<FinduAssetPage>(`/assets/findu?${q.toString()}`);
}

// ---- skills (findu-owned until 6c) ----

export interface FinduSkill {
  id: string;
  name: string;
  dir_name?: string;
  description?: string;
  path?: string;
  file_count?: number;
  file_size?: number;
  script_count?: number;
  mod_time?: string;
  version?: string;
  progressive?: boolean;
  tags?: string[] | null;
  triggers?: string[] | null;
}

export interface FinduSkillPage {
  skills: FinduSkill[];
  total: number;
  limit: number;
  offset: number;
}

export function listFinduSkills(limit = 200): Promise<FinduSkillPage> {
  // 已原生化：宿主 GET /api/skills/findu 返回同形状，不再走 /findu/api。
  return http<FinduSkillPage>(`/skills/findu?limit=${limit}`);
}

// ---- roles (findu-owned until 6c) ----

export interface FinduRole {
  name: string;
  description?: string;
  user_prompt?: string;
  enabled?: boolean;
}

export function listFinduRoles(): Promise<{ roles: FinduRole[]; source?: string }> {
  // 已原生化：宿主 GET /api/roles 返回同形状 {roles, source}，不再走 /findu/api。
  return http<{ roles: FinduRole[]; source?: string }>("/roles");
}

// ---- projects / webshell (findu-owned until 6c; read-only here) ----

export interface FinduProject {
  id: string;
  name: string;
  description?: string;
  status?: string;
  pinned?: boolean;
  created_at?: string;
  updated_at?: string;
}

export interface FinduProjectPage {
  projects: FinduProject[];
  total: number;
  limit: number;
  offset: number;
}

export function listFinduProjects(limit = 200): Promise<FinduProjectPage> {
  // 已原生化：宿主 GET /api/projects 返回同形状，不再走 /findu/api。
  return http<FinduProjectPage>(`/projects?limit=${limit}`);
}

export interface FinduWebshellConnection {
  id: string;
  project_id?: string;
  url?: string;
  type?: string;
  method?: string;
  remark?: string;
  os?: string;
  encoding?: string;
  createdAt?: string;
}

export function listFinduWebshellConnections(): Promise<FinduWebshellConnection[]> {
  // 已原生化：宿主 GET /api/webshell/connections，不再走 /findu/api。
  return http<FinduWebshellConnection[]>("/webshell/connections");
}

// ---- 空间测绘 / 信息收集（fofa / zoomeye / quake / shodan / hunter） ----

export type SpaceSearchProvider = "fofa" | "zoomeye" | "quake" | "shodan" | "hunter";

export interface SpaceSearchRequest {
  provider?: string;
  query: string;
  size?: number;
  page?: number;
  fields?: string;
  full?: boolean;
}

export interface SpaceSearchRow {
  [key: string]: unknown;
}

export interface SpaceSearchResult {
  provider?: string;
  query: string;
  size: number;
  page: number;
  total: number;
  fields: string[];
  results_count: number;
  results: SpaceSearchRow[];
}

export interface SpaceSearchParseResult {
  query: string;
  explanation?: string;
  warnings?: string[];
}

export function searchSpace(input: SpaceSearchRequest): Promise<SpaceSearchResult> {
  // 宿主 POST /api/fofa/search，五源统一入口（FOFA/ZoomEye/Quake/Shodan/Hunter）。
  return http<SpaceSearchResult>("/fofa/search", {
    method: "POST",
    body: JSON.stringify(input),
  });
}

export function parseSpaceNaturalLanguage(provider: string, text: string): Promise<SpaceSearchParseResult> {
  // 宿主 POST /api/fofa/parse，自然语言 -> 各引擎查询语法。
  return http<SpaceSearchParseResult>("/fofa/parse", {
    method: "POST",
    body: JSON.stringify({ provider, text }),
  });
}
