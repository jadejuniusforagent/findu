"use client";

import * as React from "react";

import {
  ChevronLeftIcon,
  ChevronRightIcon,
  FolderOpenIcon,
  RefreshCwIcon,
  SearchIcon,
  SparklesIcon,
} from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import {
  Pagination,
  PaginationContent,
  PaginationEllipsis,
  PaginationItem,
  PaginationLink,
  PaginationNext,
  PaginationPrevious,
} from "@/components/ui/pagination";
import { ScrollArea, ScrollBar } from "@/components/ui/scroll-area";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { formatTime } from "@/lib/findu";
import { type FinduSkill, listFinduSkills } from "@/lib/readonly-api";
import { cn } from "@/lib/utils";

const PAGE_SIZE = 12;

function skillName(s: FinduSkill) {
  const name = s.name.trim();
  if (name) return name;
  const dir = s.dir_name?.trim();
  if (dir) return dir;
  return s.id;
}

function tagsOf(s: FinduSkill) {
  return (s.tags ?? []).filter((x): x is string => typeof x === "string" && x.trim() !== "");
}

// 领域分类：多数技能无 tags 或 tags 高度重复（[渗透测试, penetration-testing, 红队]），
// 不可作为分类维度。改用「技能目录名/名称」的关键词映射到渗透测试领域类别。
const CATEGORY_RULES: ReadonlyArray<readonly [string, readonly string[]]> = [
  ["注入与反序列化", ["injection","cmdi","fastjson","jndi","log4shell","sqli","nosql","ssti","xxe","xslt","xpath","deserialization","crlf","ssrf","xss","prototype-pollution","shiro","spring","command-injection","expression-language","type-juggling","http-parameter","request-smuggling","open-redirect","path-traversal","path-403","403-bypass","upload","clickjacking","csrf","cors","race-condition","waf-bypass","web-cache","smuggling","hzero"]],
  ["内网与域控", ["active-directory","intranet","lateral","workgroup","post-exploitation","network-penetration","initial-access","phishing","domain"]],
  ["移动/二进制/硬件/链", ["mobile","binary","reversing","blockchain","contract","wireless","hardware","captcha","miniprogram","app-"]],
  ["源码与供应链", ["source-code","dependency","ci-cd","code-review","source","supply"]],
  ["云与容器", ["cloud","container","kubernetes","docker","k8s"]],
  ["认证与授权", ["auth","idor","jwt","oauth","token","saml","sso","cas-","authentication","unauthorized","access-","authorization","oidc","assertion"]],
  ["侦察与信息收集", ["recon","attack-surface","scopesentry","vuln-intel","capability","hunting","component","vuln"]],
  ["API 与接口", ["api","graphql","websocket","fuzz"]],
  ["基础设施与工具", ["env-","network-and-tooling","proxy","playwright","browser-assisted","tool"]],
  ["红队与 C2", ["c2-","redteam","opsec","tunnel","command-and"]],
  ["方法、报告与流程", ["report","methodology","output-standard","verification","adjudication","awareness","blackboard","agent-os","poc","bounty","discipline","specialized","assessment","zero-day","research","incident","automation","decompose","scope","h1-","business-logic","attack-methods","secure-code","standards","vulnerability"]],
];

function categorizeSkill(s: FinduSkill): string {
  const n = `${s.name} ${s.dir_name ?? ""} ${s.id}`.toLowerCase();
  for (const [label, words] of CATEGORY_RULES) {
    if (words.some((w) => n.includes(w))) return label;
  }
  return "其他";
}

function pageNumbers(current: number, total: number): (number | "...")[] {
  if (total <= 7) return Array.from({ length: total }, (_, i) => i + 1);
  const set = new Set([1, total, current - 1, current, current + 1]);
  const sorted = [...set].filter((p) => p >= 1 && p <= total).sort((a, b) => a - b);
  const out: (number | "...")[] = [];
  let prev = 0;
  for (const p of sorted) {
    if (p - prev > 1) out.push("...");
    out.push(p);
    prev = p;
  }
  return out;
}

