"use client";

import * as React from "react";

import { Boxes, Cable, Database, Loader2Icon, Radar, SaveIcon, ShieldCheck, Smartphone } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { api } from "@/lib/api";
import { type FeatureModuleKey, useFeatureModules } from "@/lib/feature-modules";
import type { AppConfigUpdate, FeatureModulesConfig } from "@/lib/types";
import { cn } from "@/lib/utils";

interface ModuleRow {
  key: FeatureModuleKey | "c2" | "knowledge";
  title: string;
  description: string;
  icon: React.ComponentType<{ className?: string }>;
}

const MODULES: ModuleRow[] = [
  {
    key: "info_collect",
    title: "信息收集",
    description: "空间测绘与资产管理",
    icon: Radar,
  },
  {
    key: "mobile_reverse",
    title: "移动逆向",
    description: "小程序与 APK 逆向分析",
    icon: Smartphone,
  },
  {
    key: "vulnerability",
    title: "漏洞库",
    description: "漏洞管理",
    icon: ShieldCheck,
  },
  {
    key: "webshell",
    title: "WebShell 管理",
    description: "连接、命令执行与文件操作",
    icon: Cable,
  },
  {
    key: "c2",
    title: "C2 会话",
    description: "远程控制会话管理",
    icon: Database,
  },
  {
    key: "knowledge",
    title: "知识库",
    description: "知识检索与索引",
    icon: Boxes,
  },
];

export function FeatureModulesCard() {
  const { refresh, apply } = useFeatureModules();
  const [form, setForm] = React.useState<Record<string, boolean> | null>(null);
  const [saving, setSaving] = React.useState(false);

  // 从后端加载全部 6 个模块的开关状态（4 个 feature_modules + C2 + 知识库）。
  React.useEffect(() => {
    let alive = true;
    api
      .config()
      .then((cfg) => {
        if (!alive) return;
        const fm = cfg.feature_modules ?? {};
        setForm({
          info_collect: fm.info_collect !== false,
          mobile_reverse: fm.mobile_reverse !== false,
          vulnerability: fm.vulnerability !== false,
          webshell: fm.webshell !== false,
          c2: cfg.c2?.enabled ?? true,
          knowledge: cfg.knowledge?.enabled ?? true,
        });
      })
      .catch((e: Error) => toast.error(`功能模块加载失败：${e.message}`));
    return () => {
      alive = false;
    };
  }, []);

  function toggle(key: string, value: boolean) {
    setForm((f) => (f ? { ...f, [key]: value } : f));
  }

  async function save() {
    if (!form) return;
    setSaving(true);
    try {
      const featureModules: FeatureModulesConfig = {
        info_collect: form.info_collect,
        mobile_reverse: form.mobile_reverse,
        vulnerability: form.vulnerability,
        webshell: form.webshell,
      };
      const patch: AppConfigUpdate = {
        feature_modules: featureModules,
        c2: { enabled: form.c2 },
        knowledge: { enabled: form.knowledge },
      };
      await api.updateConfig(patch);
      // 同步内存开关，驱动导航即时显隐。
      apply(featureModules);
      await refresh();
      toast.success("功能模块配置已保存");
    } catch (e) {
      toast.error(`保存功能模块失败：${(e as Error).message}`);
    } finally {
      setSaving(false);
    }
  }

  const current = form ?? {};

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">功能模块</CardTitle>
        <CardDescription>按需启用或停用功能模块，保存后立即生效</CardDescription>
      </CardHeader>
      <CardContent className="grid gap-3">
        {MODULES.map(({ key, title, description, icon: Icon }) => (
          <div
            key={key}
            className={cn(
              "flex items-center justify-between gap-6 rounded-lg border p-3",
              current[key] === false && "opacity-60",
            )}
          >
            <div className="flex min-w-0 items-start gap-3">
              <Icon className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
              <div className="min-w-0">
                <Label className="text-sm">{title}</Label>
                <p className="text-muted-foreground text-xs">{description}</p>
              </div>
            </div>
            <Switch checked={current[key] ?? true} onCheckedChange={(v) => toggle(key, v)} disabled={!form} />
          </div>
        ))}
        <div className="flex justify-end gap-2 pt-1">
          <Button size="sm" onClick={() => void save()} disabled={!form || saving}>
            {saving ? <Loader2Icon className="size-4 animate-spin" /> : <SaveIcon className="size-4" />} 保存功能模块
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}
