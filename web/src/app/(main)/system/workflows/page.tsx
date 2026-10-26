"use client";

// 工作流管理（旧 findu UI workflow 域迁入控制台，U6 收尾续）。
// 后端 /api/workflows/*（workflows_native.go，B5+R2a 已宿主原生）——
// GET list / GET {id} / POST create / PUT {id} / DELETE {id} / POST validate / GET runs/pending。
// graph 为 JSON（nodes/edges/config），通过校验后编译执行；"start" 为保留节点 id。

import * as React from "react";
import { useMemo, useState } from "react";

import {
  AlertTriangleIcon,
  CheckCircle2Icon,
  NetworkIcon,
  PencilIcon,
  PlusCircleIcon,
  RefreshCwIcon,
  Trash2Icon,
  WorkflowIcon,
} from "lucide-react";
import { toast } from "sonner";

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
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
import { Textarea } from "@/components/ui/textarea";
import { dashboardApi, type WorkflowDefinition, type WorkflowSaveRequest } from "@/lib/dashboard-api";
import { formatTime } from "@/lib/findu";
import { cn } from "@/lib/utils";

const DEFAULT_GRAPH = JSON.stringify(
  {
    nodes: [
      { id: "begin", type: "start", label: "开始", position: { x: 0, y: 0 }, config: {} },
      {
        id: "output",
        type: "output",
        label: "结束",
        position: { x: 200, y: 0 },
        config: { output_key: "result" },
      },
    ],
    edges: [{ id: "e1", source: "begin", target: "output", label: "", config: {} }],
    config: {},
  },
  null,
  2,
);

