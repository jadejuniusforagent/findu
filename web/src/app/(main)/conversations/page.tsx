"use client";

import * as React from "react";

import {
  MessageSquareIcon,
  PencilIcon,
  PinIcon,
  PinOffIcon,
  RefreshCwIcon,
  SearchIcon,
  Trash2Icon,
} from "lucide-react";
import { toast } from "sonner";

import { CodeBlock } from "@/components/code-block";
import { Markdown } from "@/components/markdown";
import { TablePagination } from "@/components/table-pagination";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
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
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { deleteConversation, renameConversation, setConversationPinned } from "@/lib/conversation-write-api";
import { formatTime } from "@/lib/findu";
import { type ConversationDetail, type ConversationRow, getConversation, listConversations } from "@/lib/readonly-api";
import { cn } from "@/lib/utils";

function roleLabel(role: string) {
  if (role === "user") return "用户";
  if (role === "assistant") return "助手";
  if (role === "tool") return "工具";
  return role || "系统";
}

export default function ConversationsPage() {
  const [rows, setRows] = React.useState<ConversationRow[]>([]);
  const [total, setTotal] = React.useState(0);
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState("");
  const [search, setSearch] = React.useState("");
  const [searchQ, setSearchQ] = React.useState("");
  const [sortBy, setSortBy] = React.useState<"updated_at" | "created_at">("updated_at");
  const [limit, setLimit] = React.useState(20);
  const [offset, setOffset] = React.useState(0);
  const [reload, setReload] = React.useState(0);

  const [selected, setSelected] = React.useState<ConversationRow | null>(null);
  const [detail, setDetail] = React.useState<ConversationDetail | null>(null);
  const [detailLoading, setDetailLoading] = React.useState(false);
  const [detailError, setDetailError] = React.useState("");

  // U2（2026-10-23）：双跑验证阀退役——写侧后端已宿主原生，页面直接可写。
  const writeEnabled = true;
  const [actionLoading, setActionLoading] = React.useState(false);
  const [renameTarget, setRenameTarget] = React.useState<ConversationRow | null>(null);
  const [renameValue, setRenameValue] = React.useState("");
  const [deleteTarget, setDeleteTarget] = React.useState<ConversationRow | null>(null);
  const [deleting, setDeleting] = React.useState(false);

  React.useEffect(() => {
    const t = setTimeout(() => {
      setSearchQ(search);
      setOffset(0);
    }, 300);
    return () => clearTimeout(t);
  }, [search]);

  // biome-ignore lint/correctness/useExhaustiveDependencies: reload is the explicit refresh trigger.
  React.useEffect(() => {
    let alive = true;
    setLoading(true);
    setError("");
    listConversations({ search: searchQ, limit, offset, sortBy })
      .then((r) => {
        if (!alive) return;
        setRows(r.conversations ?? []);
        setTotal(r.total ?? 0);
      })
      .catch((e: Error) => {
        if (alive) setError(e.message);
      })
      .finally(() => {
        if (alive) setLoading(false);
      });
    return () => {
      alive = false;
    };
  }, [searchQ, limit, offset, sortBy, reload]);

  async function open(row: ConversationRow) {
    setSelected(row);
    setDetail(null);
    setDetailError("");
    setDetailLoading(true);
    try {
      const next = await getConversation(row.id);
      setDetail(next);
      setSelected((prev) => (prev?.id === row.id ? { ...prev, ...next } : prev));
    } catch (e) {
      setDetailError((e as Error).message);
    } finally {
      setDetailLoading(false);
    }
  }

  async function togglePin() {
    const target = selected;
    if (!target) return;
    const nextPinned = !target.pinned;
    setActionLoading(true);
    try {
      await setConversationPinned(target.id, nextPinned);
      toast.success(nextPinned ? "会话已置顶" : "已取消置顶");
      setSelected((prev) => (prev ? { ...prev, pinned: nextPinned } : prev));
      setDetail((prev) => (prev ? { ...prev, pinned: nextPinned } : prev));
      setReload((n) => n + 1);
    } catch (e) {
      toast.error(`更新置顶状态失败：${(e as Error).message}`);
    } finally {
      setActionLoading(false);
    }
  }

  function openRename() {
    if (!selected) return;
    setRenameTarget(selected);
    setRenameValue(selected.title ?? "");
  }

  async function submitRename() {
    const target = renameTarget;
    const title = renameValue.trim();
    if (!target || !title) return;
    setActionLoading(true);
    try {
      await renameConversation(target.id, title);
      toast.success("会话已重命名");
      setRenameTarget(null);
      setSelected((prev) => (prev?.id === target.id ? { ...prev, title } : prev));
      setDetail((prev) => (prev?.id === target.id ? { ...prev, title } : prev));
      setReload((n) => n + 1);
    } catch (e) {
      toast.error(`重命名失败：${(e as Error).message}`);
    } finally {
      setActionLoading(false);
    }
  }

  function openDelete() {
    if (!selected) return;
    setDeleteTarget(selected);
  }

  async function submitDelete() {
    const target = deleteTarget;
    if (!target) return;
    setDeleting(true);
    try {
      await deleteConversation(target.id);
      toast.success("会话已删除");
      setDeleteTarget(null);
      setSelected(null);
      setDetail(null);
      setDetailError("");
      if (rows.length === 1 && offset >= limit) setOffset(Math.max(0, offset - limit));
      setReload((n) => n + 1);
    } catch (e) {
      toast.error(`删除会话失败：${(e as Error).message}`);
    } finally {
      setDeleting(false);
    }
  }

  return (
    <div className="flex h-[calc(100dvh-6rem)] min-h-0 flex-col gap-4 md:h-[calc(100dvh-7rem)] md:gap-6">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h1 className="flex items-center gap-2 text-xl font-semibold tracking-tight">
            <MessageSquareIcon className="size-5" /> 会话历史
          </h1>
          <p className="text-muted-foreground text-sm">
            支持标题/消息全文检索、重命名、置顶与删除
          </p>
        </div>
        <div className="flex flex-wrap items-center justify-end gap-2">
          <Button variant="outline" onClick={() => setReload((n) => n + 1)} disabled={loading}>
            <RefreshCwIcon className={cn("size-4", loading && "animate-spin")} /> 刷新
          </Button>
        </div>
      </div>

      <Card>
        <CardContent className="flex flex-wrap items-center gap-2 py-3">
          <div className="relative min-w-64 flex-1">
            <SearchIcon className="text-muted-foreground absolute top-2.5 left-2.5 size-4" />
            <Input
              className="pl-8"
              placeholder="搜索标题或消息内容…"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </div>
          <Select
            value={sortBy}
            onValueChange={(v) => {
              setSortBy(v as "updated_at" | "created_at");
              setOffset(0);
            }}
          >
            <SelectTrigger className="w-36">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="updated_at">按更新时间</SelectItem>
              <SelectItem value="created_at">按创建时间</SelectItem>
            </SelectContent>
          </Select>
        </CardContent>
      </Card>

      {error ? (
        <Card className="border-destructive/40 bg-destructive/5">
          <CardContent className="text-destructive py-3 text-sm">会话列表加载失败：{error}</CardContent>
        </Card>
      ) : null}

      <Card className="min-h-0 flex-1 overflow-y-auto">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>会话</TableHead>
              <TableHead className="w-32">角色</TableHead>
              <TableHead className="w-32">模式</TableHead>
              <TableHead className="w-24">状态</TableHead>
              <TableHead className="w-44">更新时间</TableHead>
              <TableHead className="w-20 text-right">操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading ? (
              Array.from({ length: 6 }).map((_, i) => (
                <TableRow key={i}>
                  <TableCell colSpan={6}>
                    <Skeleton className="h-7 w-full" />
                  </TableCell>
                </TableRow>
              ))
            ) : rows.length === 0 ? (
              <TableRow>
                <TableCell colSpan={6} className="text-muted-foreground py-10 text-center text-sm">
                  {searchQ ? "没有匹配的会话" : "还没有会话"}
                </TableCell>
              </TableRow>
            ) : (
              rows.map((c) => (
                <TableRow key={c.id} className="cursor-pointer" onClick={() => void open(c)}>
                  <TableCell>
                    <div className="max-w-[520px] truncate font-medium">{c.title || "（无标题会话）"}</div>
                    <div className="text-muted-foreground font-mono text-[10px]">{c.id}</div>
                  </TableCell>
                  <TableCell className="text-xs">{c.roleName || "默认"}</TableCell>
                  <TableCell className="text-xs">{c.agentMode || "eino_single"}</TableCell>
                  <TableCell>
                    <div className="flex flex-wrap gap-1">
                      {c.running ? (
                        <Badge variant="default" className="text-[10px]">
                          运行中
                        </Badge>
                      ) : null}
                      {c.pinned ? (
                        <Badge variant="secondary" className="text-[10px]">
                          置顶
                        </Badge>
                      ) : null}
                      {!c.running && !c.pinned ? <span className="text-muted-foreground text-xs">-</span> : null}
                    </div>
                  </TableCell>
                  <TableCell className="text-muted-foreground text-xs">
                    {formatTime(c.updatedAt || c.updated_at)}
                  </TableCell>
                  <TableCell className="text-right">
                    <Button variant="ghost" size="sm" onClick={() => void open(c)}>
                      查看
                    </Button>
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
        <TablePagination
          page={Math.floor(offset / limit) + 1}
          pageSize={limit}
          total={total}
          pageSizeOptions={[20, 50, 100]}
          onPageChange={(p) => setOffset((p - 1) * limit)}
          onPageSizeChange={(s) => {
            setLimit(s);
            setOffset(0);
          }}
        />
      </Card>

      <Sheet
        open={selected !== null}
        onOpenChange={(open) => {
          if (!open) {
            setSelected(null);
            setDetail(null);
            setDetailError("");
          }
        }}
      >
        <SheetContent className="w-full overflow-y-auto sm:max-w-3xl">
          <SheetHeader>
            <SheetTitle className="pr-8">{selected?.title || "（无标题会话）"}</SheetTitle>
            <SheetDescription>
              {selected?.roleName || "默认角色"} · {selected?.agentMode || "eino_single"} ·{" "}
              {formatTime(selected?.updatedAt || selected?.updated_at)}
            </SheetDescription>
          </SheetHeader>
          <div className="mt-4 flex flex-col gap-3">
            {detailLoading ? (
              <div className="text-muted-foreground py-10 text-center text-sm">加载会话内容…</div>
            ) : detailError ? (
              <div className="text-destructive rounded-lg border border-destructive/40 p-3 text-sm">
                加载失败：{detailError}
              </div>
            ) : detail?.messages && detail.messages.length > 0 ? (
              detail.messages.map((m) => (
                <div
                  key={m.id}
                  className={cn(
                    "rounded-lg border p-3",
                    m.role === "user" ? "bg-muted/40" : "bg-background",
                    m.role === "tool" ? "font-mono text-xs" : "",
                  )}
                >
                  <div className="mb-1.5 flex items-center gap-2">
                    <Badge variant={m.role === "assistant" ? "default" : "outline"} className="text-[10px]">
                      {roleLabel(m.role)}
                    </Badge>
                    <span className="text-muted-foreground text-xs">{formatTime(m.createdAt)}</span>
                  </div>
                  {m.content ? (
                    <Markdown text={m.content} />
                  ) : (
                    <div className="text-muted-foreground text-sm">（空消息）</div>
                  )}
                  {m.reasoningContent ? (
                    <details className="mt-2">
                      <summary className="text-muted-foreground cursor-pointer text-xs">推理内容</summary>
                      <CodeBlock code={m.reasoningContent} maxHeight="max-h-40" />
                    </details>
                  ) : null}
                </div>
              ))
            ) : (
              <div className="text-muted-foreground py-10 text-center text-sm">该会话没有可显示的消息</div>
            )}
          </div>
          {writeEnabled ? (
            <div className="mt-4 flex flex-wrap gap-2 border-t pt-3">
              <Button size="sm" variant="outline" onClick={() => void togglePin()} disabled={actionLoading}>
                {selected?.pinned ? <PinOffIcon className="size-4" /> : <PinIcon className="size-4" />}
                {selected?.pinned ? "取消置顶" : "置顶"}
              </Button>
              <Button size="sm" variant="outline" onClick={openRename} disabled={actionLoading}>
                <PencilIcon className="size-4" /> 重命名
              </Button>
              <Button size="sm" variant="destructive" onClick={openDelete} disabled={actionLoading}>
                <Trash2Icon className="size-4" /> 删除
              </Button>
            </div>
          ) : null}
        </SheetContent>
      </Sheet>

      <Dialog
        open={renameTarget !== null}
        onOpenChange={(open) => {
          if (!open && !actionLoading) setRenameTarget(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>重命名会话</DialogTitle>
            <DialogDescription>修改后会话标题将更新</DialogDescription>
          </DialogHeader>
          <Input
            value={renameValue}
            onChange={(e) => setRenameValue(e.target.value)}
            placeholder="会话标题"
            disabled={actionLoading}
          />
          <DialogFooter>
            <Button variant="outline" onClick={() => setRenameTarget(null)} disabled={actionLoading}>
              取消
            </Button>
            <Button onClick={() => void submitRename()} disabled={actionLoading || !renameValue.trim()}>
              {actionLoading ? "保存中…" : "保存"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog
        open={deleteTarget !== null}
        onOpenChange={(open) => {
          if (!open && !deleting) setDeleteTarget(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>删除会话</DialogTitle>
            <DialogDescription>
              {deleteTarget?.title || "（无标题会话）"}
              <br />
              会话、消息、过程详情和活动记录会被删除，不可恢复；运行中的会话会先停止。
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDeleteTarget(null)} disabled={deleting}>
              取消
            </Button>
            <Button variant="destructive" onClick={() => void submitDelete()} disabled={deleting}>
              {deleting ? "删除中…" : "确认删除"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
