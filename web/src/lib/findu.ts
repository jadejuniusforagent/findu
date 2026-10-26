// findu 子系统 API 客户端。
//
// 账号已统一到控制台（findu_token cookie），所以这里只需要 credentials: "include"，
// 不再携带 Bearer token，也不做二次登录。所有请求前缀已并入宿主 /api 命名空间：
// 宿主原生路由（hitl 等）优先，其余经 findu 兜底 / 宿主 gin 子引擎（rbac/android/
// miniprogram）服务——前端不再依赖 /findu 前缀。

export const PREFIX = "/api";

export class FinduError extends Error {
  readonly status: number;
  readonly payload: unknown;

  constructor(message: string, status: number, payload: unknown) {
    super(message);
    this.name = "FinduError";
    this.status = status;
    this.payload = payload;
  }
}

function messageFrom(payload: unknown): string {
  if (payload && typeof payload === "object") {
    const rec = payload as Record<string, unknown>;
    const text = rec.error ?? rec.message;
    if (typeof text === "string" && text.trim() !== "") return text;
  }
  return "";
}

/** finduFetch 发一次请求并解析 JSON；非 2xx 抛 FinduError（带后端 error 文本）。 */
export async function finduFetch<T>(path: string, init?: RequestInit): Promise<T> {
  const headers: Record<string, string> = {
    Accept: "application/json",
    ...((init?.headers as Record<string, string> | undefined) ?? {}),
  };
  if (init?.body !== undefined && !headers["Content-Type"]) {
    headers["Content-Type"] = "application/json";
  }

  const res = await fetch(PREFIX + path, {
    ...init,
    headers,
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
    const detail = messageFrom(payload) || `HTTP ${res.status}`;
    throw new FinduError(detail, res.status, payload);
  }
  return payload as T;
}

/** finduErrorMessage 把任意异常转成可展示的一句话。 */
export function finduErrorMessage(e: unknown): string {
  if (e instanceof FinduError) return e.message;
  if (e instanceof Error) return e.message;
  return String(e);
}

/** finduPost / finduPut / finduDelete 是常用动词的薄封装。 */
export function finduPost<T>(path: string, body?: unknown): Promise<T> {
  return finduFetch<T>(path, {
    method: "POST",
    body: body === undefined ? undefined : JSON.stringify(body),
  });
}

export function finduPut<T>(path: string, body?: unknown): Promise<T> {
  return finduFetch<T>(path, {
    method: "PUT",
    body: body === undefined ? undefined : JSON.stringify(body),
  });
}

export function finduDelete<T>(path: string, body?: unknown): Promise<T> {
  return finduFetch<T>(path, {
    method: "DELETE",
    body: body === undefined ? undefined : JSON.stringify(body),
  });
}

/** formatTime 统一时间展示：无法解析时回退原串。 */
export function formatTime(value?: string | null): string {
  if (!value) return "-";
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return value;
  return d.toLocaleString("zh-CN", { hour12: false });
}
