"use client";

import * as React from "react";

import {
  ArrowUpCircleIcon,
  DownloadIcon,
  ExternalLinkIcon,
  Loader2Icon,
  RotateCcwIcon,
  SearchIcon,
} from "lucide-react";
import { toast } from "sonner";

import { CodeBlock } from "@/components/code-block";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Progress } from "@/components/ui/progress";
import { api } from "@/lib/api";
import type { UpdateCheck } from "@/lib/types";

/**
 * 「版本与更新」卡片：承接原 findu settings 页同名卡片，读宿主 /api/update/*。
 * 顶栏 UpdateBadge 有更新时点击直达本页（卡片内自带检查更新的精确原因）。
 */
interface UpdateProgress {
  phase: string;
  percent: number;
  message: string;
  version?: string;
  error?: string;
}

function formatBytes(n?: number): string {
  if (n === undefined || n < 0) return "";
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MB`;
}

export function VersionUpdateCard() {
  const [check, setCheck] = React.useState<UpdateCheck | null>(null);
  const [loading, setLoading] = React.useState(true);
  const [checking, setChecking] = React.useState(false);
  const [progress, setProgress] = React.useState<UpdateProgress | null>(null);

  React.useEffect(() => {
    let alive = true;
    setLoading(true);
    api
      .checkUpdate()
      .then((c) => {
        if (alive) setCheck(c);
      })
      .catch((e: Error) => {
        if (alive) toast.error(`检查更新失败：${e.message}`);
      })
      .finally(() => {
        if (alive) setLoading(false);
      });
    return () => {
      alive = false;
    };
  }, []);

  async function checkNow() {
    setChecking(true);
    try {
      setCheck(await api.checkUpdate(true));
    } catch (e) {
      toast.error(`检查更新失败：${(e as Error).message}`);
    } finally {
      setChecking(false);
    }
  }

  async function apply() {
    try {
      const res = await api.applyUpdate();
      toast.info(`正在下载新版本 ${res.target}，进度见下方`);
      setProgress({ phase: "downloading", percent: 0, message: "准备中…", version: res.target });
      const stream = await fetch("/api/update/stream", { credentials: "include" });
      if (!stream.body) return;
      const reader = stream.body.getReader();
      const decoder = new TextDecoder();
      let buf = "";
      // 读 SSE 的 data: 行，逐条更新进度。
      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        buf += decoder.decode(value, { stream: true });
        const events = buf.split("\n\n");
        buf = events.pop() ?? "";
        for (const ev of events) {
          const line = ev.split("\n").find((l) => l.startsWith("data: "));
          if (!line) continue;
          try {
            const p = JSON.parse(line.slice(6)) as UpdateProgress;
            setProgress(p);
            if (p.phase === "failed") toast.error(p.error || "更新失败");
            if (p.phase === "staged") toast.info("新版本已就绪，正在重启…");
          } catch {
            // 忽略无法解析的心跳行。
          }
        }
      }
    } catch (e) {
      toast.error(`发起更新失败：${(e as Error).message}`);
    }
  }

  async function rollback() {
    try {
      await api.rollbackUpdate();
      toast.info("已回滚到上一版本，即将重启生效");
    } catch (e) {
      toast.error(`回滚失败：${(e as Error).message}`);
    }
  }

  const busy = progress !== null && progress.phase !== "failed" && progress.phase !== "staged";

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <ArrowUpCircleIcon className="size-4" /> 版本与更新
        </CardTitle>
            <CardDescription>当前版本 · 一键更新 / 回滚</CardDescription>
      </CardHeader>
      <CardContent className="grid gap-4">
        {loading ? (
          <div className="text-muted-foreground text-sm">正在读取版本信息…</div>
        ) : check ? (
          <>
            <div className="grid grid-cols-2 gap-x-6 gap-y-2 text-sm sm:grid-cols-3">
              <div>
                <div className="text-muted-foreground text-xs">当前版本</div>
                <div className="font-medium">{check.current}</div>
              </div>
              <div>
                <div className="text-muted-foreground text-xs">运行形态</div>
                <div className="font-medium">{check.mode === "docker" ? "Docker 容器" : "二进制"}</div>
              </div>
              <div>
                <div className="text-muted-foreground text-xs">平台</div>
                <div className="font-medium">
                  {check.os}/{check.arch}
                </div>
              </div>
            </div>

            {check.rolled_back || check.boot_notice ? (
              <div className="bg-muted/60 text-muted-foreground rounded-md px-3 py-2 text-sm">
                {check.rolled_back ? "启动时检测到已回滚到上一版本" : ""}
                {check.boot_notice ? `：${check.boot_notice}` : ""}
              </div>
            ) : null}

            {check.error ? (
              <div className="text-destructive text-sm">检查更新失败：{check.error}</div>
            ) : !check.comparable && check.reason ? (
              <div className="text-muted-foreground text-sm">{check.reason}</div>
            ) : check.has_update ? (
              <div className="flex flex-col gap-3">
                <div className="flex flex-wrap items-center gap-2">
                  <Badge>发现新版本 {check.latest}</Badge>
                  {check.published_at ? (
                    <span className="text-muted-foreground text-xs">
                      发布于 {new Date(check.published_at).toLocaleDateString("zh-CN")}
                    </span>
                  ) : null}
                  {check.size !== undefined && check.size >= 0 ? (
                    <span className="text-muted-foreground text-xs">包体 {formatBytes(check.size)}</span>
                  ) : null}
                  {check.html_url ? (
                    <a
                      href={check.html_url}
                      target="_blank"
                      rel="noreferrer"
                      className="text-muted-foreground inline-flex items-center gap-1 text-xs hover:underline"
                    >
                      发布说明 <ExternalLinkIcon className="size-3" />
                    </a>
                  ) : null}
                </div>
                {check.notes ? (
                  <CodeBlock code={check.notes} maxHeight="max-h-32" />
                ) : null}
                <div className="flex items-center gap-2">
                  <Button onClick={() => void apply()} disabled={busy || !check.asset_available}>
                    {busy ? <Loader2Icon className="size-4 animate-spin" /> : <DownloadIcon className="size-4" />}
                    一键更新
                  </Button>
                  {check.asset_available === false ? (
                    <span className="text-muted-foreground text-xs">
                      当前平台暂无对应发布包（{check.asset}），可到 GitHub 手动安装
                    </span>
                  ) : null}
                </div>
              </div>
            ) : (
              <div className="text-muted-foreground text-sm">当前已是最新版本。</div>
            )}

            {progress ? (
              <div className="flex flex-col gap-2">
                <div className="flex items-center justify-between text-sm">
                  <span className="text-muted-foreground">
                    {progress.message} {progress.version ? `(${progress.version})` : ""}
                  </span>
                  {progress.percent >= 0 ? <span>{progress.percent}%</span> : null}
                </div>
                {progress.percent >= 0 ? <Progress value={progress.percent} /> : null}
              </div>
            ) : null}

            <div className="flex items-center gap-2">
              <Button variant="outline" onClick={() => void checkNow()} disabled={busy || checking}>
                {checking ? <Loader2Icon className="size-4 animate-spin" /> : <SearchIcon className="size-4" />}
                检查更新
              </Button>
              <Button
                variant="outline"
                onClick={() => void rollback()}
                disabled={busy || !check.has_backup}
                title={check.has_backup ? "回滚到备份的版本" : "当前无已备份版本"}
              >
                <RotateCcwIcon className="size-4" /> 回滚到上一版本
              </Button>
            </div>
          </>
        ) : null}
      </CardContent>
    </Card>
  );
}
