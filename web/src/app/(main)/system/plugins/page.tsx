"use client";

// 插件面板：宿主 pluginhost 注册表只读快照 + 能力提供者绑定。
// 后端 GET /api/plugins（server/plugins_http.go）。当前仅 intel 插件注册；
// 能力提供者（C2/Traffic/WebShell）绑定层为扩展点，无实现时 providers 为空。

import { useCallback, useEffect, useState } from "react";

import { AlertTriangleIcon, PackageIcon, PlugIcon, RefreshCwIcon, WaypointsIcon } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Empty, EmptyDescription, EmptyTitle } from "@/components/ui/empty";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { dashboardApi, type PluginStatus } from "@/lib/dashboard-api";
import { cn } from "@/lib/utils";

const STATE_STYLES: Record<string, string> = {
  started: "bg-emerald-500/15 text-emerald-600 border-emerald-500/30",
  registered: "bg-slate-500/15 text-slate-600 border-slate-500/30",
  starting: "bg-amber-500/15 text-amber-600 border-amber-500/30",
  stopped: "bg-slate-400/15 text-slate-500 border-slate-400/30",
  failed: "bg-red-500/15 text-red-600 border-red-500/30",
};

const STATE_LABELS: Record<string, string> = {
  started: "运行中",
  registered: "已注册",
  starting: "启动中",
  stopped: "已停止",
  failed: "失败",
};

const KIND_LABELS: Record<string, string> = {
  system: "系统",
  data: "数据",
  capability: "能力",
};

export default function PluginsPage() {
  const [plugins, setPlugins] = useState<PluginStatus[] | null>(null);
  const [providers, setProviders] = useState<Record<string, string> | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const data = await dashboardApi.plugins();
      setPlugins(data.plugins ?? []);
      setProviders(data.providers ?? {});
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

  const providerEntries = Object.entries(providers ?? {});

  return (
    <div className="flex flex-1 flex-col gap-4">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h1 className="flex items-center gap-2 font-semibold text-xl tracking-tight">
            <PlugIcon className="size-5" /> 插件
          </h1>
          <p className="text-muted-foreground text-sm">pluginhost 注册表与能力提供者绑定（只读）。</p>
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

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            <PackageIcon className="size-4" /> 已注册插件
            <span className="text-muted-foreground text-xs">· {plugins?.length ?? 0}</span>
          </CardTitle>
          <CardDescription>装配阶段注册、启动，路由静态挂载（插件停止后对应 API 返回 503）。</CardDescription>
        </CardHeader>
        <CardContent>
          {!plugins || plugins.length === 0 ? (
            <Empty>
              <EmptyTitle>暂无插件</EmptyTitle>
              <EmptyDescription>插件注册表为空，等待装配层注册实现。</EmptyDescription>
            </Empty>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>ID</TableHead>
                  <TableHead>类型</TableHead>
                  <TableHead>版本</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead>错误</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {plugins.map((p) => (
                  <TableRow key={p.id}>
                    <TableCell className="font-mono text-sm">{p.id}</TableCell>
                    <TableCell>{KIND_LABELS[p.kind] ?? p.kind}</TableCell>
                    <TableCell className="font-mono text-muted-foreground text-xs">{p.version}</TableCell>
                    <TableCell>
                      <Badge variant="outline" className={STATE_STYLES[p.state]}>
                        {STATE_LABELS[p.state] ?? p.state}
                      </Badge>
                    </TableCell>
                    <TableCell className="max-w-[240px] truncate text-muted-foreground text-xs" title={p.error}>
                      {p.error || "—"}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            <WaypointsIcon className="size-4" /> 能力提供者绑定
            <span className="text-muted-foreground text-xs">· {providerEntries.length}</span>
          </CardTitle>
          <CardDescription>能力（C2 / 流量 / WebShell 等）到插件的绑定。当前为扩展点，无实现时为空。</CardDescription>
        </CardHeader>
        <CardContent>
          {providerEntries.length === 0 ? (
            <div className="flex items-center gap-2 py-4 text-muted-foreground text-sm">
              <WaypointsIcon className="size-4 text-slate-400" /> 暂无能力提供者绑定
            </div>
          ) : (
            <div className="flex flex-wrap gap-2">
              {providerEntries.map(([cap, pluginId]) => (
                <Badge key={cap} variant="secondary">
                  {cap}
                  <span className="text-muted-foreground"> → {pluginId}</span>
                </Badge>
              ))}
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
