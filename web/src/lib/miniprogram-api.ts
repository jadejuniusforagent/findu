// 小程序分析 API 客户端（FindU 移植配套前端）。
//
// 路由走 findu 子系统的 gin（/findu/api/miniprogram/*，同进程同一 handler），
// 账号已统一到控制台 cookie；查询类接口为 POST 承载（routereg.QueryPOST 语义）。
// 响应形状与 internal/miniprogram/handler.go 对齐。

import { finduFetch, PREFIX } from "./findu";

export interface MiniprogramSample {
  id: string;
  name: string;
  sha256: string;
  size: number;
  workspace_path?: string;
  workspace_display?: string;
  appid?: string;
  file_count: number;
  project_id?: string;
  owner_user_id?: string;
  source?: string;
  created_at: string;
  updated_at?: string;
  /** 多包合并注册时返回：被分包覆盖的文件清单 */
  overwritten?: string[];
}

export interface MiniprogramFinding {
  id: string;
  sample_id: string;
  category: string;
  severity: string;
  file_path: string;
  line_number: number;
  content: string;
  description: string;
  created_at: string;
}

export interface ListSamplesResponse {
  samples: MiniprogramSample[];
  total?: number;
  page?: number;
  page_size?: number;
}

export interface ListFindingsResponse {
  findings: MiniprogramFinding[];
}

export interface MiniprogramTopologyNode {
  id: string;
  label: string;
  type: "page" | "file" | "api" | "cloudfn";
  file?: string;
  refCount: number;
  detail?: Record<string, string>;
}

export interface MiniprogramTopologyEdge {
  source: string;
  target: string;
  type: "navigate" | "api_call" | "cloudfn";
  label?: string;
  file?: string;
}

export interface MiniprogramTopology {
  nodes: MiniprogramTopologyNode[];
  edges: MiniprogramTopologyEdge[];
  stats: Record<string, number>;
}

export interface MiniprogramInterfaceItem {
  id: string;
  method: string;
  url: string;
  rawUrl?: string;
  domain: string;
  sourceFile?: string;
  refCount: number;
  hasAuth: boolean;
  idorCandidate: boolean;
  unauthCandidate: boolean;
  params: string[];
  highlights: string[];
}

export interface MiniprogramDomainStat {
  domain: string;
  count: number;
}

export interface MiniprogramInterfaceWorkbench {
  interfaces: MiniprogramInterfaceItem[];
  stats: Record<string, number>;
  domains: MiniprogramDomainStat[];
}

/** listSamples 样本列表（可选 keyword / project_id 过滤）。 */
export async function listMiniprogramSamples(params?: {
  keyword?: string;
  project_id?: string;
}): Promise<ListSamplesResponse> {
  const qs = new URLSearchParams();
  if (params?.keyword) qs.set("keyword", params.keyword);
  if (params?.project_id) qs.set("project_id", params.project_id);
  const suffix = qs.size > 0 ? `?${qs.toString()}` : "";
  return finduFetch<ListSamplesResponse>(`/miniprogram/samples${suffix}`, {
    method: "POST",
    body: JSON.stringify({}),
  });
}

/** uploadSample 上传 wxapkg（支持多个包文件，PC 微信 4.x 加密包需 appid）。 */
export async function uploadMiniprogramSample(
  files: File[],
  opts?: { appid?: string; project_id?: string },
): Promise<MiniprogramSample> {
  const form = new FormData();
  for (const f of files) {
    form.append("file", f, f.name);
  }
  if (opts?.appid) form.append("appid", opts.appid);
  if (opts?.project_id) form.append("project_id", opts.project_id);
  const res = await fetch(`${PREFIX}/miniprogram/upload`, {
    method: "POST",
    body: form,
    credentials: "include",
    cache: "no-store",
  });
  const text = await res.text();
  let payload: unknown = null;
  if (text !== "") {
    try {
      payload = JSON.parse(text);
    } catch {
      payload = { error: text.slice(0, 300) };
    }
  }
  if (!res.ok) {
    const rec = (payload ?? {}) as Record<string, unknown>;
    throw new Error(typeof rec.error === "string" ? rec.error : `HTTP ${res.status}`);
  }
  const rec = (payload ?? {}) as { sample?: MiniprogramSample };
  if (!rec.sample) {
    throw new Error("上传响应缺少 sample 字段");
  }
  return rec.sample;
}

