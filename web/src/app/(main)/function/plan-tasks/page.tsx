"use client";

// 深任务板（旧 findu UI 聊天页 deep/plan_execute/supervisor 计划进度板迁入控制台，单 UI 定案第二批）。
// 数据面为只读实时监控，两个列表合并 + 任务板轮询：
//   - GET /api/conversations（宿主 BFF 列表，含实时 running 态）
//   - GET /findu/api/conversations（findu DTO 列表，含 agentMode/roleName）
//   - GET /api/conversations/{id}/plan-tasks（宿主桥 / findu 兜底双源；会话运行中才有数据）
// 轮询节奏：运行中 3s、空闲 30s、页面隐藏暂停；不构造数据、不写库。

import * as React from "react";

import { CheckCircle2Icon, CircleIcon, ListTodoIcon, Loader2Icon, RefreshCwIcon, SearchIcon } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { http } from "@/lib/api";
import { finduErrorMessage, finduFetch, formatTime } from "@/lib/findu";
import type { ConversationRow } from "@/lib/readonly-api";
import { listConversations } from "@/lib/readonly-api";

type PlanTask = {
  id: string;
  subject: string;
  description?: string;
  status: string;
  activeForm?: string;
  blocks?: string[];
  blockedBy?: string[];
  owner?: string;
};

type PlanBoard = {
  tasks: PlanTask[];
  total: number;
  completed: number;
  activeStep: number;
  running: boolean;
};

type FinduConvRow = {
  id: string;
  title?: string;
  agentMode?: string;
  roleName?: string;
  updatedAt?: string;
  updated_at?: string;
  createdAt?: string;
  created_at?: string;
};

type FinduConvPage = { conversations?: FinduConvRow[] } | FinduConvRow[];

type SessionRow = FinduConvRow & { running?: boolean };

const MULTI_AGENT_MODES = new Set(["deep", "plan_execute", "supervisor"]);

const MODE_LABEL: Record<string, string> = {
  deep: "Deep",
  plan_execute: "Plan-Execute",
  supervisor: "Supervisor",
};

const STATUS_LABEL: Record<string, string> = {
  pending: "待执行",
  in_progress: "进行中",
  completed: "已完成",
};

const ACTIVE_POLL_MS = 3000;
const IDLE_POLL_MS = 30000;
const LIST_POLL_MS = 15000;

function normalizeStatus(s?: string): string {
  const v = String(s ?? "")
    .trim()
    .toLowerCase();
  return ["pending", "in_progress", "completed"].includes(v) ? v : "pending";
}

function taskLabel(t: PlanTask): string {
  if (normalizeStatus(t.status) === "in_progress" && t.activeForm?.trim()) return t.activeForm.trim();
  return t.subject?.trim() || "未命名任务";
}

function statusBadgeClass(st: string): string {
  if (st === "completed") return "border-emerald-500/40 text-emerald-600";
  if (st === "in_progress") return "border-primary/40 text-primary";
  return "border-slate-400/40 text-slate-500";
}

function updatedAtOf(row: FinduConvRow): string {
  return row.updatedAt ?? row.updated_at ?? "";
}

function modeLabel(agentMode?: string): string {
  const key = String(agentMode ?? "");
  return MODE_LABEL[key] ?? (key || "-");
}

