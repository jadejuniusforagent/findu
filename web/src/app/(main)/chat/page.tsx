"use client";

import * as React from "react";

import {
  BotIcon,
  CheckSquareIcon,
  FileIcon,
  ListChecksIcon,
  Loader2Icon,
  MessageSquareIcon,
  PaperclipIcon,
  PinIcon,
  PinOffIcon,
  PlusIcon,
  RefreshCwIcon,
  SearchIcon,
  SendIcon,
  SquareIcon,
  Trash2Icon,
  UserIcon,
  XIcon,
} from "lucide-react";
import { toast } from "sonner";

import { Markdown } from "@/components/markdown";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Textarea } from "@/components/ui/textarea";
import { uploadChatAttachment } from "@/lib/chat-attachment-api";
import { type ChatStreamAttachment, type ChatStreamEvent, stopConversation, streamChat } from "@/lib/chat-stream-api";
import {
  createConversation,
  deleteConversation,
  setConversationPinned,
  setConversationProject,
} from "@/lib/conversation-write-api";
import { formatTime } from "@/lib/findu";
import {
  type ConversationDetail,
  type ConversationMessage,
  type ConversationRow,
  type FinduProject,
  type FinduWebshellConnection,
  getConversation,
  listConversations,
  listFinduProjects,
  listFinduWebshellConnections,
} from "@/lib/readonly-api";
import { cn } from "@/lib/utils";

const PAGE_SIZE = 50;

const MODES = [
  { value: "eino_single", label: "Eino 单代理" },
  { value: "deep", label: "Deep" },
  { value: "plan_execute", label: "Plan-Execute" },
  { value: "supervisor", label: "Supervisor" },
];

const ROLES = [
  { value: "", label: "默认" },
  { value: "渗透测试", label: "渗透测试" },
  { value: "报告撰写", label: "报告撰写" },
  { value: "漏洞复测", label: "漏洞复测" },
  { value: "审计取证", label: "审计取证" },
];

/** PendingUpload 是本地待发送附件（已上传到会话工作目录，发送时携带 serverPath）。 */
interface PendingUpload {
  key: string;
  name: string;
  size: number;
  uploading: boolean;
  error?: string;
  abs?: string;
}

function modeLabel(mode?: string) {
  return MODES.find((m) => m.value === mode)?.label ?? (mode ? mode : "Eino 单代理");
}

function roleLabel(role: string) {
  if (role === "user") return "你";
  if (role === "assistant") return "助手";
  if (role === "tool") return "工具";
  if (role === "system") return "系统";
  return role || "消息";
}

