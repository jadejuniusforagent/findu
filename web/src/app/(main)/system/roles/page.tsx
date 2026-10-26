"use client";

import * as React from "react";

import { RefreshCwIcon, SearchIcon, UsersRoundIcon } from "lucide-react";

import { Markdown } from "@/components/markdown";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import { type FinduRole, listFinduRoles } from "@/lib/readonly-api";
import { cn } from "@/lib/utils";

export default function RolesPage() {
  const [rows, setRows] = React.useState<FinduRole[]>([]);
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState("");
  const [search, setSearch] = React.useState("");
  const [reload, setReload] = React.useState(0);
  const [selected, setSelected] = React.useState<FinduRole | null>(null);

  // biome-ignore lint/correctness/useExhaustiveDependencies: reload is the explicit refresh trigger.
  React.useEffect(() => {
    let alive = true;
    setLoading(true);
    setError("");
    listFinduRoles()
      .then((r) => {
        if (!alive) return;
        setRows(r.roles ?? []);
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

  const filtered = React.useMemo(() => {
    const q = search.trim().toLowerCase();
    if (!q) return rows;
    return rows.filter((r) =>
      [r.name, r.description, r.user_prompt].filter(Boolean).some((v) => String(v).toLowerCase().includes(q)),
    );
  }, [rows, search]);

  return (
    <div className="flex flex-1 flex-col gap-4 md:gap-6">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h1 className="flex items-center gap-2 text-xl font-semibold tracking-tight">
            <UsersRoundIcon className="size-5" /> 角色
          </h1>
          <p className="text-muted-foreground text-sm">共 {rows.length} 个角色</p>
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
              placeholder="搜索角色名称、描述、提示词…"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </div>
        </CardContent>
      </Card>

      {error ? (
        <Card className="border-destructive/40 bg-destructive/5">
          <CardContent className="text-destructive py-3 text-sm">角色列表加载失败：{error}</CardContent>
        </Card>
      ) : null}

      {loading ? (
        <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
          {Array.from({ length: 6 }).map((_, i) => (
            <Skeleton key={i} className="h-36 w-full" />
          ))}
        </div>
      ) : filtered.length === 0 ? (
        <Card>
          <CardContent className="text-muted-foreground py-10 text-center text-sm">
            {search ? "没有匹配的角色" : "还没有角色"}
          </CardContent>
        </Card>
      ) : (
        <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
          {filtered.map((r) => (
            <button key={r.name} type="button" className="text-left" onClick={() => setSelected(r)}>
              <Card className="h-full transition-colors hover:border-primary/40">
                <CardContent className="flex h-full flex-col gap-2 p-4">
                  <div className="flex items-start justify-between gap-2">
                    <div className="font-medium">{r.name}</div>
                    <Badge variant={r.enabled === false ? "outline" : "secondary"} className="shrink-0 text-[10px]">
                      {r.enabled === false ? "停用" : "启用"}
                    </Badge>
                  </div>
                  <p className="text-muted-foreground line-clamp-4 text-xs">{r.description || "（无描述）"}</p>
                  <div className="text-muted-foreground mt-auto line-clamp-2 font-mono text-[10px]">
                    {(r.user_prompt || "").slice(0, 160) || "（无提示词）"}
                  </div>
                </CardContent>
              </Card>
            </button>
          ))}
        </div>
      )}

      <Sheet open={selected !== null} onOpenChange={(open) => !open && setSelected(null)}>
        <SheetContent className="w-full overflow-y-auto sm:max-w-3xl">
          <SheetHeader>
            <SheetTitle className="pr-8">{selected?.name}</SheetTitle>
            <SheetDescription>{selected?.description || "（无描述）"}</SheetDescription>
          </SheetHeader>
          {selected ? (
            <div className="mt-4 flex flex-col gap-4 text-sm">
              <div className="flex flex-wrap gap-1">
                <Badge variant={selected.enabled === false ? "outline" : "secondary"} className="text-[10px]">
                  {selected.enabled === false ? "停用" : "启用"}
                </Badge>
              </div>
              <div>
                <div className="text-muted-foreground mb-1 text-xs">系统提示词</div>
                {selected.user_prompt ? (
                  <div className="rounded-lg border p-3">
                    <Markdown text={selected.user_prompt} />
                  </div>
                ) : (
                  <div className="text-muted-foreground">（无提示词）</div>
                )}
              </div>
            </div>
          ) : null}
        </SheetContent>
      </Sheet>
    </div>
  );
}
