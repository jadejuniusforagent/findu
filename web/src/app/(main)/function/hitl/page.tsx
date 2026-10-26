"use client";

// HITL 人工审批面板（旧 findu UI hitl.js 域迁入控制台，单 UI 定案第一批）。
// 后端：/findu/api/hitl/*（findu gin protected，Phase R1/R2 已宿主桥持有）——
// pending/logs 为普通 GET query 路由，decision/dismiss 为 JSON POST。

import * as React from "react";
import { toast } from "sonner";
import { CheckCircle2Icon, ClockIcon, RefreshCwIcon, ShieldQuestionIcon, Trash2Icon, XCircleIcon } from "lucide-react";

import { CodeBlock } from "@/components/code-block";
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
import { Textarea } from "@/components/ui/textarea";
import { finduErrorMessage, finduFetch, finduPost, formatTime } from "@/lib/findu";

type HitlItem = {
  id: string;
  conversationId: string;
  messageId?: string;
  mode: string;
  toolName: string;
  toolCallId?: string;
  payload?: string;
  status: string;
  reviewer?: string;
  decision?: string;
  comment?: string;
  decidedBy?: string;
  createdAt: string;
  decidedAt?: string;
};

type ListResp = { items: HitlItem[]; page: number; pageSize: number; total: number; retentionDays?: number };

const STATUS_STYLE: Record<string, string> = {
  pending: "border-amber-500/40 text-amber-600",
  resolved: "border-emerald-500/40 text-emerald-600",
  cancelled: "border-slate-400/40 text-slate-500",
};

const DECISION_STYLE: Record<string, string> = {
  approve: "border-emerald-500/40 text-emerald-600",
  reject: "border-rose-500/40 text-rose-600",
};

function prettyPayload(raw?: string): string {
  if (!raw) return "-";
  try {
    const obj = JSON.parse(raw);
    return JSON.stringify(obj, null, 2);
  } catch {
    return raw;
  }
}

