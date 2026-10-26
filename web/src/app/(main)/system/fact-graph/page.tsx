"use client";

// 项目事实图谱（旧 findu UI fact-graph 域迁入控制台，U6 收尾续）。
// 后端 GET /api/projects/{id}/fact-graph（finduRuntimeBridge，Bearer 直连可用）——
// 返回 { nodes: [fact_key/category/confidence/label/summary/type/pinned], edges: [source/target/type/confidence] }。
// 本项目维度的推理事实知识图谱：选项目后展示事实节点与关联边。

import * as React from "react";
import { useMemo, useState } from "react";

import { AlertTriangleIcon, BoxesIcon, GitBranchIcon, RefreshCwIcon, Share2Icon, WorkflowIcon } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Label } from "@/components/ui/label";
import { dashboardApi, type ProjectBrief, type ProjectFactGraph } from "@/lib/dashboard-api";
import { cn } from "@/lib/utils";

const CATEGORY_STYLE: Record<string, string> = {
  finding: "border-rose-500/40 text-rose-600",
  poc: "border-amber-500/40 text-amber-600",
  target: "border-sky-500/40 text-sky-600",
  hypothesis: "border-violet-500/40 text-violet-600",
  technique: "border-emerald-500/40 text-emerald-600",
};

const CONFIDENCE_STYLE: Record<string, string> = {
  high: "border-emerald-500/40 text-emerald-600",
  medium: "border-amber-500/40 text-amber-600",
  tentative: "border-slate-400/40 text-slate-500",
};

