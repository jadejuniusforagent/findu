"use client";

import * as React from "react";

import {
  Loader2Icon,
  PencilIcon,
  PlusIcon,
  RefreshCwIcon,
  SearchIcon,
  TerminalSquareIcon,
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
import { finduDelete, finduErrorMessage, finduPost, finduPut, formatTime } from "@/lib/findu";
import {
  type FinduProject,
  type FinduWebshellConnection,
  listFinduProjects,
  listFinduWebshellConnections,
} from "@/lib/readonly-api";
import { cn } from "@/lib/utils";

const TYPE_OPTIONS = [
  { value: "php", label: "PHP" },
  { value: "jsp", label: "JSP" },
  { value: "asp", label: "ASP" },
  { value: "aspx", label: "ASPX" },
];

const ENCODING_OPTIONS = [
  { value: "", label: "自动" },
  { value: "utf-8", label: "UTF-8" },
  { value: "gbk", label: "GBK" },
  { value: "gb18030", label: "GB18030" },
];

const OS_OPTIONS = [
  { value: "", label: "自动" },
  { value: "linux", label: "Linux" },
  { value: "windows", label: "Windows" },
];

const METHOD_OPTIONS = [
  { value: "post", label: "POST" },
  { value: "get", label: "GET" },
];

interface ConnForm {
  project_id: string;
  url: string;
  password: string;
  type: string;
  method: string;
  cmd_param: string;
  remark: string;
  encoding: string;
  os: string;
}

function emptyForm(): ConnForm {
  return {
    project_id: "",
    url: "",
    password: "",
    type: "php",
    method: "post",
    cmd_param: "",
    remark: "",
    encoding: "",
    os: "",
  };
}

export default function WebshellPage() {
  const [rows, setRows] = React.useState<FinduWebshellConnection[]>([]);
  const [projects, setProjects] = React.useState<FinduProject[]>([]);
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState("");
  const [reload, setReload] = React.useState(0);
  // U2（2026-10-23）：双跑验证阀退役——写侧后端已宿主原生，WebShell 页直接可写。
  const writeEnabled = true;

  const [dialogOpen, setDialogOpen] = React.useState(false);
  const [editTarget, setEditTarget] = React.useState<FinduWebshellConnection | null>(null);
  const [form, setForm] = React.useState<ConnForm>(emptyForm());
  const [saving, setSaving] = React.useState(false);
  const [deleteTarget, setDeleteTarget] = React.useState<FinduWebshellConnection | null>(null);
  const [deleting, setDeleting] = React.useState(false);

  // biome-ignore lint/correctness/useExhaustiveDependencies: reload is the explicit refresh trigger.
  React.useEffect(() => {
    let alive = true;
    setLoading(true);
    setError("");
    void Promise.allSettled([listFinduWebshellConnections(), listFinduProjects(500)])
      .then(([wr, pr]) => {
        if (!alive) return;
        if (wr.status === "fulfilled") setRows(wr.value ?? []);
        if (pr.status === "fulfilled") setProjects(pr.value.projects ?? []);
        setLoading(false);
      })
      .catch((e: Error) => {
        if (alive) setError(e.message);
      });
    return () => {
      alive = false;
    };
  }, [reload]);

  function openCreate() {
    setEditTarget(null);
    setForm(emptyForm());
    setDialogOpen(true);
  }

  function openEdit(c: FinduWebshellConnection) {
    setEditTarget(c);
    setForm({
      project_id: c.project_id ?? "",
      url: c.url ?? "",
      password: "",
      type: c.type ?? "php",
      method: c.method ?? "post",
      cmd_param: "",
      remark: c.remark ?? "",
      encoding: c.encoding ?? "",
      os: c.os ?? "",
    });
    setDialogOpen(true);
  }

  async function submit() {
    const url = form.url.trim();
    if (!url) {
      toast.error("连接 URL 必填");
      return;
    }
    setSaving(true);
    try {
      const payload = {
        project_id: form.project_id,
        url,
        password: form.password,
        type: form.type,
        method: form.method,
        cmd_param: form.cmd_param,
        remark: form.remark,
        encoding: form.encoding,
        os: form.os,
      };
      if (editTarget) {
        await finduPut(`/webshell/connections/${encodeURIComponent(editTarget.id)}`, payload);
        toast.success("连接已更新");
      } else {
        await finduPost("/webshell/connections", payload);
        toast.success("连接已创建");
      }
      setDialogOpen(false);
      setReload((n) => n + 1);
    } catch (e) {
      toast.error(`保存连接失败：${finduErrorMessage(e)}`);
    } finally {
      setSaving(false);
    }
  }

  function openDelete(c: FinduWebshellConnection) {
    setDeleteTarget(c);
  }

  async function submitDelete() {
    const t = deleteTarget;
    if (!t) return;
    setDeleting(true);
    try {
      await finduDelete(`/webshell/connections/${encodeURIComponent(t.id)}`);
      toast.success("连接已删除");
      setDeleteTarget(null);
      setReload((n) => n + 1);
    } catch (e) {
      toast.error(`删除连接失败：${finduErrorMessage(e)}`);
    } finally {
      setDeleting(false);
    }
  }

  return (
    <div className="flex h-[calc(100dvh-6rem)] min-h-0 flex-col gap-4 md:h-[calc(100dvh-7rem)] md:gap-6">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h1 className="flex items-center gap-2 text-xl font-semibold tracking-tight">
            <TerminalSquareIcon className="size-5" /> WebShell 连接
          </h1>
          <p className="text-muted-foreground text-sm">
            新建 / 编辑 / 删除连接，共 {rows.length} 个
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button size="sm" onClick={openCreate}>
            <PlusIcon className="size-4" /> 新建连接
          </Button>
          <Button variant="outline" onClick={() => setReload((n) => n + 1)} disabled={loading}>
            <RefreshCwIcon className={cn("size-4", loading && "animate-spin")} /> 刷新
          </Button>
        </div>
      </div>

      {error ? (
        <Card className="border-destructive/40 bg-destructive/5">
          <CardContent className="text-destructive py-3 text-sm">连接列表加载失败：{error}</CardContent>
        </Card>
      ) : null}

      <Card className="min-h-0 flex-1 overflow-y-auto">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>备注 / 连接</TableHead>
              <TableHead className="w-28">类型</TableHead>
              <TableHead className="w-28">方法</TableHead>
              <TableHead className="w-24">系统</TableHead>
              <TableHead className="w-44">创建时间</TableHead>
              {writeEnabled ? <TableHead className="w-24 text-right">操作</TableHead> : null}
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading ? (
              Array.from({ length: 5 }).map((_, i) => (
                <TableRow key={i}>
                  <TableCell colSpan={writeEnabled ? 6 : 5}>
                    <Skeleton className="h-5 w-full" />
                  </TableCell>
                </TableRow>
              ))
            ) : rows.length === 0 ? (
              <TableRow>
                <TableCell colSpan={writeEnabled ? 6 : 5} className="text-muted-foreground py-8 text-center">
                  暂无 WebShell 连接
                </TableCell>
              </TableRow>
            ) : (
              rows.map((c) => (
                <TableRow key={c.id}>
                  <TableCell>
                    <div className="font-medium">{c.remark || c.id}</div>
                    <div className="text-muted-foreground max-w-md truncate text-xs">{c.url}</div>
                  </TableCell>
                  <TableCell>
                    <Badge variant="secondary">{(c.type ?? "php").toUpperCase()}</Badge>
                  </TableCell>
                  <TableCell className="text-xs">{c.method ?? "post"}</TableCell>
                  <TableCell className="text-xs">{c.os || "-"}</TableCell>
                  <TableCell className="text-muted-foreground text-xs">{formatTime(c.createdAt)}</TableCell>
                  {writeEnabled ? (
                    <TableCell className="text-right">
                      <div className="flex items-center justify-end gap-1">
                        <Button size="icon" variant="ghost" className="size-8" onClick={() => openEdit(c)}>
                          <PencilIcon className="size-4" />
                        </Button>
                        <Button
                          size="icon"
                          variant="ghost"
                          className="size-8 text-destructive"
                          onClick={() => openDelete(c)}
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
            <DialogTitle>{editTarget ? "编辑连接" : "新建连接"}</DialogTitle>
            <DialogDescription>
              {editTarget
                ? `编辑连接「${editTarget.remark || editTarget.id}」；密码留空表示不修改。`
                : "请确认目标 URL 可连通。"}
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-4 py-2">
            <div className="grid gap-2">
              <Label htmlFor="ws-url">连接 URL *</Label>
              <Input
                id="ws-url"
                value={form.url}
                onChange={(e) => setForm((f) => ({ ...f, url: e.target.value }))}
                placeholder="https://target.example.com/shell.php"
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="ws-pass">{editTarget ? "密码（留空不修改）" : "连接密码"}</Label>
              <Input
                id="ws-pass"
                type="password"
                value={form.password}
                onChange={(e) => setForm((f) => ({ ...f, password: e.target.value }))}
                placeholder="webshell 密码"
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="ws-remark">备注</Label>
              <Input
                id="ws-remark"
                value={form.remark}
                onChange={(e) => setForm((f) => ({ ...f, remark: e.target.value }))}
                placeholder="例如：目标主机 A 的 WebShell"
              />
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div className="grid gap-2">
                <Label>所属项目</Label>
                <Select value={form.project_id} onValueChange={(v) => setForm((f) => ({ ...f, project_id: v }))}>
                  <SelectTrigger>
                    <SelectValue placeholder="未绑定" />
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
              </div>
              <div className="grid gap-2">
                <Label>Shell 类型</Label>
                <Select value={form.type} onValueChange={(v) => setForm((f) => ({ ...f, type: v }))}>
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {TYPE_OPTIONS.map((t) => (
                      <SelectItem key={t.value} value={t.value}>
                        {t.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="grid gap-2">
                <Label>请求方法</Label>
                <Select value={form.method} onValueChange={(v) => setForm((f) => ({ ...f, method: v }))}>
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {METHOD_OPTIONS.map((m) => (
                      <SelectItem key={m.value} value={m.value}>
                        {m.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="grid gap-2">
                <Label>命令参数名</Label>
                <Input
                  value={form.cmd_param}
                  onChange={(e) => setForm((f) => ({ ...f, cmd_param: e.target.value }))}
                  placeholder="如 cmd"
                />
              </div>
              <div className="grid gap-2">
                <Label>响应编码</Label>
                <Select value={form.encoding} onValueChange={(v) => setForm((f) => ({ ...f, encoding: v }))}>
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {ENCODING_OPTIONS.map((en) => (
                      <SelectItem key={en.value} value={en.value}>
                        {en.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="grid gap-2">
                <Label>目标系统</Label>
                <Select value={form.os} onValueChange={(v) => setForm((f) => ({ ...f, os: v }))}>
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {OS_OPTIONS.map((o) => (
                      <SelectItem key={o.value} value={o.value}>
                        {o.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            </div>
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
            <DialogTitle>删除连接</DialogTitle>
            <DialogDescription>
              连接 {deleteTarget?.remark || deleteTarget?.url || deleteTarget?.id} 将被删除。此操作不可恢复，请确认目标无误。
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
