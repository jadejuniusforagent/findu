"use client";

import * as React from "react";

import { KeyRoundIcon, Loader2Icon, SaveIcon, Settings2Icon } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { api } from "@/lib/api";
import type { SpaceSearchEngineConfig } from "@/lib/types";
import { cn } from "@/lib/utils";

type EngineKey = "fofa" | "zoomeye" | "quake" | "shodan" | "hunter";

const ENGINES: { key: EngineKey; label: string; tagline: string; hint: string }[] = [
  { key: "fofa", label: "FOFA", tagline: "网络空间测绘", hint: "建议使用只读权限的 Key" },
  { key: "zoomeye", label: "ZoomEye", tagline: "网络资产检索", hint: "" },
  { key: "quake", label: "Quake", tagline: "空间测绘", hint: "" },
  { key: "shodan", label: "Shodan", tagline: "网络设备搜索", hint: "" },
  { key: "hunter", label: "Hunter", tagline: "资产测绘", hint: "" },
];

const EMPTY: SpaceSearchEngineConfig = { api_key: "", base_url: "" };

export function EngineConfigPanel() {
  const [open, setOpen] = React.useState(false);
  const [form, setForm] = React.useState<Record<EngineKey, SpaceSearchEngineConfig> | null>(null);
  const [loading, setLoading] = React.useState(false);
  const [saving, setSaving] = React.useState(false);

  async function load() {
    setLoading(true);
    try {
      const cfg = await api.config();
      setForm({
        fofa: cfg.fofa ?? EMPTY,
        zoomeye: cfg.zoomeye ?? EMPTY,
        quake: cfg.quake ?? EMPTY,
        shodan: cfg.shodan ?? EMPTY,
        hunter: cfg.hunter ?? EMPTY,
      });
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }

  function toggle() {
    setOpen((o) => {
      const next = !o;
      if (next && !form) void load();
      return next;
    });
  }

  function setEngine(key: EngineKey, field: "api_key" | "base_url", value: string) {
    setForm((f) => (f ? { ...f, [key]: { ...f[key], [field]: value } } : f));
  }

  async function save() {
    if (!form) return;
    setSaving(true);
    try {
      const patch: Record<string, SpaceSearchEngineConfig> = {};
      for (const { key } of ENGINES) {
        const v = form[key];
        if (v.api_key || v.base_url) patch[key] = v;
      }
      await api.updateConfig(patch);
      toast.success("引擎配置已保存");
      setOpen(false);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  }

  return (
    <Card className="border-dashed">
      <CardHeader className="cursor-pointer select-none py-3" onClick={toggle}>
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <KeyRoundIcon className="size-4 text-muted-foreground" />
            <CardTitle className="text-sm">空间测绘引擎配置</CardTitle>
            <CardDescription className="text-xs">配置各引擎的访问密钥与接口地址</CardDescription>
          </div>
          <Settings2Icon className={cn("size-4 text-muted-foreground transition-transform", open && "rotate-90")} />
        </div>
      </CardHeader>
      {open ? (
        <CardContent className="grid gap-4 pt-0">
          {loading ? (
            <div className="flex items-center gap-2 py-4 text-muted-foreground text-sm">
              <Loader2Icon className="size-4 animate-spin" /> 加载中…
            </div>
          ) : null}
          {!loading && form ? (
            <>
              <div className="grid gap-3">
                {ENGINES.map(({ key, label, tagline, hint }) => (
                  <div
                    key={key}
                    className="grid gap-2 rounded-lg border p-3 sm:grid-cols-[180px_minmax(0,1fr)_minmax(0,1fr)] sm:items-end"
                  >
                    <div className="sm:pb-2">
                      <p className="font-medium text-sm">{label}</p>
                      <p className="text-muted-foreground text-xs">{tagline}</p>
                      {hint ? <p className="text-[11px] text-muted-foreground/70">{hint}</p> : null}
                    </div>
                    <div className="flex flex-col gap-1.5">
                      <Label className="text-[11px] text-muted-foreground">API Key</Label>
                      <Input
                        type="password"
                        className="h-8 font-mono text-xs"
                        placeholder="留空表示不修改"
                        value={form[key].api_key ?? ""}
                        onChange={(e) => setEngine(key, "api_key", e.target.value)}
                      />
                    </div>
                    <div className="flex flex-col gap-1.5">
                      <Label className="text-[11px] text-muted-foreground">Base URL</Label>
                      <Input
                        className="h-8 font-mono text-xs"
                        placeholder="可选，默认使用官方 API"
                        value={form[key].base_url ?? ""}
                        onChange={(e) => setEngine(key, "base_url", e.target.value)}
                      />
                    </div>
                  </div>
                ))}
              </div>
              <div className="flex items-center justify-end gap-2">
                <Button variant="ghost" size="sm" onClick={() => setOpen(false)} disabled={saving}>
                  取消
                </Button>
                <Button size="sm" onClick={() => void save()} disabled={saving}>
                  {saving ? <Loader2Icon className="size-4 animate-spin" /> : <SaveIcon className="size-4" />} 保存配置
                </Button>
              </div>
            </>
          ) : null}
        </CardContent>
      ) : null}
    </Card>
  );
}
