"use client";

import * as React from "react";

import {
  ListChecksIcon,
  PauseIcon,
  PencilIcon,
  PlayIcon,
  PlusIcon,
  RefreshCwIcon,
  SearchIcon,
  Trash2Icon,
} from "lucide-react";
import { toast } from "sonner";

import { TablePagination } from "@/components/table-pagination";
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
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/api";
import { formatTime } from "@/lib/findu";
import type { DeleteTaskOptions, Task, TaskCategory, TaskStatus } from "@/lib/types";
import { cn } from "@/lib/utils";

const STATUS_LABEL: Record<TaskStatus, string> = {
  created: "已创建",
  queued: "排队中",
  running: "运行中",
  paused: "已暂停",
  done: "已完成",
  failed: "失败",
  timeout: "超时",
};

const EMPTY_CREATE_FORM = {
  name: "",
  description: "",
  goal: "",
  categoryId: "0",
  timeoutSeconds: "",
};

const EMPTY_DELETE_OPTIONS: DeleteTaskOptions = {
  delete_assets: false,
  delete_traffic: false,
  delete_files: false,
  delete_findings: false,
  delete_llm_records: false,
};

const DELETE_OPTIONS: { key: keyof DeleteTaskOptions; label: string; hint: string }[] = [
  { key: "delete_assets", label: "删除关联资产", hint: "仅删除本任务独占的资产；共享资产会解绑保留。" },
  { key: "delete_traffic", label: "删除关联流量", hint: "按任务资产 host 精确匹配，可能影响同 host 的其他任务。" },
  { key: "delete_files", label: "删除任务文件", hint: "删除任务 workspace 与 transcript 等运行文件。" },
  { key: "delete_findings", label: "删除关联漏洞", hint: "删除本任务登记的全部 finding 及证据链。" },
  { key: "delete_llm_records", label: "删除 LLM 记录", hint: "删除本任务的模型调用录制与 token 账本。" },
];

function statusVariant(status: TaskStatus): "default" | "secondary" | "destructive" | "outline" {
  if (status === "running") return "default";
  if (status === "done") return "secondary";
  if (status === "failed" || status === "timeout") return "destructive";
  return "outline";
}

function taskTitle(t: Task) {
  return t.name?.trim() || t.description.trim() || `任务 #${t.id}`;
}

function taskStatusLabel(t: Task | null) {
  if (!t) return "";
  return STATUS_LABEL[t.status] ?? t.status;
}

function totalTokens(t: Task) {
  const x = t.tokens;
  if (!x) return 0;
  return x.input_tokens + x.output_tokens + x.cache_read_tokens + x.cache_write_tokens;
}