export default function Page() {
  const [tab, setTab] = React.useState<"pending" | "logs">("pending");
  const [pending, setPending] = React.useState<ListResp | null>(null);
  const [logs, setLogs] = React.useState<ListResp | null>(null);
  const [loading, setLoading] = React.useState(false);

  // 决策对话框状态
  const [decideFor, setDecideFor] = React.useState<HitlItem | null>(null);
  const [decision, setDecision] = React.useState<"approve" | "reject">("approve");
  const [comment, setComment] = React.useState("");
  const [submitting, setSubmitting] = React.useState(false);

  const load = React.useCallback(async () => {
    setLoading(true);
    try {
      const [p, l] = await Promise.all([
        finduFetch<ListResp>("/hitl/pending?page=1&pageSize=50"),
        finduFetch<ListResp>("/hitl/logs?page=1&pageSize=50"),
      ]);
      setPending(p);
      setLogs(l);
    } catch (e) {
      toast.error("读取 HITL 数据失败：" + finduErrorMessage(e));
    } finally {
      setLoading(false);
    }
  }, []);

  React.useEffect(() => {
    void load();
    // 审批队列 30 秒自动轮询：等待审批的工具调用对时效敏感。
    const t = setInterval(() => void load(), 30_000);
    return () => clearInterval(t);
  }, [load]);

  const openDecide = (item: HitlItem, d: "approve" | "reject") => {
    setDecideFor(item);
    setDecision(d);
    setComment("");
  };

  const submitDecision = async () => {
    if (!decideFor) return;
    setSubmitting(true);
    try {
      await finduPost("/hitl/decision", {
        interruptId: decideFor.id,
        decision,
        comment: comment || undefined,
      });
      toast.success(`${decision === "approve" ? "已批准" : "已拒绝"}：${decideFor.toolName}`);
      setDecideFor(null);
      void load();
    } catch (e) {
      toast.error("提交决策失败：" + finduErrorMessage(e));
    } finally {
      setSubmitting(false);
    }
  };

  const dismiss = async (item: HitlItem) => {
    try {
      await finduPost("/hitl/dismiss", { interruptId: item.id });
      toast.success("已取消该审批请求（按拒绝处理）");
      void load();
    } catch (e) {
      toast.error("取消失败：" + finduErrorMessage(e));
    }
  };

  const clearLogs = async () => {
    try {
      await finduFetch<unknown>("/hitl/logs", { method: "DELETE" });
      toast.success("历史日志已清空");
      void load();
    } catch (e) {
      toast.error("清空失败：" + finduErrorMessage(e));
    }
  };

  const pendingItems = pending?.items ?? [];
  const logItems = logs?.items ?? [];

  return (
    <div className="flex h-[calc(100dvh-6rem)] min-h-0 flex-col gap-4 md:h-[calc(100dvh-7rem)] md:gap-6">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h1 className="flex items-center gap-2 text-xl font-semibold tracking-tight">
            <ShieldQuestionIcon className="size-5" /> HITL 审批
          </h1>
          <p className="text-muted-foreground text-sm">
            人工介入审批队列 · 代理执行敏感工具前的中断确认 · 每 30 秒自动刷新
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" onClick={() => void load()} disabled={loading}>
            <RefreshCwIcon className="size-4" /> 刷新
          </Button>
          {tab === "logs" && logItems.length > 0 ? (
            <Button variant="outline" onClick={() => void clearLogs()}>
              <Trash2Icon className="size-4" /> 清空日志
            </Button>
          ) : null}
        </div>
      </div>

      <div className="flex items-center gap-2">
        <Button
          variant={tab === "pending" ? "default" : "outline"}
          size="sm"
          onClick={() => setTab("pending")}
        >
          待审批 {pendingItems.length > 0 ? `(${pendingItems.length})` : ""}
        </Button>
        <Button variant={tab === "logs" ? "default" : "outline"} size="sm" onClick={() => setTab("logs")}>
          历史日志 {logItems.length > 0 ? `(${logItems.length})` : ""}
        </Button>
      </div>

      {tab === "pending" ? (
        pendingItems.length === 0 ? (
          <Card>
            <CardContent className="text-muted-foreground py-16 text-center text-sm">
              {loading ? "加载中…" : "没有等待审批的请求。代理遇到需人工确认的工具调用时会出现在这里。"}
            </CardContent>
          </Card>
        ) : (
          <div className="flex flex-col gap-3">
            {pendingItems.map((it) => (
              <Card key={it.id}>
                <CardHeader>
                  <div className="flex items-center justify-between gap-3">
                    <CardTitle className="flex items-center gap-2 font-mono text-sm">
                      <ClockIcon className="size-4 text-amber-500" />
                      {it.toolName || "-"}
                      <Badge variant="outline" className="px-1 py-0 text-[10px]">
                        {it.mode}
                      </Badge>
                    </CardTitle>
                    <Badge variant="outline" className={STATUS_STYLE[it.status] ?? ""}>
                      {it.status}
                    </Badge>
                  </div>
                  <CardDescription>
                    会话 {it.conversationId.slice(0, 8)} · 发起于 {formatTime(it.createdAt)}
                  </CardDescription>
                </CardHeader>
                <CardContent className="flex flex-col gap-3">
                  <CodeBlock code={prettyPayload(it.payload)} maxHeight="max-h-40" />
                  <div className="flex items-center gap-2">
                    <Button size="sm" onClick={() => openDecide(it, "approve")}>
                      <CheckCircle2Icon className="size-4" /> 批准执行
                    </Button>
                    <Button size="sm" variant="destructive" onClick={() => openDecide(it, "reject")}>
                      <XCircleIcon className="size-4" /> 拒绝
                    </Button>
                    <Button size="sm" variant="ghost" onClick={() => void dismiss(it)}>
                      取消请求
                    </Button>
                  </div>
                </CardContent>
              </Card>
            ))}
          </div>
        )
      ) : (
        <Card>
          <CardContent className="p-0">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>工具</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead>决策</TableHead>
                  <TableHead>备注</TableHead>
                  <TableHead>发起</TableHead>
                  <TableHead>处置</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {logItems.length === 0 ? (
                  <TableRow>
                    <TableCell colSpan={6} className="text-muted-foreground py-10 text-center text-sm">
                      {loading ? "加载中…" : "暂无审批历史"}
                    </TableCell>
                  </TableRow>
                ) : (
                  logItems.map((it) => (
                    <TableRow key={it.id}>
                      <TableCell className="font-mono text-xs">{it.toolName || "-"}</TableCell>
                      <TableCell>
                        <Badge variant="outline" className={STATUS_STYLE[it.status] ?? ""}>
                          {it.status}
                        </Badge>
                      </TableCell>
                      <TableCell>
                        {it.decision ? (
                          <Badge variant="outline" className={DECISION_STYLE[it.decision] ?? ""}>
                            {it.decision}
                          </Badge>
                        ) : (
                          <span className="text-muted-foreground text-xs">-</span>
                        )}
                      </TableCell>
                      <TableCell className="max-w-64 truncate text-xs" title={it.comment || ""}>
                        {it.comment || "-"}
                      </TableCell>
                      <TableCell className="text-xs">{formatTime(it.createdAt)}</TableCell>
                      <TableCell className="text-xs">
                        {it.decidedAt ? `${formatTime(it.decidedAt)} · ${it.decidedBy || "-"}` : "-"}
                      </TableCell>
                    </TableRow>
                  ))
                )}
              </TableBody>
            </Table>
          </CardContent>
        </Card>
      )}

      <Dialog open={decideFor !== null} onOpenChange={(o) => !o && setDecideFor(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {decision === "approve" ? "批准执行" : "拒绝执行"} · {decideFor?.toolName}
            </DialogTitle>
            <DialogDescription>
              决策将即时返回给等待中的代理（同一中断只可决策一次）。会话 {decideFor?.conversationId.slice(0, 8)}
            </DialogDescription>
          </DialogHeader>
          <div className="flex flex-col gap-3">
            <div className="text-muted-foreground text-xs">调用参数</div>
            <CodeBlock code={prettyPayload(decideFor?.payload)} maxHeight="max-h-40" />
            <div className="text-muted-foreground text-xs">备注（可选，会写入审批日志）</div>
            <Textarea
              value={comment}
              onChange={(e) => setComment(e.target.value)}
              placeholder={decision === "approve" ? "例如：确认本次操作范围" : "例如：目标不在授权范围"}
              rows={3}
            />
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDecideFor(null)} disabled={submitting}>
              返回
            </Button>
            <Button
              variant={decision === "approve" ? "default" : "destructive"}
              onClick={() => void submitDecision()}
              disabled={submitting}
            >
              {submitting ? "提交中…" : decision === "approve" ? "确认批准" : "确认拒绝"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
