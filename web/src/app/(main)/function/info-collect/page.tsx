"use client";

import * as React from "react";

import {
  CompassIcon,
  DatabaseIcon,
  Globe2Icon,
  GlobeIcon,
  LayoutGridIcon,
  RadarIcon,
  SatelliteIcon,
  SearchCheckIcon,
  SearchIcon,
  SparklesIcon,
} from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import {
  parseSpaceNaturalLanguage,
  type SpaceSearchProvider,
  type SpaceSearchResult,
  searchSpace,
} from "@/lib/readonly-api";
import { cn } from "@/lib/utils";

import { EngineConfigPanel } from "./engine-config-panel";

interface ProviderMeta {
  label: string;
  tagline: string;
  icon: React.ComponentType<{ className?: string }>;
  dot: string;
  placeholder: string;
  defaultFields: string;
  maxSize: number;
  supportsFull: boolean;
}

const PROVIDERS: Record<SpaceSearchProvider, ProviderMeta> = {
  fofa: {
    label: "FOFA",
    tagline: "网络空间测绘",
    icon: RadarIcon,
    dot: "bg-sky-500",
    placeholder: '例如：app="Apache" && country="CN"',
    defaultFields: "host,ip,port,domain,title,protocol,country,province,city,server",
    maxSize: 10000,
    supportsFull: true,
  },
  zoomeye: {
    label: "ZoomEye",
    tagline: "网络资产检索",
    icon: Globe2Icon,
    dot: "bg-emerald-500",
    placeholder: '例如：app="Apache" && country="CN"',
    defaultFields: "ip,port,domain,hostname,title,service,app,country,city",
    maxSize: 10000,
    supportsFull: false,
  },
  quake: {
    label: "Quake",
    tagline: "空间测绘",
    icon: SatelliteIcon,
    dot: "bg-indigo-500",
    placeholder: '例如：service.name:"http" AND country_cn:"中国"',
    defaultFields:
      "ip,port,domain,service.name,service.http.title,location.country_cn,location.province_cn,location.city_cn",
    maxSize: 10000,
    supportsFull: true,
  },
  shodan: {
    label: "Shodan",
    tagline: "网络设备搜索",
    icon: GlobeIcon,
    dot: "bg-rose-500",
    placeholder: "例如：product:nginx country:CN",
    defaultFields: "ip_str,port,hostnames,domains,org,isp,location.country_name,location.city,product,transport",
    maxSize: 1000,
    supportsFull: false,
  },
  hunter: {
    label: "Hunter",
    tagline: "资产测绘",
    icon: DatabaseIcon,
    dot: "bg-amber-500",
    placeholder: '例如：web.title="登录" && web.country="CN"',
    defaultFields: "ip,port,domain,web.title,web.company,protocol,web.server",
    maxSize: 500,
    supportsFull: false,
  },
};

const SAMPLE_PRESETS: Record<SpaceSearchProvider, string[]> = {
  fofa: ['app="Apache" && country="CN"', 'title="登录" && country="CN"', 'domain="example.com"'],
  zoomeye: ['app="Apache" && country="CN"', 'service="ssh"', 'domain="example.com"'],
  quake: ['service.name:"http" AND country_cn:"中国"', "port:443", 'domain:"example.com"'],
  shodan: ["product:nginx country:CN", "port:22", "ssl.cert.subject.cn:example.com"],
  hunter: ['web.title="登录"', 'domain="example.com"', 'ip="1.1.1.1"'],
};

function shortValue(v: unknown): string {
  if (v === null || v === undefined) return "";
  return String(v);
}

function StatCard({
  icon: Icon,
  label,
  value,
  accent,
}: {
  icon: React.ComponentType<{ className?: string }>;
  label: string;
  value: React.ReactNode;
  accent?: string;
}) {
  return (
    <div className="flex items-center gap-3 rounded-lg border bg-card p-3">
      <div
        className={cn(
          "flex size-9 shrink-0 items-center justify-center rounded-md bg-muted text-muted-foreground",
          accent,
        )}
      >
        <Icon className="size-4" />
      </div>
      <div className="min-w-0">
        <p className="text-muted-foreground text-[11px] leading-none">{label}</p>
        <p className="mt-1 truncate font-mono text-lg font-semibold tabular-nums">{value}</p>
      </div>
    </div>
  );
}