export default function SkillsPage() {
  const [rows, setRows] = React.useState<FinduSkill[]>([]);
  const [total, setTotal] = React.useState(0);
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState("");
  const [search, setSearch] = React.useState("");
  const [reload, setReload] = React.useState(0);
  const [selected, setSelected] = React.useState<FinduSkill | null>(null);
  const [category, setCategory] = React.useState("all");
  const [page, setPage] = React.useState(1);

  // biome-ignore lint/correctness/useExhaustiveDependencies: reload is the explicit refresh trigger.
  React.useEffect(() => {
    let alive = true;
    setLoading(true);
    setError("");
    listFinduSkills(500)
      .then((r) => {
        if (!alive) return;
        setRows(r.skills ?? []);
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
  }, [reload]);

  React.useEffect(() => {
    setPage(1);
  }, [category, search]);

  const categories = React.useMemo(() => {
    const counts = new Map<string, number>();
    for (const s of rows) {
      const c = categorizeSkill(s);
      counts.set(c, (counts.get(c) ?? 0) + 1);
    }
    return [...CATEGORY_RULES.map(([label]) => label), "其他"]
      .map((label) => ({ label, count: counts.get(label) ?? 0 }))
      .filter((c) => c.count > 0);
  }, [rows]);

  const catFiltered = React.useMemo(() => {
    if (category === "all") return rows;
    return rows.filter((s) => categorizeSkill(s) === category);
  }, [rows, category]);

  const searched = React.useMemo(() => {
    const q = search.trim().toLowerCase();
    if (!q) return catFiltered;
    return catFiltered.filter((s) =>
      [s.name, s.dir_name, s.description, s.path, ...(s.tags ?? []), ...(s.triggers ?? [])]
        .filter(Boolean)
        .some((v) => String(v).toLowerCase().includes(q)),
    );
  }, [catFiltered, search]);

  const totalPages = Math.max(1, Math.ceil(searched.length / PAGE_SIZE));
  const safePage = Math.min(page, totalPages);
  const pageItems = React.useMemo(
    () => searched.slice((safePage - 1) * PAGE_SIZE, safePage * PAGE_SIZE),
    [searched, safePage],
  );

  return (
    <div className="flex flex-1 flex-col gap-4 md:gap-6">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h1 className="flex items-center gap-2 text-xl font-semibold tracking-tight">
            <SparklesIcon className="size-5" /> 技能
          </h1>
          <p className="text-muted-foreground text-sm">
            已安装 {total} 个技能 · {categories.length} 个分类
          </p>
        </div>
        <Button variant="outline" onClick={() => setReload((n) => n + 1)} disabled={loading}>
          <RefreshCwIcon className={cn("size-4", loading && "animate-spin")} /> 刷新
        </Button>
      </div>

      <Card>
        <CardContent className="py-3">
          <div className="relative max-w-xl">
            <SearchIcon className="text-muted-foreground absolute top-2.5 left-2.5 size-4" />
            <Input
              className="pl-8"
              placeholder="搜索技能名称、描述、标签、触发词…"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </div>
        </CardContent>
      </Card>

      <ScrollArea className="w-full">
        <Tabs value={category} onValueChange={setCategory}>
          <TabsList className="h-auto w-max flex-wrap">
            <TabsTrigger value="all" className="data-[state=active]:bg-primary data-[state=active]:text-primary-foreground">
              全部 ({rows.length})
            </TabsTrigger>
            {categories.map((c) => (
              <TabsTrigger key={c.label} value={c.label}>
                {c.label} ({c.count})
              </TabsTrigger>
            ))}
          </TabsList>
        </Tabs>
        <ScrollBar orientation="horizontal" />
      </ScrollArea>

      {error ? (
        <Card className="border-destructive/40 bg-destructive/5">
          <CardContent className="text-destructive py-3 text-sm">技能列表加载失败：{error}</CardContent>
        </Card>
      ) : null}

      {loading ? (
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {Array.from({ length: 6 }).map((_, i) => (
            <Skeleton key={i} className="h-32 w-full rounded-lg" />
          ))}
        </div>
      ) : pageItems.length === 0 ? (
        <Card>
          <CardContent className="text-muted-foreground py-14 text-center text-sm">
            <FolderOpenIcon className="mx-auto mb-2 size-8 opacity-40" />
            {search ? "没有匹配的技能" : category !== "all" ? "该分类下暂无技能" : "还没有技能"}
          </CardContent>
        </Card>
      ) : (
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {pageItems.map((s) => (
            <Card
              key={s.id}
              className="cursor-pointer transition-colors hover:border-primary/50 hover:shadow-sm"
              onClick={() => setSelected(s)}
            >
              <CardContent className="flex h-full min-h-32 flex-col gap-2 p-4">
                <div className="flex items-center gap-2 font-medium">
                  <SparklesIcon className="text-primary size-4 shrink-0" />
                  <span className="truncate">{skillName(s)}</span>
                  {s.progressive ? (
                    <Badge variant="secondary" className="shrink-0 text-[10px]">
                      渐进
                    </Badge>
                  ) : null}
                </div>
                <p className="text-muted-foreground line-clamp-2 text-xs">{s.description || s.dir_name || "-"}</p>
                <div className="flex flex-wrap gap-1">
                  {tagsOf(s).length === 0 ? (
                    <Badge variant="secondary" className="text-[10px]">
                      未分类
                    </Badge>
                  ) : (
                    tagsOf(s)
                      .slice(0, 4)
                      .map((t) => (
                        <Badge key={t} variant="outline" className="text-[10px]">
                          {t}
                        </Badge>
                      ))
                  )}
                  {tagsOf(s).length > 4 ? (
                    <span className="text-muted-foreground text-[10px]">+{tagsOf(s).length - 4}</span>
                  ) : null}
                </div>
                <div className="text-muted-foreground mt-auto flex items-center justify-between gap-2 text-[11px]">
                  <span className="tabular-nums">{s.file_count ?? 0} 文件</span>
                  <span className="font-mono">{s.version || "-"}</span>
                  <span>{formatTime(s.mod_time)}</span>
                </div>
              </CardContent>
            </Card>
          ))}
        </div>
      )}

      {!loading && searched.length > PAGE_SIZE ? (
        <div className="flex flex-col items-center gap-2">
          <span className="text-muted-foreground text-xs">
            共 {searched.length} 项 · 第 {safePage}/{totalPages} 页
          </span>
          <Pagination>
            <PaginationContent>
              <PaginationItem>
                <PaginationPrevious
                  text="上一页"
                  aria-disabled={safePage <= 1}
                  className={cn(safePage <= 1 && "pointer-events-none opacity-50")}
                  onClick={(e) => {
                    e.preventDefault();
                    if (safePage > 1) setPage(safePage - 1);
                  }}
                />
              </PaginationItem>
              {pageNumbers(safePage, totalPages).map((p, i) =>
                p === "..." ? (
                  <PaginationItem key={`e${i}`}>
                    <PaginationEllipsis />
                  </PaginationItem>
                ) : (
                  <PaginationItem key={p}>
                    <PaginationLink
                      isActive={p === safePage}
                      onClick={(e) => {
                        e.preventDefault();
                        setPage(p);
                      }}
                    >
                      {p}
                    </PaginationLink>
                  </PaginationItem>
                ),
              )}
              <PaginationItem>
                <PaginationNext
                  text="下一页"
                  aria-disabled={safePage >= totalPages}
                  className={cn(safePage >= totalPages && "pointer-events-none opacity-50")}
                  onClick={(e) => {
                    e.preventDefault();
                    if (safePage < totalPages) setPage(safePage + 1);
                  }}
                >
                  <ChevronRightIcon />
                </PaginationNext>
              </PaginationItem>
            </PaginationContent>
          </Pagination>
        </div>
      ) : null}

      <Sheet open={selected !== null} onOpenChange={(open) => !open && setSelected(null)}>
        <SheetContent className="w-full overflow-y-auto sm:max-w-2xl">
          <SheetHeader>
            <SheetTitle className="pr-8">{selected ? skillName(selected) : ""}</SheetTitle>
            <SheetDescription>
              {selected?.dir_name || selected?.id} · {selected?.file_count ?? 0} 个文件 · {formatTime(selected?.mod_time)}
            </SheetDescription>
          </SheetHeader>
          {selected ? (
            <div className="mt-4 flex flex-col gap-4 text-sm">
              <div>
                <div className="text-muted-foreground mb-1 text-xs">描述</div>
                <div className="whitespace-pre-wrap">{selected.description || "-"}</div>
              </div>
              <div className="grid gap-3 sm:grid-cols-2">
                <div className="rounded-lg border p-3">
                  <div className="text-muted-foreground text-xs">版本</div>
                  <div className="mt-1 font-mono text-xs">{selected.version || "-"}</div>
                </div>
                <div className="rounded-lg border p-3">
                  <div className="text-muted-foreground text-xs">脚本数</div>
                  <div className="mt-1 tabular-nums">{selected.script_count ?? 0}</div>
                </div>
                <div className="rounded-lg border p-3">
                  <div className="text-muted-foreground text-xs">文件大小</div>
                  <div className="mt-1 tabular-nums">{selected.file_size ?? 0} bytes</div>
                </div>
                <div className="rounded-lg border p-3">
                  <div className="text-muted-foreground text-xs">渐进加载</div>
                  <div className="mt-1">{selected.progressive ? "是" : "否"}</div>
                </div>
              </div>
              <div>
                <div className="text-muted-foreground mb-1 text-xs">路径</div>
                <div className="break-all font-mono text-xs">{selected.path || "-"}</div>
              </div>
              <div>
                <div className="text-muted-foreground mb-1 text-xs">标签</div>
                <div className="flex flex-wrap gap-1">
                  {tagsOf(selected).length === 0 ? <span className="text-muted-foreground">-</span> : null}
                  {tagsOf(selected).map((t) => (
                    <Badge key={t} variant="outline" className="text-[10px]">
                      {t}
                    </Badge>
                  ))}
                </div>
              </div>
              <div>
                <div className="text-muted-foreground mb-1 text-xs">触发词</div>
                <div className="flex flex-wrap gap-1">
                  {(selected.triggers ?? []).length === 0 ? <span className="text-muted-foreground">-</span> : null}
                  {(selected.triggers ?? []).map((t) => (
                    <Badge key={t} variant="secondary" className="text-[10px]">
                      {t}
                    </Badge>
                  ))}
                </div>
              </div>
            </div>
          ) : null}
        </SheetContent>
      </Sheet>
    </div>
  );
}