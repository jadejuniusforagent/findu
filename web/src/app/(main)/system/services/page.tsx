"use client";

// 系统服务管理（旧 findu UI system-services 域迁入控制台，U6 收尾续·auth 修复后）。
// 后端 GET /api/system-services（走 findu 兜底，auth 修复后宿主 Bearer 直连 200）——
// 返回 { enabled, modules:[{id,name,description,services:[...]}], running/stopped/total }。
// 服务支持启动/停止/重启/自启（POST /api/system-services/{id}/start|stop|restart|autostart，
// 需 config:write 权限）。服务与 findu 原 system-services 一致。

import { useCallback, useEffect, useState } from "react";

import {
  AlertTriangleIcon,
  BoxesIcon,
  CirclePlayIcon,
  PowerIcon,
  RefreshCwIcon,
  RotateCcwIcon,
  ServerIcon,
  Settings2Icon,
} from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { dashboardApi, type SystemServicesOverview } from "@/lib/dashboard-api";
import { cn } from "@/lib/utils";

export default function SystemServicesPage() {
  const [data, setData] = useState<SystemServicesOverview | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [acting, setActing] = useState<string | null>(null);
  const [actionMsg, setActionMsg] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setData(await dashboardApi.systemServices());
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

  const act = async (id: string, action: "start" | "stop" | "restart" | "autostart") => {
    setActing(`${id}:${action}`);
    setActionMsg(null);
    try {
      const res = await dashboardApi.systemServiceAction(id, action);
      setActionMsg(`${id} ${action}: ${res.message ?? "ok"}`);
      await load();
    } catch (e) {
      setActionMsg(`操作失败：${(e as Error).message}`);
    } finally {
      setActing(null);
    }
  };

  const modules = data?.modules ?? [];

  return (
    <div className="flex flex-1 flex-col gap-4">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h1 className="flex items-center gap-2 font-semibold text-xl tracking-tight">
            <ServerIcon className="size-5" /> 系统服务
          </h1>
          <p className="text-muted-foreground text-sm">系统服务与模块的运行状态</p>
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

      {actionMsg ? (
        <Card className="border-sky-500/30 bg-sky-500/5">
          <CardContent className="py-2 text-sm">{actionMsg}</CardContent>
        </Card>
      ) : null}

      <div className="grid gap-3 sm:grid-cols-4">
        <StatChip title="模块" value={modules.length} accent="text-violet-600" />
        <StatChip title="服务总数" value={data?.total ?? 0} accent="" />
        <StatChip title="运行中" value={data?.running ?? 0} accent="text-emerald-600" />
        <StatChip title="已停止" value={data?.stopped ?? 0} accent="text-rose-600" />
      </div>

      {loading && !data ? (
        <Card>
          <CardContent className="py-8 text-center text-muted-foreground text-sm">加载中…</CardContent>
        </Card>
      ) : null}
      {!loading && data && modules.length === 0 ? (
        <Card>
          <CardContent className="py-8 text-center text-muted-foreground text-sm">暂无模块</CardContent>
        </Card>
      ) : null}
      {!loading && data && modules.length > 0
        ? modules.map((m) => (
            <Card key={m.id}>
              <CardHeader>
                <CardTitle className="flex items-center gap-2 text-base">
                  <BoxesIcon className="size-4" /> {m.name}
                  <span className="font-normal text-muted-foreground text-xs">· {m.description}</span>
                  <Badge variant="secondary" className="ml-auto text-[10px]">
                    {m.total ?? 0} 服务 · {m.running ?? 0} 运行 / {m.stopped ?? 0} 停止
                  </Badge>
                </CardTitle>
              </CardHeader>
              <CardContent>
                <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
                  {(m.services ?? []).map((sv) => (
                    <div key={sv.id} className="rounded-lg border border-input p-3">
                      <div className="flex items-center justify-between gap-2">
                        <div className="flex items-center gap-2 font-medium">
                          <CirclePlayIcon
                            className={cn("size-3.5", sv.running ? "text-emerald-600" : "text-slate-400")}
                          />
                          {sv.name}
                          <span className="font-mono text-[10px] text-muted-foreground">{sv.id}</span>
                        </div>
                        <Badge
                          variant="outline"
                          className={cn(
                            "text-[10px]",
                            sv.running ? "border-emerald-500/40 text-emerald-600" : "border-rose-500/40 text-rose-600",
                          )}
                        >
                          {sv.running ? "运行中" : "已停止"}
                        </Badge>
                      </div>
                      {sv.description ? (
                        <div className="mt-1 line-clamp-2 text-muted-foreground text-xs">{sv.description}</div>
                      ) : null}
                      {sv.detail ? (
                        <div className="mt-1 truncate font-mono text-[10px] text-muted-foreground">{sv.detail}</div>
                      ) : null}
                      <div className="mt-2 flex flex-wrap items-center gap-1.5">
                        {sv.start_supported ? (
                          <ActionBtn id={sv.id} label="启动" act="start" acting={acting} onAct={act} />
                        ) : null}
                        {sv.stop_supported ? (
                          <ActionBtn id={sv.id} label="停止" act="stop" acting={acting} onAct={act} danger />
                        ) : null}
                        {sv.start_supported ? (
                          <ActionBtn id={sv.id} label="重启" act="restart" acting={acting} onAct={act} />
                        ) : null}
                        {sv.autostart_supported ? (
                          <Button
                            variant="outline"
                            size="sm"
                            className="h-7 gap-1 text-xs"
                            disabled={acting !== null}
                            onClick={() => void act(sv.id, "autostart")}
                          >
                            <Settings2Icon className="size-3.5" />
                            自启
                          </Button>
                        ) : null}
                        {sv.autostart_enabled ? <Badge className="text-[10px]">自启开</Badge> : null}
                        {sv.optional ? (
                          <Badge variant="secondary" className="text-[10px]">
                            可选
                          </Badge>
                        ) : null}
                      </div>
                    </div>
                  ))}
                </div>
              </CardContent>
            </Card>
          ))
        : null}
    </div>
  );
}

function ActionBtn({
  id,
  label,
  act,
  acting,
  onAct,
  danger,
}: {
  id: string;
  label: string;
  act: "start" | "stop" | "restart";
  acting: string | null;
  onAct: (id: string, action: "start" | "stop" | "restart") => void;
  danger?: boolean;
}) {
  return (
    <Button
      variant={danger ? "destructive" : "outline"}
      size="sm"
      className="h-7 gap-1 text-xs"
      disabled={acting !== null}
      onClick={() => onAct(id, act)}
    >
      {danger ? <PowerIcon className="size-3.5" /> : <RotateCcwIcon className="size-3.5" />}
      {label}
    </Button>
  );
}

function StatChip({ title, value, accent }: { title: string; value: number; accent: string }) {
  return (
    <Card className="py-4">
      <CardHeader className="px-4">
        <CardTitle className="font-medium text-muted-foreground text-xs">{title}</CardTitle>
      </CardHeader>
      <CardContent className="px-4">
        <div className={cn("font-semibold text-2xl tabular-nums", accent)}>{value}</div>
      </CardContent>
    </Card>
  );
}
