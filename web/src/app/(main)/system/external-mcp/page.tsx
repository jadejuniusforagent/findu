"use client";

// 外部 MCP 管理（旧 findu UI mcp-management 域迁入控制台，U6 收尾首批）。
// 后端 /api/external-mcp/*（external_mcp.go，R1 external-mcp 域 + B3 脱敏已宿主原生）——
// GET list / PUT :name upsert / DELETE :name / POST :name/start|stop。
// 有 mcp:write 权限时 env/headers 明文回显，否则掩码 "***"（后端 externalMCPConfigForResponse）。

import * as React from "react";

import {
  AlertTriangleIcon,
  CableIcon,
  PencilIcon,
  PlayIcon,
  PlusCircleIcon,
  RefreshCwIcon,
  SquareIcon,
  Trash2Icon,
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
import { dashboardApi, type ExternalMCPList, type ExternalMCPServerConfig } from "@/lib/dashboard-api";
import { cn } from "@/lib/utils";

type ServerEntry = {
  name: string;
  config: ExternalMCPServerConfig;
  status: string;
  tool_count: number;
  error?: string;
};

const STATUS_STYLE: Record<string, string> = {
  connected: "border-emerald-500/40 text-emerald-600",
  connecting: "border-amber-500/40 text-amber-600",
  disabled: "border-slate-400/40 text-slate-500",
  disconnected: "border-slate-400/40 text-slate-500",
  error: "border-rose-500/40 text-rose-600",
};

const TYPE_LABEL: Record<string, string> = {
  stdio: "stdio",
  http: "Streamable HTTP",
  sse: "SSE",
};

function entryList(data: ExternalMCPList | null): ServerEntry[] {
  if (!data) return [];
  return Object.entries(data.servers ?? {}).map(([name, r]) => ({
    name,
    config: r.config,
    status: r.status,
    tool_count: r.tool_count ?? 0,
    error: r.error,
  }));
}

function kvToText(map?: Record<string, string>): string {
  if (!map) return "";
  return Object.entries(map)
    .map(([k, v]) => `${k}=${v}`)
    .join("\n");
}

function textToKv(text: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const line of text.split("\n")) {
    const trimmed = line.trim();
    if (!trimmed) continue;
    const eq = trimmed.indexOf("=");
    if (eq <= 0) continue;
    out[trimmed.slice(0, eq).trim()] = trimmed.slice(eq + 1).trim();
  }
  return out;
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

export default function ExternalMcpPage() {
  const [data, setData] = React.useState<ExternalMCPList | null>(null);
  const [loading, setLoading] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  // 新增 / 编辑对话框
  const [editing, setEditing] = React.useState<{ name: string; existing: boolean } | null>(null);
  const [form, setForm] = React.useState<ExternalMCPServerConfig>({});
  const [formName, setFormName] = React.useState("");
  const [envText, setEnvText] = React.useState("");
  const [headerText, setHeaderText] = React.useState("");
  const [saving, setSaving] = React.useState(false);

  // 删除确认
  const [deleting, setDeleting] = React.useState<string | null>(null);
  const [deletingLoading, setDeletingLoading] = React.useState(false);

  const load = React.useCallback(async () => {
    setLoading(true);
    try {
      setData(await dashboardApi.externalMcpList());
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

  const servers = React.useMemo(() => entryList(data), [data]);
  const stats = data?.stats ?? { connected: 0, enabled: 0, disabled: 0, total: 0 };

  const openCreate = () => {
    setEditing({ name: "", existing: false });
    setFormName("");
    setForm({ type: "stdio", external_mcp_enable: true });
    setEnvText("");
    setHeaderText("");
  };

  const openEdit = (s: ServerEntry) => {
    setEditing({ name: s.name, existing: true });
    setFormName(s.name);
    setForm({ ...s.config });
    setEnvText(kvToText(s.config.env));
    setHeaderText(kvToText(s.config.headers));
  };

  const handleSave = async () => {
    if (!editing) return;
    const name = formName.trim();
    if (!name) {
      toast.error("名称不能为空");
      return;
    }
    setSaving(true);
    try {
      const config: ExternalMCPServerConfig = {
        ...form,
        env: envText.trim() ? textToKv(envText) : undefined,
        headers: headerText.trim() ? textToKv(headerText) : undefined,
      };
      await dashboardApi.externalMcpUpsert(name, config);
      toast.success("配置已更新");
      setEditing(null);
      await load();
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setSaving(false);
    }
  };

  const handleStart = async (name: string) => {
    try {
      await dashboardApi.externalMcpStart(name);
      toast.success("启动请求已提交");
      await load();
    } catch (e) {
      toast.error((e as Error).message);
    }
  };

  const handleStop = async (name: string) => {
    try {
      await dashboardApi.externalMcpStop(name);
      toast.success("已停止");
      await load();
    } catch (e) {
      toast.error((e as Error).message);
    }
  };

  const handleDelete = async () => {
    if (!deleting) return;
    setDeletingLoading(true);
    try {
      await dashboardApi.externalMcpDelete(deleting);
      toast.success("配置已删除");
      setDeleting(null);
      await load();
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setDeletingLoading(false);
    }
  };

  const canStart = (s: ServerEntry) => s.status === "disabled" || s.status === "disconnected" || s.status === "error";
  const canStop = (s: ServerEntry) => s.status === "connected" || s.status === "connecting";

  return (
    <div className="flex flex-1 flex-col gap-4">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h1 className="flex items-center gap-2 text-xl font-semibold tracking-tight">
            <CableIcon className="size-5" /> 外部 MCP
          </h1>
          <p className="text-muted-foreground text-sm">
            外部 MCP Server 管理：增删改查、启停与健康状态
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" onClick={() => void load()} disabled={loading}>
            <RefreshCwIcon className={cn("size-4", loading && "animate-spin")} /> 刷新
          </Button>
          <Button onClick={openCreate}>
            <PlusCircleIcon className="size-4" /> 新增
          </Button>
        </div>
      </div>

      {error ? (
        <Card className="border-amber-500/40 bg-amber-500/5">
          <CardContent className="flex items-start gap-2 py-3 text-sm">
            <AlertTriangleIcon className="mt-0.5 size-4 shrink-0 text-amber-500" />
            <div>
              <div className="font-medium">加载失败：{error}</div>
            </div>
          </CardContent>
        </Card>
      ) : null}

      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <StatChip title="已连接" value={stats.connected} accent="text-emerald-600" />
        <StatChip title="启用" value={stats.enabled} accent="text-sky-600" />
        <StatChip title="禁用" value={stats.disabled} accent="text-slate-500" />
        <StatChip title="总数" value={stats.total} accent="" />
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            <CableIcon className="size-4" /> 服务器
          </CardTitle>
          <CardDescription>外部 MCP Server 列表（status / 工具数 / 最近错误）。</CardDescription>
        </CardHeader>
        <CardContent>
          {loading && !data ? (
            <div className="text-muted-foreground py-6 text-center text-sm">加载中…</div>
          ) : servers.length === 0 ? (
            <div className="text-muted-foreground py-6 text-center text-sm">暂无外部 MCP 配置</div>
          ) : (
            <div className="flex flex-col gap-3">
              {servers.map((s) => (
                <div
                  key={s.name}
                  className="flex flex-col gap-3 rounded-lg border p-3 sm:flex-row sm:items-center sm:justify-between"
                >
                  <div className="min-w-0">
                    <div className="flex items-center gap-2">
                      <span className="font-medium">{s.name}</span>
                      <Badge variant="outline" className={cn("text-[10px]", STATUS_STYLE[s.status] ?? "")}>
                        {s.status}
                      </Badge>
                      {s.config.type ? (
                        <Badge variant="secondary" className="text-[10px]">
                          {TYPE_LABEL[s.config.type] ?? s.config.type}
                        </Badge>
                      ) : null}
                      <Badge variant="secondary" className="text-[10px]">
                        {s.tool_count} 工具
                      </Badge>
                    </div>
                    <div className="text-muted-foreground mt-1 truncate text-xs">
                      {s.config.command
                        ? `${s.config.command} ${(s.config.args ?? []).join(" ")}`
                        : s.config.url || s.config.description || "—"}
                    </div>
                    {s.config.description ? (
                      <div className="text-muted-foreground mt-0.5 truncate text-xs">{s.config.description}</div>
                    ) : null}
                    {s.error ? <div className="mt-1 truncate text-xs text-rose-500">{s.error}</div> : null}
                  </div>
                  <div className="flex shrink-0 items-center gap-2">
                    {canStart(s) ? (
                      <Button variant="outline" size="sm" onClick={() => void handleStart(s.name)}>
                        <PlayIcon className="size-3.5" /> 启动
                      </Button>
                    ) : null}
                    {canStop(s) ? (
                      <Button variant="outline" size="sm" onClick={() => void handleStop(s.name)}>
                        <SquareIcon className="size-3.5" /> 停止
                      </Button>
                    ) : null}
                    <Button variant="outline" size="sm" onClick={() => openEdit(s)}>
                      <PencilIcon className="size-3.5" /> 编辑
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      className="text-rose-500 hover:bg-rose-500/10 hover:text-rose-600"
                      onClick={() => setDeleting(s.name)}
                    >
                      <Trash2Icon className="size-3.5" /> 删除
                    </Button>
                  </div>
                </div>
              ))}
            </div>
          )}
        </CardContent>
      </Card>

      <Dialog open={editing !== null} onOpenChange={(o) => !o && setEditing(null)}>
        <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{editing?.existing ? `编辑 ${editing.name}` : "新增外部 MCP"}</DialogTitle>
            <DialogDescription>配置传输类型与连接参数</DialogDescription>
          </DialogHeader>
          <div className="grid gap-3">
            <FormInput id="name" label="名称" value={formName} onChange={setFormName} placeholder="如 sliver" />
            <div className="grid gap-1.5">
              <Label htmlFor="type">传输类型</Label>
              <select
                id="type"
                className="border-input bg-background flex h-9 w-full rounded-md border px-3 text-sm focus-visible:outline-none"
                value={form.type ?? "stdio"}
                onChange={(e) => setForm((f) => ({ ...f, type: e.target.value }))}
              >
                <option value="stdio">stdio</option>
                <option value="http">Streamable HTTP</option>
                <option value="sse">SSE</option>
              </select>
            </div>
            <FormInput
              id="command"
              label="Command（stdio）"
              value={form.command ?? ""}
              onChange={(v) => setForm((f) => ({ ...f, command: v }))}
              placeholder="/app/data/sliver-mcp"
            />
            <FormInput
              id="args"
              label="Args（空格分隔）"
              value={(form.args ?? []).join(" ")}
              onChange={(v) => setForm((f) => ({ ...f, args: v ? v.split(/\s+/) : [] }))}
              placeholder="-config xxx.cfg -output /out"
            />
            <FormInput
              id="url"
              label="URL（http / sse）"
              value={form.url ?? ""}
              onChange={(v) => setForm((f) => ({ ...f, url: v }))}
            />
            <FormInput
              id="description"
              label="描述"
              value={form.description ?? ""}
              onChange={(v) => setForm((f) => ({ ...f, description: v }))}
            />
            <div className="grid grid-cols-2 gap-3">
              <FormInput
                id="timeout"
                label="连接超时（秒）"
                type="number"
                value={form.timeout ?? 0}
                onChange={(v) => setForm((f) => ({ ...f, timeout: v ? Number(v) : undefined }))}
              />
              <div className="grid gap-1.5">
                <Label htmlFor="enable">启用</Label>
                <div className="flex h-9 items-center gap-2">
                  <input
                    id="enable"
                    type="checkbox"
                    className="size-4"
                    checked={form.external_mcp_enable ?? true}
                    onChange={(e) => setForm((f) => ({ ...f, external_mcp_enable: e.target.checked }))}
                  />
                  <span className="text-sm">external_mcp_enable</span>
                </div>
              </div>
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor="env">环境变量（每行 key=value）</Label>
              <Textarea
                id="env"
                rows={3}
                value={envText}
                onChange={(e) => setEnvText(e.target.value)}
                placeholder="TOKEN=xxx"
              />
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor="headers">Headers（每行 key=value）</Label>
              <Textarea
                id="headers"
                rows={2}
                value={headerText}
                onChange={(e) => setHeaderText(e.target.value)}
                placeholder="Authorization=Bearer xxx"
              />
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
            <AlertDialogTitle>删除外部 MCP「{deleting ?? ""}」？</AlertDialogTitle>
            <AlertDialogDescription>移除后将停止该客户端，且无法恢复，请确认。</AlertDialogDescription>
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
