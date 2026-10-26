// Step 6c chat attachment upload client.
//
// Uploads go to the host's /api/chat/upload with scope=session. The file lands
// in <workDir>/sessions/conv-<id>/uploads/, which is the bridge conversation's
// working dir, so the returned absolute path (abs) can be passed straight to
// the bridge as attachments[].serverPath.

export interface ChatUploadAttachment {
  name: string;
  path: string;
  size: number;
  abs: string;
}

export interface ChatUploadResult {
  attachments: ChatUploadAttachment[];
}

function uploadToken(): string | null {
  if (typeof window === "undefined") return null;
  return localStorage.getItem("findu_token");
}

/** uploadChatAttachment 把文件传到会话工作目录；conversationId 是 UUID，内部拼成 conv-<id>。 */
export async function uploadChatAttachment(conversationId: string, file: File): Promise<ChatUploadAttachment> {
  const form = new FormData();
  form.append("file", file);
  const token = uploadToken();
  const res = await fetch(`/api/chat/upload?scope=session&id=conv-${encodeURIComponent(conversationId)}`, {
    method: "POST",
    headers: token ? { Authorization: `Bearer ${token}` } : {},
    body: form,
  });
  if (!res.ok) {
    const text = await res.text().catch(() => "");
    throw new Error(text || `上传失败: ${res.status}`);
  }
  const data = (await res.json()) as ChatUploadResult;
  const att = data.attachments?.[0];
  if (!att) throw new Error("上传失败：服务端未返回附件");
  return att;
}
