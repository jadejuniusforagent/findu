"use client";

import * as React from "react";

import Link from "next/link";

import {
  AlertTriangleIcon,
  BoxesIcon,
  BugIcon,
  LayoutDashboardIcon,
  ListChecksIcon,
  MessageSquareIcon,
  RefreshCwIcon,
  SparklesIcon,
  UsersRoundIcon,
} from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api } from "@/lib/api";
import { formatTime } from "@/lib/findu";
import {
  type ConversationRow,
  listConversations,
  listFinduAssets,
  listFinduRoles,
  listFinduSkills,
} from "@/lib/readonly-api";
import type { Finding, FindingStats, Task } from "@/lib/types";
import { cn } from "@/lib/utils";

import { OverviewInsights } from "./_components/overview-insights";

function StatCard({
  title,
  value,
  detail,
  detailNode,
  icon: Icon,
  href,
  loading,
}: {
  title: string;
  value: string | number;
  detail?: string;
  detailNode?: React.ReactNode;
  icon: React.ComponentType<{ className?: string }>;
  href: string;
  loading: boolean;
}) {
  return (
    <Link href={href} className="group block">
      <Card className="h-full transition-colors group-hover:border-primary/40">
        <CardHeader className="pb-2">
          <CardTitle className="flex items-center justify-between gap-2 text-sm font-medium text-muted-foreground">
            <span>{title}</span>
            <Icon className="size-4" />
          </CardTitle>
        </CardHeader>
        <CardContent>
          {loading ? (
            <Skeleton className="h-8 w-20" />
          ) : (
            <div className="text-2xl font-semibold tabular-nums">{value}</div>
          )}
          {detailNode ?? (detail ? <p className="text-muted-foreground mt-1 text-xs">{detail}</p> : null)}
        </CardContent>
      </Card>
    </Link>
  );
}

/* framed-sheet 风险分级 pill：严重红 / 高危琥珀（牌面柔和底色色阶），
 * 剩余计数保持中性灰——安全产品一眼可辨（评审 P0-1） */
function SeverityPills({ critical, high, pending }: { critical: number; high: number; pending: number }) {
  return (
    <p className="text-muted-foreground mt-1 flex flex-wrap items-center gap-1 text-xs">
      <span className="inline-flex items-center rounded bg-[#ffedea] px-1.5 py-0.5 font-medium text-[#831c1e] dark:bg-red-950 dark:text-red-300">
        严重 {critical}
      </span>
      <span className="inline-flex items-center rounded bg-[#fffbeb] px-1.5 py-0.5 font-medium text-[#92400e] dark:bg-amber-950 dark:text-amber-300">
        高危 {high}
      </span>
      <span>待处理 {pending}</span>
    </p>
  );
}

function severityVariant(severity: string): "destructive" | "default" | "secondary" | "outline" {
  if (severity === "critical") return "destructive";
  if (severity === "high") return "default";
  if (severity === "medium") return "secondary";
  return "outline";
}

