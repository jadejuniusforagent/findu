"use client";

// 工具防护（调用拦截）管理（旧 findu UI tool-guard 域迁入控制台，U6 收尾续）。
// 后端 /api/tool-guard*（finduRuntimeBridge 已做 Bearer→cookie 提升）——
// GET config / PUT 保存（enabled+rules，allowPatterns 省略时保留现有）/ POST test 试匹配。

import * as React from "react";
import { useMemo, useState } from "react";

import {
  AlertTriangleIcon,
  CheckCircle2Icon,
  FlaskConicalIcon,
  PencilIcon,
  PlusCircleIcon,
  RefreshCwIcon,
  ShieldAlertIcon,
  Trash2Icon,
  XIcon,
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
import { dashboardApi, type ToolGuardConfig, type ToolGuardRule } from "@/lib/dashboard-api";
import { cn } from "@/lib/utils";

const EMPTY_CONFIG: ToolGuardConfig = { enabled: false, rules: [], allowPatterns: [] };

export default function ToolGuardPage() {
  const [config, setConfig] = useState<ToolGuardConfig | null>(null);
  const [dirty, setDirty] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const [editing, setEditing] = useState<{ rule: ToolGuardRule; index: number } | null>(null);
  const [form, setForm] = useState<ToolGuardRule>({ id: "", name: "", enabled: true, pattern: "", message: "" });
  const [saving, setSaving] = useState(false);

  const [deleting, setDeleting] = useState<number | null>(null);

  const [newPattern, setNewPattern] = useState("");

  const [testOpen, setTestOpen] = useState(false);
  const [testTool, setTestTool] = useState("");
  const [testArgs, setTestArgs] = useState("{}");
  const [testResult, setTestResult] = useState<string | null>(null);
  const [testBlocked, setTestBlocked] = useState(false);
  const [testLoading, setTestLoading] = useState(false);

  const load = React.useCallback(async () => {
    setLoading(true);
    try {
      const res = await dashboardApi.toolGuardGet();
      setConfig({ enabled: res.enabled, rules: res.rules ?? [], allowPatterns: res.allowPatterns ?? [] });
      setDirty(false);
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

  const rules = config?.rules ?? [];
  const allowPatterns = config?.allowPatterns ?? [];
  const enabledRules = useMemo(() => rules.filter((r) => r.enabled).length, [rules]);

  const setRules = (next: ToolGuardRule[]) => {
    setConfig((c) => (c ? { ...c, rules: next } : c));
    setDirty(true);
  };

  const openCreate = () => {
    setEditing({ rule: { id: "", name: "", enabled: true, pattern: "", message: "" }, index: -1 });
    setForm({ id: "", name: "", enabled: true, pattern: "", message: "" });
  };

  const openEdit = (index: number) => {
    setEditing({ rule: rules[index], index });
    setForm({ ...rules[index] });
  };

  const handleRuleSave = () => {
    if (!editing) return;
    const name = form.name?.trim() ?? "";
    const pattern = form.pattern?.trim() ?? "";
    if (!form.id?.trim()) {
      toast.error("规则 ID 不能为空");
      return;
    }
    if (!name) {
      toast.error("规则名称不能为空");
      return;
    }
    if (!pattern) {
      toast.error("匹配表达式（pattern）不能为空");
      return;
    }
    const next = [...rules];
    if (editing.index >= 0) {
      next[editing.index] = { ...form, name, pattern };
    } else {
      if (next.some((r) => r.id === form.id.trim())) {
        toast.error("规则 ID 已存在，请换一个");
        return;
      }
      next.push({ ...form, name, pattern });
    }
    setRules(next);
    setEditing(null);
    toast.success("规则已更新（待保存）");
  };

  const handleDelete = () => {
    if (deleting === null) return;
    const next = rules.filter((_, i) => i !== deleting);
    setRules(next);
    setDeleting(null);
    toast.success("规则已移除（待保存）");
  };

  const addPattern = () => {
    const p = newPattern.trim();
    if (!p) return;
    if (allowPatterns.includes(p)) {
      toast.error("该放行模式已存在");
      return;
    }
    setConfig((c) => (c ? { ...c, allowPatterns: [...c.allowPatterns, p] } : c));
    setDirty(true);
    setNewPattern("");
  };

  const removePattern = (idx: number) => {
    setConfig((c) => (c ? { ...c, allowPatterns: c.allowPatterns.filter((_, i) => i !== idx) } : c));
    setDirty(true);
  };

  const handleSave = async () => {
    if (!config) return;
    setSaving(true);
    try {
      const res = await dashboardApi.toolGuardUpdate({
        enabled: config.enabled,
        rules: config.rules,
        allowPatterns: config.allowPatterns,
      });
      setConfig({ enabled: res.enabled, rules: res.rules ?? [], allowPatterns: res.allowPatterns ?? [] });
      setDirty(false);
      toast.success("调用拦截配置已保存并生效");
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setSaving(false);
    }
  };

  const handleTest = async () => {
    if (!config) return;
    let args: Record<string, unknown>;
    try {
      args = JSON.parse(testArgs || "{}");
    } catch {
      toast.error("arguments 必须是合法 JSON");
      return;
    }
    if (!testTool.trim()) {
      toast.error("请填写工具名");
      return;
    }
    setTestLoading(true);
    setTestResult(null);
    try {
      const res = await dashboardApi.toolGuardTest({
        config,
        toolName: testTool.trim(),
        arguments: args,
      });
      setTestBlocked(res.blocked);
      setTestResult(
        res.blocked
          ? `已拦截（规则: ${res.match?.ruleName ?? res.match?.ruleId ?? "未知"}）：${
              res.match?.message || "命中拦截规则"
            }`
          : "未命中，放行",
      );
    } catch (e) {
      setTestResult(`调用失败: ${(e as Error).message}`);
      setTestBlocked(false);
    } finally {
      setTestLoading(false);
    }
  };

  return (
    <div className="flex flex-1 flex-col gap-4">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h1 className="flex items-center gap-2 text-xl font-semibold tracking-tight">
            <ShieldAlertIcon className="size-5" /> 工具防护
          </h1>
          <p className="text-muted-foreground text-sm">
            调用拦截规则（tool-guard）。按工具参数正则拦截危险目标，含放行豁免名单与试匹配。
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" onClick={() => void load()} disabled={loading}>
            <RefreshCwIcon className={cn("size-4", loading && "animate-spin")} /> 刷新
          </Button>
          <Button variant="outline" onClick={() => setTestOpen(true)} disabled={!config}>
            <FlaskConicalIcon className="size-4" /> 试匹配
          </Button>
          <Button onClick={() => void handleSave()} disabled={!config || !dirty || saving}>
            {saving ? "保存中…" : "保存"}
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

      {dirty ? (
        <div className="flex items-center gap-2 rounded-md border border-amber-500/40 bg-amber-500/5 px-3 py-2 text-sm text-amber-600">
          <AlertTriangleIcon className="size-4" /> 有未保存的修改，点击「保存」应用。
        </div>
      ) : null}

      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="flex items-center justify-between text-base">
            <span className="flex items-center gap-2">
              <ShieldAlertIcon className="size-4" /> 全局开关
            </span>
            <button
              type="button"
              onClick={() => {
                setConfig((c) => (c ? { ...c, enabled: !c.enabled } : c));
                setDirty(true);
              }}
              className={cn(
                "rounded-full border px-3 py-1 text-xs transition-colors",
                config?.enabled
                  ? "border-emerald-500/40 text-emerald-600 hover:bg-emerald-500/10"
                  : "border-slate-400/40 text-slate-500 hover:bg-slate-400/10",
              )}
            >
              {config?.enabled ? "已启用" : "已禁用"}
            </button>
          </CardTitle>
          <CardDescription>开启后所有工具调用按下方规则预检，命中即拦截。</CardDescription>
        </CardHeader>
      </Card>

      <div className="grid gap-3 sm:grid-cols-3">
        <StatChip title="规则" value={rules.length} accent="" />
        <StatChip title="启用规则" value={enabledRules} accent="text-emerald-600" />
        <StatChip title="放行模式" value={allowPatterns.length} accent="text-sky-600" />
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            拦截规则
            <Badge variant="secondary" className="text-[10px]">
              {rules.length}
            </Badge>
          </CardTitle>
          <CardDescription>按工具参数正则匹配，命中即拦截（规则可启停 / 编辑 / 删除）。</CardDescription>
        </CardHeader>
        <CardContent>
          {loading && !config ? (
            <div className="text-muted-foreground py-6 text-center text-sm">加载中…</div>
          ) : rules.length === 0 ? (
            <div className="text-muted-foreground py-6 text-center text-sm">暂无拦截规则</div>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b text-muted-foreground text-xs">
                    <th className="px-3 py-2 text-left font-medium">名称</th>
                    <th className="px-3 py-2 text-left font-medium">匹配表达式</th>
                    <th className="px-3 py-2 text-left font-medium">状态</th>
                    <th className="px-3 py-2 text-right font-medium">操作</th>
                  </tr>
                </thead>
                <tbody>
                  {rules.map((r, i) => (
                    <tr key={r.id ?? i} className="border-b last:border-0">
                      <td className="px-3 py-2">
                        <div className="font-medium">{r.name}</div>
                        <div className="text-muted-foreground text-xs">{r.id}</div>
                        {r.message ? (
                          <div className="text-muted-foreground mt-0.5 max-w-[320px] truncate text-xs">{r.message}</div>
                        ) : null}
                      </td>
                      <td className="max-w-[320px] px-3 py-2">
                        <code className="text-muted-foreground block truncate font-mono text-xs">{r.pattern}</code>
                      </td>
                      <td className="px-3 py-2">
                        <button
                          type="button"
                          onClick={() => {
                            const next = [...rules];
                            next[i] = { ...next[i], enabled: !next[i].enabled };
                            setRules(next);
                          }}
                          className={cn(
                            "rounded-full border px-2 py-0.5 text-[10px] transition-colors",
                            r.enabled
                              ? "border-emerald-500/40 text-emerald-600 hover:bg-emerald-500/10"
                              : "border-slate-400/40 text-slate-500 hover:bg-slate-400/10",
                          )}
                        >
                          {r.enabled ? "启用" : "禁用"}
                        </button>
                      </td>
                      <td className="px-3 py-2">
                        <div className="flex justify-end gap-1">
                          <Button variant="ghost" size="sm" onClick={() => openEdit(i)}>
                            <PencilIcon className="size-3.5" /> 编辑
                          </Button>
                          <Button
                            variant="ghost"
                            size="sm"
                            className="text-rose-500 hover:bg-rose-500/10 hover:text-rose-600"
                            onClick={() => setDeleting(i)}
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
          <div className="mt-3">
            <Button variant="outline" size="sm" onClick={openCreate}>
              <PlusCircleIcon className="size-3.5" /> 新增规则
            </Button>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            放行豁免模式
            <Badge variant="secondary" className="text-[10px]">
              {allowPatterns.length}
            </Badge>
          </CardTitle>
          <CardDescription>命中这些正则的调用不受拦截规则影响（漏洞库 / 知识站点等）。</CardDescription>
        </CardHeader>
        <CardContent>
          <div className="flex flex-col gap-2">
            <div className="flex gap-2">
              <Input
                value={newPattern}
                onChange={(e) => setNewPattern(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") {
                    e.preventDefault();
                    addPattern();
                  }
                }}
                placeholder="如 (?i)\bcve\.org\b"
                className="font-mono text-xs"
              />
              <Button variant="outline" onClick={addPattern}>
                <PlusCircleIcon className="size-3.5" /> 添加
              </Button>
            </div>
            {allowPatterns.length === 0 ? (
              <div className="text-muted-foreground py-4 text-center text-sm">暂无放行模式</div>
            ) : (
              <div className="flex flex-wrap gap-2">
                {allowPatterns.map((p, i) => (
                  <span
                    key={`${p}-${i}`}
                    className="group flex items-center gap-1 rounded-md border bg-muted/40 px-2 py-1 font-mono text-xs"
                  >
                    <span className="max-w-[280px] truncate">{p}</span>
                    <button
                      type="button"
                      onClick={() => removePattern(i)}
                      className="text-muted-foreground hover:text-rose-500"
                    >
                      <XIcon className="size-3" />
                    </button>
                  </span>
                ))}
              </div>
            )}
          </div>
        </CardContent>
      </Card>

      <Dialog open={editing !== null} onOpenChange={(o) => !o && setEditing(null)}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{editing && editing.index >= 0 ? "编辑拦截规则" : "新增拦截规则"}</DialogTitle>
            <DialogDescription>按工具参数正则匹配，命中即拦截该次调用。</DialogDescription>
          </DialogHeader>
          <div className="grid gap-3">
            <FormInput
              id="ruleId"
              label="规则 ID"
              value={form.id}
              onChange={(v) => setForm((f) => ({ ...f, id: v }))}
            />
            <FormInput
              id="ruleName"
              label="规则名称"
              value={form.name}
              onChange={(v) => setForm((f) => ({ ...f, name: v }))}
            />
            <div className="grid gap-1.5">
              <Label htmlFor="rulePattern">匹配表达式（正则）</Label>
              <Textarea
                id="rulePattern"
                rows={3}
                className="font-mono text-xs"
                value={form.pattern}
                onChange={(e) => setForm((f) => ({ ...f, pattern: e.target.value }))}
                placeholder="(?i)gov\.(cn|com)"
              />
            </div>
            <FormInput
              id="ruleMessage"
              label="拦截提示"
              value={form.message}
              onChange={(v) => setForm((f) => ({ ...f, message: v }))}
            />
            <div className="grid gap-1.5">
              <Label htmlFor="ruleEnabled">启用</Label>
              <div className="flex h-9 items-center gap-2">
                <input
                  id="ruleEnabled"
                  type="checkbox"
                  className="size-4"
                  checked={form.enabled}
                  onChange={(e) => setForm((f) => ({ ...f, enabled: e.target.checked }))}
                />
                <span className="text-sm">保存后立即启用</span>
              </div>
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setEditing(null)}>
              取消
            </Button>
            <Button onClick={handleRuleSave}>确定</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <AlertDialog open={deleting !== null} onOpenChange={(o) => !o && setDeleting(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>删除拦截规则「{deleting !== null ? rules[deleting]?.name : ""}」？</AlertDialogTitle>
            <AlertDialogDescription>移除该规则后需保存生效。不可恢复。</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction
              onClick={(e) => {
                e.preventDefault();
                handleDelete();
              }}
              className="bg-rose-600 text-white hover:bg-rose-700"
            >
              删除
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <Dialog open={testOpen} onOpenChange={(o) => !o && setTestOpen(false)}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>试匹配</DialogTitle>
            <DialogDescription>用当前生效的规则对一次工具调用做干跑校验，不实际调用。</DialogDescription>
          </DialogHeader>
          <div className="grid gap-3">
            <FormInput
              id="testTool"
              label="工具名"
              value={testTool}
              onChange={setTestTool}
              placeholder="如 http_request / fetch_url"
            />
            <div className="grid gap-1.5">
              <Label htmlFor="testArgs">参数（JSON）</Label>
              <Textarea
                id="testArgs"
                rows={5}
                className="font-mono text-xs"
                value={testArgs}
                onChange={(e) => setTestArgs(e.target.value)}
                placeholder='{"url":"https://example.gov.cn/"}'
              />
            </div>
            {testResult ? (
              <div
                className={cn(
                  "flex items-start gap-1.5 rounded-md border px-3 py-2 text-xs",
                  testBlocked ? "border-rose-500/40 text-rose-600" : "border-emerald-500/40 text-emerald-600",
                )}
              >
                {testBlocked ? (
                  <AlertTriangleIcon className="mt-0.5 size-3.5 shrink-0" />
                ) : (
                  <CheckCircle2Icon className="mt-0.5 size-3.5 shrink-0" />
                )}
                <span className="whitespace-pre-wrap">{testResult}</span>
              </div>
            ) : null}
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setTestOpen(false)}>
              关闭
            </Button>
            <Button onClick={() => void handleTest()} disabled={testLoading}>
              {testLoading ? "校验中…" : "试匹配"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

function FormInput({
  id,
  label,
  value,
  onChange,
  placeholder,
}: {
  id: string;
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
}) {
  return (
    <div className="grid gap-1.5">
      <Label htmlFor={id}>{label}</Label>
      <Input id={id} value={value} placeholder={placeholder} onChange={(e) => onChange(e.target.value)} />
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
