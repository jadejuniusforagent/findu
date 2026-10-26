"use client";

import * as React from "react";

import {
  ArrowLeftIcon,
  FileCodeIcon,
  Loader2Icon,
  PackageIcon,
  RefreshCwIcon,
  SearchIcon,
  SmartphoneIcon,
  Trash2Icon,
  UploadIcon,
} from "lucide-react";
import { toast } from "sonner";

import { CodeBlock } from "@/components/code-block";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { formatTime } from "@/lib/findu";
import {
  type ApkSample,
  deleteApkSample,
  formatApkBytes,
  getApkAutoReport,
  getApkSource,
  listApkSamples,
  readApkManifest,
  uploadApkSample,
} from "@/lib/apk-api";
import { listFinduProjects, type FinduProject } from "@/lib/readonly-api";
import { cn } from "@/lib/utils";

const SOURCE_EXAMPLE = "Lcom/github/skylot/simple/MainActivity;";

export default function AndroidPage() {
  const [samples, setSamples] = React.useState<ApkSample[]>([]);
  const [projects, setProjects] = React.useState<FinduProject[]>([]);
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState("");
  const [reload, setReload] = React.useState(0);
  const [keyword, setKeyword] = React.useState("");

  // 详情视图
  const [detail, setDetail] = React.useState<ApkSample | null>(null);
  const [manifest, setManifest] = React.useState("");
  const [report, setReport] = React.useState<string>("");
  const [classQuery, setClassQuery] = React.useState("");
  const [source, setSource] = React.useState("");
  const [detailLoading, setDetailLoading] = React.useState(false);
  const [sourceLoading, setSourceLoading] = React.useState(false);
  const [tab, setTab] = React.useState<"manifest" | "report" | "source">("manifest");

  // 上传
  const [uploadOpen, setUploadOpen] = React.useState(false);
  const [uploadFile, setUploadFile] = React.useState<File | null>(null);
  const [uploadProject, setUploadProject] = React.useState("");
  const [uploading, setUploading] = React.useState(false);

  // 删除
  const [deleteTarget, setDeleteTarget] = React.useState<ApkSample | null>(null);
  const [deleting, setDeleting] = React.useState(false);

  // biome-ignore lint/correctness/useExhaustiveDependencies: reload is the explicit refresh trigger.
  React.useEffect(() => {
    let alive = true;
    setLoading(true);
    setError("");
    void Promise.allSettled([listApkSamples(), listFinduProjects(500)])
      .then(([sr, pr]) => {
        if (!alive) return;
        if (sr.status === "fulfilled") setSamples(sr.value.samples ?? []);
        else setError(sr.reason instanceof Error ? sr.reason.message : String(sr.reason));
        if (pr.status === "fulfilled") setProjects(pr.value.projects ?? []);
        setLoading(false);
      })
      .catch((e: Error) => {
        if (alive) setError(e.message);
      });
    return () => {
      alive = false;
    };
  }, [reload]);

  function refresh() {
    setReload((n) => n + 1);
  }

  async function submitUpload() {
    if (!uploadFile) {
      toast.error("请选择 .apk 文件");
      return;
    }
    setUploading(true);
    try {
      const sample = await uploadApkSample(uploadFile, {
        project_id: uploadProject || undefined,
      });
      toast.success(`样本已上传（自动分析已启动：${sample.id.slice(0, 8)}）`);
      setUploadOpen(false);
      setUploadFile(null);
      setUploadProject("");
      refresh();
    } catch (e) {
      toast.error(`上传失败：${e instanceof Error ? e.message : String(e)}`);
    } finally {
      setUploading(false);
    }
  }

  async function openDetail(s: ApkSample) {
    setDetail(s);
    setDetailLoading(true);
    setManifest("");
    setReport("");
    setSource("");
    setClassQuery("");
    setTab("manifest");
    try {
      const [mxml, rpt] = await Promise.allSettled([
        readApkManifest(s.id),
        getApkAutoReport(s.id),
      ]);
      if (mxml.status === "fulfilled") setManifest(mxml.value);
      if (rpt.status === "fulfilled") setReport(JSON.stringify(rpt.value, null, 2));
    } finally {
      setDetailLoading(false);
    }
  }

  async function loadSource() {
    if (!detail || !classQuery.trim()) {
      toast.error("请输入类描述符，如 " + SOURCE_EXAMPLE);
      return;
    }
    setSourceLoading(true);
    setSource("");
    try {
      const r = await getApkSource(detail.id, classQuery.trim());
      setSource(r.source);
    } catch (e) {
      setSource(`// 反编译失败：${e instanceof Error ? e.message : String(e)}`);
    } finally {
      setSourceLoading(false);
    }
  }

  async function submitDelete() {
    const t = deleteTarget;
    if (!t) return;
    setDeleting(true);
    try {
      await deleteApkSample(t.id);
      toast.success("样本已删除（含工作区）");
      setDeleteTarget(null);
      if (detail?.id === t.id) setDetail(null);
      refresh();
    } catch (e) {
      toast.error(`删除失败：${e instanceof Error ? e.message : String(e)}`);
    } finally {
      setDeleting(false);
    }
  }

  // ---------- 渲染 ----------
  if (detail) {
    return (
      <div className="flex h-[calc(100dvh-6rem)] min-h-0 flex-col gap-4 md:h-[calc(100dvh-7rem)] md:gap-6">
        <div className="flex items-start justify-between gap-3">
          <div>
            <Button variant="ghost" size="sm" className="-ml-2 mb-1" onClick={() => setDetail(null)}>
              <ArrowLeftIcon className="size-4" /> 返回样本列表
            </Button>
            <h1 className="flex items-center gap-2 text-xl font-semibold tracking-tight">
              <SmartphoneIcon className="size-5" /> {detail.name}
            </h1>
            <p className="text-muted-foreground text-sm">
              {formatApkBytes(detail.size)} · SHA256 前 12 位 {detail.sha256.slice(0, 12)}
              {detail.package_name ? ` · ${detail.package_name}` : ""}
            </p>
          </div>
          <div className="flex items-center gap-2">
            <Button
              size="sm"
              variant="destructive"
              onClick={() => {
                setDeleteTarget(detail);
              }}
            >
              <Trash2Icon className="size-4" /> 删除样本
            </Button>
          </div>
        </div>

        <div className="flex items-center gap-2">
          <Button variant={tab === "manifest" ? "default" : "outline"} size="sm" onClick={() => setTab("manifest")}>
            <FileCodeIcon className="size-4" /> Manifest
          </Button>
          <Button variant={tab === "report" ? "default" : "outline"} size="sm" onClick={() => setTab("report")}>
            <PackageIcon className="size-4" /> 自动分析报告
          </Button>
          <Button variant={tab === "source" ? "default" : "outline"} size="sm" onClick={() => setTab("source")}>
            <FileCodeIcon className="size-4" /> 反编译
          </Button>
        </div>

        {detailLoading ? (
          <Skeleton className="h-64 w-full" />
        ) : tab === "manifest" ? (
          <Card className="max-h-[60vh] overflow-auto">
            <CardContent className="p-0">
              <CodeBlock code={manifest || "（无 Manifest 或解析失败）"} maxHeight="max-h-[60vh]" />
            </CardContent>
          </Card>
        ) : tab === "report" ? (
          <Card className="max-h-[60vh] overflow-auto">
            <CardContent className="p-0">
              <CodeBlock code={report || "（自动分析进行中或无报告）"} maxHeight="max-h-[60vh]" />
            </CardContent>
          </Card>
        ) : (
          <div className="flex flex-col gap-3">
            <div className="flex items-center gap-2">
              <Input
                className="max-w-md font-mono text-xs"
                placeholder={`类描述符，如 ${SOURCE_EXAMPLE}`}
                value={classQuery}
                onChange={(e) => setClassQuery(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") void loadSource();
                }}
              />
              <Button size="sm" onClick={() => void loadSource()} disabled={sourceLoading}>
                {sourceLoading ? <Loader2Icon className="size-4 animate-spin" /> : <SearchIcon className="size-4" />} 反编译
              </Button>
            </div>
            <Card className="max-h-[55vh] overflow-auto">
              <CardContent className="p-0">
                {sourceLoading ? (
                  <Skeleton className="m-3 h-48 w-[calc(100%-1.5rem)]" />
                ) : (
                  <CodeBlock code={source || "输入类描述符后点击反编译（jadx/dad/smali 降级链，无后端时输出 smali 字节码视图）"} maxHeight="max-h-[55vh]" />
                )}
              </CardContent>
            </Card>
          </div>
        )}

        {deleteDialog()}
      </div>
    );
  }

  // ---------- 列表视图 ----------
  const filtered = keyword
    ? samples.filter(
        (s) =>
          s.name.toLowerCase().includes(keyword.toLowerCase()) ||
          (s.package_name ?? "").toLowerCase().includes(keyword.toLowerCase()),
      )
    : samples;

  return (
    <div className="flex h-[calc(100dvh-6rem)] min-h-0 flex-col gap-4 md:h-[calc(100dvh-7rem)] md:gap-6">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h1 className="flex items-center gap-2 text-xl font-semibold tracking-tight">
            <SmartphoneIcon className="size-5" /> APK 分析
          </h1>
          <p className="text-muted-foreground text-sm">
            APK 上传 · 纯 Go DEX 引擎 · Manifest/反编译/敏感扫描 · 共 {samples.length} 个样本
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button size="sm" onClick={() => setUploadOpen(true)}>
            <UploadIcon className="size-4" /> 上传 APK
          </Button>
          <Button variant="outline" onClick={refresh} disabled={loading}>
            <RefreshCwIcon className={cn("size-4", loading && "animate-spin")} /> 刷新
          </Button>
        </div>
      </div>

      <Card className="border-dashed">
        <CardContent className="py-3">
          <div className="text-muted-foreground text-xs">
            引擎为 Droid ASC 的 Go 重写（apkengine，毫秒级 DEX 检索，无需外部依赖）；反编译走
            jadx → DAD → smali 降级链。agent 工具{" "}
            <code>android_analyze / android_findrefs / android_source / android_extract_apis</code>{" "}
            可在会话中直接调用同一引擎。
          </div>
        </CardContent>
      </Card>

      {error ? (
        <Card className="border-destructive/40 bg-destructive/5">
          <CardContent className="text-destructive py-3 text-sm">样本列表加载失败：{error}</CardContent>
        </Card>
      ) : null}

      <div className="flex items-center gap-2">
        <div className="relative w-64">
          <SearchIcon className="text-muted-foreground absolute top-1/2 left-2 size-4 -translate-y-1/2" />
          <Input
            className="pl-8"
            placeholder="按名称 / 包名过滤"
            value={keyword}
            onChange={(e) => setKeyword(e.target.value)}
          />
        </div>
      </div>

      <Card className="overflow-hidden">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>样本</TableHead>
              <TableHead className="w-56">包名</TableHead>
              <TableHead className="w-24">大小</TableHead>
              <TableHead className="w-28">来源</TableHead>
              <TableHead className="w-40">上传时间</TableHead>
              <TableHead className="w-24 text-right">操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading ? (
              Array.from({ length: 5 }).map((_, i) => (
                <TableRow key={i}>
                  <TableCell colSpan={6}>
                    <Skeleton className="h-5 w-full" />
                  </TableCell>
                </TableRow>
              ))
            ) : filtered.length === 0 ? (
              <TableRow>
                <TableCell colSpan={6} className="text-muted-foreground py-8 text-center">
                  <PackageIcon className="mx-auto mb-2 size-6 opacity-50" />
                  暂无样本 · 点击右上角「上传 APK」开始
                </TableCell>
              </TableRow>
            ) : (
              filtered.map((s) => (
                <TableRow key={s.id}>
                  <TableCell>
                    <button
                      type="button"
                      className="font-medium hover:underline"
                      onClick={() => void openDetail(s)}
                    >
                      {s.name}
                    </button>
                    <div className="text-muted-foreground font-mono text-xs" title={s.sha256}>
                      {s.sha256.slice(0, 12)}
                    </div>
                  </TableCell>
                  <TableCell className="font-mono text-xs">{s.package_name || "—"}</TableCell>
                  <TableCell className="text-xs">{formatApkBytes(s.size)}</TableCell>
                  <TableCell className="text-xs">{s.source || "gui_upload"}</TableCell>
                  <TableCell className="text-muted-foreground text-xs">{formatTime(s.created_at)}</TableCell>
                  <TableCell className="text-right">
                    <div className="flex items-center justify-end gap-1">
                      {s.status === "analyzing" ? (
                        <Badge variant="secondary">分析中</Badge>
                      ) : null}
                      <Button
                        size="icon"
                        variant="ghost"
                        className="text-destructive size-8"
                        title="删除样本"
                        onClick={() => setDeleteTarget(s)}
                      >
                        <Trash2Icon className="size-4" />
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </Card>

      {/* 上传 Dialog */}
      <Dialog open={uploadOpen} onOpenChange={setUploadOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>上传 APK 样本</DialogTitle>
            <DialogDescription>
              上传即自动解包分析（后台任务）；报告在详情页「自动分析报告」查看。
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-4 py-2">
            <div className="grid gap-2">
              <Label htmlFor="apk-file">APK 文件</Label>
              <Input
                id="apk-file"
                type="file"
                accept=".apk"
                onChange={(e) => setUploadFile(e.target.files?.[0] ?? null)}
              />
              {uploadFile ? (
                <div className="text-muted-foreground text-xs">
                  已选：{uploadFile.name}（{formatApkBytes(uploadFile.size)}）
                </div>
              ) : null}
            </div>
            <div className="grid gap-2">
              <Label>所属项目</Label>
              <Select value={uploadProject} onValueChange={setUploadProject}>
                <SelectTrigger>
                  <SelectValue placeholder="未绑定" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="">未绑定</SelectItem>
                  {projects.map((p) => (
                    <SelectItem key={p.id} value={p.id}>
                      {p.name || p.id}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setUploadOpen(false)} disabled={uploading}>
              取消
            </Button>
            <Button onClick={() => void submitUpload()} disabled={uploading}>
              {uploading ? <Loader2Icon className="size-4 animate-spin" /> : <UploadIcon className="size-4" />} 上传并分析
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {deleteDialog()}
    </div>
  );

  function deleteDialog() {
    return (
      <Dialog open={Boolean(deleteTarget)} onOpenChange={(o) => !o && setDeleteTarget(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>删除样本</DialogTitle>
            <DialogDescription>
              样本 {deleteTarget?.name} 连同工作区与数据库行将被删除。此操作不可恢复。
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDeleteTarget(null)} disabled={deleting}>
              取消
            </Button>
            <Button variant="destructive" onClick={() => void submitDelete()} disabled={deleting}>
              {deleting ? <Loader2Icon className="size-4 animate-spin" /> : <Trash2Icon className="size-4" />} 删除
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    );
  }
}