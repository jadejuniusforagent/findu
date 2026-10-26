"use client";

import * as React from "react";

import { Loader2Icon, RefreshCwIcon, SaveIcon, SettingsIcon } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { api } from "@/lib/api";
import type { Settings } from "@/lib/types";
import { cn } from "@/lib/utils";

import { FeatureModulesCard } from "./_components/feature-modules-card";
import { VersionUpdateCard } from "./_components/version-update-card";

interface SettingsForm {
  traffic_capture: boolean;
  agent_traffic_binding: boolean;
  llm_record: boolean;
  web_search_enabled: boolean;
  web_search_backend: string;
  web_search_proxy: string;
  global_proxy: string;
  python_interpreter: string;
  workers: number;
  task_concurrency_enabled: boolean;
  task_concurrency_limit: number;
  llm_pool_enabled: boolean;
  llm_pool_bind_fallback: boolean;
  constraints_inject_planner: boolean;
  constraints_inject_worker: boolean;
  noa_compaction: boolean;
  notify_enabled: boolean;
  notify_public_base_url: string;
  notify_digest_interval_min: number;
}

const BACKEND_OPTIONS = [
  { value: "ddgs", label: "DuckDuckGo（ddgs）" },
  { value: "brave-free", label: "Brave Free" },
  { value: "tavily", label: "Tavily" },
  { value: "deepseek", label: "DeepSeek" },
];

function fromSettings(s: Settings): SettingsForm {
  return {
    traffic_capture: Boolean(s.traffic_capture),
    agent_traffic_binding: Boolean(s.agent_traffic_binding),
    llm_record: Boolean(s.llm_record),
    web_search_enabled: Boolean(s.web_search_enabled),
    web_search_backend: s.web_search_backend || "ddgs",
    web_search_proxy: s.web_search_proxy ?? "",
    global_proxy: s.global_proxy ?? "",
    python_interpreter: s.python_interpreter ?? "",
    workers: s.workers ?? 3,
    task_concurrency_enabled: Boolean(s.task_concurrency_enabled),
    task_concurrency_limit: s.task_concurrency_limit ?? 5,
    llm_pool_enabled: Boolean(s.llm_pool_enabled),
    llm_pool_bind_fallback: Boolean(s.llm_pool_bind_fallback),
    constraints_inject_planner: s.constraints_inject_planner ?? true,
    constraints_inject_worker: s.constraints_inject_worker ?? true,
    noa_compaction: Boolean(s.noa_compaction),
    notify_enabled: s.notify_enabled ?? true,
    notify_public_base_url: s.notify_public_base_url ?? "",
    notify_digest_interval_min: s.notify_digest_interval_min ?? 30,
  };
}

function Section({ title, description, children }: { title: string; description?: string; children: React.ReactNode }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">{title}</CardTitle>
        {description ? <CardDescription>{description}</CardDescription> : null}
      </CardHeader>
      <CardContent className="grid gap-4">{children}</CardContent>
    </Card>
  );
}

function Row({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <div className="flex items-start justify-between gap-6">
      <div className="min-w-0">
        <Label className="text-sm">{label}</Label>
        {hint ? <p className="text-muted-foreground mt-0.5 text-xs">{hint}</p> : null}
      </div>
      <div className="shrink-0">{children}</div>
    </div>
  );
}