export default function FactGraphPage() {
  const [projects, setProjects] = useState<ProjectBrief[]>([]);
  const [selectedId, setSelectedId] = useState<string>("");
  const [graph, setGraph] = useState<ProjectFactGraph | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const loadProjects = React.useCallback(async () => {
    try {
      const res = await dashboardApi.projectsList();
      setProjects(res.projects ?? []);
      if (!selectedId && res.projects?.[0]) {
        setSelectedId(res.projects[0].id);
      }
    } catch (e) {
      setError((e as Error).message);
    }
  }, [selectedId]);

  const loadGraph = React.useCallback(async (id: string) => {
    if (!id) return;
    setLoading(true);
    try {
      setGraph(await dashboardApi.projectFactGraph(id));
      setError(null);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setLoading(false);
    }
  }, []);

  React.useEffect(() => {
    void loadProjects();
  }, [loadProjects]);

  React.useEffect(() => {
    if (selectedId) void loadGraph(selectedId);
  }, [selectedId, loadGraph]);

  React.useEffect(() => {
    if (!selectedId) return;
    const t = setInterval(() => void loadGraph(selectedId), 30000);
    return () => clearInterval(t);
  }, [selectedId, loadGraph]);

  const nodes = graph?.nodes ?? [];
  const edges = graph?.edges ?? [];
  const categories = useMemo(() => Array.from(new Set(nodes.map((n) => n.category).filter(Boolean))), [nodes]);
  const typeCounts = useMemo(() => {
    const m: Record<string, number> = {};
    for (const n of nodes) {
      const t = n.type || n.category || "other";
      m[t] = (m[t] ?? 0) + 1;
    }
    return m;
  }, [nodes]);

  const selectedProject = projects.find((p) => p.id === selectedId);

  return (
    <div className="flex flex-1 flex-col gap-4">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h1 className="flex items-center gap-2 text-xl font-semibold tracking-tight">
            <Share2Icon className="size-5" /> 事实图谱
          </h1>
          <p className="text-muted-foreground text-sm">
            项目推理事实知识图谱（fact-graph）。展示事实节点与关联边，含置信度与分类。
          </p>
        </div>
        <Button variant="outline" onClick={() => void loadGraph(selectedId)} disabled={loading || !selectedId}>
          <RefreshCwIcon className={cn("size-4", loading && "animate-spin")} /> 刷新
        </Button>
      </div>

      <Card>
        <CardContent className="pt-6">
          <div className="grid gap-1.5 sm:max-w-sm">
            <Label htmlFor="project-select">项目</Label>
            <select
              id="project-select"
              className="border-input bg-background flex h-9 w-full rounded-md border px-3 text-sm focus-visible:outline-none"
              value={selectedId}
              onChange={(e) => setSelectedId(e.target.value)}
            >
              {projects.length === 0 ? <option value="">加载项目…</option> : null}
              {projects.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name}
                </option>
              ))}
            </select>
          </div>
        </CardContent>
      </Card>

      {error ? (
        <Card className="border-amber-500/40 bg-amber-500/5">
          <CardContent className="flex items-start gap-2 py-3 text-sm">
            <AlertTriangleIcon className="mt-0.5 size-4 shrink-0 text-amber-500" />
            <div className="font-medium">加载失败：{error}</div>
          </CardContent>
        </Card>
      ) : null}

      <div className="grid gap-3 sm:grid-cols-4">
        <StatChip title="事实节点" value={nodes.length} accent="" />
        <StatChip title="关联边" value={edges.length} accent="text-sky-600" />
        <StatChip title="分类" value={categories.length} accent="text-emerald-600" />
        <StatChip title="类型" value={Object.keys(typeCounts).length} accent="text-violet-600" />
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            <BoxesIcon className="size-4" /> 事实节点
            <Badge variant="secondary" className="text-[10px]">
              {nodes.length}
            </Badge>
            {selectedProject ? <span className="text-muted-foreground text-xs">· {selectedProject.name}</span> : null}
          </CardTitle>
          <CardDescription>推理得出的事实，含分类 / 类型 / 置信度 / 摘要。</CardDescription>
        </CardHeader>
        <CardContent>
          {loading && !graph ? (
            <div className="text-muted-foreground py-6 text-center text-sm">加载中…</div>
          ) : nodes.length === 0 ? (
            <div className="text-muted-foreground py-6 text-center text-sm">该项目暂无事实节点</div>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b text-muted-foreground text-xs">
                    <th className="px-3 py-2 text-left font-medium">事实</th>
                    <th className="px-3 py-2 text-left font-medium">分类</th>
                    <th className="px-3 py-2 text-left font-medium">类型</th>
                    <th className="px-3 py-2 text-left font-medium">置信度</th>
                    <th className="px-3 py-2 text-left font-medium">固定</th>
                  </tr>
                </thead>
                <tbody>
                  {nodes.map((n) => (
                    <tr key={n.id} className="border-b last:border-0">
                      <td className="px-3 py-2">
                        <div className="font-medium">{n.label || n.fact_key || n.id}</div>
                        {n.summary ? (
                          <div className="text-muted-foreground mt-0.5 max-w-[420px] truncate text-xs">{n.summary}</div>
                        ) : null}
                        <div className="text-muted-foreground mt-0.5 font-mono text-[10px]">{n.fact_key || n.id}</div>
                      </td>
                      <td className="px-3 py-2">
                        {n.category ? (
                          <Badge variant="outline" className={cn("text-[10px]", CATEGORY_STYLE[n.category])}>
                            {n.category}
                          </Badge>
                        ) : (
                          "—"
                        )}
                      </td>
                      <td className="text-muted-foreground px-3 py-2 text-xs">{n.type || "—"}</td>
                      <td className="px-3 py-2">
                        {n.confidence ? (
                          <Badge variant="outline" className={cn("text-[10px]", CONFIDENCE_STYLE[n.confidence])}>
                            {n.confidence}
                          </Badge>
                        ) : (
                          "—"
                        )}
                      </td>
                      <td className="px-3 py-2 text-xs">{n.pinned ? "✓" : "—"}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            <GitBranchIcon className="size-4" /> 关联边
            <Badge variant="secondary" className="text-[10px]">
              {edges.length}
            </Badge>
          </CardTitle>
          <CardDescription>事实之间的推理关系（source → 关系 → target）。</CardDescription>
        </CardHeader>
        <CardContent>
          {edges.length === 0 ? (
            <div className="text-muted-foreground py-6 text-center text-sm">该项目暂无关联边</div>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b text-muted-foreground text-xs">
                    <th className="px-3 py-2 text-left font-medium">来源</th>
                    <th className="px-3 py-2 text-center font-medium">关系</th>
                    <th className="px-3 py-2 text-left font-medium">目标</th>
                    <th className="px-3 py-2 text-left font-medium">置信度</th>
                  </tr>
                </thead>
                <tbody>
                  {edges.map((e) => {
                    const s = nodes.find((n) => n.id === e.source);
                    const t = nodes.find((n) => n.id === e.target);
                    return (
                      <tr key={e.id} className="border-b last:border-0">
                        <td className="max-w-[220px] px-3 py-2">
                          <div className="truncate text-xs font-medium">{s?.label || e.source}</div>
                        </td>
                        <td className="px-3 py-2 text-center">
                          <div className="inline-flex items-center gap-1 rounded-full border bg-muted/40 px-2 py-0.5 text-[10px]">
                            <WorkflowIcon className="size-3" />
                            <span className="text-muted-foreground">{e.type || "related"}</span>
                          </div>
                        </td>
                        <td className="max-w-[220px] px-3 py-2">
                          <div className="truncate text-xs font-medium">{t?.label || e.target}</div>
                        </td>
                        <td className="px-3 py-2">
                          {e.confidence ? (
                            <Badge variant="outline" className={cn("text-[10px]", CONFIDENCE_STYLE[e.confidence])}>
                              {e.confidence}
                            </Badge>
                          ) : (
                            "—"
                          )}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )}
        </CardContent>
      </Card>
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
