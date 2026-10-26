"use client";

// 技能候选审核（旧 findu UI skills/candidates 域迁入控制台，U6 收尾续·auth 修复后）。
// 后端 GET /api/skills/candidates（走 findu 兜底，auth 修复后宿主 Bearer 直连 200）——
// 返回 { candidates:[{skillName,source,capability,evidence,occurrences,score,status,hasDraft,...}], count }。
// 支持查看详情 / 生成草稿 / 批准 / 拒绝（POST /api/skills/candidates/{id}/draft|approve|reject）。

import { useCallback, useEffect, useState } from "react";

import {
  AlertTriangleIcon,
  CheckIcon,
  FileEditIcon,
  FilePlus2Icon,
  RefreshCwIcon,
  SparklesIcon,
  XIcon,
} from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { dashboardApi, type SkillCandidate } from "@/lib/dashboard-api";
import { cn } from "@/lib/utils";

const STATUS_STYLE: Record<string, string> = {
  new: "border-sky-500/40 text-sky-600",
  drafted: "border-amber-500/40 text-amber-600",
  approved: "border-emerald-500/40 text-emerald-600",
  rejected: "border-rose-500/40 text-rose-600",
};

const SOURCE_LABEL: Record<string, string> = {
  tool_failure: "工具失败",
  skill_miss: "技能缺失",
};

