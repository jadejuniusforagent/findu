"use client";

import * as React from "react";

import { BugIcon, RefreshCwIcon, SearchIcon } from "lucide-react";

import { CodeBlock } from "@/components/code-block";
import { Markdown } from "@/components/markdown";
import { TablePagination } from "@/components/table-pagination";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api } from "@/lib/api";
import { formatTime } from "@/lib/findu";
import type { Finding, FindingStatus, Severity } from "@/lib/types";
import { cn } from "@/lib/utils";

const SEVERITY_LABEL: Record<Severity, string> = {
  critical: "严重",
  high: "高危",
  medium: "中危",
  low: "低危",
};

const STATUS_LABEL: Record<FindingStatus, string> = {
  pending: "待处理",
  in_progress: "处理中",
  confirmed: "已确认",
  resolved: "已处理",
  fixed: "已修复",
  false_positive: "误报",
  ignored: "忽略",
  duplicate: "重复",
  risk_accepted: "风险接受",
};

function severityVariant(severity: Severity): "destructive" | "default" | "secondary" | "outline" {
  if (severity === "critical") return "destructive";
  if (severity === "high") return "default";
  if (severity === "medium") return "secondary";
  return "outline";
}

function statusVariant(status: FindingStatus): "destructive" | "default" | "secondary" | "outline" {
  if (status === "confirmed") return "default";
  if (status === "pending" || status === "in_progress") return "outline";
  if (status === "false_positive" || status === "ignored" || status === "duplicate") return "secondary";
  return "secondary";
}

// ---- 漏洞分类推导（前端，不改后端）----
// vulnclass/name → OWASP Top 10 2021 映射。无命中则不展示 OWASP 徽标。
const OWASP_MAP: { code: string; label: string; keys: string[] }[] = [
  {
    code: "A01",
    label: "访问控制失效",
    keys: ["越权", "未授权", "访问控制", "目录遍历", "路径穿越", "任意文件", "Broken Access Control"],
  },
  {
    code: "A02",
    label: "加密机制失效",
    keys: ["弱口令", "弱密码", "明文", "加密", "密码", "凭证", "凭据", "Cryptographic"],
  },
  {
    code: "A03",
    label: "注入",
    keys: [
      "注入",
      "sql",
      "xss",
      "命令执行",
      "rce",
      "模板注入",
      "ssti",
      "ldap",
      "xxe",
      "injection",
      "upload",
      "文件上传",
      "文件读取",
    ],
  },
  { code: "A05", label: "安全配置错误", keys: ["配置", "错误配置", "misconfig"] },
  { code: "A06", label: "易受攻击过时组件", keys: ["组件", "cve", "版本", "过时", "依赖", "outdated", "vulnerable"] },
  {
    code: "A07",
    label: "身份认证失败",
    keys: ["认证", "登录", "会话", "cookie", "验证码", "爆破", "认证绕过", "session"],
  },
  { code: "A08", label: "数据完整性失效", keys: ["反序列化", "deserial", "完整性"] },
  { code: "A09", label: "安全日志监控失效", keys: ["日志", "监控", "log", "audit"] },
  { code: "A10", label: "服务端请求伪造", keys: ["ssrf", "请求伪造"] },
];

function owaspOf(s: string): { code: string; label: string } | null {
  const t = (s || "").toLowerCase();
  for (const m of OWASP_MAP) {
    if (m.keys.some((k) => t.includes(k.toLowerCase()))) return { code: m.code, label: m.label };
  }
  return null;
}

// 从 name/vulnclass/summary/report 等文本提取 CVE 编号。
function extractCves(...texts: (string | undefined)[]): string[] {
  const set = new Set<string>();
  const re = /CVE-\d{4}-\d{4,}/gi;
  for (const t of texts) {
    for (const m of (t ?? "").matchAll(re)) set.add(m[0].toUpperCase());
  }
  return [...set];
}

