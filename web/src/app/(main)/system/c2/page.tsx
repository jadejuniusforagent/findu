"use client";

// C2 会话面板（旧 findu UI c2 域迁入控制台，U6 收尾续·auth 修复后）。
// 后端 GET /api/c2/listeners、/api/c2/sessions（findu 外部接入槽位，auth 修复后
// Bearer 直连 200，当前数据为 null——需 sliver 等外部 C2 接入后才有内容）。

import { useCallback, useEffect, useState } from "react";

import { AlertTriangleIcon, RadioIcon, RefreshCwIcon, SatelliteIcon, ServerIcon } from "lucide-react";

import { CodeBlock } from "@/components/code-block";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { dashboardApi } from "@/lib/dashboard-api";
import { cn } from "@/lib/utils";

export default function C2PanelPage() {
  const [listeners, setListeners] = useState<unknown[] | null>(null);
  const [sessions, setSessions] = useState<unknown[] | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [l, s] = await Promise.all([dashboardApi.c2Listeners(), dashboardApi.c2Sessions()]);
      setListeners(l.listeners ?? []);
      setSessions(s.sessions ?? []);
      setError(null);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    const t = setInterval(() => void load(), 30000);
    return () => clearInterval(t);
  }, [load]);

  const lCount = Array.isArray(listeners) ? listeners.length : 0;
  const sCount = Array.isArray(sessions) ? sessions.length : 0;

  return (
    <div className="flex flex-1 flex-col gap-4">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h1 className="flex items-center gap-2 font-semibold text-xl tracking-tight">
            <RadioIcon className="size-5" /> C2 会话
          </h1>
          <p className="text-muted-foreground text-sm">
            C2 外部接入槽位（listeners / sessions）。需外部 C2 框架接入后才有数据。
          </p>
        </div>
        <Button variant="outline" onClick={() => void load()} disabled={loading}>
          <RefreshCwIcon className={cn("size-4", loading && "animate-spin")} /> 刷新
        </Button>
      </div>

      {error ? (
        <Card className="border-amber-500/40 bg-amber-500/5">
          <CardContent className="flex items-start gap-2 py-3 text-sm">
            <AlertTriangleIcon className="mt-0.5 size-4 shrink-0 text-amber-500" />
            <div className="font-medium">加载失败：{error}</div>
          </CardContent>
        </Card>
      ) : null}

      <div className="grid gap-3 sm:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <SatelliteIcon className="size-4" /> Listeners
              <span className="text-muted-foreground text-xs">· {lCount}</span>
            </CardTitle>
            <CardDescription>已注册的 C2 监听器。</CardDescription>
          </CardHeader>
          <CardContent>
            {lCount === 0 ? (
              <div className="flex items-center gap-2 py-6 text-muted-foreground text-sm">
                <ServerIcon className="size-4 text-slate-400" /> 暂无监听器（C2 外部接入后显示）
              </div>
            ) : (
              <CodeBlock code={JSON.stringify(listeners, null, 2)} maxHeight="max-h-[320px]" />
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <RadioIcon className="size-4" /> Sessions
              <span className="text-muted-foreground text-xs">· {sCount}</span>
            </CardTitle>
            <CardDescription>已建立的控制会话。</CardDescription>
          </CardHeader>
          <CardContent>
            {sCount === 0 ? (
              <div className="flex items-center gap-2 py-6 text-muted-foreground text-sm">
                <ServerIcon className="size-4 text-slate-400" /> 暂无会话（C2 外部接入后显示）
              </div>
            ) : (
              <CodeBlock code={JSON.stringify(sessions, null, 2)} maxHeight="max-h-[320px]" />
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