function newLocalId(prefix: string) {
  return `${prefix}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
}

function conversationBadge(c: ConversationRow) {
  if (c.running) {
    return (
      <Badge variant="secondary" className="shrink-0 text-[10px]">
        运行中
      </Badge>
    );
  }
  if (c.pinned) {
    return (
      <Badge variant="outline" className="shrink-0 text-[10px]">
        置顶
      </Badge>
    );
  }
  return null;
}

function ConversationList({
  rows,
  listLoading,
  listError,
  selectedId,
  streaming,
  searchQ,
  onSearch,
  onRefresh,
  onOpen,
  total,
  hasMore,
  loadingMore,
  onLoadMore,
  bulkMode,
  onToggleBulkMode,
  selectedSet,
  onToggleSelect,
  onDeleteOne,
  onBulkDelete,
  onBulkPin,
  bulkBusy,
}: {
  rows: ConversationRow[];
  listLoading: boolean;
  listError: string;
  selectedId: string | null;
  streaming: boolean;
  searchQ: string;
  onSearch: (value: string) => void;
  onRefresh: () => void;
  onOpen: (row: ConversationRow) => void;
  total: number;
  hasMore: boolean;
  loadingMore: boolean;
  onLoadMore: () => void;
  bulkMode: boolean;
  onToggleBulkMode: () => void;
  selectedSet: Set<string>;
  onToggleSelect: (id: string) => void;
  onDeleteOne: (row: ConversationRow) => void;
  onBulkDelete: () => void;
  onBulkPin: () => void;
  bulkBusy: boolean;
}) {
  const allSelected = rows.length > 0 && rows.every((r) => selectedSet.has(r.id));
  let body: React.ReactNode;
  if (listLoading && rows.length === 0) {
    body = (
      <div className="space-y-2">
        {Array.from({ length: 6 }).map((_, i) => (
          <Skeleton key={i} className="h-16 w-full" />
        ))}
      </div>
    );
  } else if (listError) {
    body = <div className="text-destructive p-3 text-sm">加载失败：{listError}</div>;
  } else if (rows.length === 0) {
    body = <div className="text-muted-foreground p-3 text-sm">没有匹配的会话</div>;
  } else {
    body = (
      <div className="space-y-1.5">
        {rows.map((c) => {
          const isSelected = selectedSet.has(c.id);
          return (
            <div
              key={c.id}
              className={cn(
                "group flex items-center gap-1.5 rounded-lg border transition-colors hover:bg-muted",
                selectedId === c.id && !bulkMode && "border-primary bg-muted",
                bulkMode && isSelected && "border-primary bg-muted",
              )}
            >
              {bulkMode ? (
                <button
                  type="button"
                  className="flex shrink-0 items-center self-stretch pl-2.5"
                  onClick={() => onToggleSelect(c.id)}
                  aria-label={isSelected ? "取消选择" : "选择"}
                >
                  <Checkbox checked={isSelected} />
                </button>
              ) : null}
              <button
                type="button"
                onClick={() => onOpen(c)}
                disabled={streaming}
                className={cn("min-w-0 flex-1 p-2.5 text-left", bulkMode && "pr-1")}
              >
                <div className="flex items-center justify-between gap-2">
                  <span className="truncate text-sm font-medium">{c.title || "（无标题会话）"}</span>
                  {conversationBadge(c)}
                </div>
                <div className="text-muted-foreground mt-1 flex items-center gap-1.5 text-[10px]">
                  <span>{c.roleName || "默认"}</span>
                  <span>·</span>
                  <span>{modeLabel(c.agentMode)}</span>
                  {c.updatedAt || c.updated_at ? (
                    <>
                      <span>·</span>
                      <span>{formatTime(c.updatedAt || c.updated_at)}</span>
                    </>
                  ) : null}
                </div>
              </button>
              {!bulkMode ? (
                <button
                  type="button"
                  onClick={() => onDeleteOne(c)}
                  disabled={streaming || c.running}
                  title={c.running ? "运行中的会话不能删除" : "删除会话"}
                  className="text-muted-foreground mr-1.5 hidden shrink-0 items-center rounded p-1.5 hover:bg-destructive/10 hover:text-destructive group-hover:flex disabled:opacity-50"
                >
                  <Trash2Icon className="size-3.5" />
                </button>
              ) : null}
            </div>
          );
        })}
      </div>
    );
  }

  return (
    <Card className="flex min-h-0 flex-col overflow-hidden">
      <div className="border-b p-3">
        <div className="relative">
          <SearchIcon className="text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2" />
          <Input value={searchQ} onChange={(e) => onSearch(e.target.value)} placeholder="搜索会话" className="pl-8" />
        </div>
        <div className="text-muted-foreground mt-2 flex items-center justify-between gap-2 text-[10px]">
          <span>{listLoading ? "加载中…" : `${rows.length} / ${total} 个会话`}</span>
          <div className="flex items-center gap-1">
            <Button
              size="sm"
              variant={bulkMode ? "default" : "ghost"}
              className="h-6 px-2"
              onClick={onToggleBulkMode}
              disabled={listLoading}
            >
              <ListChecksIcon className="size-3" /> {bulkMode ? "退出批量" : "批量"}
            </Button>
            <Button size="sm" variant="ghost" className="h-6 px-2" onClick={onRefresh} disabled={listLoading}>
              <RefreshCwIcon className={cn("size-3", listLoading && "animate-spin")} /> 刷新
            </Button>
          </div>
        </div>
        {bulkMode ? (
          <div className="mt-2 flex flex-wrap items-center justify-between gap-2 rounded-md border bg-muted/40 p-1.5">
            <div className="flex cursor-pointer items-center gap-1.5 text-xs font-medium">
              <Checkbox checked={allSelected} onCheckedChange={onToggleSelect.bind(null, "__all__")} />
              <span>全选（{selectedSet.size}）</span>
            </div>
            <div className="flex items-center gap-1">
              <Button
                size="sm"
                variant="outline"
                className="h-6 px-2 text-[10px]"
                disabled={selectedSet.size === 0 || bulkBusy}
                onClick={onBulkPin}
              >
                <PinIcon className="size-3" /> 置顶
              </Button>
              <Button
                size="sm"
                variant="destructive"
                className="h-6 px-2 text-[10px]"
                disabled={selectedSet.size === 0 || bulkBusy}
                onClick={onBulkDelete}
              >
                <Trash2Icon className="size-3" /> 批量删除
              </Button>
            </div>
          </div>
        ) : null}
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto p-2">{body}</div>
      {hasMore ? (
        <div className="border-t p-2">
          <Button
            size="sm"
            variant="ghost"
            className="w-full"
            onClick={onLoadMore}
            disabled={loadingMore || listLoading}
          >
            {loadingMore ? <Loader2Icon className="size-3.5 animate-spin" /> : <RefreshCwIcon className="size-3.5" />}
            加载更多
          </Button>
        </div>
      ) : null}
    </Card>
  );
}

function MessageBubble({ message }: { message: ConversationMessage }) {
  const isUser = message.role === "user";
  return (
    <div className={cn("flex gap-2", isUser ? "justify-end" : "justify-start")}>
      {!isUser ? (
        <div className="bg-muted mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-full">
          <BotIcon className="size-4" />
        </div>
      ) : null}
      <div
        className={cn(
          "max-w-[85%] rounded-lg border p-3",
          isUser ? "bg-primary text-primary-foreground" : "bg-background",
          message.role === "system" && "border-destructive/40 text-destructive",
        )}
      >
        <div
          className={cn("mb-1 text-[10px] opacity-70", isUser ? "text-primary-foreground/80" : "text-muted-foreground")}
        >
          {roleLabel(message.role)}
          {message.createdAt ? ` · ${formatTime(message.createdAt)}` : ""}
        </div>
        {message.role === "assistant" ? (
          <Markdown text={message.content || "…"} />
        ) : (
          <div className="text-sm whitespace-pre-wrap break-words">{message.content}</div>
        )}
      </div>
      {isUser ? (
        <div className="bg-muted mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-full">
          <UserIcon className="size-4" />
        </div>
      ) : null}
    </div>
  );
}

function ChatMessages({
  messages,
  selectedId,
  detailLoading,
  detailError,
  writeEnabled,
  scrollRef,
  endRef,
}: {
  messages: ConversationMessage[];
  selectedId: string | null;
  detailLoading: boolean;
  detailError: string;
  writeEnabled: boolean;
  scrollRef: React.RefObject<HTMLDivElement | null>;
  endRef: React.RefObject<HTMLDivElement | null>;
}) {
  let body: React.ReactNode;
  if (!selectedId) {
    body = (
      <div className="text-muted-foreground flex h-full items-center justify-center text-sm">选择左侧会话开始查看</div>
    );
  } else if (detailLoading && messages.length === 0) {
    body = (
      <div className="space-y-3">
        {Array.from({ length: 4 }).map((_, i) => (
          <Skeleton key={i} className="h-20 w-full" />
        ))}
      </div>
    );
  } else if (detailError) {
    body = (
      <div className="text-destructive rounded-lg border border-destructive/40 p-3 text-sm">
        加载失败：{detailError}
      </div>
    );
  } else if (messages.length === 0) {
    body = (
      <div className="text-muted-foreground flex h-full items-center justify-center text-sm">
        {writeEnabled ? "发送第一条消息开始对话" : "该会话还没有消息"}
      </div>
    );
  } else {
    body = (
      <div className="space-y-4">
        {messages.map((m) => (
          <MessageBubble key={m.id} message={m} />
        ))}
      </div>
    );
  }

  return (
    <div ref={scrollRef} className="min-h-0 flex-1 overflow-y-auto p-4">
      {body}
      <div ref={endRef} />
    </div>
  );
}

function Composer({
  writeEnabled,
  selectedId,
  draft,
  onDraft,
  onSend,
  onStop,
  isBusy,
  canSend,
  textareaRef,
  pending,
  onAddFiles,
  onRemoveFile,
  role,
  onRoleChange,
  projects,
  projectId,
  onProjectChange,
  webshells,
  webshellConnectionId,
  onWebshellChange,
  fileInputRef,
}: {
  writeEnabled: boolean;
  selectedId: string | null;
  draft: string;
  onDraft: (value: string) => void;
  onSend: () => void;
  onStop: () => void;
  isBusy: boolean;
  canSend: boolean;
  textareaRef: React.RefObject<HTMLTextAreaElement | null>;
  pending: PendingUpload[];
  onAddFiles: (files: FileList | null) => void;
  onRemoveFile: (key: string) => void;
  role: string;
  onRoleChange: (value: string) => void;
  projects: FinduProject[];
  projectId: string;
  onProjectChange: (value: string) => void;
  webshells: FinduWebshellConnection[];
  webshellConnectionId: string;
  onWebshellChange: (value: string) => void;
  fileInputRef: React.RefObject<HTMLInputElement | null>;
}) {
  if (!writeEnabled) {
    return (
      <div className="text-muted-foreground flex items-center justify-between gap-3 text-sm">
        <span>只读模式：发送/停止入口已隐藏。</span>
      </div>
    );
  }
  const canUpload = Boolean(selectedId) && !isBusy;
  return (
    <div className="border-t p-3">
      <div className="mb-2 flex flex-wrap items-center gap-2">
        <input
          ref={fileInputRef}
          type="file"
          multiple
          className="hidden"
          onChange={(e) => {
            onAddFiles(e.target.files);
            e.target.value = "";
          }}
        />
        <Button
          size="sm"
          variant="outline"
          className="h-7 px-2 text-xs"
          onClick={() => fileInputRef.current?.click()}
          disabled={!canUpload}
        >
          <PaperclipIcon className="size-3.5" /> 附件
        </Button>
        <Select value={role} onValueChange={onRoleChange} disabled={!selectedId || isBusy}>
          <SelectTrigger className="h-7 w-[118px] text-xs">
            <SelectValue placeholder="角色" />
          </SelectTrigger>
          <SelectContent>
            {ROLES.map((r) => (
              <SelectItem key={r.value || "default"} value={r.value}>
                {r.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Select value={projectId} onValueChange={onProjectChange} disabled={!selectedId || isBusy}>
          <SelectTrigger className="h-7 w-[150px] text-xs">
            <SelectValue placeholder="项目（未绑定）" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="">未绑定</SelectItem>
            {projects.map((p) => (
              <SelectItem key={p.id} value={p.id}>
                {p.name || p.id}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Select value={webshellConnectionId} onValueChange={onWebshellChange} disabled={!selectedId || isBusy}>
          <SelectTrigger className="h-7 w-[150px] text-xs">
            <SelectValue placeholder="WebShell（未绑定）" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="">未绑定</SelectItem>
            {webshells.map((w) => (
              <SelectItem key={w.id} value={w.id}>
                {w.remark || w.id}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
      <Textarea
        ref={textareaRef}
        value={draft}
        onChange={(e) => onDraft(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" && !e.shiftKey) {
            e.preventDefault();
            onSend();
          }
        }}
        placeholder={selectedId ? "输入消息，Enter 发送，Shift+Enter 换行" : "先选择或新建会话"}
        rows={3}
        disabled={!selectedId || isBusy}
      />
      <div className="mt-2 flex items-center justify-between gap-2">
        <div className="flex flex-wrap items-center gap-2">
          <PendingFileChips pending={pending} onRemove={onRemoveFile} />
          <span className="text-muted-foreground text-[10px]">
            {isBusy ? "当前会话正在生成；可停止后继续。" : "附件、角色、项目/WebShell 绑定已接入 /api BFF。"}
          </span>
        </div>
        <div className="flex items-center gap-2">
          {isBusy ? (
            <Button variant="destructive" onClick={onStop}>
              <SquareIcon className="size-4" /> 停止
            </Button>
          ) : null}
          <Button onClick={onSend} disabled={!canSend}>
            <SendIcon className="size-4" /> 发送
          </Button>
        </div>
      </div>
    </div>
  );
}

function PendingFileChips({ pending, onRemove }: { pending: PendingUpload[]; onRemove: (key: string) => void }) {
  if (pending.length === 0) return null;
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      {pending.map((a) => (
        <span
          key={a.key}
          title={a.error || `${a.name}（${a.size} 字节）`}
          className={cn(
            "bg-muted flex items-center gap-1 rounded-md px-2 py-0.5 text-[11px]",
            a.error ? "border border-destructive/50 text-destructive" : "",
          )}
        >
          <FileIcon className="size-3" />
          <span className="max-w-[160px] truncate">{a.name}</span>
          {a.uploading ? <Loader2Icon className="size-3 animate-spin" /> : null}
          <button type="button" onClick={() => onRemove(a.key)} className="hover:text-foreground text-muted-foreground">
            <XIcon className="size-3" />
          </button>
        </span>
      ))}
    </div>
  );
}

export default function ChatPage() {
  // U2（2026-10-23）：双跑验证阀退役——写侧后端已宿主原生，聊天直接可写。
  const writeEnabled = true;
  const [rows, setRows] = React.useState<ConversationRow[]>([]);
  const [listLoading, setListLoading] = React.useState(true);
  const [listError, setListError] = React.useState("");
  const [listTotal, setListTotal] = React.useState(0);
  const [loadingMore, setLoadingMore] = React.useState(false);
  const [bulkMode, setBulkMode] = React.useState(false);
  const [selectedSet, setSelectedSet] = React.useState<Set<string>>(new Set());
  const [deleteOneTarget, setDeleteOneTarget] = React.useState<ConversationRow | null>(null);
  const [deleteOneLoading, setDeleteOneLoading] = React.useState(false);
  const [bulkDeleteOpen, setBulkDeleteOpen] = React.useState(false);
  const [bulkDeleteLoading, setBulkDeleteLoading] = React.useState(false);
  const [bulkPinLoading, setBulkPinLoading] = React.useState(false);
  const [searchQ, setSearchQ] = React.useState("");
  const [selectedId, setSelectedId] = React.useState<string | null>(null);
  const [detail, setDetail] = React.useState<ConversationDetail | null>(null);
  const [messages, setMessages] = React.useState<ConversationMessage[]>([]);
  const [detailLoading, setDetailLoading] = React.useState(false);
  const [detailError, setDetailError] = React.useState("");
  const [draft, setDraft] = React.useState("");
  const [streaming, setStreaming] = React.useState(false);
  const [streamConvId, setStreamConvId] = React.useState<string | null>(null);
  const [streamError, setStreamError] = React.useState("");
  const [newMode, setNewMode] = React.useState("eino_single");
  const [creating, setCreating] = React.useState(false);
  const [pending, setPending] = React.useState<PendingUpload[]>([]);
  const [role, setRole] = React.useState("");
  const [projectId, setProjectId] = React.useState("");
  const [webshellConnectionId, setWebshellConnectionId] = React.useState("");
  const [projects, setProjects] = React.useState<FinduProject[]>([]);
  const [webshells, setWebshells] = React.useState<FinduWebshellConnection[]>([]);
  const [refsLoading, setRefsLoading] = React.useState(false);

  const abortRef = React.useRef<AbortController | null>(null);
  const streamStateRef = React.useRef<{ streamId: string; assistantId: string }>({
    streamId: "",
    assistantId: "",
  });
  const listSeqRef = React.useRef(0);
  const detailSeqRef = React.useRef(0);
  const selectedIdRef = React.useRef<string | null>(null);
  const scrollRef = React.useRef<HTMLDivElement | null>(null);
  const endRef = React.useRef<HTMLDivElement | null>(null);
  const textareaRef = React.useRef<HTMLTextAreaElement | null>(null);
  const fileInputRef = React.useRef<HTMLInputElement | null>(null);

  React.useEffect(() => {
    selectedIdRef.current = selectedId;
  }, [selectedId]);

  React.useEffect(() => {
    return () => abortRef.current?.abort();
  }, []);

  // biome-ignore lint/correctness/useExhaustiveDependencies: 依赖用于触发切会话/新消息后滚动到底。
  React.useEffect(() => {
    const raf = requestAnimationFrame(() => {
      endRef.current?.scrollIntoView({ block: "end", behavior: "auto" });
    });
    return () => cancelAnimationFrame(raf);
  }, [messages, selectedId, detailLoading]);

  const loadList = React.useCallback(async (search: string, offset = 0, append = false) => {
    const seq = ++listSeqRef.current;
    if (append) setLoadingMore(true);
    else setListLoading(true);
    setListError("");
    try {
      const page = await listConversations({ limit: PAGE_SIZE, offset, search: search.trim() || undefined });
      if (seq !== listSeqRef.current) return;
      setListTotal(page.total ?? 0);
      setRows((prev) => (append ? [...prev, ...(page.conversations ?? [])] : (page.conversations ?? [])));
    } catch (e) {
      if (seq !== listSeqRef.current) return;
      setListError((e as Error).message);
    } finally {
      if (seq === listSeqRef.current) {
        setListLoading(false);
        setLoadingMore(false);
      }
    }
  }, []);

  React.useEffect(() => {
    const timer = window.setTimeout(() => void loadList(searchQ, 0, false), 250);
    return () => window.clearTimeout(timer);
  }, [loadList, searchQ]);

  const loadMore = React.useCallback(() => {
    if (loadingMore || listLoading) return;
    void loadList(searchQ, rows.length, true);
  }, [loadList, loadingMore, listLoading, rows.length, searchQ]);

  function toggleBulkMode() {
    setBulkMode((v) => !v);
    setSelectedSet(new Set());
  }

  function toggleSelect(id: string) {
    if (id === "__all__") {
      setSelectedSet((prev) => (prev.size === rows.length ? new Set() : new Set(rows.map((r) => r.id))));
      return;
    }
    setSelectedSet((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  async function submitDeleteOne() {
    const target = deleteOneTarget;
    if (!target) return;
    setDeleteOneLoading(true);
    try {
      await deleteConversation(target.id);
      toast.success("会话已删除");
      setDeleteOneTarget(null);
      setRows((prev) => prev.filter((r) => r.id !== target.id));
      if (selectedId === target.id) {
        setSelectedId(null);
        setDetail(null);
        setMessages([]);
        setDetailError("");
      }
    } catch (e) {
      toast.error(`删除会话失败：${(e as Error).message}`);
    } finally {
      setDeleteOneLoading(false);
    }
  }

  async function submitBulkDelete() {
    const ids = Array.from(selectedSet);
    if (ids.length === 0) return;
    setBulkDeleteLoading(true);
    try {
      for (const id of ids) await deleteConversation(id);
      toast.success(`已删除 ${ids.length} 个会话`);
      setBulkDeleteOpen(false);
      setSelectedSet(new Set());
      setBulkMode(false);
      setRows((prev) => prev.filter((r) => !ids.includes(r.id)));
      if (selectedId && ids.includes(selectedId)) {
        setSelectedId(null);
        setDetail(null);
        setMessages([]);
        setDetailError("");
      }
    } catch (e) {
      toast.error(`批量删除失败：${(e as Error).message}`);
    } finally {
      setBulkDeleteLoading(false);
    }
  }

  async function submitBulkPin() {
    const ids = Array.from(selectedSet);
    if (ids.length === 0) return;
    const targetPin = rows.some((r) => selectedSet.has(r.id) && !r.pinned);
    setBulkPinLoading(true);
    try {
      for (const id of ids) await setConversationPinned(id, targetPin);
      toast.success(targetPin ? `已置顶 ${ids.length} 个会话` : `已取消 ${ids.length} 个会话的置顶`);
      setRows((prev) => prev.map((r) => (selectedSet.has(r.id) ? { ...r, pinned: targetPin } : r)));
      setSelectedSet(new Set());
      setBulkMode(false);
    } catch (e) {
      toast.error(`批量置顶失败：${(e as Error).message}`);
    } finally {
      setBulkPinLoading(false);
    }
  }

  // 加载项目 / WebShell 引用数据（findu 只读接口），供绑定选择器使用。
  // biome-ignore lint/correctness/useExhaustiveDependencies: writeEnabled 是真实 guard 依赖。
  React.useEffect(() => {
    if (!writeEnabled) return;
    let cancelled = false;
    setRefsLoading(true);
    void Promise.allSettled([listFinduProjects(500), listFinduWebshellConnections()]).then(([pr, wr]) => {
      if (cancelled) return;
      if (pr.status === "fulfilled") setProjects(pr.value.projects ?? []);
      if (wr.status === "fulfilled") setWebshells(wr.value ?? []);
      setRefsLoading(false);
    });
    return () => {
      cancelled = true;
    };
  }, [writeEnabled]);

  const loadDetail = React.useCallback(async (id: string) => {
    const seq = ++detailSeqRef.current;
    setDetailLoading(true);
    setDetailError("");
    try {
      const next = await getConversation(id);
      if (seq !== detailSeqRef.current) return;
      setDetail(next);
      setMessages(next.messages ?? []);
      setRole(next.roleName ?? "");
      setProjectId(next.projectId ?? "");
      const row: ConversationRow = {
        id: next.id,
        title: next.title,
        roleName: next.roleName,
        agentMode: next.agentMode,
        projectId: next.projectId,
        pinned: next.pinned,
        running: next.running,
        createdAt: next.createdAt,
        updatedAt: next.updatedAt,
        created_at: next.created_at,
        updated_at: next.updated_at,
      };
      setRows((prev) => {
        const idx = prev.findIndex((r) => r.id === id);
        if (idx === -1) return [row, ...prev];
        return prev.map((r) => (r.id === id ? { ...r, ...row } : r));
      });
    } catch (e) {
      if (seq !== detailSeqRef.current) return;
      setDetailError((e as Error).message);
    } finally {
      if (seq === detailSeqRef.current) setDetailLoading(false);
    }
  }, []);

  const openConversation = React.useCallback(
    (row: ConversationRow) => {
      if (streaming) {
        toast.info("当前会话正在生成，请先停止");
        return;
      }
      setSelectedId(row.id);
      setDetail(null);
      setMessages([]);
      setDetailError("");
      setStreamError("");
      window.history.replaceState(null, "", `/chat/?id=${encodeURIComponent(row.id)}`);
      void loadDetail(row.id);
    },
    [loadDetail, streaming],
  );

  React.useEffect(() => {
    if (selectedId) return;
    const fromUrl = new URLSearchParams(window.location.search).get("id");
    if (fromUrl) {
      const row = rows.find((r) => r.id === fromUrl);
      if (row) openConversation(row);
      else {
        setSelectedId(fromUrl);
        void loadDetail(fromUrl);
      }
      return;
    }
    if (rows.length > 0) openConversation(rows[0]);
  }, [loadDetail, openConversation, rows, selectedId]);

  const selectedRow = rows.find((r) => r.id === selectedId) ?? detail ?? null;
  const selectedMode = detail?.agentMode || selectedRow?.agentMode || "eino_single";
  const isBusy = streaming || Boolean(selectedRow?.running);
  const canSend = writeEnabled && Boolean(selectedId) && !isBusy && draft.trim().length > 0;

  async function newConversation() {
    if (!writeEnabled || streaming || creating) return;
    setCreating(true);
    try {
      const conv = await createConversation({
        title: "新会话",
        agentMode: newMode,
        roleName: role || undefined,
        projectId: projectId || undefined,
      });
      setRows((prev) => [conv, ...prev.filter((r) => r.id !== conv.id)]);
      setSelectedId(conv.id);
      setDetail(conv);
      setMessages([]);
      setDetailError("");
      setStreamError("");
      setPending([]);
      window.history.replaceState(null, "", `/chat/?id=${encodeURIComponent(conv.id)}`);
      toast.success(`已新建会话（${modeLabel(newMode)}）`);
      textareaRef.current?.focus();
    } catch (e) {
      toast.error(`新建会话失败：${(e as Error).message}`);
    } finally {
      setCreating(false);
    }
  }

  function addFiles(files: FileList | null) {
    const convId = selectedId;
    if (!writeEnabled || !convId || !files || files.length === 0 || streaming) return;
    const fileArr = Array.from(files);
    const entries: PendingUpload[] = fileArr.map((f) => ({
      key: newLocalId("upload"),
      name: f.name,
      size: f.size,
      uploading: true,
    }));
    setPending((prev) => [...prev, ...entries]);
    for (let i = 0; i < fileArr.length; i++) {
      const file = fileArr[i];
      const key = entries[i].key;
      void uploadChatAttachment(convId, file)
        .then((att) => {
          setPending((prev) =>
            prev.map((p) =>
              p.key === key ? { ...p, uploading: false, abs: att.abs, name: att.name, size: att.size } : p,
            ),
          );
        })
        .catch((e) => {
          setPending((prev) =>
            prev.map((p) => (p.key === key ? { ...p, uploading: false, error: (e as Error).message } : p)),
          );
          toast.error(`上传失败：${(e as Error).message}`);
        });
    }
  }

  function removeFile(key: string) {
    setPending((prev) => prev.filter((p) => p.key !== key));
  }

  function commitProjectBinding(value: string) {
    const convId = selectedId;
    setProjectId(value);
    if (!writeEnabled || !convId || !detail) return;
    void setConversationProject(convId, value)
      .then(() => toast.success(value ? "已绑定项目" : "已解除项目绑定"))
      .catch((e) => toast.error(`项目绑定失败：${(e as Error).message}`));
  }

  function appendSystemMessage(content: string) {
    setMessages((prev) => [
      ...prev,
      {
        id: newLocalId("local-system"),
        role: "system",
        content,
        createdAt: new Date().toISOString(),
      },
    ]);
  }

  function applyStreamEvent(event: ChatStreamEvent) {
    const data = event.data ?? {};
    if (data.einoScope === "sub") return;
    const streamId = typeof data.streamId === "string" ? data.streamId : "";

    if (event.type === "response_delta") {
      const delta = event.message ?? "";
      if (!delta) return;
      const current = streamStateRef.current;
      if (!current.assistantId || (streamId && current.streamId && streamId !== current.streamId)) {
        const assistantId = newLocalId("local-assistant");
        streamStateRef.current = { streamId, assistantId };
        setMessages((prev) => [
          ...prev,
          { id: assistantId, role: "assistant", content: delta, createdAt: new Date().toISOString() },
        ]);
        return;
      }
      const assistantId = current.assistantId;
      setMessages((prev) => prev.map((m) => (m.id === assistantId ? { ...m, content: m.content + delta } : m)));
      return;
    }

    if (event.type === "response") {
      const finalText = event.message ?? "";
      const current = streamStateRef.current;
      if (!current.assistantId || (streamId && current.streamId && streamId !== current.streamId)) {
        if (!finalText.trim()) return;
        const assistantId = newLocalId("local-assistant");
        streamStateRef.current = { streamId, assistantId };
        setMessages((prev) => [
          ...prev,
          { id: assistantId, role: "assistant", content: finalText, createdAt: new Date().toISOString() },
        ]);
        return;
      }
      const assistantId = current.assistantId;
      setMessages((prev) => prev.map((m) => (m.id === assistantId ? { ...m, content: finalText } : m)));
      streamStateRef.current = { streamId: "", assistantId: "" };
      return;
    }

    if (event.type === "error" || event.type === "cancelled") {
      let message = event.message;
      if (!message) message = event.type === "cancelled" ? "已停止" : "执行失败";
      setStreamError(message);
      appendSystemMessage(message);
      return;
    }

    if (event.type === "done" && data.workflowStatus === "awaiting_hitl") {
      const message = "等待人工审批，请到「HITL 审批」页面处理";
      setStreamError(message);
      appendSystemMessage(message);
    }
  }

  async function send() {
    const text = draft.trim();
    const convId = selectedId;
    if (!writeEnabled || !convId || !text || streaming) return;

    const ready = pending.filter((p) => !p.uploading && p.abs);
    const pendingUploads = pending.filter((p) => p.uploading);
    if (pendingUploads.length > 0) {
      toast.error("附件仍在上传，请稍候再发送");
      return;
    }
    const attachments: ChatStreamAttachment[] = ready.map((p) => ({
      fileName: p.name,
      serverPath: p.abs as string,
    }));

    const controller = new AbortController();
    abortRef.current = controller;
    setStreaming(true);
    setStreamConvId(convId);
    setStreamError("");
    setDraft("");
    setPending([]);
    streamStateRef.current = { streamId: "", assistantId: "" };
    setMessages((prev) => [
      ...prev,
      {
        id: newLocalId("local-user"),
        role: "user",
        content: text,
        createdAt: new Date().toISOString(),
      },
    ]);

    let sawDone = false;
    try {
      await streamChat({
        conversationId: convId,
        message: text,
        agentMode: selectedMode,
        role: role || undefined,
        projectId: projectId || undefined,
        webshellConnectionId: webshellConnectionId || undefined,
        attachments,
        signal: controller.signal,
        onEvent: (event) => {
          if (event.type === "done") sawDone = true;
          applyStreamEvent(event);
        },
      });
      if (!sawDone) {
        setStreamError("连接提前结束，任务可能仍在后端执行；请刷新或到「深任务板」查看。");
      }
    } catch (e) {
      if ((e as Error).name !== "AbortError") {
        const message = (e as Error).message || String(e);
        setStreamError(message);
        toast.error(`发送失败：${message}`);
      }
    } finally {
      abortRef.current = null;
      setStreaming(false);
      setStreamConvId(null);
      void loadList(searchQ);
      if (selectedIdRef.current === convId) {
        window.setTimeout(() => void loadDetail(convId), 300);
      }
    }
  }

  async function stop() {
    const convId = streamConvId || selectedId;
    if (!convId) return;
    try {
      const result = await stopConversation(convId);
      toast.success(result.status === "idle" ? "当前没有运行中的任务" : "已发送停止请求");
    } catch (e) {
      toast.error(`停止失败：${(e as Error).message}`);
    } finally {
      abortRef.current?.abort();
      abortRef.current = null;
      setStreaming(false);
      setStreamConvId(null);
      if (selectedIdRef.current === convId) {
        window.setTimeout(() => void loadDetail(convId), 500);
      }
    }
  }

  return (
    <div className="flex h-[calc(100dvh-6rem)] min-h-0 flex-col gap-4 md:h-[calc(100dvh-7rem)] md:gap-6">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h1 className="flex items-center gap-2 text-xl font-semibold tracking-tight">
            <MessageSquareIcon className="size-5" /> 聊天
          </h1>
          <p className="text-muted-foreground text-sm">
            文本消息 / SSE / 停止 / 附件 / 角色 / 项目 / WebShell · 多代理引擎（Eino 单代理 / Deep / Plan-Execute /
            Supervisor）
          </p>
        </div>
        <div className="flex flex-wrap items-center justify-end gap-2">
          <Select value={newMode} onValueChange={setNewMode} disabled={streaming || creating}>
            <SelectTrigger className="w-[150px]">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {MODES.map((m) => (
                <SelectItem key={m.value} value={m.value}>
                  {m.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Button variant="outline" onClick={() => void newConversation()} disabled={streaming || creating}>
            {creating ? <Loader2Icon className="size-4 animate-spin" /> : <PlusIcon className="size-4" />} 新建会话
          </Button>
        </div>
      </div>

      {streamError ? (
        <div className="rounded-lg border border-destructive/40 p-3 text-sm text-destructive">{streamError}</div>
      ) : null}

      <div className="grid min-h-0 flex-1 grid-rows-[minmax(0,1fr)] gap-4 lg:grid-cols-[320px_minmax(0,1fr)]">
        <ConversationList
          rows={rows}
          listLoading={listLoading}
          listError={listError}
          selectedId={selectedId}
          streaming={streaming}
          searchQ={searchQ}
          onSearch={setSearchQ}
          onRefresh={() => void loadList(searchQ, 0, false)}
          onOpen={openConversation}
          total={listTotal}
          hasMore={rows.length < listTotal}
          loadingMore={loadingMore}
          onLoadMore={loadMore}
          bulkMode={bulkMode}
          onToggleBulkMode={toggleBulkMode}
          selectedSet={selectedSet}
          onToggleSelect={toggleSelect}
          onDeleteOne={(row) => setDeleteOneTarget(row)}
          onBulkDelete={() => setBulkDeleteOpen(true)}
          onBulkPin={() => void submitBulkPin()}
          bulkBusy={bulkDeleteLoading || bulkPinLoading}
        />

        <Card className="flex min-h-0 flex-col overflow-hidden">
          <div className="flex items-center justify-between gap-2 border-b p-3">
            <div className="min-w-0">
              <div className="truncate font-medium">{detail?.title || selectedRow?.title || "选择会话"}</div>
              <div className="text-muted-foreground mt-0.5 flex items-center gap-1.5 text-[10px]">
                <span>{selectedRow?.roleName || "默认角色"}</span>
                <span>·</span>
                <span>{modeLabel(selectedMode)}</span>
                {selectedRow?.running ? (
                  <Badge variant="secondary" className="ml-1 text-[10px]">
                    运行中
                  </Badge>
                ) : null}
              </div>
            </div>
            <div className="flex items-center gap-2">
              {streaming ? (
                <Badge variant="secondary" className="text-[10px]">
                  <Loader2Icon className="size-3 animate-spin" /> 生成中
                </Badge>
              ) : null}
              {selectedId ? (
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => void loadDetail(selectedId)}
                  disabled={detailLoading || streaming}
                >
                  <RefreshCwIcon className={cn("size-3.5", detailLoading && "animate-spin")} /> 刷新
                </Button>
              ) : null}
            </div>
          </div>

          <ChatMessages
            messages={messages}
            selectedId={selectedId}
            detailLoading={detailLoading}
            detailError={detailError}
            writeEnabled={writeEnabled}
            scrollRef={scrollRef}
            endRef={endRef}
          />

          <Composer
            writeEnabled={writeEnabled}
            selectedId={selectedId}
            draft={draft}
            onDraft={setDraft}
            onSend={() => void send()}
            onStop={() => void stop()}
            isBusy={isBusy}
            canSend={canSend}
            textareaRef={textareaRef}
            pending={pending}
            onAddFiles={addFiles}
            onRemoveFile={removeFile}
            role={role}
            onRoleChange={setRole}
            projects={projects}
            projectId={projectId}
            onProjectChange={commitProjectBinding}
            webshells={webshells}
            webshellConnectionId={webshellConnectionId}
            onWebshellChange={setWebshellConnectionId}
            fileInputRef={fileInputRef}
          />
        </Card>
      </div>

      <Dialog
        open={deleteOneTarget !== null}
        onOpenChange={(open) => {
          if (!open && !deleteOneLoading) setDeleteOneTarget(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>删除会话</DialogTitle>
            <DialogDescription>
              {deleteOneTarget?.title || "（无标题会话）"}
              <br />
              会话、消息、过程详情和活动记录会被删除，不可恢复。
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDeleteOneTarget(null)} disabled={deleteOneLoading}>
              取消
            </Button>
            <Button variant="destructive" onClick={() => void submitDeleteOne()} disabled={deleteOneLoading}>
              {deleteOneLoading ? "删除中…" : "确认删除"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog
        open={bulkDeleteOpen}
        onOpenChange={(open) => {
          if (!open && !bulkDeleteLoading) setBulkDeleteOpen(false);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>批量删除会话</DialogTitle>
            <DialogDescription>
              将删除选中的 {selectedSet.size} 个会话（会话、消息、过程详情和活动记录），不可恢复。
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setBulkDeleteOpen(false)} disabled={bulkDeleteLoading}>
              取消
            </Button>
            <Button variant="destructive" onClick={() => void submitBulkDelete()} disabled={bulkDeleteLoading}>
              {bulkDeleteLoading ? "删除中…" : "确认删除"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