export default function DashboardPage() {
  const [stats, setStats] = React.useState<FindingStats | null>(null);
  const [recentFindings, setRecentFindings] = React.useState<Finding[]>([]);
  const [tasks, setTasks] = React.useState<Task[]>([]);
  const [conversations, setConversations] = React.useState<ConversationRow[]>([]);
  const [assets, setAssets] = React.useState<number | null>(null);
  const [skills, setSkills] = React.useState<number | null>(null);
  const [roles, setRoles] = React.useState<number | null>(null);
  const [errors, setErrors] = React.useState<string[]>([]);
  const [loading, setLoading] = React.useState(true);

  const load = React.useCallback(async () => {
    setLoading(true);
    const results = await Promise.allSettled([
      api.findingStats(),
      api.findingsPage({ page: 1, pageSize: 5, sort: "time" }),
      api.tasks(),
      listConversations({ limit: 5 }),
      listFinduAssets({ page: 1, pageSize: 1 }),
      listFinduSkills(1),
      listFinduRoles(),
    ]);
    const nextErrors: string[] = [];

    const [findingsRes, recentRes, tasksRes, convRes, assetsRes, skillsRes, rolesRes] = results;
    if (findingsRes.status === "fulfilled") setStats(findingsRes.value);
    else nextErrors.push("漏洞统计：" + (findingsRes.reason as Error).message);
    if (recentRes.status === "fulfilled") setRecentFindings(recentRes.value.items ?? []);
    else nextErrors.push("最近漏洞：" + (recentRes.reason as Error).message);
    if (tasksRes.status === "fulfilled") setTasks(tasksRes.value.tasks ?? []);
    else nextErrors.push("任务：" + (tasksRes.reason as Error).message);
    if (convRes.status === "fulfilled") setConversations(convRes.value.conversations ?? []);
    else nextErrors.push("会话：" + (convRes.reason as Error).message);
    if (assetsRes.status === "fulfilled") setAssets(assetsRes.value.total ?? 0);
    else nextErrors.push("资产：" + (assetsRes.reason as Error).message);
    if (skillsRes.status === "fulfilled") setSkills(skillsRes.value.total ?? 0);
    else nextErrors.push("技能：" + (skillsRes.reason as Error).message);
    if (rolesRes.status === "fulfilled") setRoles(rolesRes.value.roles?.length ?? 0);
    else nextErrors.push("角色：" + (rolesRes.reason as Error).message);

    setErrors(nextErrors);
    setLoading(false);
  }, []);

  React.useEffect(() => {
    void load();
  }, [load]);

  const running = tasks.filter((t) => t.status === "running").length;
  const active = tasks.filter((t) => t.status === "running" || t.status === "queued").length;

  return (
    <div className="flex flex-1 flex-col gap-4 md:gap-6">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h1 className="flex items-center gap-2 text-xl font-semibold tracking-tight">
            <LayoutDashboardIcon className="size-5" /> 总览
          </h1>
          <p className="text-muted-foreground text-sm">
            漏洞、任务、资产、技能与角色的运行总览
          </p>
        </div>
        <Button variant="outline" onClick={() => void load()} disabled={loading}>
          <RefreshCwIcon className={cn("size-4", loading && "animate-spin")} /> 刷新
        </Button>
      </div>

      {errors.length > 0 ? (
        <Card className="border-amber-500/40 bg-amber-500/5">
          <CardContent className="flex items-start gap-2 py-3 text-sm">
            <AlertTriangleIcon className="mt-0.5 size-4 shrink-0 text-amber-500" />
            <div>
              <div className="font-medium">部分数据源不可用（不影响其他卡片）</div>
              <ul className="text-muted-foreground mt-1 list-disc space-y-0.5 pl-4 text-xs">
                {errors.slice(0, 4).map((e) => (
                  <li key={e}>{e}</li>
                ))}
              </ul>
            </div>
          </CardContent>
        </Card>
      ) : null}

      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-6">
        <StatCard
          title="漏洞"
          value={stats?.total ?? 0}
          detail={stats ? `严重 ${stats.critical} · 高危 ${stats.high} · 待处理 ${stats.pending}` : undefined}
          detailNode={stats ? <SeverityPills critical={stats.critical} high={stats.high} pending={stats.pending} /> : undefined}
          icon={BugIcon}
          href="/function/findings"
          loading={loading}
        />
        <StatCard
          title="资产"
          value={assets ?? "-"}
          detail={assets === null ? "同步中" : "全部资产"}
          icon={BoxesIcon}
          href="/function/assets"
          loading={loading}
        />
        <StatCard
          title="任务"
          value={tasks.length}
          detail={`运行中 ${running} · 活跃 ${active}`}
          icon={ListChecksIcon}
          href="/function/tasks"
          loading={loading}
        />
        <StatCard
          title="会话"
          value={conversations.length > 0 ? "5+" : 0}
          detail="最近 5 条会话"
          icon={MessageSquareIcon}
          href="/conversations"
          loading={loading}
        />
        <StatCard
          title="技能"
          value={skills ?? "-"}
          detail={skills === null ? "同步中" : "已安装技能"}
          icon={SparklesIcon}
          href="/system/skills"
          loading={loading}
        />
        <StatCard
          title="角色"
          value={roles ?? "-"}
          detail={roles === null ? "同步中" : "全部角色"}
          icon={UsersRoundIcon}
          href="/system/roles"
          loading={loading}
        />
      </div>

      <OverviewInsights stats={stats} />

      <div className="grid gap-4 xl:grid-cols-2">
        <Card>
          <CardHeader className="flex flex-row items-center justify-between gap-2">
            <div>
              <CardTitle className="flex items-center gap-2 text-base">
                <BugIcon className="size-4" /> 最近漏洞
              </CardTitle>
              <CardDescription>按发现时间倒序，只读。</CardDescription>
            </div>
            <Button variant="ghost" size="sm" asChild>
              <Link href="/function/findings">查看全部</Link>
            </Button>
          </CardHeader>
          <CardContent>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>漏洞</TableHead>
                  <TableHead className="w-20">严重度</TableHead>
                  <TableHead className="w-28">时间</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {loading ? (
                  <TableRow>
                    <TableCell colSpan={3} className="text-muted-foreground py-6 text-center text-sm">
                      加载中…
                    </TableCell>
                  </TableRow>
                ) : recentFindings.length === 0 ? (
                  <TableRow>
                    <TableCell colSpan={3} className="text-muted-foreground py-6 text-center text-sm">
                      暂无漏洞
                    </TableCell>
                  </TableRow>
                ) : (
                  recentFindings.map((f) => (
                    <TableRow key={f.id}>
                      <TableCell>
                        <div className="max-w-[420px] truncate font-medium">{f.name || f.vulnclass}</div>
                        <div className="text-muted-foreground max-w-[420px] truncate text-xs">{f.vulnclass}</div>
                      </TableCell>
                      <TableCell>
                        <Badge variant={severityVariant(f.severity)} className="text-[10px]">
                          {f.severity}
                        </Badge>
                      </TableCell>
                      <TableCell className="text-muted-foreground text-xs">{formatTime(f.ts)}</TableCell>
                    </TableRow>
                  ))
                )}
              </TableBody>
            </Table>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="flex flex-row items-center justify-between gap-2">
            <div>
              <CardTitle className="flex items-center gap-2 text-base">
                <MessageSquareIcon className="size-4" /> 最近会话
              </CardTitle>
              <CardDescription>近期会话概览</CardDescription>
            </div>
            <Button variant="ghost" size="sm" asChild>
              <Link href="/conversations">查看全部</Link>
            </Button>
          </CardHeader>
          <CardContent className="flex flex-col gap-2">
            {loading ? (
              <div className="text-muted-foreground py-6 text-center text-sm">加载中…</div>
            ) : conversations.length === 0 ? (
              <div className="text-muted-foreground py-6 text-center text-sm">暂无会话</div>
            ) : (
              conversations.map((c) => (
                <div key={c.id} className="flex items-start justify-between gap-3 rounded-lg border px-3 py-2">
                  <div className="min-w-0">
                    <div className="truncate text-sm font-medium">{c.title || "（无标题会话）"}</div>
                    <div className="text-muted-foreground mt-0.5 flex flex-wrap gap-1.5 text-xs">
                      <span>{c.roleName || "默认角色"}</span>
                      <span>·</span>
                      <span>{c.agentMode || "eino_single"}</span>
                    </div>
                  </div>
                  <span className="text-muted-foreground shrink-0 text-xs">
                    {formatTime(c.updatedAt || c.updated_at)}
                  </span>
                </div>
              ))
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
