"use client";

import * as React from "react";

import { BoxesIcon, RefreshCwIcon, SearchIcon } from "lucide-react";

import { TablePagination } from "@/components/table-pagination";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { formatTime } from "@/lib/findu";
import { type FinduAsset, listFinduAssets } from "@/lib/readonly-api";
import { cn } from "@/lib/utils";

function assetName(a: FinduAsset) {
  return a.host?.trim() || a.domain?.trim() || a.ip?.trim() || a.id;
}

function riskVariant(level?: string): "destructive" | "default" | "secondary" | "outline" {
  if (level === "critical") return "destructive";
  if (level === "high") return "default";
  if (level === "medium") return "secondary";
  return "outline";
}

export default function AssetsPage() {
  const [rows, setRows] = React.useState<FinduAsset[]>([]);
  const [total, setTotal] = React.useState(0);
  const [totalPages, setTotalPages] = React.useState(1);
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState("");
  const [search, setSearch] = React.useState("");
  const [searchQ, setSearchQ] = React.useState("");
  const [status, setStatus] = React.useState<"all" | "active" | "inactive">("all");
  const [page, setPage] = React.useState(1);
  const [pageSize, setPageSize] = React.useState(50);
  const [reload, setReload] = React.useState(0);
  const [selected, setSelected] = React.useState<FinduAsset | null>(null);

  React.useEffect(() => {
    const t = setTimeout(() => {
      setSearchQ(search);
      setPage(1);
    }, 300);
    return () => clearTimeout(t);
  }, [search]);

  // biome-ignore lint/correctness/useExhaustiveDependencies: reload is the explicit refresh trigger.
  React.useEffect(() => {
    let alive = true;
    setLoading(true);
    setError("");
    listFinduAssets({
      page,
      pageSize,
      search: searchQ,
      status: status === "all" ? undefined : status,
    })
      .then((r) => {
        if (!alive) return;
        setRows(r.assets ?? []);
        setTotal(r.total ?? 0);
        setTotalPages(r.total_pages ?? 1);
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
  }, [page, pageSize, searchQ, status, reload]);

  return (
    <div className="flex h-[calc(100dvh-6rem)] min-h-0 flex-col gap-4 md:h-[calc(100dvh-7rem)] md:gap-6">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h1 className="flex items-center gap-2 text-xl font-semibold tracking-tight">
            <BoxesIcon className="size-5" /> 资产
          </h1>
          <p className="text-muted-foreground text-sm">资产库共 {total} 条</p>
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
              placeholder="搜索 host、域名、IP、标题、来源…"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </div>
          <Select
            value={status}
            onValueChange={(v) => {
              setStatus(v as "all" | "active" | "inactive");
              setPage(1);
            }}
          >
            <SelectTrigger className="w-36">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">全部状态</SelectItem>
              <SelectItem value="active">活跃</SelectItem>
              <SelectItem value="inactive">不活跃</SelectItem>
            </SelectContent>
          </Select>
        </CardContent>
      </Card>

      {error ? (
        <Card className="border-destructive/40 bg-destructive/5">
          <CardContent className="text-destructive py-3 text-sm">资产列表加载失败：{error}</CardContent>
        </Card>
      ) : null}

      <Card className="min-h-0 flex-1 overflow-y-auto">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>资产</TableHead>
              <TableHead className="w-28">协议/端口</TableHead>
              <TableHead>标题</TableHead>
              <TableHead className="w-28">来源</TableHead>
              <TableHead className="w-20">漏洞</TableHead>
              <TableHead className="w-24">风险</TableHead>
              <TableHead className="w-44">最近发现</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading ? (
              Array.from({ length: 8 }).map((_, i) => (
                <TableRow key={i}>
                  <TableCell colSpan={7}>
                    <Skeleton className="h-7 w-full" />
                  </TableCell>
                </TableRow>
              ))
            ) : rows.length === 0 ? (
              <TableRow>
                <TableCell colSpan={7} className="text-muted-foreground py-10 text-center text-sm">
                  {searchQ || status !== "all" ? "没有匹配的资产" : "还没有资产"}
                </TableCell>
              </TableRow>
            ) : (
              rows.map((a) => (
                <TableRow key={a.id} className="cursor-pointer" onClick={() => setSelected(a)}>
                  <TableCell>
                    <div className="max-w-[420px] truncate font-mono text-xs font-medium">{assetName(a)}</div>
                    <div className="text-muted-foreground max-w-[420px] truncate text-[10px]">
                      {[a.domain, a.ip, a.server].filter(Boolean).join(" · ") || a.id}
                    </div>
                  </TableCell>
                  <TableCell className="font-mono text-xs">
                    {[a.protocol?.toUpperCase(), a.port || undefined].filter(Boolean).join(":") || "-"}
                  </TableCell>
                  <TableCell>
                    <div className="max-w-[360px] truncate text-sm">{a.title || "-"}</div>
                  </TableCell>
                  <TableCell className="text-xs">{a.source || "-"}</TableCell>
                  <TableCell className="tabular-nums">
                    {a.vulnerability_count ? (
                      <Badge variant="destructive" className="text-[10px]">
                        {a.vulnerability_count}
                      </Badge>
                    ) : (
                      <span className="text-muted-foreground text-xs">0</span>
                    )}
                  </TableCell>
                  <TableCell>
                    <Badge variant={riskVariant(a.risk_level)} className="text-[10px]">
                      {a.risk_level || "unassessed"}
                    </Badge>
                  </TableCell>
                  <TableCell className="text-muted-foreground text-xs">{formatTime(a.last_seen_at)}</TableCell>
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
        {totalPages > 1 ? (
          <div className="text-muted-foreground border-t px-4 py-2 text-right text-xs">
            第 {page} / {totalPages} 页
          </div>
        ) : null}
      </Card>

      <Sheet open={selected !== null} onOpenChange={(open) => !open && setSelected(null)}>
        <SheetContent className="w-full overflow-y-auto sm:max-w-2xl">
          <SheetHeader>
            <SheetTitle className="break-all pr-8 font-mono text-base">
              {selected ? assetName(selected) : ""}
            </SheetTitle>
            <SheetDescription>
              {selected?.id} · 最近发现 {formatTime(selected?.last_seen_at)}
            </SheetDescription>
          </SheetHeader>
          {selected ? (
            <div className="mt-4 flex flex-col gap-4 text-sm">
              <div className="grid gap-3 sm:grid-cols-2">
                <div className="rounded-lg border p-3">
                  <div className="text-muted-foreground text-xs">域名</div>
                  <div className="mt-1 break-all font-mono text-xs">{selected.domain || "-"}</div>
                </div>
                <div className="rounded-lg border p-3">
                  <div className="text-muted-foreground text-xs">IP</div>
                  <div className="mt-1 break-all font-mono text-xs">{selected.ip || "-"}</div>
                </div>
                <div className="rounded-lg border p-3">
                  <div className="text-muted-foreground text-xs">协议 / 端口</div>
                  <div className="mt-1 font-mono text-xs">
                    {selected.protocol || "-"} / {selected.port || "-"}
                  </div>
                </div>
                <div className="rounded-lg border p-3">
                  <div className="text-muted-foreground text-xs">服务器</div>
                  <div className="mt-1 break-all text-xs">{selected.server || "-"}</div>
                </div>
              </div>
              <div>
                <div className="text-muted-foreground mb-1 text-xs">标题</div>
                <div>{selected.title || "-"}</div>
              </div>
              <div className="grid gap-3 sm:grid-cols-2">
                <div className="rounded-lg border p-3">
                  <div className="text-muted-foreground text-xs">来源</div>
                  <div className="mt-1 text-xs">{selected.source || "-"}</div>
                  {selected.source_query ? (
                    <div className="text-muted-foreground mt-1 break-all font-mono text-[10px]">
                      {selected.source_query}
                    </div>
                  ) : null}
                </div>
                <div className="rounded-lg border p-3">
                  <div className="text-muted-foreground text-xs">状态 / 风险</div>
                  <div className="mt-1 flex flex-wrap gap-1">
                    <Badge variant="outline" className="text-[10px]">
                      {selected.status || "-"}
                    </Badge>
                    <Badge variant={riskVariant(selected.risk_level)} className="text-[10px]">
                      {selected.risk_level || "unassessed"}
                    </Badge>
                    {selected.vulnerability_count ? (
                      <Badge variant="destructive" className="text-[10px]">
                        漏洞 {selected.vulnerability_count}
                      </Badge>
                    ) : null}
                  </div>
                </div>
              </div>
              <div className="grid gap-3 sm:grid-cols-2">
                <div className="rounded-lg border p-3">
                  <div className="text-muted-foreground text-xs">首次发现</div>
                  <div className="mt-1 text-xs">{formatTime(selected.first_seen_at)}</div>
                </div>
                <div className="rounded-lg border p-3">
                  <div className="text-muted-foreground text-xs">最近发现</div>
                  <div className="mt-1 text-xs">{formatTime(selected.last_seen_at)}</div>
                </div>
              </div>
            </div>
          ) : null}
        </SheetContent>
      </Sheet>
    </div>
  );
}