export default function SettingsPage() {
  const [form, setForm] = React.useState<SettingsForm | null>(null);
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState("");
  const [saving, setSaving] = React.useState(false);

  // biome-ignore lint/correctness/useExhaustiveDependencies: 初始化只跑一次。
  React.useEffect(() => {
    let alive = true;
    setLoading(true);
    setError("");
    api
      .settings()
      .then((s) => {
        if (alive) setForm(fromSettings(s));
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
  }, []);

  function reload() {
    setLoading(true);
    setError("");
    api
      .settings()
      .then((s) => setForm(fromSettings(s)))
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false));
  }

  function set<K extends keyof SettingsForm>(key: K, value: SettingsForm[K]) {
    setForm((f) => (f ? { ...f, [key]: value } : f));
  }

  async function save() {
    if (!form) return;
    setSaving(true);
    try {
      const patch: Partial<Settings> = {
        traffic_capture: form.traffic_capture,
        agent_traffic_binding: form.agent_traffic_binding,
        llm_record: form.llm_record,
        web_search_enabled: form.web_search_enabled,
        web_search_backend: form.web_search_backend,
        web_search_proxy: form.web_search_proxy,
        global_proxy: form.global_proxy,
        python_interpreter: form.python_interpreter,
        workers: form.workers,
        task_concurrency_enabled: form.task_concurrency_enabled,
        task_concurrency_limit: form.task_concurrency_limit,
        llm_pool_enabled: form.llm_pool_enabled,
        llm_pool_bind_fallback: form.llm_pool_bind_fallback,
        constraints_inject_planner: form.constraints_inject_planner,
        constraints_inject_worker: form.constraints_inject_worker,
        noa_compaction: form.noa_compaction,
        notify_enabled: form.notify_enabled,
        notify_public_base_url: form.notify_public_base_url,
        notify_digest_interval_min: form.notify_digest_interval_min,
      };
      const savedSettings = await api.setSettings(patch);
      setForm(fromSettings(savedSettings));
      toast.success("设置已保存");
    } catch (e) {
      toast.error(`保存设置失败：${(e as Error).message}`);
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="flex flex-1 flex-col gap-4 md:gap-6">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h1 className="flex items-center gap-2 text-xl font-semibold tracking-tight">
            <SettingsIcon className="size-5" /> 系统设置
          </h1>
          <p className="text-muted-foreground text-sm">
            运行时开关与出口配置
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" onClick={reload} disabled={loading}>
            <RefreshCwIcon className={cn("size-4", loading && "animate-spin")} /> 刷新
          </Button>
          <Button onClick={() => void save()} disabled={!form || saving}>
            {saving ? <Loader2Icon className="size-4 animate-spin" /> : <SaveIcon className="size-4" />} 保存
          </Button>
        </div>
      </div>

      {error ? (
        <Card className="border-destructive/40 bg-destructive/5">
          <CardContent className="text-destructive py-3 text-sm">设置加载失败：{error}</CardContent>
        </Card>
      ) : null}

      {!form && loading ? (
        <div className="grid gap-4">
          {Array.from({ length: 3 }).map((_, i) => (
            <Card key={i}>
              <CardContent className="py-4">
                <Skeleton className="h-4 w-40" />
                <Skeleton className="mt-3 h-8 w-full" />
              </CardContent>
            </Card>
          ))}
        </div>
      ) : form ? (
        <>
          <Section title="运行开关" description="即时生效；切换流量捕获会重建 agent 使代理/流量工具/提示词生效">
            <Row label="流量捕获" hint="记录目标流量（MITM）；开启后代理进入上游">
              <Switch checked={form.traffic_capture} onCheckedChange={(v) => set("traffic_capture", v)} />
            </Row>
            <Row label="Agent 自动绑定流量证据" hint="任务自动绑定捕获流量；默认关，不影响人工绑定">
              <Switch checked={form.agent_traffic_binding} onCheckedChange={(v) => set("agent_traffic_binding", v)} />
            </Row>
            <Row label="LLM 录制" hint="记录所有 LLM 调用，用于模型录制页；默认关">
              <Switch checked={form.llm_record} onCheckedChange={(v) => set("llm_record", v)} />
            </Row>
          </Section>

          <FeatureModulesCard />

          <Section title="联网搜索" description="搜索后端与独立出口代理（访问搜索端点用）">
            <Row label="启用联网搜索">
              <Switch checked={form.web_search_enabled} onCheckedChange={(v) => set("web_search_enabled", v)} />
            </Row>
            <div className="grid gap-2">
              <Label>搜索后端</Label>
              <Select value={form.web_search_backend} onValueChange={(v) => set("web_search_backend", v)}>
                <SelectTrigger className="max-w-xs">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {BACKEND_OPTIONS.map((b) => (
                    <SelectItem key={b.value} value={b.value}>
                      {b.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="grid gap-2">
              <Label htmlFor="ws-proxy">搜索出口代理（http/https/socks5，空=直连）</Label>
              <Input
                id="ws-proxy"
                value={form.web_search_proxy}
                onChange={(e) => set("web_search_proxy", e.target.value)}
                placeholder="如 http://127.0.0.1:7897"
              />
            </div>
          </Section>

          <Section title="出口与执行">
            <div className="grid gap-2">
              <Label htmlFor="g-proxy">全局出口代理（所有目标流量，空=直连）</Label>
              <Input
                id="g-proxy"
                value={form.global_proxy}
                onChange={(e) => set("global_proxy", e.target.value)}
                placeholder="如 http://127.0.0.1:7897"
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="py-interp">Python 解释器路径（空=运行时检测）</Label>
              <Input
                id="py-interp"
                value={form.python_interpreter}
                onChange={(e) => set("python_interpreter", e.target.value)}
                placeholder="如 /usr/bin/python3"
              />
            </div>
          </Section>

          <Section title="任务与并发" description="对之后启动的任务生效">
            <Row label="并发工作 agent 数">
              <Input
                type="number"
                min={1}
                className="w-24"
                value={String(form.workers)}
                onChange={(e) => set("workers", Math.max(1, Number(e.target.value) || 1))}
              />
            </Row>
            <Row label="任务并发上限" hint="开启后同时运行任务数受限，超限排队">
              <div className="flex items-center gap-2">
                <Switch
                  checked={form.task_concurrency_enabled}
                  onCheckedChange={(v) => set("task_concurrency_enabled", v)}
                />
                <Input
                  type="number"
                  min={1}
                  className="w-20"
                  disabled={!form.task_concurrency_enabled}
                  value={String(form.task_concurrency_limit)}
                  onChange={(e) => set("task_concurrency_limit", Math.max(1, Number(e.target.value) || 1))}
                />
              </div>
            </Row>
            <Row label="约束注入 Planner / Worker" hint="把任务 allow/deny 约束拼进 agent 系统提示">
              <div className="flex items-center gap-3">
                <Switch
                  checked={form.constraints_inject_planner}
                  onCheckedChange={(v) => set("constraints_inject_planner", v)}
                />
                <Switch
                  checked={form.constraints_inject_worker}
                  onCheckedChange={(v) => set("constraints_inject_worker", v)}
                />
              </div>
            </Row>
          </Section>

          <Section title="LLM 高可用" description="模型故障转移（需重建 provider 链生效）">
            <Row label="LLM 轮询（故障转移）" hint="未指定模型的 agent 在当前配置不可用时自动切下一个">
              <Switch checked={form.llm_pool_enabled} onCheckedChange={(v) => set("llm_pool_enabled", v)} />
            </Row>
            <Row label="绑定失败也回落轮询链" hint="绑定了指定配置的 agent 失败时也回落到轮询链">
              <Switch checked={form.llm_pool_bind_fallback} onCheckedChange={(v) => set("llm_pool_bind_fallback", v)} />
            </Row>
            <Row label="noa 上下文压缩（实验）" hint="由 noa 接管上下文压缩，取代内置 compaction">
              <Switch checked={form.noa_compaction} onCheckedChange={(v) => set("noa_compaction", v)} />
            </Row>
          </Section>

          <Section title="漏洞 IM 推送（全局）" description="渠道本身是独立资源，见「通知推送」页">
            <Row label="推送总开关" hint="维护期一键止血用">
              <Switch checked={form.notify_enabled} onCheckedChange={(v) => set("notify_enabled", v)} />
            </Row>
            <div className="grid gap-2">
              <Label htmlFor="notify-base">漏洞详情回链地址（空=消息不带回链）</Label>
              <Input
                id="notify-base"
                value={form.notify_public_base_url}
                onChange={(e) => set("notify_public_base_url", e.target.value)}
                placeholder="如 https://console.example.com"
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="notify-min">汇总模式周期（分钟）</Label>
              <Input
                id="notify-min"
                type="number"
                min={1}
                className="w-32"
                value={String(form.notify_digest_interval_min)}
                onChange={(e) => set("notify_digest_interval_min", Math.max(1, Number(e.target.value) || 1))}
              />
            </div>
          </Section>

          <VersionUpdateCard />
        </>
      ) : null}
    </div>
  );
}