export default function TasksPage() {
  const [tasks, setTasks] = React.useState<Task[]>([]);
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState("");
  const [search, setSearch] = React.useState("");
  const [status, setStatus] = React.useState<"all" | TaskStatus>("all");
  const [page, setPage] = React.useState(1);
  const [pageSize, setPageSize] = React.useState(20);
  const [reload, setReload] = React.useState(0);

  const [selected, setSelected] = React.useState<Task | null>(null);
  const [detail, setDetail] = React.useState<Task | null>(null);
  const [detailLoading, setDetailLoading] = React.useState(false);
  const [detailError, setDetailError] = React.useState("");

  // U2（2026-10-23）：双跑验证阀退役——写侧后端已宿主原生，任务页直接可写。
  const writeEnabled = true;
  const [categories, setCategories] = React.useState<TaskCategory[]>([]);
  const [actionLoading, setActionLoading] = React.useState(false);

  const [createOpen, setCreateOpen] = React.useState(false);
  const [createForm, setCreateForm] = React.useState(EMPTY_CREATE_FORM);
  const [createPause, setCreatePause] = React.useState(true);
  const [creating, setCreating] = React.useState(false);

  const [renameTarget, setRenameTarget] = React.useState<Task | null>(null);
  const [renameValue, setRenameValue] = React.useState("");

  const [deleteTarget, setDeleteTarget] = React.useState<Task | null>(null);
  const [deleteOptions, setDeleteOptions] = React.useState<DeleteTaskOptions>(EMPTY_DELETE_OPTIONS);
  const [deleting, setDeleting] = React.useState(false);

  React.useEffect(() => {
    let alive = true;
    api
      .taskCategories()
      .then((rows) => {
        if (alive) setCategories(rows);
      })
      .catch((e: Error) => {
        if (alive) toast.error(`任务分类加载失败：${e.message}`);
      });
    return () => {
      alive = false;
    };
  }, [writeEnabled]);

  // biome-ignore lint/correctness/useExhaustiveDependencies: reload is the explicit refresh trigger.
  React.useEffect(() => {
    let alive = true;
    setLoading(true);
    setError("");
    api
      .tasks()
      .then((r) => {
        if (alive) setTasks(r.tasks ?? []);
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
  }, [reload]);

  const filtered = React.useMemo(() => {
    const q = search.trim().toLowerCase();
    return tasks.filter((t) => {
      if (status !== "all" && t.status !== status) return false;
      if (!q) return true;
      return [t.id, t.name, t.description, t.goal, t.category_name]
        .filter(Boolean)
        .some((v) => String(v).toLowerCase().includes(q));
    });
  }, [tasks, search, status]);

  // biome-ignore lint/correctness/useExhaustiveDependencies: filters intentionally reset pagination.
  React.useEffect(() => {
    setPage(1);
  }, [search, status]);

  const pageRows = React.useMemo(() => {
    const start = (page - 1) * pageSize;
    return filtered.slice(start, start + pageSize);
  }, [filtered, page, pageSize]);

  async function open(t: Task) {
    setSelected(t);
    setDetail(t);
    setDetailError("");
    setDetailLoading(true);
    try {
      const next = await api.task(t.id);
      setDetail(next);
      setSelected(next);
    } catch (e) {
      setDetailError((e as Error).message);
    } finally {
      setDetailLoading(false);
    }
  }

  async function control(action: "pause" | "resume") {
    const t = detail ?? selected;
    if (!t) return;
    setActionLoading(true);
    try {
      await api.controlTask(t.id, action);
      toast.success(action === "pause" ? "任务已暂停" : "任务已继续");
      setReload((n) => n + 1);
      const next = await api.task(t.id);
      setSelected(next);
      setDetail(next);
    } catch (e) {
      toast.error(`${action === "pause" ? "暂停" : "继续"}任务失败：${(e as Error).message}`);
    } finally {
      setActionLoading(false);
    }
  }

  function openRename() {
    const t = detail ?? selected;
    if (!t) return;
    setRenameTarget(t);
    setRenameValue(t.name ?? "");
  }

  async function submitRename() {
    const t = renameTarget;
    if (!t) return;
    setActionLoading(true);
    try {
      const updated = await api.renameTask(t.id, renameValue.trim());
      toast.success("任务已重命名");
      setRenameTarget(null);
      setReload((n) => n + 1);
      if (selected?.id === t.id) {
        setSelected(updated);
        setDetail(updated);
      }
    } catch (e) {
      toast.error(`重命名失败：${(e as Error).message}`);
    } finally {
      setActionLoading(false);
    }
  }

  function openDelete() {
    const t = detail ?? selected;
    if (!t) return;
    setDeleteTarget(t);
    setDeleteOptions(EMPTY_DELETE_OPTIONS);
  }

  async function submitDelete() {
    const t = deleteTarget;
    if (!t) return;
    setDeleting(true);
    try {
      const result = await api.deleteTask(t.id, deleteOptions);
      toast.success(
        result.cleanup_warning
          ? `任务 #${t.id} 已删除，但清理有警告：${result.cleanup_warning}`
          : `任务 #${t.id} 已删除`,
      );
      setDeleteTarget(null);
      setSelected(null);
      setDetail(null);
      setReload((n) => n + 1);
    } catch (e) {
      toast.error(`删除任务失败：${(e as Error).message}`);
    } finally {
      setDeleting(false);
    }
  }

  async function submitCreate(e: React.FormEvent) {
    e.preventDefault();
    const description = createForm.description.trim();
    const goal = createForm.goal.trim();
    if (!description || !goal) {
      toast.error("描述和目标必填");
      return;
    }
    setCreating(true);
    try {
      const created = await api.createTask({
        name: createForm.name.trim(),
        description,
        goal,
        categoryId: createForm.categoryId === "0" ? undefined : Number(createForm.categoryId),
        timeoutSeconds: Number(createForm.timeoutSeconds) || 0,
      });
      if (createPause) {
        try {
          await api.controlTask(created.id, "pause");
        } catch (pauseErr) {
          toast.error(`任务已创建，但自动暂停失败：${(pauseErr as Error).message}`);
        }
      }
      toast.success(`任务 #${created.id} 已创建${createPause ? "并暂停" : ""}`);
      setCreateOpen(false);
      setCreateForm(EMPTY_CREATE_FORM);
      setCreatePause(true);
      setReload((n) => n + 1);
      await open(created);
    } catch (e) {
      toast.error(`创建任务失败：${(e as Error).message}`);
    } finally {
      setCreating(false);
    }
  }

  const running = tasks.filter((t) => t.status === "running").length;

  return (
    <div className="flex h-[calc(100dvh-6rem)] min-h-0 flex-col gap-4 md:h-[calc(100dvh-7rem)] md:gap-6">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h1 className="flex items-center gap-2 text-xl font-semibold tracking-tight">
            <ListChecksIcon className="size-5" /> 任务
          </h1>
          <p className="text-muted-foreground text-sm">
            共 {tasks.length} 个任务，运行中 {running} 个
          </p>
        </div>
        <div className="flex flex-wrap items-center justify-end gap-2">
          <Button onClick={() => setCreateOpen(true)}>
            <PlusIcon className="size-4" /> 新建任务
          </Button>
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
              placeholder="搜索任务 id、名称、描述、目标…"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </div>
          <Select value={status} onValueChange={(v) => setStatus(v as "all" | TaskStatus)}>
            <SelectTrigger className="w-36">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">全部状态</SelectItem>
              {(Object.keys(STATUS_LABEL) as TaskStatus[]).map((s) => (
                <SelectItem key={s} value={s}>
                  {STATUS_LABEL[s]}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </CardContent>
      </Card>

      {error ? (
        <Card className="border-destructive/40 bg-destructive/5">
          <CardContent className="text-destructive py-3 text-sm">任务列表加载失败：{error}</CardContent>
        </Card>
      ) : null}

      <Card className="min-h-0 flex-1 overflow-y-auto">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-16">ID</TableHead>
              <TableHead>任务</TableHead>
              <TableHead className="w-24">状态</TableHead>
              <TableHead className="w-36">目标</TableHead>
              <TableHead className="w-28">漏洞</TableHead>
              <TableHead className="w-28">Token</TableHead>
              <TableHead className="w-44">最近活动</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading ? (
              Array.from({ length: 6 }).map((_, i) => (
                <TableRow key={i}>
                  <TableCell colSpan={7}>
                    <Skeleton className="h-7 w-full" />
                  </TableCell>
                </TableRow>
              ))
            ) : pageRows.length === 0 ? (
              <TableRow>
                <TableCell colSpan={7} className="text-muted-foreground py-10 text-center text-sm">
                  {search || status !== "all" ? "没有匹配的任务" : "还没有任务"}
                </TableCell>
              </TableRow>
            ) : (
              pageRows.map((t) => {
                const f = t.findings;
                const fTotal = f ? f.critical + f.high + f.medium + f.low : 0;
                return (
                  <TableRow key={t.id} className="cursor-pointer" onClick={() => void open(t)}>
                    <TableCell className="font-mono text-xs">{t.id}</TableCell>
                    <TableCell>
                      <div className="max-w-[520px] truncate font-medium">{taskTitle(t)}</div>
                      {t.name?.trim() && t.description?.trim() ? (
                        <div className="text-muted-foreground max-w-[520px] truncate text-xs">{t.description}</div>
                      ) : null}
                    </TableCell>
                    <TableCell>
                      <Badge variant={statusVariant(t.status)} className="text-[10px]">
                        {STATUS_LABEL[t.status] ?? t.status}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-xs">
                      {t.goals_total ? `${t.goals_met ?? 0}/${t.goals_total}` : "-"}
                    </TableCell>
                    <TableCell className="text-xs">
                      {fTotal > 0 ? (
                        <div className="flex flex-wrap gap-1">
                          {f && f.critical > 0 ? (
                            <Badge variant="destructive" className="text-[10px]">
                              C {f.critical}
                            </Badge>
                          ) : null}
                          {f && f.high > 0 ? (
                            <Badge variant="default" className="text-[10px]">
                              H {f.high}
                            </Badge>
                          ) : null}
                          {f && f.medium > 0 ? (
                            <Badge variant="secondary" className="text-[10px]">
                              M {f.medium}
                            </Badge>
                          ) : null}
                          {f && f.low > 0 ? (
                            <Badge variant="outline" className="text-[10px]">
                              L {f.low}
                            </Badge>
                          ) : null}
                        </div>
                      ) : (
                        <span className="text-muted-foreground">-</span>
                      )}
                    </TableCell>
                    <TableCell className="font-mono text-xs tabular-nums">{totalTokens(t).toLocaleString()}</TableCell>
                    <TableCell className="text-muted-foreground text-xs">
                      {formatTime(t.last_activity || t.completed_at || t.created_at)}
                    </TableCell>
                  </TableRow>
                );
              })
            )}
          </TableBody>
        </Table>
        <TablePagination
          page={page}
          pageSize={pageSize}
          total={filtered.length}
          pageSizeOptions={[20, 50, 100]}
          onPageChange={setPage}
          onPageSizeChange={(s) => {
            setPageSize(s);
            setPage(1);
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
        <SheetContent className="w-full overflow-y-auto sm:max-w-2xl">
          <SheetHeader>
            <SheetTitle className="pr-8">{selected ? taskTitle(selected) : ""}</SheetTitle>
            <SheetDescription>
              任务 #{selected?.id} · {taskStatusLabel(detail ?? selected)} · {formatTime(selected?.created_at)}
            </SheetDescription>
          </SheetHeader>
          {detailLoading ? (
            <div className="text-muted-foreground py-10 text-center text-sm">加载任务详情…</div>
          ) : detailError ? (
            <div className="text-destructive mt-4 rounded-lg border border-destructive/40 p-3 text-sm">
              加载失败：{detailError}
            </div>
          ) : detail ? (
            <div className="mt-4 flex flex-col gap-4 text-sm">
              <div>
                <div className="text-muted-foreground mb-1 text-xs">描述</div>
                <div className="whitespace-pre-wrap">{detail.description || "-"}</div>
              </div>
              <div>
                <div className="text-muted-foreground mb-1 text-xs">目标</div>
                <div className="whitespace-pre-wrap">{detail.goal || "-"}</div>
              </div>
              <div className="grid gap-3 sm:grid-cols-2">
                <div className="rounded-lg border p-3">
                  <div className="text-muted-foreground text-xs">进度</div>
                  <div className="mt-1">
                    {detail.goals_total ? `${detail.goals_met ?? 0} / ${detail.goals_total} 个目标` : "未设置目标"}
                  </div>
                  <div className="text-muted-foreground mt-1 text-xs">
                    进行中 {detail.in_flight ?? 0} · 引擎 {detail.engine_mode ?? "-"}
                  </div>
                </div>
                <div className="rounded-lg border p-3">
                  <div className="text-muted-foreground text-xs">Token</div>
                  <div className="mt-1 font-mono tabular-nums">{totalTokens(detail).toLocaleString()}</div>
                  <div className="text-muted-foreground mt-1 text-xs">
                    输入 {detail.tokens?.input_tokens ?? 0} · 输出 {detail.tokens?.output_tokens ?? 0}
                  </div>
                </div>
              </div>
              <div className="grid gap-3 sm:grid-cols-2">
                <div className="rounded-lg border p-3">
                  <div className="text-muted-foreground text-xs">创建</div>
                  <div className="mt-1">{formatTime(detail.created_at)}</div>
                </div>
                <div className="rounded-lg border p-3">
                  <div className="text-muted-foreground text-xs">完成</div>
                  <div className="mt-1">{formatTime(detail.completed_at)}</div>
                </div>
              </div>
              {detail.findings ? (
                <div>
                  <div className="text-muted-foreground mb-1 text-xs">漏洞</div>
                  <div className="flex flex-wrap gap-1">
                    <Badge variant="destructive" className="text-[10px]">
                      严重 {detail.findings.critical}
                    </Badge>
                    <Badge variant="default" className="text-[10px]">
                      高危 {detail.findings.high}
                    </Badge>
                    <Badge variant="secondary" className="text-[10px]">
                      中危 {detail.findings.medium}
                    </Badge>
                    <Badge variant="outline" className="text-[10px]">
                      低危 {detail.findings.low}
                    </Badge>
                  </div>
                </div>
              ) : null}
              {writeEnabled ? (
                <div className="flex flex-wrap gap-2 border-t pt-3">
                  {detail.status === "paused" ? (
                    <Button size="sm" onClick={() => void control("resume")} disabled={actionLoading}>
                      <PlayIcon className="size-4" /> 继续
                    </Button>
                  ) : null}
                  {detail.status === "created" || detail.status === "queued" || detail.status === "running" ? (
                    <Button size="sm" variant="outline" onClick={() => void control("pause")} disabled={actionLoading}>
                      <PauseIcon className="size-4" /> 暂停
                    </Button>
                  ) : null}
                  <Button size="sm" variant="outline" onClick={openRename} disabled={actionLoading}>
                    <PencilIcon className="size-4" /> 重命名
                  </Button>
                  <Button size="sm" variant="destructive" onClick={openDelete} disabled={actionLoading}>
                    <Trash2Icon className="size-4" /> 删除
                  </Button>
                </div>
              ) : null}
            </div>
          ) : null}
        </SheetContent>
      </Sheet>

      <Dialog
        open={createOpen}
        onOpenChange={(open) => {
          if (!open && !creating) setCreateOpen(false);
        }}
      >
        <DialogContent className="sm:max-w-2xl">
          <form className="flex flex-col gap-4" onSubmit={(e) => void submitCreate(e)}>
            <DialogHeader>
              <DialogTitle>新建任务</DialogTitle>
              <DialogDescription>
                任务创建后将自动启动。建议使用简短的目标描述，可勾选“创建后立即暂停”。
              </DialogDescription>
            </DialogHeader>
            <div className="grid gap-1.5">
              <label className="text-sm font-medium" htmlFor="step6c-task-name">
                名称（可选）
              </label>
              <Input
                id="step6c-task-name"
                value={createForm.name}
                onChange={(e) => setCreateForm((f) => ({ ...f, name: e.target.value }))}
                placeholder="留空则使用描述作为标题"
              />
            </div>
            <div className="grid gap-1.5">
              <label className="text-sm font-medium" htmlFor="step6c-task-description">
                描述 *
              </label>
              <Textarea
                id="step6c-task-description"
                value={createForm.description}
                onChange={(e) => setCreateForm((f) => ({ ...f, description: e.target.value }))}
                rows={3}
                placeholder="例如：内网横向移动排查（只读探测）"
              />
            </div>
            <div className="grid gap-1.5">
              <label className="text-sm font-medium" htmlFor="step6c-task-goal">
                目标 *
              </label>
              <Textarea
                id="step6c-task-goal"
                value={createForm.goal}
                onChange={(e) => setCreateForm((f) => ({ ...f, goal: e.target.value }))}
                rows={3}
                placeholder="例如：创建后立即暂停并删除，不执行任何工具"
              />
            </div>
            <div className="grid gap-3 sm:grid-cols-2">
              <div className="grid gap-1.5">
                <label className="text-sm font-medium" htmlFor="step6c-task-category">
                  分类
                </label>
                <Select
                  value={createForm.categoryId}
                  onValueChange={(v) => setCreateForm((f) => ({ ...f, categoryId: v }))}
                >
                  <SelectTrigger id="step6c-task-category">
                    <SelectValue placeholder="不分类" />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="0">不分类</SelectItem>
                    {categories.map((c) => (
                      <SelectItem key={c.id} value={String(c.id)}>
                        {c.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="grid gap-1.5">
                <label className="text-sm font-medium" htmlFor="step6c-task-timeout">
                  超时（秒）
                </label>
                <Input
                  id="step6c-task-timeout"
                  type="number"
                  min={0}
                  value={createForm.timeoutSeconds}
                  onChange={(e) => setCreateForm((f) => ({ ...f, timeoutSeconds: e.target.value }))}
                  placeholder="0 = 使用引擎默认"
                />
              </div>
            </div>
            <div className="flex items-start gap-2 text-sm">
              <Checkbox checked={createPause} onCheckedChange={(v) => setCreatePause(v === true)} />
              <span>
                创建后立即暂停
                <span className="text-muted-foreground block text-xs">推荐用于先创建再手动放行的场景。</span>
              </span>
            </div>
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => setCreateOpen(false)} disabled={creating}>
                取消
              </Button>
              <Button type="submit" disabled={creating || !createForm.description.trim() || !createForm.goal.trim()}>
                {creating ? "创建中…" : "创建任务"}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      <Dialog
        open={renameTarget !== null}
        onOpenChange={(open) => {
          if (!open && !actionLoading) setRenameTarget(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>重命名任务 #{renameTarget?.id}</DialogTitle>
            <DialogDescription>留空则回退到任务描述。</DialogDescription>
          </DialogHeader>
          <Input
            value={renameValue}
            onChange={(e) => setRenameValue(e.target.value)}
            placeholder="任务名称"
            disabled={actionLoading}
          />
          <DialogFooter>
            <Button variant="outline" onClick={() => setRenameTarget(null)} disabled={actionLoading}>
              取消
            </Button>
            <Button onClick={() => void submitRename()} disabled={actionLoading}>
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
            <DialogTitle>删除任务 #{deleteTarget?.id}</DialogTitle>
            <DialogDescription>
              任务记录会被删除；以下关联数据默认保留。删除不可恢复，请确认关联数据选项。
            </DialogDescription>
          </DialogHeader>
          <div className="flex flex-col gap-2 text-sm">
            {DELETE_OPTIONS.map((opt) => (
              <div key={opt.key} className="flex items-start gap-2">
                <Checkbox
                  checked={deleteOptions[opt.key]}
                  onCheckedChange={(v) => setDeleteOptions((prev) => ({ ...prev, [opt.key]: v === true }))}
                  disabled={deleting}
                />
                <span>
                  {opt.label}
                  <span className="text-muted-foreground block text-xs">{opt.hint}</span>
                </span>
              </div>
            ))}
          </div>
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