export default function Page() {
  const [rows, setRows] = React.useState<SessionRow[]>([]);
  const [listLoading, setListLoading] = React.useState(true);
  const [listError, setListError] = React.useState("");
  const [search, setSearch] = React.useState("");
  const [selectedId, setSelectedId] = React.useState<string | null>(null);
  const [board, setBoard] = React.useState<PlanBoard | null>(null);
  const [boardFor, setBoardFor] = React.useState<string | null>(null);
  const [boardError, setBoardError] = React.useState("");
  const [boardAt, setBoardAt] = React.useState("");

  const loadList = React.useCallback(async () => {
    try {
      const [hostPage, finduPage] = await Promise.all([
        listConversations({ limit: 200 }),
        finduFetch<FinduConvPage>("/conversations?limit=200"),
      ]);
      const hostMap = new Map<string, ConversationRow>();
      for (const h of hostPage.conversations ?? []) hostMap.set(h.id, h);
      const finduRows = Array.isArray(finduPage) ? finduPage : (finduPage.conversations ?? []);
      const merged: SessionRow[] = finduRows
        .filter((c) => c && typeof c.id === "string" && MULTI_AGENT_MODES.has(String(c.agentMode ?? "")))
        .map((c) => ({ ...c, running: hostMap.get(c.id)?.running ?? false }))
        .sort((a, b) => {
          if (!!a.running !== !!b.running) return a.running ? -1 : 1;
          return updatedAtOf(b).localeCompare(updatedAtOf(a));
        });
      setRows(merged);
      setListError("");
      setSelectedId((prev) => {
        if (prev && merged.some((c) => c.id === prev)) return prev;
        return merged[0]?.id ?? null;
      });
    } catch (e) {
      setListError(finduErrorMessage(e));
    } finally {
      setListLoading(false);
    }
  }, []);

  React.useEffect(() => {
    void loadList();
    const t = setInterval(() => void loadList(), LIST_POLL_MS);
    return () => clearInterval(t);
  }, [loadList]);

  // 切换会话时旧板面仍在状态里，用 boardFor 标记归属，渲染只认当前会话的板面。
  const boardReady = board !== null && boardFor === selectedId && selectedId !== null;

  const selected = rows.find((c) => c.id === selectedId) ?? null;
  const selectedRunning = !!(selected?.running || (selectedId !== null && boardReady && board.running));

  React.useEffect(() => {
    if (!selectedId) return;
    let cancelled = false;
    let timer: number | undefined;
    const tick = async () => {
      if (!cancelled && !document.hidden) {
        try {
          const b = await http<PlanBoard>(`/conversations/${encodeURIComponent(selectedId)}/plan-tasks`);
          if (!cancelled) {
            setBoard(b);
            setBoardFor(selectedId);
            setBoardAt(new Date().toISOString());
            setBoardError("");
          }
        } catch (e) {
          // 轮询失败不清空最后一次已知板面，只提示并自动重试。
          if (!cancelled) setBoardError(finduErrorMessage(e));
        }
      }
      if (!cancelled) {
        const delay = document.hidden || !selectedRunning ? IDLE_POLL_MS : ACTIVE_POLL_MS;
        timer = window.setTimeout(tick, delay);
      }
    };
    void tick();
    return () => {
      cancelled = true;
      if (timer !== undefined) window.clearTimeout(timer);
    };
  }, [selectedId, selectedRunning]);

  const q = search.trim().toLowerCase();
  const visible = rows.filter((c) => {
    if (!q) return true;
    return [c.title, c.id, c.roleName].some((v) =>
      String(v ?? "")
        .toLowerCase()
        .includes(q),
    );
  });

  const activeBoard = boardReady ? board : null;
  const allCompleted = activeBoard !== null && activeBoard.total > 0 && activeBoard.completed >= activeBoard.total;
  const progressPct =
    activeBoard && activeBoard.total > 0 ? Math.round((activeBoard.completed / activeBoard.total) * 100) : 0;

  return (
    <div className="flex h-[calc(100dvh-6rem)] min-h-0 flex-col gap-4 md:h-[calc(100dvh-7rem)] md:gap-6">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h1 className="flex items-center gap-2 text-xl font-semibold tracking-tight">
            <ListTodoIcon className="size-5" /> 深任务板
          </h1>
          <p className="text-muted-foreground text-sm">
            Deep / Plan-Execute / Supervisor 多代理会话的计划进度 · 会话运行中实时轮询（3s）· 空闲 30s
          </p>
        </div>
        <Button variant="outline" onClick={() => void loadList()} disabled={listLoading}>
          <RefreshCwIcon className="size-4" /> 刷新
        </Button>
      </div>

      {listError !== "" ? (
        <div className="border-destructive/40 text-destructive rounded-md border px-3 py-2 text-sm">
          读取会话列表失败：{listError}
        </div>
      ) : null}

      <div className="grid flex-1 gap-4 md:gap-6 lg:grid-cols-[340px,minmax(0,1fr)]">
        <Card className="flex flex-col overflow-hidden">
          <CardHeader>
            <CardTitle className="text-sm">多代理会话</CardTitle>
            <CardDescription>仅显示 Deep / Plan-Execute / Supervisor 会话</CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-2 p-0 pt-0">
            <div className="relative px-3 pb-1">
              <SearchIcon className="text-muted-foreground absolute top-1/2 left-2 size-3.5 -translate-y-1/2" />
              <Input
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder="搜索标题 / 会话 ID / 角色"
                className="pl-7"
              />
            </div>
            <div className="max-h-[60vh] overflow-y-auto">
              {listLoading ? (
                <div className="flex flex-col gap-2 p-3">
                  <Skeleton className="h-12" />
                  <Skeleton className="h-12" />
                  <Skeleton className="h-12" />
                </div>
              ) : visible.length === 0 ? (
                <div className="text-muted-foreground p-6 text-center text-sm">
                  {rows.length === 0
                    ? "暂无多代理会话。在聊天页新建会话并选择 Deep / Plan-Execute / Supervisor 模式。"
                    : "没有匹配的会话"}
                </div>
              ) : (
                <ul>
                  {visible.map((c) => {
                    const active = c.id === selectedId;
                    return (
                      <li key={c.id} className="border-b last:border-b-0">
                        <button
                          type="button"
                          onClick={() => setSelectedId(c.id)}
                          className={
                            "w-full px-3 py-2.5 text-left transition-colors hover:bg-accent " +
                            (active ? "bg-accent/70" : "")
                          }
                        >
                          <div className="flex items-center gap-2">
                            {c.running ? (
                              <Loader2Icon className="size-3.5 shrink-0 animate-spin text-emerald-500" />
                            ) : (
                              <CircleIcon className="text-muted-foreground/50 size-3.5 shrink-0" />
                            )}
                            <span className="truncate text-sm font-medium">{c.title?.trim() || c.id.slice(0, 8)}</span>
                          </div>
                          <div className="text-muted-foreground mt-1 flex items-center gap-2 pl-5 text-xs">
                            <Badge variant="outline" className="px-1 py-0 text-[10px]">
                              {modeLabel(c.agentMode)}
                            </Badge>
                            <span className="truncate">
                              {c.roleName ? c.roleName + " · " : ""}
                              {formatTime(updatedAtOf(c))}
                            </span>
                          </div>
                        </button>
                      </li>
                    );
                  })}
                </ul>
              )}
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            {selected ? (
              <>
                <CardTitle className="flex flex-wrap items-center gap-2 text-sm">
                  <span className="truncate">{selected.title?.trim() || selected.id.slice(0, 8)}</span>
                  <Badge variant="outline" className="px-1.5 py-0 text-[10px]">
                    {modeLabel(selected.agentMode)}
                  </Badge>
                  {selectedRunning ? (
                    <Badge className="bg-emerald-500/15 text-emerald-600 border border-emerald-500/40 px-1.5 py-0 text-[10px]">
                      运行中
                    </Badge>
                  ) : (
                    <Badge variant="outline" className="text-muted-foreground px-1.5 py-0 text-[10px]">
                      未运行
                    </Badge>
                  )}
                </CardTitle>
                <CardDescription>会话 {selected.id.slice(0, 8)} · 任务板仅在会话运行中有实时数据</CardDescription>
              </>
            ) : (
              <>
                <CardTitle className="text-sm">任务板</CardTitle>
                <CardDescription>在左侧选择一个多代理会话查看计划进度</CardDescription>
              </>
            )}
          </CardHeader>
          <CardContent>
            {!selected ? (
              <div className="text-muted-foreground py-16 text-center text-sm">
                {listLoading ? "加载中…" : "暂无可选的多代理会话"}
              </div>
            ) : !selectedRunning ? (
              <div className="text-muted-foreground py-16 text-center text-sm">
                该会话未在运行。任务板只展示多代理会话运行期间的实时进度。
              </div>
            ) : activeBoard === null ? (
              <div className="flex flex-col gap-2">
                <Skeleton className="h-8 w-48" />
                <Skeleton className="h-12" />
                <Skeleton className="h-12" />
                <Skeleton className="h-12" />
              </div>
            ) : activeBoard.tasks.length === 0 ? (
              <div className="text-muted-foreground py-16 text-center text-sm">
                运行中，代理尚未生成计划任务（taskcreate 之后板面自动更新）。
              </div>
            ) : (
              <div className="flex flex-col gap-4">
                <div className="flex items-center justify-between gap-3">
                  <div className="text-sm font-medium">
                    {allCompleted ? "全部完成" : `第 ${activeBoard.activeStep} / ${activeBoard.total} 步`}
                    <span className="text-muted-foreground ml-2 font-normal">
                      已完成 {activeBoard.completed} / {activeBoard.total}
                    </span>
                  </div>
                  <div className="text-muted-foreground text-xs">{boardAt ? `更新于 ${formatTime(boardAt)}` : ""}</div>
                </div>
                <div className="bg-muted h-1.5 overflow-hidden rounded-full">
                  <div className="bg-primary h-full rounded-full transition-all" style={{ width: `${progressPct}%` }} />
                </div>
                <ul className="flex flex-col gap-2">
                  {activeBoard.tasks.map((t) => {
                    const st = normalizeStatus(t.status);
                    return (
                      <li
                        key={t.id}
                        className={
                          "flex items-start gap-3 rounded-lg border px-3 py-2.5 " +
                          (st === "in_progress" ? "border-primary/40 bg-primary/5" : "")
                        }
                      >
                        {st === "completed" ? (
                          <CheckCircle2Icon className="mt-0.5 size-4 shrink-0 text-emerald-500" />
                        ) : st === "in_progress" ? (
                          <Loader2Icon className="mt-0.5 size-4 shrink-0 animate-spin text-primary" />
                        ) : (
                          <CircleIcon className="text-muted-foreground/60 mt-0.5 size-4 shrink-0" />
                        )}
                        <div className="min-w-0 flex-1">
                          <div className="flex items-center gap-2">
                            <span className="truncate text-sm font-medium">{taskLabel(t)}</span>
                            <Badge variant="outline" className={"px-1 py-0 text-[10px] " + statusBadgeClass(st)}>
                              {STATUS_LABEL[st]}
                            </Badge>
                          </div>
                          {t.description?.trim() ? (
                            <div className="text-muted-foreground mt-0.5 truncate text-xs" title={t.description}>
                              {t.description}
                            </div>
                          ) : null}
                        </div>
                      </li>
                    );
                  })}
                </ul>
                {boardError !== "" ? (
                  <div className="text-muted-foreground text-xs">最近一次轮询失败：{boardError}（自动重试）</div>
                ) : null}
              </div>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
