"use client";

// 总览「仪表盘增强」：把 findu 仪表盘的分析面板在控制台重建（数据接口已宿主原生）。
// 设计约束：
//  - 每个面板独立取数、独立降级（Promise.allSettled），单个失败不影响其它面板；
//  - 漏洞分布与首页「漏洞」卡同源（exploration findings），避免 230 vs 44 两套口径；
//  - Token 趋势按日聚合多 profile（llm_usage 台账，/api/tokens/usage?days=7）。

import * as React from "react";

import {
  ActivityIcon,
  BotIcon,
  CableIcon,
  CoinsIcon,
  LayersIcon,
  LightbulbIcon,
  ListChecksIcon,
  ServerIcon,
  ShieldAlertIcon,
  ShieldQuestionIcon,
  TerminalSquareIcon,
  TimerIcon,
} from "lucide-react";
import { Area, AreaChart, Bar, BarChart, CartesianGrid, Cell, Pie, PieChart, XAxis, YAxis } from "recharts";

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { type ChartConfig, ChartContainer, ChartTooltip, ChartTooltipContent } from "@/components/ui/chart";
import { Skeleton } from "@/components/ui/skeleton";
import { api } from "@/lib/api";
import {
  type AgentLoopTasks,
  type BatchQueues,
  type C2Listeners,
  type C2Sessions,
  type C2Tasks,
  dashboardApi,
  type ExternalMcpStats,
  type HitlPendingHead,
  type KnowledgeStats,
  type MarkdownAgents,
  type MonitorStats,
  type ProjectsSummary,
  type WebshellConnection,
} from "@/lib/dashboard-api";
import type { FindingStats, Task, UsageStats } from "@/lib/types";
import { cn } from "@/lib/utils";

interface Insights {
  usage?: UsageStats;
  monitor?: MonitorStats;
  hitl?: HitlPendingHead;
  mcp?: ExternalMcpStats;
  knowledge?: KnowledgeStats;
  projects?: ProjectsSummary;
  webshell?: WebshellConnection[];
  c2l?: C2Listeners;
  c2s?: C2Sessions;
  c2t?: C2Tasks;
  batch?: BatchQueues;
  engineTasks?: { tasks?: Task[] | null; active?: string };
  agentLoop?: AgentLoopTasks;
  agents?: MarkdownAgents;
}

const SEVERITY = [
  { key: "critical", label: "严重", color: "#dc2626" },
  { key: "high", label: "高危", color: "#f97316" },
  { key: "medium", label: "中危", color: "#f59e0b" },
  { key: "low", label: "低危", color: "#3b82f6" },
] as const;

const DONUT_CONFIG: ChartConfig = Object.fromEntries(SEVERITY.map((s) => [s.key, { label: s.label, color: s.color }]));

const TOKEN_CONFIG: ChartConfig = {
  input: { label: "输入 Token", color: "#6366f1" },
  output: { label: "输出 Token", color: "#10b981" },
};

const TOOL_CONFIG: ChartConfig = {
  totalCalls: { label: "调用次数", color: "#6366f1" },
};

