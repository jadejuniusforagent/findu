// Step 6c chat streaming client.
//
// The console chat page talks to the host-owned eino bridge:
//   - eino_single -> POST /api/eino-agent/stream
//   - deep / plan_execute / supervisor -> POST /api/multi-agent/stream
// Both return the same SSE frame shape used by findu, so this module only
// normalizes transport and leaves message/UI state to the page.

import { http } from "@/lib/api";

export interface ChatStreamEvent {
  type: string;
  message?: string;
  data?: Record<string, unknown>;
}

export interface ChatStreamAttachment {
  fileName: string;
  serverPath: string;
}

export interface ChatStreamInput {
  conversationId?: string;
  message: string;
  agentMode?: string;
  /** findu 角色名称（默认/渗透测试/报告撰写/漏洞复测/审计取证），host 侧映射 agent key。 */
  role?: string;
  projectId?: string;
  webshellConnectionId?: string;
  /** 已上传到会话工作目录的附件（serverPath 为上传返回的绝对路径）。 */
  attachments?: ChatStreamAttachment[];
  signal?: AbortSignal;
  onEvent: (event: ChatStreamEvent) => void;
}

function streamPath(agentMode?: string): string {
  const raw = (agentMode ?? "").trim();
  const mode = raw ? raw : "eino_single";
  return mode === "eino_single" ? "/api/eino-agent/stream" : "/api/multi-agent/stream";
}

function parseDataLine(line: string): ChatStreamEvent | null {
  const trimmed = line.trim();
  if (!trimmed.startsWith("data:")) return null;
  const raw = trimmed.slice(5).trim();
  if (!raw) return null;
  try {
    return JSON.parse(raw) as ChatStreamEvent;
  } catch {
    return null;
  }
}

export async function streamChat(input: ChatStreamInput): Promise<void> {
  const rawMode = (input.agentMode ?? "").trim();
  const mode = rawMode ? rawMode : "eino_single";
  const body: Record<string, unknown> = { message: input.message };
  if (input.conversationId) body.conversationId = input.conversationId;
  if (mode !== "eino_single") body.orchestration = mode;
  if (input.role?.trim()) body.role = input.role.trim();
  if (input.projectId?.trim()) body.projectId = input.projectId.trim();
  if (input.webshellConnectionId?.trim()) body.webshellConnectionId = input.webshellConnectionId.trim();
  if (input.attachments?.length) {
    body.attachments = input.attachments.map((a) => ({
      fileName: a.fileName,
      serverPath: a.serverPath,
    }));
  }

  const token = typeof window !== "undefined" ? localStorage.getItem("findu_token") : null;
  const response = await fetch(streamPath(mode), {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
    body: JSON.stringify(body),
    signal: input.signal,
  });
  if (!response.ok) {
    const text = await response.text().catch(() => "");
    throw new Error(text || `请求失败: ${response.status}`);
  }
  if (!response.body) throw new Error("浏览器不支持流式响应");

  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  const dispatch = (line: string) => {
    const event = parseDataLine(line);
    if (event) input.onEvent(event);
  };

  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });
    const lines = buffer.split("\n");
    buffer = lines.pop() ?? "";
    for (const line of lines) dispatch(line);
  }
  buffer += decoder.decode();
  if (buffer.trim()) {
    for (const line of buffer.split("\n")) dispatch(line);
  }
}

export function stopConversation(id: string): Promise<{ status?: string }> {
  return http<{ status?: string }>(`/conversations/${encodeURIComponent(id)}/stop`, {
    method: "POST",
  });
}