export default function InfoCollectPage() {
  const [provider, setProvider] = React.useState<SpaceSearchProvider>("fofa");
  const [query, setQuery] = React.useState("");
  const [nlText, setNlText] = React.useState("");
  const [fields, setFields] = React.useState(PROVIDERS.fofa.defaultFields);
  const [size, setSize] = React.useState(100);
  const [full, setFull] = React.useState(false);
  const [page, setPage] = React.useState(1);
  const [parsing, setParsing] = React.useState(false);
  const [searching, setSearching] = React.useState(false);
  const [parseMsg, setParseMsg] = React.useState("");
  const [error, setError] = React.useState("");
  const [result, setResult] = React.useState<SpaceSearchResult | null>(null);

  const meta = PROVIDERS[provider];

  const switchProvider = (p: SpaceSearchProvider) => {
    setProvider(p);
    setPage(1);
    setResult(null);
    setError("");
    setFields(PROVIDERS[p].defaultFields);
  };

  const handleParse = async () => {
    if (!nlText.trim()) return;
    setParsing(true);
    setParseMsg("");
    setError("");
    try {
      const r = await parseSpaceNaturalLanguage(provider, nlText.trim());
      setParseMsg(r.explanation ? `解析完成：${r.explanation}` : "解析完成，已填入查询框");
      setQuery(r.query);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setParsing(false);
    }
  };

  const handleSearch = async (targetPage = 1) => {
    if (!query.trim()) return;
    setSearching(true);
    setError("");
    setPage(targetPage);
    try {
      const r = await searchSpace({
        provider,
        query: query.trim(),
        fields: fields.trim() || undefined,
        size,
        page: targetPage,
        full,
      });
      setResult(r);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setResult(null);
    } finally {
      setSearching(false);
    }
  };

  const cols = result?.fields?.length ? result.fields : [];
  const shown = result?.results ?? [];

  return (
    <div className="flex h-[calc(100dvh-6rem)] min-h-0 flex-col gap-4 md:h-[calc(100dvh-7rem)] md:gap-6">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h1 className="flex items-center gap-2 font-semibold text-xl tracking-tight">
            <span className="flex size-7 items-center justify-center rounded-md bg-primary/10 text-primary">
              <CompassIcon className="size-4" />
            </span>
            信息收集
          </h1>
          <p className="mt-1 pl-9 text-muted-foreground text-sm">
            空间测绘 · FOFA / ZoomEye / Quake / Shodan / Hunter 五源统一查询
          </p>
        </div>
      </div>

      <EngineConfigPanel />

      <Card>
        <CardContent className="flex flex-col gap-4 py-4">
          <div className="flex flex-col gap-2">
            <Label className="text-xs text-muted-foreground">数据源</Label>
            <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-5">
              {Object.entries(PROVIDERS).map(([k, p]) => {
                const Icon = p.icon;
                const active = provider === k;
                return (
                  <button
                    key={k}
                    type="button"
                    onClick={() => switchProvider(k as SpaceSearchProvider)}
                    className={cn(
                      "group flex items-center gap-3 rounded-lg border px-3 py-2.5 text-left transition-all",
                      active
                        ? "border-primary/50 bg-primary/5 shadow-sm"
                        : "border-border bg-card hover:border-primary/30 hover:bg-muted/40",
                    )}
                  >
                    <span
                      className={cn(
                        "flex size-8 shrink-0 items-center justify-center rounded-md transition-colors",
                        active
                          ? "bg-primary/10 text-primary"
                          : "bg-muted text-muted-foreground group-hover:text-foreground",
                      )}
                    >
                      <Icon className="size-4" />
                    </span>
                    <span className="min-w-0">
                      <span className="flex items-center gap-1.5 text-sm font-medium">
                        <span className={cn("size-1.5 shrink-0 rounded-full", p.dot)} />
                        <span className="truncate">{p.label}</span>
                      </span>
                      <span className="block truncate text-[11px] text-muted-foreground">{p.tagline}</span>
                    </span>
                  </button>
                );
              })}
            </div>
          </div>

          <div className="flex flex-col gap-1.5">
            <div className="flex items-center justify-between">
              <Label className="text-xs text-muted-foreground">查询语句</Label>
              <span className="text-[11px] text-muted-foreground/70">Ctrl/⌘ + Enter 快速查询</span>
            </div>
            <div className="flex items-start gap-2">
              <Textarea
                className="min-h-[68px] flex-1 font-mono text-xs"
                placeholder={meta.placeholder}
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) void handleSearch();
                }}
              />
              <Button
                className="h-[68px] shrink-0 px-5"
                onClick={() => handleSearch()}
                disabled={searching || !query.trim()}
              >
                <SearchIcon className={cn("size-4", searching && "animate-pulse")} />
                查询
              </Button>
            </div>
          </div>

          <div className="flex flex-wrap items-center gap-x-5 gap-y-2 text-sm">
            <div className="flex items-center gap-2">
              <span className="text-muted-foreground">返回数量</span>
              <Input
                type="number"
                className="h-8 w-24 font-mono text-xs"
                min={1}
                max={meta.maxSize}
                value={size}
                onChange={(e) => setSize(Math.max(1, Number(e.target.value) || 100))}
              />
            </div>
            <div className="flex items-center gap-2">
              <span className="text-muted-foreground">字段</span>
              <Input
                className="h-8 w-72 font-mono text-xs md:w-80"
                placeholder="逗号分隔，留空用默认"
                value={fields}
                onChange={(e) => setFields(e.target.value)}
              />
            </div>
            {meta.supportsFull ? (
              <div className="flex items-center gap-2">
                <Switch checked={full} onCheckedChange={setFull} />
                <span className="text-muted-foreground">完整模式</span>
              </div>
            ) : null}
          </div>

          <div className="flex flex-wrap items-center gap-1.5">
            <span className="text-muted-foreground text-xs">示例：</span>
            {SAMPLE_PRESETS[provider].map((p) => (
              <Button
                key={p}
                variant="outline"
                size="sm"
                className="h-6 px-2 font-mono text-[11px]"
                onClick={() => setQuery(p)}
              >
                {p}
              </Button>
            ))}
          </div>

          <div className="flex flex-col gap-1.5 border-t pt-4">
            <Label className="flex items-center gap-1.5 text-xs text-muted-foreground">
              <SparklesIcon className="size-3.5" />
              自然语言解析（可选，需配置 AI 模型）
            </Label>
            <div className="flex items-start gap-2">
              <Textarea
                className="min-h-[60px] flex-1 text-sm"
                placeholder="例如：找中国的 Apache 站点，标题包含登录"
                value={nlText}
                onChange={(e) => setNlText(e.target.value)}
              />
              <Button
                variant="outline"
                className="h-[60px] shrink-0 px-4"
                onClick={handleParse}
                disabled={parsing || !nlText.trim()}
              >
                <SparklesIcon className={cn("size-4", parsing && "animate-pulse")} />
                解析为查询
              </Button>
            </div>
          </div>

          {parseMsg ? (
            <p className="border-primary/40 border-l-2 pl-2 text-muted-foreground text-xs">{parseMsg}</p>
          ) : null}
          {error ? <p className="border-destructive/40 border-l-2 pl-2 text-destructive text-xs">{error}</p> : null}
        </CardContent>
      </Card>

      <Card className="min-h-0 flex-1 overflow-y-auto">
        {!result ? (
          <CardContent className="flex flex-col items-center justify-center gap-3 py-20 text-center">
            <div className="flex size-14 items-center justify-center rounded-2xl bg-muted">
              <SearchCheckIcon className="size-7 text-muted-foreground/60" />
            </div>
            <div>
              <p className="font-medium text-sm">开始一次空间测绘</p>
              <p className="mt-1 max-w-sm text-muted-foreground text-sm">
                选择数据源，输入查询语句后点击「查询」，五源测绘结果将统一展示在此处。
              </p>
            </div>
          </CardContent>
        ) : (
          <>
            <CardHeader className="flex flex-row items-center gap-2 pb-3 pt-4">
              <Badge variant="outline" className="gap-1.5">
                <span className={cn("size-1.5 rounded-full", meta.dot)} />
                {PROVIDERS[result.provider as SpaceSearchProvider]?.label ?? result.provider}
              </Badge>
              <span className="truncate font-mono text-muted-foreground text-xs">{result.query}</span>
            </CardHeader>
            <CardContent className="flex flex-col gap-3 pt-0">
              <div className="grid grid-cols-3 gap-2">
                <StatCard icon={LayoutGridIcon} label="命中总数" value={result.total ?? "-"} />
                <StatCard icon={DatabaseIcon} label="返回条数" value={result.results_count ?? "-"} />
                <StatCard icon={GlobeIcon} label="当前页" value={page} accent="bg-primary/10 text-primary" />
              </div>

              {shown.length === 0 ? (
                <p className="py-10 text-center text-muted-foreground text-sm">没有匹配的结果</p>
              ) : (
                <div className="overflow-x-auto">
                  <Table>
                    <TableHeader>
                      <TableRow>
                        {cols.map((c) => (
                          <TableHead key={c} className="whitespace-nowrap font-mono text-[11px]">
                            {c}
                          </TableHead>
                        ))}
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {searching
                        ? Array.from({ length: 5 }).map((_, i) => (
                            <TableRow key={`skeleton-${i}`}>
                              {cols.map((c) => (
                                <TableCell key={c}>
                                  <Skeleton className="h-4 w-full" />
                                </TableCell>
                              ))}
                            </TableRow>
                          ))
                        : shown.map((row, i) => {
                            const rowKey = (cols.length > 0 ? shortValue(row[cols[0]]) : "") || `row-${i}`;
                            return (
                              <TableRow key={rowKey}>
                                {cols.map((c) => {
                                  const v = shortValue(row[c]);
                                  return (
                                    <TableCell key={c} className="max-w-[260px] truncate font-mono text-xs">
                                      {v || "-"}
                                    </TableCell>
                                  );
                                })}
                              </TableRow>
                            );
                          })}
                    </TableBody>
                  </Table>
                </div>
              )}
              <div className="flex items-center justify-end gap-2 pt-1">
                <Button
                  variant="outline"
                  size="sm"
                  disabled={page <= 1 || searching}
                  onClick={() => handleSearch(page - 1)}
                >
                  上一页
                </Button>
                <span className="text-muted-foreground text-xs">第 {page} 页</span>
                <Button
                  variant="outline"
                  size="sm"
                  disabled={searching || shown.length === 0}
                  onClick={() => handleSearch(page + 1)}
                >
                  下一页
                </Button>
              </div>
            </CardContent>
          </>
        )}
      </Card>
    </div>
  );
}
