"use client";

import * as React from "react";

import {
  FolderKanbanIcon,
  Loader2Icon,
  PencilIcon,
  PlusIcon,
  RefreshCwIcon,
  SearchIcon,
  Trash2Icon,
} from "lucide-react";
import { toast } from "sonner";

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
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { finduDelete, finduErrorMessage, finduPost, finduPut, formatTime } from "@/lib/findu";
import { type FinduProject, listFinduProjects } from "@/lib/readonly-api";
import { cn } from "@/lib/utils";

const STATUS_OPTIONS = [
  { value: "", label: "未设置" },
  { value: "active", label: "进行中" },
  { value: "paused", label: "已暂停" },
  { value: "completed", label: "已完成" },
  { value: "archived", label: "已归档" },
];

const STATUS_LABEL: Record<string, string> = Object.fromEntries(STATUS_OPTIONS.map((s) => [s.value, s.label]));

interface ProjectForm {
  name: string;
  description: string;
  status: string;
  pinned: boolean;
}

function emptyForm(): ProjectForm {
  return { name: "", description: "", status: "", pinned: false };
}

export default function ProjectsPage() {
  const [rows, setRows] = React.useState<FinduProject[]>([]);
  const [total, setTotal] = React.useState(0);
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState("");
  const [search, setSearch] = React.useState("");
  const [reload, setReload] = React.useState(0);
  // U2（2026-10-23）：双跑验证阀退役——写侧后端已宿主原生，项目页直接可写。
  const writeEnabled = true;

  const [dialogOpen, setDialogOpen] = React.useState(false);
  const [editTarget, setEditTarget] = React.useState<FinduProject | null>(null);
  const [form, setForm] = React.useState<ProjectForm>(emptyForm());
  const [saving, setSaving] = React.useState(false);
  const [deleteTarget, setDeleteTarget] = React.useState<FinduProject | null>(null);
  const [deleting, setDeleting] = React.useState(false);

  // biome-ignore lint/correctness/useExhaustiveDependencies: reload is the explicit refresh trigger.
  React.useEffect(() => {
    let alive = true;
    setLoading(true);
    setError("");
    listFinduProjects(500)
      .then((r) => {
        if (!alive) return;
        setRows(r.projects ?? []);
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
  }, [reload]);

  const filtered = React.useMemo(() => {
    const q = search.trim().toLowerCase();
    if (!q) return rows;
    return rows.filter((p) =>
      [p.name, p.description, p.status].filter(Boolean).some((v) => String(v).toLowerCase().includes(q)),
    );
  }, [rows, search]);

  function openCreate() {
    setEditTarget(null);
    setForm(emptyForm());
    setDialogOpen(true);
  }

  function openEdit(p: FinduProject) {
    setEditTarget(p);
    setForm({
      name: p.name ?? "",
      description: p.description ?? "",
      status: p.status ?? "",
      pinned: Boolean(p.pinned),
    });
    setDialogOpen(true);
  }

  async function submit() {
    const name = form.name.trim();
    if (!name) {
      toast.error("项目名称必填");
      return;
    }
    setSaving(true);
    try {
      const payload = { name, description: form.description, status: form.status, pinned: form.pinned };
      if (editTarget) {
        const updated = await finduPut<FinduProject>(`/projects/${encodeURIComponent(editTarget.id)}`, payload);
        toast.success("项目已更新");
        if (updated) setRows((prev) => prev.map((p) => (p.id === editTarget.id ? { ...p, ...updated } : p)));
      } else {
        const created = await finduPost<FinduProject>("/projects", payload);
        toast.success("项目已创建");
        if (created) setRows((prev) => [created, ...prev]);
      }
      setDialogOpen(false);
      setTotal((n) => n + (editTarget ? 0 : 1));
    } catch (e) {
      toast.error(`保存项目失败：${finduErrorMessage(e)}`);
    } finally {
      setSaving(false);
    }
  }

  function openDelete(p: FinduProject) {
    setDeleteTarget(p);
  }

  async function submitDelete() {
    const t = deleteTarget;
    if (!t) return;
    setDeleting(true);
    try {
      await finduDelete(`/projects/${encodeURIComponent(t.id)}`);
      toast.success("项目已删除");
      setDeleteTarget(null);
      setRows((prev) => prev.filter((p) => p.id !== t.id));
      setTotal((n) => Math.max(0, n - 1));
    } catch (e) {
      toast.error(`删除项目失败：${finduErrorMessage(e)}`);
    } finally {
      setDeleting(false);
    }
  }

  return (
    <div className="flex h-[calc(100dvh-6rem)] min-h-0 flex-col gap-4 md:h-[calc(100dvh-7rem)] md:gap-6">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h1 className="flex items-center gap-2 text-xl font-semibold tracking-tight">
            <FolderKanbanIcon className="size-5" /> 项目
          </h1>
          <p className="text-muted-foreground text-sm">
            新建 / 编辑 / 删除，共 {total} 个项目
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button size="sm" onClick={openCreate}>
            <PlusIcon className="size-4" /> 新建项目
          </Button>
          <Button variant="outline" onClick={() => setReload((n) => n + 1)} disabled={loading}>
            <RefreshCwIcon className={cn("size-4", loading && "animate-spin")} /> 刷新
          </Button>
        </div>
      </div>

      <Card>
        <CardContent className="py-3">
          <div className="relative max-w-xl">
            <SearchIcon className="text-muted-foreground absolute top-2.5 left-2.5 size-4" />
            <Input
              className="pl-8"
              placeholder="搜索项目名称、描述、状态…"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </div>
        </CardContent>
      </Card>

      {error ? (
        <Card className="border-destructive/40 bg-destructive/5">
          <CardContent className="text-destructive py-3 text-sm">项目列表加载失败：{error}</CardContent>
        </Card>
      ) : null}

      <Card className="min-h-0 flex-1 overflow-y-auto">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>项目</TableHead>
              <TableHead className="w-40">状态</TableHead>
              <TableHead className="w-24">置顶</TableHead>
              <TableHead className="w-44">更新时间</TableHead>
              {writeEnabled ? <TableHead className="w-24 text-right">操作</TableHead> : null}
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading ? (
              Array.from({ length: 6 }).map((_, i) => (
                <TableRow key={i}>
                  <TableCell colSpan={writeEnabled ? 5 : 4}>
                    <Skeleton className="h-5 w-full" />
                  </TableCell>
                </TableRow>
              ))
            ) : filtered.length === 0 ? (
              <TableRow>
                <TableCell colSpan={writeEnabled ? 5 : 4} className="text-muted-foreground py-8 text-center">
                  {search ? "没有匹配的项目" : "暂无项目"}
                </TableCell>
              </TableRow>
            ) : (
              filtered.map((p) => (
                <TableRow key={p.id}>
                  <TableCell>
                    <div className="font-medium">{p.name || p.id}</div>
                    {p.description ? (
                      <div className="text-muted-foreground line-clamp-1 max-w-md text-xs">{p.description}</div>
                    ) : null}
                  </TableCell>
                  <TableCell>
                    <Badge variant={p.status === "active" ? "default" : "secondary"}>
                      {STATUS_LABEL[p.status ?? ""] ?? p.status}
                    </Badge>
                  </TableCell>
                  <TableCell>
                    {p.pinned ? (
                      <Badge variant="outline">置顶</Badge>
                    ) : (
                      <span className="text-muted-foreground text-xs">-</span>
                    )}
                  </TableCell>
                  <TableCell className="text-muted-foreground text-xs">{formatTime(p.updated_at)}</TableCell>
                  {writeEnabled ? (
                    <TableCell className="text-right">
                      <div className="flex items-center justify-end gap-1">
                        <Button size="icon" variant="ghost" className="size-8" onClick={() => openEdit(p)}>
                          <PencilIcon className="size-4" />
                        </Button>
                        <Button
                          size="icon"
                          variant="ghost"
                          className="size-8 text-destructive"
                          onClick={() => openDelete(p)}
                        >
                          <Trash2Icon className="size-4" />
                        </Button>
                      </div>
                    </TableCell>
                  ) : null}
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </Card>

      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{editTarget ? "编辑项目" : "新建项目"}</DialogTitle>
            <DialogDescription>
              {editTarget
                ? `确认要修改「${editTarget.name || editTarget.id}」吗？`
                : "请确认项目名称与描述。"}
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-4 py-2">
            <div className="grid gap-2">
              <Label htmlFor="proj-name">项目名称 *</Label>
              <Input
                id="proj-name"
                value={form.name}
                onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
                placeholder="例如：渗透测试项目 A"
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="proj-desc">描述</Label>
              <Textarea
                id="proj-desc"
                value={form.description}
                onChange={(e) => setForm((f) => ({ ...f, description: e.target.value }))}
                rows={2}
                placeholder="项目说明（可选）"
              />
            </div>
            <div className="grid gap-2">
              <Label>状态</Label>
              <Select value={form.status} onValueChange={(v) => setForm((f) => ({ ...f, status: v }))}>
                <SelectTrigger>
                  <SelectValue placeholder="选择状态" />
                </SelectTrigger>
                <SelectContent>
                  {STATUS_OPTIONS.map((s) => (
                    <SelectItem key={s.value} value={s.value}>
                      {s.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <label className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={form.pinned}
                onChange={(e) => setForm((f) => ({ ...f, pinned: e.target.checked }))}
              />
              置顶项目
            </label>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDialogOpen(false)} disabled={saving}>
              取消
            </Button>
            <Button onClick={() => void submit()} disabled={saving}>
              {saving ? <Loader2Icon className="size-4 animate-spin" /> : null} 保存
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={Boolean(deleteTarget)} onOpenChange={(o) => !o && setDeleteTarget(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>删除项目</DialogTitle>
            <DialogDescription>
              项目 {deleteTarget?.name || deleteTarget?.id} 将被删除，关联数据随之清理。此操作不可恢复，请确认目标无误。
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDeleteTarget(null)} disabled={deleting}>
              取消
            </Button>
            <Button variant="destructive" onClick={() => void submitDelete()} disabled={deleting}>
              {deleting ? <Loader2Icon className="size-4 animate-spin" /> : <Trash2Icon className="size-4" />} 删除
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