export default function WorkflowsPage() {
  const [workflows, setWorkflows] = useState<WorkflowDefinition[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const [editing, setEditing] = useState<{ id: string; existing: boolean } | null>(null);
  const [form, setForm] = useState<WorkflowSaveRequest>({
    name: "",
    description: "",
    graph: DEFAULT_GRAPH,
    enabled: true,
  });
  const [validateMsg, setValidateMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [saving, setSaving] = useState(false);

  const [deleting, setDeleting] = useState<WorkflowDefinition | null>(null);
  const [deletingLoading, setDeletingLoading] = useState(false);

  const load = React.useCallback(async () => {
    setLoading(true);
    try {
      const res = await dashboardApi.workflowsList(true);
      setWorkflows(res.workflows ?? []);
      setError(null);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setLoading(false);
    }
  }, []);

  React.useEffect(() => {
    void load();
    const t = setInterval(() => void load(), 30000);
    return () => clearInterval(t);
  }, [load]);

  const enabledCount = useMemo(() => workflows.filter((w) => w.enabled).length, [workflows]);
  const disabledCount = workflows.length - enabledCount;

  const openCreate = () => {
    setEditing({ id: "", existing: false });
    setForm({ id: "", name: "", description: "", graph: DEFAULT_GRAPH, enabled: true });
    setValidateMsg(null);
  };

  const openEdit = (w: WorkflowDefinition) => {
    setEditing({ id: w.id, existing: true });
    setForm({
      id: w.id,
      name: w.name,
      description: w.description ?? "",
      graph: w.graph_json,
      enabled: w.enabled,
    });
    setValidateMsg(null);
  };

  const parseGraph = (text: string): unknown | null => {
    try {
      return JSON.parse(text);
    } catch {
      return null;
    }
  };

  const handleValidate = async () => {
    const graphText = String(form.graph ?? "").trim();
    if (!graphText) {
      setValidateMsg({ ok: false, text: "graph 不能为空" });
      return;
    }
    const graph = parseGraph(graphText);
    if (graph === null) {
      setValidateMsg({ ok: false, text: "graph 不是合法 JSON，请检查格式" });
      return;
    }
    try {
      const res = await dashboardApi.workflowValidate(graph);
      setValidateMsg(res.ok ? { ok: true, text: "校验通过，可保存" } : { ok: false, text: res.error ?? "校验失败" });
    } catch (e) {
      setValidateMsg({ ok: false, text: (e as Error).message });
    }
  };

  const handleSave = async () => {
    if (!editing) return;
    const name = form.name?.trim() ?? "";
    if (!name) {
      toast.error("名称不能为空");
      return;
    }
    if (!editing.existing) {
      const id = String(form.id ?? "").trim();
      if (!id) {
        toast.error("工作流 ID 不能为空");
        return;
      }
      if (id === "start") {
        toast.error("'start' 是保留节点 ID，请换一个工作流 ID");
        return;
      }
    }
    const graph = parseGraph(String(form.graph ?? "").trim());
    if (graph === null) {
      toast.error("graph 不是合法 JSON，请先校验");
      return;
    }
    setSaving(true);
    try {
      await dashboardApi.workflowSave({ ...form, graph }, editing.existing ? editing.id : undefined);
      toast.success("工作流已保存");
      setEditing(null);
      await load();
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setSaving(false);
    }
  };

  const handleToggle = async (w: WorkflowDefinition, enabled: boolean) => {
    try {
      await dashboardApi.workflowSave({ ...w, enabled });
      toast.success(enabled ? "已启用" : "已禁用");
      await load();
    } catch (e) {
      toast.error((e as Error).message);
    }
  };

  const handleDelete = async () => {
    if (!deleting) return;
    setDeletingLoading(true);
    try {
      await dashboardApi.workflowDelete(deleting.id);
      toast.success("工作流已删除");
      setDeleting(null);
      await load();
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setDeletingLoading(false);
    }
  };

  const graphNodes = (graphJson?: string): number => {
    try {
      const g = JSON.parse(graphJson ?? "{}");
      return Array.isArray(g.nodes) ? g.nodes.length : 0;
    } catch {
      return 0;
    }
  };

  return (
    <div className="flex flex-1 flex-col gap-4">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h1 className="flex items-center gap-2 text-xl font-semibold tracking-tight">
            <WorkflowIcon className="size-5" /> 工作流
          </h1>
          <p className="text-muted-foreground text-sm">
            工作流定义管理（增删改查 + 启停 + 图校验）。graph 为 JSON，含节点与边，保存后编译执行。
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" onClick={() => void load()} disabled={loading}>
            <RefreshCwIcon className={cn("size-4", loading && "animate-spin")} /> 刷新
          </Button>
          <Button onClick={openCreate}>
            <PlusCircleIcon className="size-4" /> 新建
          </Button>
        </div>
      </div>

      {error ? (
        <Card className="border-amber-500/40 bg-amber-500/5">
          <CardContent className="flex items-start gap-2 py-3 text-sm">
            <AlertTriangleIcon className="mt-0.5 size-4 shrink-0 text-amber-500" />
            <div className="font-medium">加载失败：{error}</div>
          </CardContent>
        </Card>
      ) : null}

      <div className="grid gap-3 sm:grid-cols-3">
        <StatChip title="工作流" value={workflows.length} accent="" />
        <StatChip title="已启用" value={enabledCount} accent="text-emerald-600" />
        <StatChip title="已禁用" value={disabledCount} accent="text-slate-500" />
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            <NetworkIcon className="size-4" /> 工作流定义
          </CardTitle>
          <CardDescription>工作流列表（版本 / 启停 / 图节点数 / 更新时间）。</CardDescription>
        </CardHeader>
        <CardContent>
          {loading && workflows.length === 0 ? (
            <div className="text-muted-foreground py-6 text-center text-sm">加载中…</div>
          ) : workflows.length === 0 ? (
            <div className="text-muted-foreground py-6 text-center text-sm">暂无工作流定义，点击「新建」创建</div>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b text-muted-foreground text-xs">
                    <th className="px-3 py-2 text-left font-medium">名称</th>
                    <th className="px-3 py-2 text-left font-medium">描述</th>
                    <th className="px-3 py-2 text-left font-medium">版本</th>
                    <th className="px-3 py-2 text-left font-medium">节点</th>
                    <th className="px-3 py-2 text-left font-medium">状态</th>
                    <th className="px-3 py-2 text-left font-medium">更新时间</th>
                    <th className="px-3 py-2 text-right font-medium">操作</th>
                  </tr>
                </thead>
                <tbody>
                  {workflows.map((w) => (
                    <tr key={w.id} className="border-b last:border-0">
                      <td className="px-3 py-2">
                        <div className="flex items-center gap-1.5">
                          <WorkflowIcon className="size-3.5 text-muted-foreground" />
                          <span className="font-medium">{w.name}</span>
                        </div>
                        <div className="text-muted-foreground text-xs">{w.id}</div>
                      </td>
                      <td className="text-muted-foreground max-w-[240px] truncate px-3 py-2 text-xs">
                        {w.description || "—"}
                      </td>
                      <td className="px-3 py-2 text-xs tabular-nums">v{w.version}</td>
                      <td className="px-3 py-2 text-xs tabular-nums">{graphNodes(w.graph_json)}</td>
                      <td className="px-3 py-2">
                        <button
                          type="button"
                          onClick={() => void handleToggle(w, !w.enabled)}
                          className={cn(
                            "rounded-full border px-2 py-0.5 text-[10px] transition-colors",
                            w.enabled
                              ? "border-emerald-500/40 text-emerald-600 hover:bg-emerald-500/10"
                              : "border-slate-400/40 text-slate-500 hover:bg-slate-400/10",
                          )}
                        >
                          {w.enabled ? "已启用" : "已禁用"}
                        </button>
                      </td>
                      <td className="text-muted-foreground px-3 py-2 text-xs">{formatTime(w.updated_at)}</td>
                      <td className="px-3 py-2">
                        <div className="flex justify-end gap-1">
                          <Button variant="ghost" size="sm" onClick={() => openEdit(w)}>
                            <PencilIcon className="size-3.5" /> 编辑
                          </Button>
                          <Button
                            variant="ghost"
                            size="sm"
                            className="text-rose-500 hover:bg-rose-500/10 hover:text-rose-600"
                            onClick={() => setDeleting(w)}
                          >
                            <Trash2Icon className="size-3.5" /> 删除
                          </Button>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </CardContent>
      </Card>

      <Dialog open={editing !== null} onOpenChange={(o) => !o && setEditing(null)}>
        <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>{editing?.existing ? `编辑工作流「${editing.id}」` : "新建工作流"}</DialogTitle>
            <DialogDescription>名称 + graph JSON（含节点与边），保存前可校验。</DialogDescription>
          </DialogHeader>
          <div className="grid gap-3">
            {!editing?.existing ? (
              <FormInput
                id="wfid"
                label="工作流 ID"
                value={String(form.id ?? "")}
                onChange={(v) => setForm((f) => ({ ...f, id: v }))}
                placeholder="如 my-flow（'start' 为保留 ID）"
              />
            ) : null}
            <FormInput
              id="name"
              label="名称"
              value={form.name ?? ""}
              onChange={(v) => setForm((f) => ({ ...f, name: v }))}
            />
            <FormInput
              id="description"
              label="描述"
              value={form.description ?? ""}
              onChange={(v) => setForm((f) => ({ ...f, description: v }))}
            />
            <div className="grid grid-cols-2 gap-3">
              <FormInput
                id="version"
                label="版本"
                type="number"
                value={form.version ?? 0}
                onChange={(v) => setForm((f) => ({ ...f, version: v ? Number(v) : undefined }))}
              />
              <div className="grid gap-1.5">
                <Label htmlFor="enabled">启用</Label>
                <div className="flex h-9 items-center gap-2">
                  <input
                    id="enabled"
                    type="checkbox"
                    className="size-4"
                    checked={form.enabled ?? true}
                    onChange={(e) => setForm((f) => ({ ...f, enabled: e.target.checked }))}
                  />
                  <span className="text-sm">保存后立即启用</span>
                </div>
              </div>
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor="graph">Graph JSON</Label>
              <Textarea
                id="graph"
                rows={12}
                className="font-mono text-xs"
                value={String(form.graph ?? "")}
                onChange={(e) => {
                  setForm((f) => ({ ...f, graph: e.target.value }));
                  setValidateMsg(null);
                }}
                placeholder='{"nodes":[...],"edges":[...],"config":{}}'
              />
              {validateMsg ? (
                <div
                  className={cn(
                    "flex items-start gap-1.5 text-xs",
                    validateMsg.ok ? "text-emerald-600" : "text-rose-500",
                  )}
                >
                  {validateMsg.ok ? (
                    <CheckCircle2Icon className="mt-0.5 size-3.5 shrink-0" />
                  ) : (
                    <AlertTriangleIcon className="mt-0.5 size-3.5 shrink-0" />
                  )}
                  <span className="whitespace-pre-wrap">{validateMsg.text}</span>
                </div>
              ) : null}
              <div className="flex justify-end">
                <Button variant="outline" size="sm" onClick={() => void handleValidate()}>
                  校验
                </Button>
              </div>
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setEditing(null)} disabled={saving}>
              取消
            </Button>
            <Button onClick={() => void handleSave()} disabled={saving}>
              {saving ? "保存中…" : "保存"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <AlertDialog open={deleting !== null} onOpenChange={(o) => !o && setDeleting(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>删除工作流「{deleting?.name ?? ""}」？</AlertDialogTitle>
            <AlertDialogDescription>将删除该工作流定义及其运行记录，不可恢复。</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={deletingLoading}>取消</AlertDialogCancel>
            <AlertDialogAction
              onClick={(e) => {
                e.preventDefault();
                void handleDelete();
              }}
              disabled={deletingLoading}
              className="bg-rose-600 text-white hover:bg-rose-700"
            >
              {deletingLoading ? "删除中…" : "删除"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

function FormInput({
  id,
  label,
  value,
  onChange,
  placeholder,
  type = "text",
}: {
  id: string;
  label: string;
  value: string | number;
  onChange: (v: string) => void;
  placeholder?: string;
  type?: string;
}) {
  return (
    <div className="grid gap-1.5">
      <Label htmlFor={id}>{label}</Label>
      <Input id={id} type={type} value={value} placeholder={placeholder} onChange={(e) => onChange(e.target.value)} />
    </div>
  );
}

function StatChip({ title, value, accent }: { title: string; value: number; accent: string }) {
  return (
    <Card className="py-4">
      <CardHeader className="px-4">
        <CardTitle className="text-muted-foreground text-xs font-medium">{title}</CardTitle>
      </CardHeader>
      <CardContent className="px-4">
        <div className={cn("text-2xl font-semibold tabular-nums", accent)}>{value}</div>
      </CardContent>
    </Card>
  );
}
