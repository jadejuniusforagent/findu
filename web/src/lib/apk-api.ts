// Android APK 分析 API 客户端（FindU 移植配套前端）。
// 路由走 findu 子系统 gin（/findu/api/android/*）；查询接口 POST 承载
//（X-Api-Query-Post 标记头 + JSON body，postQueryAdapter 注入 query）。

import { finduFetch, PREFIX } from "./findu";

export interface ApkSample {
  id: string;
  name: string;
  sha256: string;
  size: number;
  workspace_path?: string;
  package_name?: string;
  file_count?: number;
  dex_count?: number;
  project_id?: string;
  owner_user_id?: string;
  source?: string;
  status?: string;
  created_at: string;
  updated_at?: string;
}

export interface ApkAutoReport {
  sample?: ApkSample;
  report?: {
    sample_name?: string;
    package_name?: string;
    [k: string]: unknown;
  } | null;
  [k: string]: unknown;
}

/** queryPost POST 查询（标记头 + JSON body 扁平注入 query）。 */
function queryPost<T>(path: string, params?: Record<string, unknown>): Promise<T> {
  return finduFetch<T>(path, {
    method: "POST",
    headers: { "X-Api-Query-Post": "1" },
    body: JSON.stringify(params ?? {}),
  });
}

/** listApkSamples 样本列表。 */
export async function listApkSamples(params?: {
  keyword?: string;
  project_id?: string;
}): Promise<{ samples: ApkSample[] }> {
  return queryPost("/android/samples", params);
}

/** getApkAutoReport 自动分析报告（上传后轮询）。后端读 query sample_id（handler.go AutoReport）。 */
export async function getApkAutoReport(apk: string): Promise<ApkAutoReport> {
  return queryPost(`/android/auto-report`, { sample_id: apk });
}

/** readApkManifest AndroidManifest.xml 文本（响应为 XML）。 */
export async function readApkManifest(apk: string): Promise<string> {
  const res = await fetch(`${PREFIX}/android/manifest?apk=` + encodeURIComponent(apk), {
    method: "POST",
    headers: { "X-Api-Query-Post": "1", "Content-Type": "application/json" },
    body: JSON.stringify({ apk }),
    credentials: "include",
    cache: "no-store",
  });
  if (!res.ok) {
    const text = await res.text().catch(() => "");
    throw new Error(`读取 Manifest 失败：HTTP ${res.status} ${text.slice(0, 120)}`);
  }
  return res.text();
}

/** getApkSource 反编译单类（jadx/dad/smali 降级链）。 */
export async function getApkSource(
  apk: string,
  classDescriptor: string,
): Promise<{ class: string; dex: string; source: string }> {
  return queryPost("/android/source", { apk, class: classDescriptor });
}

/** deleteApkSample 删除样本（含工作区）。 */
export async function deleteApkSample(id: string): Promise<void> {
  await finduFetch(`/android/samples/${encodeURIComponent(id)}`, { method: "DELETE" });
}

/** uploadApkSample 上传 APK（multipart）。 */
export async function uploadApkSample(file: File, opts?: { project_id?: string }): Promise<ApkSample> {
  const form = new FormData();
  form.append("file", file, file.name);
  if (opts?.project_id) form.append("project_id", opts.project_id);
  const res = await fetch(`${PREFIX}/android/upload`, {
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
  const rec = (payload ?? {}) as { sample?: ApkSample };
  if (!rec.sample) throw new Error("上传响应缺少 sample 字段");
  return rec.sample;
}

/** formatBytes 字节数人性化。 */
export function formatApkBytes(bytes?: number): string {
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