function fmt(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 10_000) return `${(n / 1_000).toFixed(1)}K`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(2)}K`;
  return String(n);
}

function KpiCard({
  title,
  value,
  detail,
  icon: Icon,
  loading,
}: {
  title: string;
  value: React.ReactNode;
  detail?: string;
  icon: React.ComponentType<{ className?: string }>;
  loading?: boolean;
}) {
  return (
    <Card className="gap-2 py-4">
      <CardHeader className="px-4">
        <CardTitle className="text-muted-foreground flex items-center justify-between text-xs font-medium">
          <span>{title}</span>
          <Icon className="size-4" />
        </CardTitle>
      </CardHeader>
      <CardContent className="px-4">
        {loading ? (
          <Skeleton className="h-7 w-16" />
        ) : (
          <div className="text-2xl font-semibold tracking-tight">{value}</div>
        )}
        {detail ? <div className="text-muted-foreground mt-1 truncate text-xs">{detail}</div> : null}
      </CardContent>
    </Card>
  );
}

function PanelSkeleton({ className }: { className?: string }) {
  return (
    <div className={cn("flex items-center justify-center", className)}>
      <Skeleton className="h-full w-full" />
    </div>
  );
}

export function OverviewInsights({ stats }: { stats: FindingStats | null }) {
  const [data, setData] = React.useState<Insights>({});
  const [failed, setFailed] = React.useState<string[]>([]);

  React.useEffect(() => {
    let alive = true;
    void Promise.allSettled([
      api.usageStats(7),
      dashboardApi.monitorStats(8),
      dashboardApi.hitlPendingHead(),
      dashboardApi.externalMcpStats(),
      dashboardApi.knowledgeStats(),
      dashboardApi.projectsSummary(5),
      dashboardApi.webshellConnections(),
      dashboardApi.c2Listeners(),
      dashboardApi.c2Sessions(5),
      dashboardApi.c2Tasks(),
      dashboardApi.batchQueues(5),
      api.tasks(),
      dashboardApi.agentLoopTasks(),
      dashboardApi.markdownAgents(),
    ]).then((results) => {
      if (!alive) return;
      const [
        usage,
        monitor,
        hitl,
        mcp,
        knowledge,
        projects,
        webshell,
        c2l,
        c2s,
        c2t,
        batch,
        engine,
        agentLoop,
        agents,
      ] = results;
      const next: Insights = {};
      const errs: string[] = [];
      if (usage.status === "fulfilled") next.usage = usage.value;
      else errs.push("Token 用量");
      if (monitor.status === "fulfilled") next.monitor = monitor.value;
      else errs.push("工具统计");
      if (hitl.status === "fulfilled") next.hitl = hitl.value;
      else errs.push("HITL 待办");
      if (mcp.status === "fulfilled") next.mcp = mcp.value;
      else errs.push("外部 MCP");
      if (knowledge.status === "fulfilled") next.knowledge = knowledge.value;
      else errs.push("知识库");
      if (projects.status === "fulfilled") next.projects = projects.value;
      else errs.push("最近事实");
      if (webshell.status === "fulfilled") next.webshell = webshell.value;
      else errs.push("WebShell");
      if (c2l.status === "fulfilled") next.c2l = c2l.value;
      if (c2s.status === "fulfilled") next.c2s = c2s.value;
      if (c2t.status === "fulfilled") next.c2t = c2t.value;
      if (c2l.status !== "fulfilled" && c2s.status !== "fulfilled" && c2t.status !== "fulfilled") errs.push("C2");
      if (batch.status === "fulfilled") next.batch = batch.value;
      else errs.push("批量队列");
      if (engine.status === "fulfilled") next.engineTasks = engine.value;
      if (agentLoop.status === "fulfilled") next.agentLoop = agentLoop.value;
      if (agents.status === "fulfilled") next.agents = agents.value;
      setData(next);
      setFailed(errs);
    });
    return () => {
      alive = false;
    };
  }, []);

  const tokenDaily = React.useMemo(() => {
    const map = new Map<string, { date: string; input: number; output: number }>();
    for (const row of data.usage?.daily ?? []) {
      const key = row.date.slice(5);
      const cur = map.get(key) ?? { date: key, input: 0, output: 0 };
      cur.input += row.input_tokens;
      cur.output += row.output_tokens;
      map.set(key, cur);
    }
    return Array.from(map.values())
      .sort((a, b) => a.date.localeCompare(b.date))
      .slice(-7);
  }, [data.usage]);

  const tokenTotal = tokenDaily.reduce((sum, d) => sum + d.input + d.output, 0);
  const successRate =
    data.monitor && data.monitor.summary.totalCalls > 0
      ? (data.monitor.summary.successCalls / data.monitor.summary.totalCalls) * 100
      : null;

  const severityData = SEVERITY.map((s) => ({
    key: s.key,
    label: s.label,
    value: stats?.[s.key] ?? 0,
    color: s.color,
  }));
  const severityTotal = severityData.reduce((sum, s) => sum + s.value, 0);
  const topTools = (data.monitor?.topTools ?? []).slice(0, 8);

  // 风险分：与 findu 仪表盘同算法（严重×10 + 高危×5 + 中危×2 + 低危×0.5；
  // 阈值 0=安全 / ≤3 低 / ≤10 中 / ≤30 高 / >30 极高）。
  const risk = React.useMemo(() => {
    const c = stats?.critical ?? 0;
    const h = stats?.high ?? 0;
    const m = stats?.medium ?? 0;
    const l = stats?.low ?? 0;
    const total = stats?.total ?? 0;
    const score = c * 10 + h * 5 + m * 2 + l * 0.5;
    let level = "安全";
    if (total > 0 && score > 0) {
      if (score <= 3) level = "低";
      else if (score <= 10) level = "中";
      else if (score <= 30) level = "高";
      else level = "极高";
    }
    return { score, level, pct: Math.max(0, Math.min(100, score)) };
  }, [stats]);

  const recommendations = React.useMemo(() => {
    const items: string[] = [];
    const c = stats?.critical ?? 0;
    const h = stats?.high ?? 0;
    if (c > 0) items.push(`${c} 个严重漏洞待处理，建议优先处置`);
    if (h > 0) items.push(`${h} 个高危漏洞待处理`);
    if ((data.hitl?.total ?? 0) > 0) items.push(`${data.hitl?.total} 个 HITL 审批待处理`);
    if ((data.mcp?.disabled ?? 0) > 0) items.push(`${data.mcp?.disabled} 个外部 MCP 已禁用`);
    if ((data.monitor?.summary.failedCalls ?? 0) > 0)
      items.push(`工具调用失败 ${data.monitor?.summary.failedCalls} 次，建议检查工具配置`);
    if (items.length === 0) items.push("当前无高优先级事项，保持监控");
    return items.slice(0, 5);
  }, [stats, data]);

  const facts = (data.projects?.recent_facts ?? []).slice(0, 5);
  const shells = (data.webshell ?? []).slice(0, 5);
  const c2Listeners = Array.isArray(data.c2l?.listeners) ? (data.c2l?.listeners?.length ?? 0) : 0;
  const c2Sessions = Array.isArray(data.c2s?.sessions) ? (data.c2s?.sessions?.length ?? 0) : 0;
  const c2Pending = data.c2t?.pending_queued_count ?? 0;
  const c2Running = data.c2t?.status_counts?.running ?? 0;
  const batchTotal = data.batch?.total ?? 0;
  const batchQueues = Array.isArray(data.batch?.queues) ? (data.batch?.queues?.length ?? 0) : 0;

  // 运行中对话：agent-loop 运行任务 + findu 引擎任务（running / in_flight）合并，
  // 与 findu 仪表盘同口径（dashboard.js agentRunningCount + engineRunningCount）。
  const engineRunning = (data.engineTasks?.tasks ?? []).filter(
    (t) => t.status === "running" || Number(t.in_flight || 0) > 0,
  ).length;
  const runningConversations = (data.agentLoop?.tasks?.length ?? 0) + engineRunning;

  const agentItems = (data.agents?.agents ?? []).slice(0, 8);
  const agentsCount = data.agents?.agents?.length ?? 0;
  const orchestrators = (data.agents?.agents ?? []).filter((a) => a.is_orchestrator).length;

  return (
    <section className="flex flex-col gap-4">
      <div className="flex items-center justify-between gap-3">
        <h2 className="flex items-center gap-2 text-sm font-semibold">
          <ActivityIcon className="size-4" /> 运行洞察
        </h2>
        {failed.length > 0 ? (
          <span className="text-muted-foreground text-xs">部分数据源不可用：{failed.join("、")}</span>
        ) : null}
      </div>

      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-6">
        <KpiCard
          title="工具调用"
          value={fmt(data.monitor?.summary.totalCalls ?? 0)}
          detail={
            data.monitor
              ? `成功率 ${successRate === null ? "-" : successRate.toFixed(1)}% · 失败 ${data.monitor.summary.failedCalls}`
              : undefined
          }
          icon={ActivityIcon}
          loading={!data.monitor && failed.length === 0}
        />
        <KpiCard
          title="7 日 Token"
          value={data.usage ? fmt(tokenTotal) : "-"}
          detail={
            data.usage
              ? `输入 ${fmt(tokenDaily.reduce((s, d) => s + d.input, 0))} · 输出 ${fmt(tokenDaily.reduce((s, d) => s + d.output, 0))}`
              : undefined
          }
          icon={CoinsIcon}
          loading={!data.usage && failed.length === 0}
        />
        <KpiCard
          title="HITL 待审批"
          value={data.hitl?.total ?? 0}
          detail="人工审批队列"
          icon={ShieldQuestionIcon}
          loading={!data.hitl && failed.length === 0}
        />
        <KpiCard
          title="外部 MCP"
          value={data.mcp ? `${data.mcp.connected}/${data.mcp.total}` : "-"}
          detail={data.mcp ? `启用 ${data.mcp.enabled} · 禁用 ${data.mcp.disabled}` : undefined}
          icon={CableIcon}
          loading={!data.mcp && failed.length === 0}
        />
        <KpiCard
          title="知识库"
          value={data.knowledge ? data.knowledge.total_items : "-"}
          detail={
            data.knowledge
              ? `${data.knowledge.total_categories} 个分类 · ${data.knowledge.enabled ? "已启用" : "已停用"}`
              : undefined
          }
          icon={ListChecksIcon}
          loading={!data.knowledge && failed.length === 0}
        />
        <KpiCard
          title="运行中对话"
          value={runningConversations}
          detail={runningConversations > 0 ? "正在执行" : "系统空闲"}
          icon={TimerIcon}
          loading={!data.agentLoop && !data.engineTasks && failed.length === 0}
        />
      </div>

      <div className="grid gap-4 xl:grid-cols-3">
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">漏洞严重程度分布</CardTitle>
            <CardDescription>与「漏洞」卡同源（exploration findings）。</CardDescription>
          </CardHeader>
          <CardContent>
            {!stats ? (
              <PanelSkeleton className="h-[220px]" />
            ) : severityTotal === 0 ? (
              <div className="text-muted-foreground flex h-[220px] items-center justify-center text-sm">暂无漏洞</div>
            ) : (
              <div className="flex items-center gap-4">
                <ChartContainer config={DONUT_CONFIG} className="aspect-auto h-[200px] w-[200px] shrink-0">
                  <PieChart>
                    <ChartTooltip content={<ChartTooltipContent nameKey="label" hideLabel />} />
                    <Pie
                      data={severityData.filter((s) => s.value > 0)}
                      dataKey="value"
                      nameKey="label"
                      innerRadius={52}
                      outerRadius={80}
                      paddingAngle={2}
                      stroke="none"
                    >
                      {severityData
                        .filter((s) => s.value > 0)
                        .map((s) => (
                          <Cell key={s.key} fill={s.color} />
                        ))}
                    </Pie>
                  </PieChart>
                </ChartContainer>
                <div className="flex min-w-0 flex-1 flex-col gap-1.5">
                  <div className="mb-1 text-2xl font-semibold tracking-tight">{severityTotal}</div>
                  {severityData.map((s) => (
                    <div key={s.key} className="flex items-center justify-between gap-2 text-xs">
                      <span className="flex items-center gap-1.5">
                        <span className="size-2 rounded-full" style={{ backgroundColor: s.color }} />
                        {s.label}
                      </span>
                      <span className="text-muted-foreground tabular-nums">{s.value}</span>
                    </div>
                  ))}
                </div>
              </div>
            )}
          </CardContent>
        </Card>

        <Card className="xl:col-span-2">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">Token 用量趋势（7 日）</CardTitle>
            <CardDescription>llm_usage 台账按日聚合，输入/输出分别统计。</CardDescription>
          </CardHeader>
          <CardContent>
            {!data.usage ? (
              <PanelSkeleton className="h-[220px]" />
            ) : tokenDaily.length === 0 ? (
              <div className="text-muted-foreground flex h-[220px] items-center justify-center text-sm">
                近 7 日无用量记录
              </div>
            ) : (
              <ChartContainer config={TOKEN_CONFIG} className="aspect-auto h-[220px] w-full">
                <AreaChart data={tokenDaily} margin={{ left: 0, right: 8, top: 8 }}>
                  <CartesianGrid vertical={false} strokeDasharray="3 3" />
                  <XAxis dataKey="date" tickLine={false} axisLine={false} tickMargin={6} />
                  <YAxis tickLine={false} axisLine={false} width={44} tickFormatter={(v: number) => fmt(v)} />
                  <ChartTooltip content={<ChartTooltipContent indicator="line" />} />
                  <Area
                    type="monotone"
                    dataKey="input"
                    stroke="var(--color-input)"
                    fill="var(--color-input)"
                    fillOpacity={0.15}
                    strokeWidth={2}
                  />
                  <Area
                    type="monotone"
                    dataKey="output"
                    stroke="var(--color-output)"
                    fill="var(--color-output)"
                    fillOpacity={0.15}
                    strokeWidth={2}
                  />
                </AreaChart>
              </ChartContainer>
            )}
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">工具调用 Top 8</CardTitle>
          <CardDescription>来自 /api/monitor/stats（tool_usage 台账）。</CardDescription>
        </CardHeader>
        <CardContent>
          {!data.monitor ? (
            <PanelSkeleton className="h-[240px]" />
          ) : topTools.length === 0 ? (
            <div className="text-muted-foreground flex h-[200px] items-center justify-center text-sm">
              暂无工具调用记录
            </div>
          ) : (
            <ChartContainer config={TOOL_CONFIG} className="aspect-auto h-[260px] w-full">
              <BarChart data={topTools} layout="vertical" margin={{ left: 8, right: 16, top: 4 }}>
                <CartesianGrid horizontal={false} strokeDasharray="3 3" />
                <XAxis type="number" hide />
                <YAxis
                  type="category"
                  dataKey="toolName"
                  width={150}
                  tickLine={false}
                  axisLine={false}
                  tick={{ fontSize: 11 }}
                />
                <ChartTooltip content={<ChartTooltipContent indicator="line" />} />
                <Bar dataKey="totalCalls" fill="var(--color-totalCalls)" radius={[0, 4, 4, 0]} barSize={14} />
              </BarChart>
            </ChartContainer>
          )}
        </CardContent>
      </Card>

      <div className="grid gap-4 xl:grid-cols-3">
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <ShieldAlertIcon className="size-4" /> 风险与建议
            </CardTitle>
            <CardDescription>按漏洞严重度加权：严重×10 + 高危×5 + 中危×2 + 低危×0.5</CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-3">
            {!stats ? (
              <PanelSkeleton className="h-[180px]" />
            ) : (
              <>
                <div className="flex items-end justify-between gap-2">
                  <div>
                    <div className="text-muted-foreground text-xs">风险等级</div>
                    <div className="text-xl font-semibold">{risk.level}</div>
                  </div>
                  <div className="text-muted-foreground text-right text-xs">
                    加权分
                    <div className="text-foreground text-lg font-semibold tabular-nums">
                      {Number.isInteger(risk.score) ? risk.score : risk.score.toFixed(1)}
                    </div>
                  </div>
                </div>
                <div className="bg-muted h-2 w-full overflow-hidden rounded-full">
                  <div
                    className="h-full rounded-full bg-gradient-to-r from-emerald-500 via-amber-500 to-red-500"
                    style={{ width: `${risk.pct}%` }}
                  />
                </div>
                <div className="flex flex-col gap-1.5 border-t pt-2">
                  {recommendations.map((r) => (
                    <div key={r} className="flex items-start gap-1.5 text-xs">
                      <LightbulbIcon className="text-muted-foreground mt-0.5 size-3.5 shrink-0" />
                      <span>{r}</span>
                    </div>
                  ))}
                </div>
              </>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <ListChecksIcon className="size-4" /> 最近事实
            </CardTitle>
            <CardDescription>项目知识库最近沉淀（projects dashboard-summary）。</CardDescription>
          </CardHeader>
          <CardContent>
            {!data.projects ? (
              <PanelSkeleton className="h-[180px]" />
            ) : facts.length === 0 ? (
              <div className="text-muted-foreground flex h-[180px] items-center justify-center text-sm">
                暂无事实记录
              </div>
            ) : (
              <div className="flex flex-col gap-2">
                {facts.map((f) => (
                  <div key={f.id} className="min-w-0 border-b pb-2 last:border-0 last:pb-0">
                    <div className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
                      <span className="truncate">{f.project_name || "未命名项目"}</span>
                      {f.category ? <span className="rounded bg-muted px-1 py-0.5">{f.category}</span> : null}
                    </div>
                    <div className="line-clamp-2 text-xs">{f.summary || f.fact_key || f.id}</div>
                  </div>
                ))}
              </div>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <TerminalSquareIcon className="size-4" /> WebShell 最近连接
            </CardTitle>
            <CardDescription>webshell_connections 台账（最多 5 条）。</CardDescription>
          </CardHeader>
          <CardContent>
            {!data.webshell ? (
              <PanelSkeleton className="h-[180px]" />
            ) : shells.length === 0 ? (
              <div className="text-muted-foreground flex h-[180px] items-center justify-center text-sm">暂无连接</div>
            ) : (
              <div className="flex flex-col gap-2">
                {shells.map((s) => (
                  <div
                    key={s.id}
                    className="flex items-center justify-between gap-2 border-b pb-2 text-xs last:border-0 last:pb-0"
                  >
                    <div className="min-w-0">
                      <div className="truncate font-medium">{s.remark || s.url || s.id}</div>
                      {s.url && s.remark ? <div className="text-muted-foreground truncate">{s.url}</div> : null}
                    </div>
                    {s.type ? (
                      <span className="text-muted-foreground rounded bg-muted px-1 py-0.5 text-[10px]">{s.type}</span>
                    ) : null}
                  </div>
                ))}
              </div>
            )}
          </CardContent>
        </Card>
      </div>

      <div className="grid gap-4 xl:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <ServerIcon className="size-4" /> C2 状态
            </CardTitle>
            <CardDescription>监听 / 在线会话 / 任务队列（c2 域）。</CardDescription>
          </CardHeader>
          <CardContent>
            {!data.c2l && !data.c2s && !data.c2t ? (
              <PanelSkeleton className="h-[110px]" />
            ) : (
              <div className="grid grid-cols-4 gap-2 text-center">
                <div>
                  <div className="text-2xl font-semibold tabular-nums">{c2Listeners}</div>
                  <div className="text-muted-foreground text-xs">监听</div>
                </div>
                <div>
                  <div className="text-2xl font-semibold tabular-nums">{c2Sessions}</div>
                  <div className="text-muted-foreground text-xs">在线会话</div>
                </div>
                <div>
                  <div className="text-2xl font-semibold tabular-nums">{c2Running}</div>
                  <div className="text-muted-foreground text-xs">执行中任务</div>
                </div>
                <div>
                  <div className={cn("text-2xl font-semibold tabular-nums", c2Pending > 0 && "text-amber-600")}>
                    {c2Pending}
                  </div>
                  <div className="text-muted-foreground text-xs">待处理</div>
                </div>
              </div>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <LayersIcon className="size-4" /> 批量队列
            </CardTitle>
            <CardDescription>batch-tasks 队列（executor 内存队列 + DB 混合）。</CardDescription>
          </CardHeader>
          <CardContent>
            {!data.batch ? (
              <PanelSkeleton className="h-[110px]" />
            ) : (
              <div className="grid grid-cols-2 gap-2 text-center">
                <div>
                  <div className="text-2xl font-semibold tabular-nums">{batchTotal}</div>
                  <div className="text-muted-foreground text-xs">批任务总数</div>
                </div>
                <div>
                  <div className="text-2xl font-semibold tabular-nums">{batchQueues}</div>
                  <div className="text-muted-foreground text-xs">活跃队列</div>
                </div>
              </div>
            )}
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            <BotIcon className="size-4" /> 代理
          </CardTitle>
          <CardDescription>multi-agent markdown 代理（markdown-agents），含编排代理。</CardDescription>
        </CardHeader>
        <CardContent>
          {!data.agents ? (
            <PanelSkeleton className="h-[120px]" />
          ) : agentsCount === 0 ? (
            <div className="text-muted-foreground flex h-[120px] items-center justify-center text-sm">暂无代理</div>
          ) : (
            <>
              <div className="mb-3 grid grid-cols-3 gap-2 text-center">
                <div>
                  <div className="text-2xl font-semibold tabular-nums">{agentsCount}</div>
                  <div className="text-muted-foreground text-xs">代理总数</div>
                </div>
                <div>
                  <div className="text-2xl font-semibold tabular-nums">{orchestrators}</div>
                  <div className="text-muted-foreground text-xs">编排代理</div>
                </div>
                <div>
                  <div className="text-2xl font-semibold tabular-nums">{agentsCount - orchestrators}</div>
                  <div className="text-muted-foreground text-xs">常规代理</div>
                </div>
              </div>
              <div className="flex flex-col gap-2">
                {agentItems.map((a) => (
                  <div
                    key={a.filename ?? a.id ?? a.name}
                    className="flex items-center justify-between gap-2 border-b pb-2 text-xs last:border-0 last:pb-0"
                  >
                    <div className="min-w-0">
                      <div className="flex items-center gap-1.5 font-medium">
                        <span className="truncate">{a.name || a.filename || a.id}</span>
                        {a.is_orchestrator ? (
                          <span className="rounded bg-primary/10 px-1 py-0.5 text-[10px] text-primary">编排</span>
                        ) : null}
                      </div>
                      {a.description ? <div className="text-muted-foreground truncate">{a.description}</div> : null}
                    </div>
                    {a.kind ? (
                      <span className="text-muted-foreground shrink-0 rounded bg-muted px-1 py-0.5 text-[10px]">
                        {a.kind}
                      </span>
                    ) : null}
                  </div>
                ))}
              </div>
            </>
          )}
        </CardContent>
      </Card>
    </section>
  );
}