export default function SkillCandidatesPage() {
  const [candidates, setCandidates] = useState<SkillCandidate[]>([]);
  const [count, setCount] = useState(0);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [acting, setActing] = useState<string | null>(null);
  const [actionMsg, setActionMsg] = useState<string | null>(null);
  const [detail, setDetail] = useState<SkillCandidate | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const res = await dashboardApi.skillCandidates();
      setCandidates(res.candidates ?? []);
      setCount(res.count ?? (res.candidates ?? []).length);
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

  const act = async (c: SkillCandidate, action: "draft" | "approve" | "reject") => {
    setActing(`${c.id}:${action}`);
    setActionMsg(null);
    try {
      const res = await dashboardApi.skillCandidateAction(c.id, action);
      setActionMsg(`${c.skillName} ${action}: ${res.message ?? "ok"}`);
      await load();
    } catch (e) {
      setActionMsg(`操作失败：${(e as Error).message}`);
    } finally {
      setActing(null);
    }
  };

  const drafted = candidates.filter((c) => c.hasDraft || c.status === "drafted").length;

  return (
    <div className="flex flex-1 flex-col gap-4">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h1 className="flex items-center gap-2 font-semibold text-xl tracking-tight">
            <SparklesIcon className="size-5" /> 技能候选审核
          </h1>
          <p className="text-muted-foreground text-sm">
            模型运行中沉淀的候选技能（工具失败 / 技能缺失）。可生成草稿、批准或拒绝。
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

      {actionMsg ? (
        <Card className="border-sky-500/30 bg-sky-500/5">
          <CardContent className="py-2 text-sm">{actionMsg}</CardContent>
        </Card>
      ) : null}

      <div className="grid gap-3 sm:grid-cols-3">
        <StatChip title="候选数" value={count} accent="text-sky-600" />
        <StatChip title="已有草稿" value={drafted} accent="text-amber-600" />
        <StatChip
          title="技能缺失"
          value={candidates.filter((c) => c.source === "skill_miss").length}
          accent="text-violet-600"
        />
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            候选列表
            <Badge variant="secondary" className="text-[10px]">
              {candidates.length}
            </Badge>
          </CardTitle>
          <CardDescription>按更新时间倒序展示。点击「查看」看完整证据。</CardDescription>
        </CardHeader>
        <CardContent>
          {loading && candidates.length === 0 ? (
            <div className="py-6 text-center text-muted-foreground text-sm">加载中…</div>
          ) : null}
          {!loading && candidates.length === 0 ? (
            <div className="py-6 text-center text-muted-foreground text-sm">暂无技能候选</div>
          ) : null}
          {!loading && candidates.length > 0 ? (
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b text-muted-foreground text-xs">
                    <th className="px-3 py-2 text-left font-medium">技能</th>
                    <th className="px-3 py-2 text-left font-medium">来源</th>
                    <th className="px-3 py-2 text-left font-medium">能力描述</th>
                    <th className="px-3 py-2 text-center font-medium">出现</th>
                    <th className="px-3 py-2 text-center font-medium">得分</th>
                    <th className="px-3 py-2 text-left font-medium">状态</th>
                    <th className="px-3 py-2 text-left font-medium">更新</th>
                    <th className="px-3 py-2 text-right font-medium">操作</th>
                  </tr>
                </thead>
                <tbody>
                  {candidates.map((c) => (
                    <tr key={c.id} className="border-b last:border-0">
                      <td className="px-3 py-2">
                        <div className="font-medium">{c.skillName}</div>
                        <div className="font-mono text-[10px] text-muted-foreground">{c.id}</div>
                      </td>
                      <td className="px-3 py-2">
                        <Badge variant="outline" className="text-[10px]">
                          {SOURCE_LABEL[c.source ?? ""] ?? c.source}
                        </Badge>
                      </td>
                      <td className="max-w-[260px] px-3 py-2">
                        <div className="line-clamp-2 text-xs">{c.capability || "—"}</div>
                      </td>
                      <td className="px-3 py-2 text-center text-xs">{c.occurrences ?? 0}</td>
                      <td className="px-3 py-2 text-center text-xs">{c.score ?? 0}</td>
                      <td className="px-3 py-2">
                        <Badge variant="outline" className={cn("text-[10px]", STATUS_STYLE[c.status ?? ""] ?? "")}>
                          {c.hasDraft || c.status === "drafted" ? "已草稿" : (c.status ?? "new")}
                        </Badge>
                      </td>
                      <td className="px-3 py-2 text-muted-foreground text-xs">
                        {c.updatedAt ? new Date(c.updatedAt).toLocaleString() : "—"}
                      </td>
                      <td className="px-3 py-2 text-right">
                        <div className="flex justify-end gap-1">
                          <Button
                            variant="outline"
                            size="sm"
                            className="h-7 gap-1 text-xs"
                            onClick={() => setDetail(c)}
                          >
                            <FileEditIcon className="size-3.5" /> 查看
                          </Button>
                          {!c.hasDraft && c.status !== "drafted" ? (
                            <Button
                              variant="outline"
                              size="sm"
                              className="h-7 gap-1 text-xs"
                              disabled={acting !== null}
                              onClick={() => void act(c, "draft")}
                            >
                              <FilePlus2Icon className="size-3.5" /> 草稿
                            </Button>
                          ) : null}
                          <Button
                            variant="outline"
                            size="sm"
                            className="h-7 gap-1 text-emerald-600 text-xs"
                            disabled={acting !== null}
                            onClick={() => void act(c, "approve")}
                          >
                            <CheckIcon className="size-3.5" /> 批准
                          </Button>
                          <Button
                            variant="outline"
                            size="sm"
                            className="h-7 gap-1 text-rose-600 text-xs"
                            disabled={acting !== null}
                            onClick={() => void act(c, "reject")}
                          >
                            <XIcon className="size-3.5" /> 拒绝
                          </Button>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : null}
        </CardContent>
      </Card>

      {detail ? <DetailDialog c={detail} onClose={() => setDetail(null)} /> : null}
    </div>
  );
}

function DetailDialog({ c, onClose }: { c: SkillCandidate; onClose: () => void }) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4">
      <button
        type="button"
        aria-label="关闭"
        className="absolute inset-0 cursor-default"
        tabIndex={-1}
        onClick={onClose}
      />
      <div className="w-full max-w-2xl rounded-lg border bg-background p-5 shadow-lg">
        <div className="flex items-start justify-between gap-3">
          <div>
            <h2 className="flex items-center gap-2 font-semibold text-lg">
              <SparklesIcon className="size-4" /> {c.skillName}
            </h2>
            <p className="font-mono text-[10px] text-muted-foreground">{c.id}</p>
          </div>
          <Button variant="ghost" size="sm" onClick={onClose}>
            <XIcon className="size-4" />
          </Button>
        </div>
        <div className="mt-4 space-y-3 text-sm">
          <Row
            label="来源"
            value={`${SOURCE_LABEL[c.source ?? ""] ?? c.source ?? "—"} · 出现 ${c.occurrences ?? 0} 次 · 得分 ${c.score ?? 0}`}
          />
          <Row
            label="状态"
            value={`${c.hasDraft || c.status === "drafted" ? "已草稿" : (c.status ?? "new")}${c.draftModel ? `（模型 ${c.draftModel}）` : ""}`}
          />
          <Row label="能力描述" value={c.capability ?? "—"} />
          <Row label="证据" value={c.evidence ?? "—"} mono />
          <Row label="来源引用" value={c.sourceRef ?? "—"} mono />
          <Row label="项目" value={c.projectId ?? "—"} mono />
          <Row label="更新时间" value={c.updatedAt ? new Date(c.updatedAt).toLocaleString() : "—"} />
        </div>
      </div>
    </div>
  );
}

function Row({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="grid grid-cols-1 gap-0.5">
      <span className="text-muted-foreground text-xs">{label}</span>
      <span className={cn("text-sm", mono && "font-mono text-xs")}>{value}</span>
    </div>
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
