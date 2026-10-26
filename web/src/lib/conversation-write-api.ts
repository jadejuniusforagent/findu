// Step 6c conversation mutations.
//
// Read-only DTOs stay in readonly-api.ts; this module is the explicit write
// boundary for the console's dual-run conversation management. All calls use
// the host's /api BFF, which serves the same shared PG rows as findu.

import { http } from "@/lib/api";
import type { ConversationRow } from "@/lib/readonly-api";

export function createConversation(
  input: { title?: string; agentMode?: string; roleName?: string; projectId?: string } = {},
): Promise<ConversationRow> {
  const body: Record<string, unknown> = {
    title: input.title ?? "",
    agentMode: input.agentMode ?? "eino_single",
  };
  if (input.roleName?.trim()) body.roleName = input.roleName.trim();
  if (input.projectId?.trim()) body.projectId = input.projectId.trim();
  return http<ConversationRow>("/conversations", {
    method: "POST",
    body: JSON.stringify(body),
  });
}

export function renameConversation(id: string, title: string): Promise<ConversationRow> {
  return http<ConversationRow>(`/conversations/${encodeURIComponent(id)}`, {
    method: "PUT",
    body: JSON.stringify({ title }),
  });
}

export function setConversationPinned(id: string, pinned: boolean): Promise<{ message?: string }> {
  return http<{ message?: string }>(`/conversations/${encodeURIComponent(id)}/pinned`, {
    method: "PUT",
    body: JSON.stringify({ pinned }),
  });
}

/** setConversationProject 把会话绑定到项目（空值解绑）；走宿主 PUT /api/conversations/{id}/project。 */
export function setConversationProject(
  id: string,
  projectId: string,
): Promise<{ success: boolean; projectId: string }> {
  return http<{ success: boolean; projectId: string }>(`/conversations/${encodeURIComponent(id)}/project`, {
    method: "PUT",
    body: JSON.stringify({ projectId }),
  });
}

export function deleteConversation(id: string): Promise<{ deleted: string }> {
  return http<{ deleted: string }>(`/conversations/${encodeURIComponent(id)}`, {
    method: "DELETE",
  });
}