/** deleteSample 删除样本（工作区文件一并清理）。 */
export async function deleteMiniprogramSample(id: string): Promise<void> {
  await finduFetch(`/miniprogram/samples/${encodeURIComponent(id)}`, { method: "DELETE" });
}

/** fillAppID 为挂起的加密包样本补填 AppID，触发解密解包与重扫。 */
export async function fillMiniprogramAppID(id: string, appid: string): Promise<MiniprogramSample> {
  const rec = await finduFetch<{ sample: MiniprogramSample }>(`/miniprogram/samples/${encodeURIComponent(id)}/appid`, {
    method: "POST",
    body: JSON.stringify({ appid }),
  });
  return rec.sample;
}

/** rescanSample 触发重新敏感扫描（落一条分析记录）。 */
export async function rescanMiniprogramSample(id: string): Promise<void> {
  await finduFetch(`/miniprogram/samples/${encodeURIComponent(id)}/scan`, {
    method: "POST",
    body: JSON.stringify({}),
  });
}

/** listSourceFiles 源码文件相对路径列表。 */
export async function listMiniprogramSourceFiles(id: string): Promise<string[]> {
  const rec = await finduFetch<{ files: string[] | null }>(`/miniprogram/samples/${encodeURIComponent(id)}/source`, {
    method: "POST",
    body: JSON.stringify({}),
  });
  return rec.files ?? [];
}

/** readSourceFile 读取单个源码文件内容（纯文本响应）。 */
export async function readMiniprogramSourceFile(id: string, path: string): Promise<string> {
  const qs = new URLSearchParams({ path });
  const res = await fetch(`${PREFIX}/miniprogram/samples/${encodeURIComponent(id)}/file?${qs.toString()}`, {
    credentials: "include",
    cache: "no-store",
  });
  if (!res.ok) {
    throw new Error(`读取文件失败：HTTP ${res.status}`);
  }
  return res.text();
}

/** listFindings 敏感信息发现列表。 */
export async function listMiniprogramFindings(id: string, category?: string): Promise<MiniprogramFinding[]> {
  const qs = category ? `?category=${encodeURIComponent(category)}` : "";
  const rec = await finduFetch<ListFindingsResponse>(`/miniprogram/samples/${encodeURIComponent(id)}/findings${qs}`, {
    method: "POST",
    body: JSON.stringify({}),
  });
  return rec.findings ?? [];
}

/** listTopology 样本调用链/接口拓扑分析。 */
export async function listMiniprogramTopology(id: string): Promise<MiniprogramTopology> {
  const rec = await finduFetch<MiniprogramTopology>(
    `/miniprogram/samples/${encodeURIComponent(id)}/topology`,
    { method: "POST", body: JSON.stringify({}) },
  );
  return {
    nodes: rec.nodes ?? [],
    edges: rec.edges ?? [],
    stats: rec.stats ?? {},
  };
}

/** listMiniprogramInterfaceWorkbench 样本接口工作台（接口清单 + 越权/未授权候选标注）。 */
export async function listMiniprogramInterfaceWorkbench(
  id: string,
): Promise<MiniprogramInterfaceWorkbench> {
  const rec = await finduFetch<MiniprogramInterfaceWorkbench>(
    `/miniprogram/samples/${encodeURIComponent(id)}/interface-workbench`,
    { method: "POST", body: JSON.stringify({}) },
  );
  return {
    interfaces: rec.interfaces ?? [],
    stats: rec.stats ?? {},
    domains: rec.domains ?? [],
  };
}

/** formatBytes 字节数人性化展示。 */
export function formatMiniprogramBytes(bytes?: number): string {
  if (bytes === undefined || bytes === null) return "-";
  if (bytes < 1024) return `${bytes} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let v = bytes / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i += 1;
  }
  return `${v.toFixed(1)} ${units[i]}`;
}