// 验证是否通过：由处置状态近似推导（无独立 verified 字段）。
const VERDICT: Record<string, { label: string; ok?: boolean }> = {
  confirmed: { label: "已验证通过", ok: true },
  resolved: { label: "已处理(通过)", ok: true },
  fixed: { label: "已修复", ok: true },
  false_positive: { label: "验证未通过(误报)", ok: false },
  pending: { label: "未验证" },
  in_progress: { label: "验证中" },
  ignored: { label: "已忽略" },
  duplicate: { label: "重复" },
  risk_accepted: { label: "风险接受" },
};

export default function FindingsPage() {
  const [rows, setRows] = React.useState<Finding[]>([]);
  const [total, setTotal] = React.useState(0);
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState("");
  const [search, setSearch] = React.useState("");
  const [query, setQuery] = React.useState("");
  const [severity, setSeverity] = React.useState<"all" | Severity>("all");
  const [status, setStatus] = React.useState<"all" | FindingStatus>("all");
  const [sort, setSort] = React.useState<"time" | "severity">("time");
  const [page, setPage] = React.useState(1);
  const [pageSize, setPageSize] = React.useState(20);
  const [reload, setReload] = React.useState(0);

  const [selected, setSelected] = React.useState<Finding | null>(null);
  const [detail, setDetail] = React.useState<Finding | null>(null);
  const [detailLoading, setDetailLoading] = React.useState(false);
  const [detailError, setDetailError] = React.useState("");

  React.useEffect(() => {
    const t = setTimeout(() => {
      setQuery(search);
      setPage(1);
    }, 300);
    return () => clearTimeout(t);
  }, [search]);

  // biome-ignore lint/correctness/useExhaustiveDependencies: reload is the explicit refresh trigger.
  React.useEffect(() => {
    let alive = true;
    setLoading(true);
    setError("");
    api
      .findingsPage({ page, pageSize, severity, status, query, sort })
      .then((r) => {
        if (!alive) return;
        setRows(r.items ?? []);
        setTotal(r.total ?? 0);
      })
      .catch((e: Error) => {
        if (alive) setError(e.message);
      })
      .finally(() => {
        if (alive) setLoading(false);
      });
    return () => {
      alive = false;
    };
  }, [page, pageSize, severity, status, query, sort, reload]);

  async function open(f: Finding) {
    setSelected(f);
    setDetail(null);
    setDetailError("");
    setDetailLoading(true);
    try {
      setDetail(await api.getFinding(f.id, f.task_id));
    } catch (e) {
      setDetailError((e as Error).message);
    } finally {
      setDetailLoading(false);
    }
  }

  return (
    <div className="flex h-[calc(100dvh-6rem)] min-h-0 flex-col gap-4 md:h-[calc(100dvh-7rem)] md:gap-6">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h1 className="flex items-center gap-2 text-xl font-semibold tracking-tight">
            <BugIcon className="size-5" /> 漏洞
          </h1>
          <p className="text-muted-foreground text-sm">只读 · 已登记漏洞共 {total} 条</p>
        </div>
        <Button variant="outline" onClick={() => setReload((n) => n + 1)} disabled={loading}>
          <RefreshCwIcon className={cn("size-4", loading && "animate-spin")} /> 刷新
        </Button>
      </div>

      <Card>
        <CardContent className="flex flex-wrap items-center gap-2 py-3">
          <div className="relative min-w-64 flex-1">
            <SearchIcon className="text-muted-foreground absolute top-2.5 left-2.5 size-4" />
            <Input
              className="pl-8"
              placeholder="搜索漏洞名称、类型、摘要、证据…"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </div>
          <Select
            value={severity}
            onValueChange={(v) => {
              setSeverity(v as "all" | Severity);
              setPage(1);
            }}
          >
            <SelectTrigger className="w-32">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">全部等级</SelectItem>
              {(Object.keys(SEVERITY_LABEL) as Severity[]).map((s) => (
                <SelectItem key={s} value={s}>
                  {SEVERITY_LABEL[s]}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Select
            value={status}
            onValueChange={(v) => {
              setStatus(v as "all" | FindingStatus);
              setPage(1);
            }}
          >
            <SelectTrigger className="w-36">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">全部状态</SelectItem>
              {(Object.keys(STATUS_LABEL) as FindingStatus[]).map((s) => (
                <SelectItem key={s} value={s}>
                  {STATUS_LABEL[s]}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Select
            value={sort}
            onValueChange={(v) => {
              setSort(v as "time" | "severity");
              setPage(1);
            }}
          >
            <SelectTrigger className="w-32">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="time">按时间</SelectItem>
              <SelectItem value="severity">按等级</SelectItem>
            </SelectContent>
          </Select>
        </CardContent>
      </Card>

      {error ? (
        <Card className="border-destructive/40 bg-destructive/5">
          <CardContent className="text-destructive py-3 text-sm">漏洞列表加载失败：{error}</CardContent>
        </Card>
      ) : null}

      <Card className="min-h-0 flex-1 overflow-y-auto">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>漏洞</TableHead>
              <TableHead className="w-20">等级</TableHead>
              <TableHead className="w-28">状态</TableHead>
              <TableHead className="w-20">任务</TableHead>
              <TableHead className="w-40">资产</TableHead>
              <TableHead className="w-44">发现时间</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading ? (
              Array.from({ length: 8 }).map((_, i) => (
                <TableRow key={i}>
                  <TableCell colSpan={6}>
                    <Skeleton className="h-7 w-full" />
                  </TableCell>
                </TableRow>
              ))
            ) : rows.length === 0 ? (
              <TableRow>
                <TableCell colSpan={6} className="text-muted-foreground py-10 text-center text-sm">
                  {query || severity !== "all" || status !== "all" ? "没有匹配的漏洞" : "还没有漏洞"}
                </TableCell>
              </TableRow>
            ) : (
              rows.map((f) => (
                <TableRow key={f.id} className="cursor-pointer" onClick={() => void open(f)}>
                  <TableCell>
                    <div className="max-w-[520px] truncate font-medium">{f.name || f.vulnclass}</div>
                    <div className="text-muted-foreground max-w-[520px] truncate text-xs">{f.vulnclass}</div>
                    {(() => {
                      const ow = owaspOf(`${f.name ?? ""} ${f.vulnclass}`);
                      const cves = extractCves(f.name, f.vulnclass, f.summary);
                      if (!ow && cves.length === 0) return null;
                      return (
                        <div className="mt-1 flex max-w-[520px] flex-wrap gap-1">
                          {ow ? (
                            <Badge variant="outline" className="border-slate-400/40 text-[10px] text-muted-foreground">
                              OWASP {ow.code} {ow.label}
                            </Badge>
                          ) : null}
                          {cves.map((c) => (
                            <Badge
                              key={c}
                              variant="outline"
                              className="border-emerald-500/40 bg-emerald-500/10 font-mono text-[10px] text-emerald-600 dark:text-emerald-400"
                            >
                              {c}
                            </Badge>
                          ))}
                        </div>
                      );
                    })()}
                  </TableCell>
                  <TableCell>
                    <Badge variant={severityVariant(f.severity)} className="text-[10px]">
                      {SEVERITY_LABEL[f.severity] ?? f.severity}
                    </Badge>
                  </TableCell>
                  <TableCell>
                    <Badge variant={statusVariant(f.status)} className="text-[10px]">
                      {STATUS_LABEL[f.status] ?? f.status}
                    </Badge>
                  </TableCell>
                  <TableCell className="font-mono text-xs">{f.task_id || "-"}</TableCell>
                  <TableCell>
                    <div className="flex max-w-[240px] flex-wrap gap-1">
                      {(f.assets ?? []).slice(0, 3).map((a) => (
                        <Badge key={a.id} variant="outline" className="max-w-full truncate text-[10px]">
                          {a.label}
                        </Badge>
                      ))}
                      {(f.assets ?? []).length > 3 ? (
                        <span className="text-muted-foreground text-[10px]">+{(f.assets ?? []).length - 3}</span>
                      ) : null}
                      {(f.assets ?? []).length === 0 ? <span className="text-muted-foreground text-xs">-</span> : null}
                    </div>
                  </TableCell>
                  <TableCell className="text-muted-foreground text-xs">{formatTime(f.ts)}</TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
        <TablePagination
          page={page}
          pageSize={pageSize}
          total={total}
          pageSizeOptions={[20, 50, 100]}
          onPageChange={setPage}
          onPageSizeChange={(s) => {
            setPageSize(s);
            setPage(1);
          }}
        />
      </Card>

      <Sheet
        open={selected !== null}
        onOpenChange={(open) => {
          if (!open) {
            setSelected(null);
            setDetail(null);
            setDetailError("");
          }
        }}
      >
        <SheetContent className="w-full overflow-y-auto sm:max-w-3xl">
          <SheetHeader>
            <SheetTitle className="pr-8">{selected?.name || selected?.vulnclass}</SheetTitle>
            <SheetDescription>
              {selected?.vulnclass} · {formatTime(selected?.ts)}
            </SheetDescription>
          </SheetHeader>
          {detailLoading ? (
            <div className="text-muted-foreground py-10 text-center text-sm">加载漏洞详情…</div>
          ) : detailError ? (
            <div className="text-destructive mt-4 rounded-lg border border-destructive/40 p-3 text-sm">
              加载失败：{detailError}
            </div>
          ) : detail ? (
            <div className="mt-4 flex flex-col gap-4 text-sm">
              <div className="flex flex-wrap gap-1">
                <Badge variant={severityVariant(detail.severity)} className="text-[10px]">
                  {SEVERITY_LABEL[detail.severity] ?? detail.severity}
                </Badge>
                <Badge variant={statusVariant(detail.status)} className="text-[10px]">
                  {STATUS_LABEL[detail.status] ?? detail.status}
                </Badge>
                {(() => {
                  const v = VERDICT[detail.status];
                  if (!v) return null;
                  const cls =
                    v.ok === true
                      ? "border-emerald-500/40 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400"
                      : v.ok === false
                        ? "border-rose-500/40 bg-rose-500/10 text-rose-600 dark:text-rose-400"
                        : "";
                  return (
                    <Badge variant="outline" className={`text-[10px] ${cls}`}>
                      验证：{v.label}
                    </Badge>
                  );
                })()}
                {(() => {
                  const ow = owaspOf(`${detail.name ?? ""} ${detail.vulnclass}`);
                  if (!ow) return null;
                  return (
                    <Badge variant="outline" className="border-slate-400/40 text-[10px] text-muted-foreground">
                      OWASP {ow.code} {ow.label}
                    </Badge>
                  );
                })()}
                {extractCves(detail.name, detail.vulnclass, detail.summary, detail.report).map((c) => (
                  <Badge
                    key={c}
                    variant="outline"
                    className="border-emerald-500/40 bg-emerald-500/10 font-mono text-[10px] text-emerald-600 dark:text-emerald-400"
                  >
                    {c}
                  </Badge>
                ))}
                {detail.task_id ? (
                  <Badge variant="outline" className="text-[10px]">
                    任务 #{detail.task_id}
                  </Badge>
                ) : null}
                {(detail.assets ?? []).map((a) => (
                  <Badge key={a.id} variant="outline" className="text-[10px]">
                    {a.label}
                  </Badge>
                ))}
              </div>
              <div>
                <div className="text-muted-foreground mb-1 text-xs">摘要</div>
                {detail.summary ? <Markdown text={detail.summary} /> : <div>-</div>}
              </div>
              <div>
                <div className="text-muted-foreground mb-1 text-xs">证据</div>
                {detail.evidence ? <CodeBlock code={detail.evidence} maxHeight="max-h-96" /> : <div>-</div>}
              </div>
              {detail.report ? (
                <div>
                  <div className="text-muted-foreground mb-1 text-xs">详细报告</div>
                  <div className="rounded-lg border p-3">
                    <Markdown text={detail.report} />
                  </div>
                </div>
              ) : null}
            </div>
          ) : null}
        </SheetContent>
      </Sheet>
    </div>
  );
}
