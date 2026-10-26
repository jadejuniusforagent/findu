"use client";

import * as React from "react";
import { toast } from "sonner";
import { HistoryIcon, RefreshCwIcon } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { finduErrorMessage, finduFetch } from "@/lib/findu";

// ---------------------------------------------------------------------------
// 类型：对应 findu internal/handler/changelog.go 的解析结果
// ---------------------------------------------------------------------------

type Entry = { title: string; items: { type: string; text: string }[] };
type Day = { date: string; entries: Entry[] };

type Changelog = {
  available: boolean;
  message?: string;
  source?: string;
  updatedAt?: string;
  dayCount?: number;
  entryCount?: number;
  groups: Day[] | null;
};

const TYPE_STYLE: Record<string, string> = {
  新增: "border-emerald-500/40 text-emerald-600",
  优化: "border-sky-500/40 text-sky-600",
  修复: "border-amber-500/40 text-amber-600",
  决策: "border-violet-500/40 text-violet-600",
  风险: "border-rose-500/40 text-rose-600",
  评估: "border-cyan-500/40 text-cyan-600",
  未决: "border-slate-500/40 text-slate-600",
};

export default function Page() {
  const [data, setData] = React.useState<Changelog | null>(null);
  const [loading, setLoading] = React.useState(true);

  const load = React.useCallback(async () => {
    setLoading(true);
    try {
      const d = await finduFetch<Changelog>("/changelog");
      setData(d);
    } catch (e) {
      toast.error("读取版本记录失败：" + finduErrorMessage(e));
    } finally {
      setLoading(false);
    }
  }, []);

  React.useEffect(() => {
    void load();
  }, [load]);

  const days = data?.groups ?? [];

  return (
    <div className="flex flex-1 flex-col gap-4 md:gap-6">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h1 className="flex items-center gap-2 text-xl font-semibold tracking-tight">
            <HistoryIcon className="size-5" /> 版本记录
          </h1>
          <p className="text-muted-foreground text-sm">
            {data?.available
              ? `${data.dayCount ?? days.length} 天 · ${data.entryCount ?? 0} 条变更 · 更新于 ${data.updatedAt || "-"}`
              : loading
                ? "加载中…"
                : (data?.message ?? "暂无变更记录")}
          </p>
        </div>
        <Button variant="outline" onClick={() => void load()} disabled={loading}>
          <RefreshCwIcon className="size-4" /> 刷新
        </Button>
      </div>

      {!loading && days.length === 0 ? (
        <Card>
          <CardContent className="text-muted-foreground py-16 text-center text-sm">
            {data?.available ? "变更记录为空" : (data?.message ?? "暂无变更记录")}
          </CardContent>
        </Card>
      ) : null}

      <div className="flex flex-col gap-4">
        {days.map((day) => (
          <Card key={day.date}>
            <CardHeader>
              <CardTitle className="font-mono text-base">{day.date}</CardTitle>
              <CardDescription>{day.entries.length} 项变更</CardDescription>
            </CardHeader>
            <CardContent className="flex flex-col gap-4">
              {day.entries.map((entry, i) => (
                <div key={`${day.date}-${i}`} className="border-l-2 pl-3">
                  <div className="mb-1.5 text-sm font-medium">{entry.title}</div>
                  <ul className="flex flex-col gap-1">
                    {entry.items.map((item, j) => (
                      <li key={`${day.date}-${i}-${j}`} className="flex items-start gap-2 text-xs">
                        {item.type ? (
                          <Badge
                            variant="outline"
                            className={`mt-0.5 shrink-0 px-1 py-0 text-[10px] ${TYPE_STYLE[item.type] ?? ""}`}
                          >
                            {item.type}
                          </Badge>
                        ) : (
                          <span className="text-muted-foreground mt-0.5 shrink-0">·</span>
                        )}
                        <span className="text-muted-foreground leading-relaxed">{item.text}</span>
                      </li>
                    ))}
                  </ul>
                </div>
              ))}
            </CardContent>
          </Card>
        ))}
      </div>
    </div>
  );
}
