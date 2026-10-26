"use client";

// 知识库管理（旧 findu UI knowledge.js 域迁入控制台，U6 收尾续）。
// 读侧 7 条已宿主原生（server/knowledge_native.go，Phase B7）：categories/items/
// items:{id}/retrieval-logs/stats + 删除项/删除日志；写侧（scan/index/search/
// items create-update）仍走 findu 兼容桥。本页聚焦分类条目浏览 + 检索日志，管理视角。

import * as React from "react";

import {
  AlertTriangleIcon,
  BookOpenTextIcon,
  DatabaseIcon,
  EyeIcon,
  FileTextIcon,
  ListChecksIcon,
  RefreshCwIcon,
  ScrollTextIcon,
  Trash2Icon,
} from "lucide-react";
import { toast } from "sonner";

import { CodeBlock } from "@/components/code-block";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import {
  dashboardApi,
  type KnowledgeItem,
  type KnowledgeItemSummary,
  type KnowledgeItemsResp,
  type KnowledgeRetrievalLog,
} from "@/lib/dashboard-api";
import { formatTime } from "@/lib/findu";
import { cn } from "@/lib/utils";

export default function KnowledgePage() {
  const [tab, setTab] = React.useState<"items" | "logs">("items");
  const [stats, setStats] = React.useState<{ enabled: boolean; total_categories: number; total_items: number } | null>(
    null,
  );
  const [items, setItems] = React.useState<KnowledgeItemsResp | null>(null);
  const [logs, setLogs] = React.useState<KnowledgeRetrievalLog[] | null>(null);
  const [loading, setLoading] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  // 条目详情
  const [viewing, setViewing] = React.useState<KnowledgeItem | null>(null);
  const [viewLoading, setViewLoading] = React.useState(false);

  // 删除确认
  const [deleting, setDeleting] = React.useState<{ kind: "item" | "log"; id: string; label: string } | null>(null);
  const [deletingLoading, setDeletingLoading] = React.useState(false);

  const loadAll = React.useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const [s, itemsRes, logsRes] = await Promise.allSettled([
        dashboardApi.knowledgeStats(),
        dashboardApi.knowledgeItems(100),
        dashboardApi.knowledgeRetrievalLogs(100),
      ]);
      if (s.status === "fulfilled") setStats(s.value);
      if (itemsRes.status === "fulfilled") setItems(itemsRes.value);
      if (logsRes.status === "fulfilled") setLogs(logsRes.value.logs ?? []);
      const errs: string[] = [];
      if (s.status === "rejected") errs.push("统计");
      if (itemsRes.status === "rejected") errs.push("条目");
      if (logsRes.status === "rejected") errs.push("检索日志");
      if (errs.length) setError(`部分数据源不可用：${errs.join("、")}`);
    } finally {
      setLoading(false);
    }
  }, []);

  React.useEffect(() => {
    void loadAll();
    const t = setInterval(() => void loadAll(), 30000);
    return () => clearInterval(t);
  }, [loadAll]);

  const allItems = React.useMemo(() => (items?.categories ?? []).flatMap((c) => c.items ?? []), [items]);

  const handleView = async (id: string) => {
    setViewLoading(true);
    try {
      setViewing(await dashboardApi.knowledgeItem(id));
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setViewLoading(false);
    }
  };

  const handleDelete = async () => {
    if (!deleting) return;
    setDeletingLoading(true);
    try {
      if (deleting.kind === "item") {
        await dashboardApi.knowledgeDeleteItem(deleting.id);
        toast.success("条目已删除");
      } else {
        await dashboardApi.knowledgeDeleteLog(deleting.id);
        toast.success("检索日志已删除");
      }
      setDeleting(null);
      await loadAll();
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setDeletingLoading(false);
    }
  };

  const allLogs = logs ?? [];

  return (
    <div className="flex flex-1 flex-col gap-4">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h1 className="flex items-center gap-2 text-xl font-semibold tracking-tight">
            <DatabaseIcon className="size-5" /> 知识库
          </h1>
          <p className="text-muted-foreground text-sm">
            知识库分类条目与检索日志
          </p>
        </div>
        <Button variant="outline" onClick={() => void loadAll()} disabled={loading}>
          <RefreshCwIcon className={cn("size-4", loading && "animate-spin")} /> 刷新
        </Button>
      </div>

      {error ? (
        <Card className="border-amber-500/40 bg-amber-500/5">
          <CardContent className="flex items-start gap-2 py-3 text-sm">
            <AlertTriangleIcon className="mt-0.5 size-4 shrink-0 text-amber-500" />
            <div className="font-medium">{error}</div>
          </CardContent>
        </Card>
      ) : null}

      <div className="grid gap-3 sm:grid-cols-3">
        <StatChip title="分类" value={stats?.total_categories ?? 0} icon={BookOpenTextIcon} />
        <StatChip title="条目" value={stats?.total_items ?? 0} icon={FileTextIcon} />
        <StatChip
          title="状态"
          value={stats ? (stats.enabled ? "已启用" : "已停用") : "-"}
          accent={stats?.enabled ? "text-emerald-600" : "text-slate-500"}
          icon={ListChecksIcon}
        />
      </div>

      <div className="flex items-center gap-1 border-b">
        <button
          type="button"
          onClick={() => setTab("items")}
          className={cn(
            "border-b-2 px-3 py-2 text-sm font-medium",
            tab === "items"
              ? "border-primary text-foreground"
              : "border-transparent text-muted-foreground hover:text-foreground",
          )}
        >
          分类条目（{allItems.length}）
        </button>
        <button
          type="button"
          onClick={() => setTab("logs")}
          className={cn(
            "border-b-2 px-3 py-2 text-sm font-medium",
            tab === "logs"
              ? "border-primary text-foreground"
              : "border-transparent text-muted-foreground hover:text-foreground",
          )}
        >
          检索日志（{allLogs.length}）
        </button>
      </div>

      {tab === "items" ? (
        <div className="flex flex-col gap-3">
          {loading && !items ? (
            <div className="text-muted-foreground py-6 text-center text-sm">加载中…</div>
          ) : allItems.length === 0 ? (
            <div className="text-muted-foreground py-6 text-center text-sm">暂无知识条目</div>
          ) : (
            (items?.categories ?? []).map((cat) => (
              <Card key={cat.category}>
                <CardHeader className="pb-2">
                  <CardTitle className="flex items-center justify-between text-base">
                    <span className="flex items-center gap-2">
                      <BookOpenTextIcon className="size-4" /> {cat.category}
                    </span>
                    <Badge variant="secondary">{cat.itemCount} 条</Badge>
                  </CardTitle>
                </CardHeader>
                <CardContent>
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>标题</TableHead>
                        <TableHead>文件路径</TableHead>
                        <TableHead className="w-32">更新时间</TableHead>
                        <TableHead className="w-32 text-right">操作</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {(cat.items ?? []).map((it) => (
                        <TableRow key={it.id}>
                          <TableCell>
                            <span className="font-medium">{it.cn_title || it.title || it.filePath}</span>
                            {it.cn_title && it.title ? (
                              <span className="ml-2 font-mono text-[10px] text-muted-foreground">{it.title}</span>
                            ) : null}
                          </TableCell>
                          <TableCell className="text-muted-foreground text-xs">{it.filePath}</TableCell>
                          <TableCell className="text-muted-foreground text-xs">{formatTime(it.updatedAt)}</TableCell>
                          <TableCell className="text-right">
                            <div className="flex justify-end gap-1">
                              <Button variant="ghost" size="sm" onClick={() => void handleView(it.id)}>
                                <EyeIcon className="size-3.5" /> 查看
                              </Button>
                              <Button
                                variant="ghost"
                                size="sm"
                                className="text-rose-500 hover:bg-rose-500/10 hover:text-rose-600"
                                onClick={() => setDeleting({ kind: "item", id: it.id, label: it.title || it.filePath })}
                              >
                                <Trash2Icon className="size-3.5" />
                              </Button>
                            </div>
                          </TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                </CardContent>
              </Card>
            ))
          )}
        </div>
      ) : (
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <ScrollTextIcon className="size-4" /> 检索日志
            </CardTitle>
            <CardDescription>知识检索历史（query / riskType / 命中条目）。</CardDescription>
          </CardHeader>
          <CardContent>
            {loading && !logs ? (
              <div className="text-muted-foreground py-6 text-center text-sm">加载中…</div>
            ) : allLogs.length === 0 ? (
              <div className="text-muted-foreground py-6 text-center text-sm">暂无检索日志</div>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>查询</TableHead>
                    <TableHead className="w-28">风险类型</TableHead>
                    <TableHead>命中条目</TableHead>
                    <TableHead className="w-32">时间</TableHead>
                    <TableHead className="w-16 text-right">操作</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {allLogs.map((log) => (
                    <TableRow key={log.id}>
                      <TableCell className="max-w-[280px] truncate font-medium">{log.query}</TableCell>
                      <TableCell>
                        {log.riskType ? (
                          <Badge variant="outline">{log.riskType}</Badge>
                        ) : (
                          <span className="text-muted-foreground">-</span>
                        )}
                      </TableCell>
                      <TableCell className="text-muted-foreground text-xs">
                        {(log.retrievedItems ?? []).join("、") || "-"}
                      </TableCell>
                      <TableCell className="text-muted-foreground text-xs">{formatTime(log.createdAt)}</TableCell>
                      <TableCell className="text-right">
                        <Button
                          variant="ghost"
                          size="sm"
                          className="text-rose-500 hover:bg-rose-500/10 hover:text-rose-600"
                          onClick={() => setDeleting({ kind: "log", id: log.id, label: log.query })}
                        >
                          <Trash2Icon className="size-3.5" />
                        </Button>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </CardContent>
        </Card>
      )}

      <Dialog open={viewing !== null} onOpenChange={(o) => !o && setViewing(null)}>
        <DialogContent className="max-h-[80vh] overflow-y-auto sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>{viewing?.title || viewing?.filePath || "条目详情"}</DialogTitle>
            <DialogDescription>{viewing?.category ? `分类：${viewing.category}` : "知识条目"}</DialogDescription>
          </DialogHeader>
          {viewLoading ? (
            <div className="text-muted-foreground py-6 text-center text-sm">加载中…</div>
          ) : (
            <CodeBlock code={viewing?.content || "（无内容）"} maxHeight="max-h-[50vh]" />
          )}
          <DialogFooter>
            <Button variant="outline" onClick={() => setViewing(null)}>
              关闭
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <AlertDialog open={deleting !== null} onOpenChange={(o) => !o && setDeleting(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              删除{deleting?.kind === "item" ? "条目" : "检索日志"}「{deleting?.label ?? ""}」？
            </AlertDialogTitle>
            <AlertDialogDescription>
              {deleting?.kind === "item"
                ? "将从知识库删除该条目及其向量索引，不可恢复。"
                : "将从检索台账删除该记录，不可恢复。"}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={deletingLoading}>取消</AlertDialogCancel>
            <AlertDialogAction
              onClick={(e) => {
                e.preventDefault();
                void handleDelete();
              }}
              disabled={deletingLoading}
              className="bg-rose-600 text-white hover:bg-rose-700"
            >
              {deletingLoading ? "删除中…" : "删除"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

function StatChip({
  title,
  value,
  icon: Icon,
  accent,
}: {
  title: string;
  value: number | string;
  icon: React.ComponentType<{ className?: string }>;
  accent?: string;
}) {
  return (
    <Card className="py-4">
      <CardHeader className="px-4">
        <CardTitle className="text-muted-foreground flex items-center justify-between text-xs font-medium">
          <span>{title}</span>
          <Icon className="size-4" />
        </CardTitle>
      </CardHeader>
      <CardContent className="px-4">
        <div className={cn("text-2xl font-semibold tabular-nums", accent)}>{value}</div>
      </CardContent>
    </Card>
  );
}
