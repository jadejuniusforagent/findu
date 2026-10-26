"use client";

import * as React from "react";

import {
  AlertTriangleIcon,
  ArrowLeftIcon,
  CableIcon,
  FileCodeIcon,
  Loader2Icon,
  NetworkIcon,
  PackageIcon,
  RefreshCwIcon,
  ScanSearchIcon,
  SearchIcon,
  ShieldCheckIcon,
  SmartphoneIcon,
  Trash2Icon,
  UploadIcon,
} from "lucide-react";
import { toast } from "sonner";

import { CodeBlock } from "@/components/code-block";
import { MiniprogramTopologyGraph, type MiniprogramTopologyNode } from "@/components/miniprogram-topology";
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
  type MiniprogramFinding,
  type MiniprogramInterfaceItem,
  type MiniprogramInterfaceWorkbench,
  type MiniprogramSample,
  type MiniprogramTopology,
  deleteMiniprogramSample,
  fillMiniprogramAppID,
  formatMiniprogramBytes,
  listMiniprogramFindings,
  listMiniprogramInterfaceWorkbench,
  listMiniprogramSamples,
  listMiniprogramSourceFiles,
  listMiniprogramTopology,
  readMiniprogramSourceFile,
  rescanMiniprogramSample,
  uploadMiniprogramSample,
} from "@/lib/miniprogram-api";
import { listFinduProjects, type FinduProject } from "@/lib/readonly-api";
import { cn } from "@/lib/utils";

function severityBadge(sev: string) {
  const s = sev.toLowerCase();
  if (s === "critical" || s === "high") return <Badge variant="destructive">{sev}</Badge>;
  if (s === "medium") return <Badge>{sev}</Badge>;
  if (s === "low") return <Badge variant="secondary">{sev}</Badge>;
  return <Badge variant="outline">{sev}</Badge>;
}

interface UploadState {
  files: File[];
  appid: string;
  project_id: string;
}

export default function MiniprogramPage() {
  const [samples, setSamples] = React.useState<MiniprogramSample[]>([]);
  const [projects, setProjects] = React.useState<FinduProject[]>([]);
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState("");
  const [reload, setReload] = React.useState(0);
  const [keyword, setKeyword] = React.useState("");

  // 详情视图状态
  const [detail, setDetail] = React.useState<MiniprogramSample | null>(null);
  const [files, setFiles] = React.useState<string[]>([]);
  const [activeFile, setActiveFile] = React.useState<string>("");
  const [fileContent, setFileContent] = React.useState<string>("");
  const [fileLoading, setFileLoading] = React.useState(false);
  const [findings, setFindings] = React.useState<MiniprogramFinding[]>([]);
  const [detailLoading, setDetailLoading] = React.useState(false);
  const [tab, setTab] = React.useState<"source" | "findings" | "architecture" | "workbench">("source");
  const [topology, setTopology] = React.useState<MiniprogramTopology>({ nodes: [], edges: [], stats: {} });
  const [topologyLoading, setTopologyLoading] = React.useState(false);
  const [workbench, setWorkbench] = React.useState<MiniprogramInterfaceWorkbench>({ interfaces: [], stats: {}, domains: [] });
  const [workbenchLoading, setWorkbenchLoading] = React.useState(false);
  const [workbenchFilter, setWorkbenchFilter] = React.useState<"all" | "idor" | "unauth">("all");

  // 上传 Dialog
  const [uploadOpen, setUploadOpen] = React.useState(false);
  const [uploadForm, setUploadForm] = React.useState<UploadState>({ files: [], appid: "", project_id: "" });
  const [uploading, setUploading] = React.useState(false);

  // 删除确认
  const [deleteTarget, setDeleteTarget] = React.useState<MiniprogramSample | null>(null);
  const [deleting, setDeleting] = React.useState(false);

  // AppID 补填
  const [appidTarget, setAppidTarget] = React.useState<MiniprogramSample | null>(null);
  const [appidInput, setAppidInput] = React.useState("");
  const [appidSaving, setAppidSaving] = React.useState(false);

  const [rescanning, setRescanning] = React.useState(false);

  // biome-ignore lint/correctness/useExhaustiveDependencies: reload is the explicit refresh trigger.
  React.useEffect(() => {
    let alive = true;
    setLoading(true);
    setError("");
    void Promise.allSettled([listMiniprogramSamples(), listFinduProjects(500)])
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

  // ---------- 上传 ----------
  function onPickFiles(fl: FileList | null) {
    const arr = fl ? Array.from(fl) : [];
    const invalid = arr.filter((f) => !f.name.toLowerCase().endsWith(".wxapkg"));
    if (invalid.length > 0) {
      toast.error(`仅接受 .wxapkg 文件：${invalid.map((f) => f.name).join("、")}`);
      setUploadForm((f) => ({ ...f, files: [] }));
      return;
    }
    setUploadForm((f) => ({ ...f, files: arr }));
  }

  async function submitUpload() {
    if (uploadForm.files.length === 0) {
      toast.error("请选择 .wxapkg 文件");
      return;
    }
    setUploading(true);
    try {
      const sample = await uploadMiniprogramSample(uploadForm.files, {
        appid: uploadForm.appid.trim() || undefined,
        project_id: uploadForm.project_id || undefined,
      });
      toast.success(
        sample.overwritten && sample.overwritten.length > 0
          ? `样本已注册（合并 ${sample.file_count} 个文件，分包覆盖 ${sample.overwritten.length} 个文件）`
          : `样本已注册（解包 ${sample.file_count} 个文件）`,
      );
      setUploadOpen(false);
      setUploadForm({ files: [], appid: "", project_id: "" });
      refresh();
    } catch (e) {
      toast.error(`上传失败：${e instanceof Error ? e.message : String(e)}`);
    } finally {
      setUploading(false);
    }
  }

  // ---------- 详情 ----------
  async function openDetail(s: MiniprogramSample) {
    setDetail(s);
    setDetailLoading(true);
    setFiles([]);
    setActiveFile("");
    setFileContent("");
    setFindings([]);
    setTopology({ nodes: [], edges: [], stats: {} });
    setWorkbench({ interfaces: [], stats: {}, domains: [] });
    setTab("source");
    try {
      const [fs, fds] = await Promise.all([listMiniprogramSourceFiles(s.id), listMiniprogramFindings(s.id)]);
      const sorted = [...(fs ?? [])].sort();
      setFiles(sorted);
      setFindings(fds ?? []);
      setTopologyLoading(true);
      void listMiniprogramTopology(s.id)
        .then((t) => {
          setTopology(t);
        })
        .catch(() => {
          setTopology({ nodes: [], edges: [], stats: {} });
        })
        .finally(() => setTopologyLoading(false));
      setWorkbenchLoading(true);
      void listMiniprogramInterfaceWorkbench(s.id)
        .then((w) => {
          setWorkbench(w);
        })
        .catch(() => {
          setWorkbench({ interfaces: [], stats: {}, domains: [] });
        })
        .finally(() => setWorkbenchLoading(false));
      if (sorted.length > 0) {
        await loadFile(s.id, sorted[0]);
      }
    } catch (e) {
      toast.error(`加载详情失败：${e instanceof Error ? e.message : String(e)}`);
    } finally {
      setDetailLoading(false);
    }
  }

  function handleTopologySelect(n: MiniprogramTopologyNode) {
    if (!detail) return;
    if (n.file) {
      setTab("source");
      void loadFile(detail.id, n.file);
    } else {
      toast.info("该页面已合并进主包/分包，无独立源码文件，请查看 app-service.js 或对应 chunk");
    }
  }

  const filteredWorkbench = workbench.interfaces.filter((i) => {
    if (workbenchFilter === "idor") return i.idorCandidate;
    if (workbenchFilter === "unauth") return i.unauthCandidate;
    return true;
  });

  function methodBadge(m: string) {
    const tone =
      m === "POST"
        ? "bg-orange-500/15 text-orange-600 dark:text-orange-400"
        : m === "GET"
          ? "bg-blue-500/15 text-blue-600 dark:text-blue-400"
          : "bg-slate-500/15 text-slate-600 dark:text-slate-300";
    return (
      <span className={cn("inline-flex rounded px-1.5 py-0.5 font-mono text-[10px] font-semibold", tone)}>
        {m}
      </span>
    );
  }

  async function loadFile(sampleID: string, path: string) {
    setActiveFile(path);
    setFileLoading(true);
    setFileContent("");
    try {
      const text = await readMiniprogramSourceFile(sampleID, path);
      setFileContent(text);
    } catch (e) {
      setFileContent(`// 读取失败：${e instanceof Error ? e.message : String(e)}`);
    } finally {
      setFileLoading(false);
    }
  }

  async function submitDelete() {
    const t = deleteTarget;
    if (!t) return;
    setDeleting(true);
    try {
      await deleteMiniprogramSample(t.id);
      toast.success("样本已删除（含工作区文件）");
      setDeleteTarget(null);
      if (detail?.id === t.id) setDetail(null);
      refresh();
    } catch (e) {
      toast.error(`删除失败：${e instanceof Error ? e.message : String(e)}`);
    } finally {
      setDeleting(false);
    }
  }

  async function submitAppID() {
    const t = appidTarget;
    if (!t) return;
    const appid = appidInput.trim();
    if (!appid) {
      toast.error("AppID 不能为空");
      return;
    }
    setAppidSaving(true);
    try {
      const updated = await fillMiniprogramAppID(t.id, appid);
      toast.success(`已解密解包：${updated.file_count} 个文件`);
      setAppidTarget(null);
      setAppidInput("");
      if (detail?.id === t.id) {
        await openDetail(updated);
      }
      refresh();
    } catch (e) {
      toast.error(`补填 AppID 失败：${e instanceof Error ? e.message : String(e)}`);
    } finally {
      setAppidSaving(false);
    }
  }

  async function submitRescan() {
    if (!detail) return;
    setRescanning(true);
    try {
      await rescanMiniprogramSample(detail.id);
      toast.success("已触发重新扫描");
      const fds = await listMiniprogramFindings(detail.id);
      setFindings(fds ?? []);
    } catch (e) {
      toast.error(`重扫失败：${e instanceof Error ? e.message : String(e)}`);
    } finally {
      setRescanning(false);
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
              {detail.file_count} 个文件 · {formatMiniprogramBytes(detail.size)} · SHA256 前 12 位{" "}
              {detail.sha256.slice(0, 12)}
            </p>
          </div>
          <div className="flex items-center gap-2">
            {!detail.appid ? (
              <Button
                size="sm"
                variant="outline"
                onClick={() => {
                  setAppidTarget(detail);
                  setAppidInput("");
                }}
              >
                <UploadIcon className="size-4" /> 补填 AppID 解密
              </Button>
            ) : null}
            <Button size="sm" variant="outline" onClick={() => void submitRescan()} disabled={rescanning}>
              {rescanning ? (
                <Loader2Icon className="size-4 animate-spin" />
              ) : (
                <ScanSearchIcon className="size-4" />
              )}{" "}
              重新扫描
            </Button>
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

        <Card>
          <CardContent className="grid grid-cols-2 gap-2 py-3 text-sm md:grid-cols-4">
            <div>
              <span className="text-muted-foreground text-xs">AppID</span>
              <div className="font-mono text-xs">{detail.appid || "—（未提供）"}</div>
            </div>
            <div>
              <span className="text-muted-foreground text-xs">来源</span>
              <div>{detail.source || "gui_upload"}</div>
            </div>
            <div>
              <span className="text-muted-foreground text-xs">上传时间</span>
              <div>{formatTime(detail.created_at)}</div>
            </div>
            <div>
              <span className="text-muted-foreground text-xs">工作区</span>
              <div className="font-mono text-xs">{detail.workspace_display || detail.workspace_path}</div>
            </div>
          </CardContent>
        </Card>

        <div className="flex items-center gap-2">
          <Button variant={tab === "source" ? "default" : "outline"} size="sm" onClick={() => setTab("source")}>
            <FileCodeIcon className="size-4" /> 源码（{files.length}）
          </Button>
          <Button variant={tab === "findings" ? "default" : "outline"} size="sm" onClick={() => setTab("findings")}>
            <ScanSearchIcon className="size-4" /> 敏感发现（{findings.length}）
          </Button>
          <Button variant={tab === "architecture" ? "default" : "outline"} size="sm" onClick={() => setTab("architecture")}>
            <NetworkIcon className="size-4" /> 架构拓扑
          </Button>
          <Button variant={tab === "workbench" ? "default" : "outline"} size="sm" onClick={() => setTab("workbench")}>
            <CableIcon className="size-4" /> 接口工作台
          </Button>
        </div>

        {detailLoading ? (
          <Skeleton className="h-64 w-full" />
        ) : tab === "source" ? (
          <div className="grid grid-cols-1 gap-4 lg:grid-cols-[260px_1fr]">
            <Card className="max-h-[60vh] overflow-auto">
              <CardContent className="p-2">
                {files.length === 0 ? (
                  <div className="text-muted-foreground py-8 text-center text-sm">无源码文件</div>
                ) : (
                  files.map((f) => (
                    <button
                      key={f}
                      type="button"
                      className={cn(
                        "block w-full truncate rounded px-2 py-1.5 text-left font-mono text-xs hover:bg-accent",
                        f === activeFile && "bg-accent font-medium",
                      )}
                      onClick={() => void loadFile(detail.id, f)}
                      title={f}
                    >
                      {f}
                    </button>
                  ))
                )}
              </CardContent>
            </Card>
            <Card className="max-h-[60vh] overflow-auto">
              <CardContent className="p-0">
                <div className="border-b px-3 py-2 font-mono text-xs">{activeFile || "选择左侧文件查看内容"}</div>
                {fileLoading ? (
                  <Skeleton className="m-3 h-48 w-[calc(100%-1.5rem)]" />
                ) : (
                  <CodeBlock code={fileContent} maxHeight="max-h-[60vh]" />
                )}
              </CardContent>
            </Card>
          </div>
        ) : tab === "findings" ? (
          <Card className="overflow-hidden">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="w-28">严重度</TableHead>
                  <TableHead className="w-36">类别</TableHead>
                  <TableHead className="w-64">位置</TableHead>
                  <TableHead>内容</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {findings.length === 0 ? (
                  <TableRow>
                    <TableCell colSpan={4} className="text-muted-foreground py-8 text-center">
                      暂无敏感信息发现（可点击右上角重新扫描）
                    </TableCell>
                  </TableRow>
                ) : (
                  findings.map((f) => (
                    <TableRow key={f.id}>
                      <TableCell>{severityBadge(f.severity)}</TableCell>
                      <TableCell className="text-xs">{f.category}</TableCell>
                      <TableCell className="font-mono text-xs">
                        {f.file_path}
                        {f.line_number > 0 ? `:${f.line_number}` : ""}
                      </TableCell>
                      <TableCell>
                        <div className="max-w-xl truncate font-mono text-xs" title={f.content}>
                          {f.content}
                        </div>
                        {f.description ? (
                          <div className="text-muted-foreground text-xs">{f.description}</div>
                        ) : null}
                      </TableCell>
                    </TableRow>
                  ))
                )}
              </TableBody>
            </Table>
          </Card>
        ) : tab === "architecture" ? (
          <div className="h-[62vh] min-h-0">
            {topologyLoading ? (
              <Skeleton className="h-full w-full" />
            ) : (
              <MiniprogramTopologyGraph
                topology={topology}
                onSelect={handleTopologySelect}
                className="h-full"
              />
            )}
          </div>
        ) : (
          <div className="flex flex-col gap-3">
            <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
              <div className="flex items-center gap-3 rounded-lg border bg-card p-3">
                <span className="flex size-9 items-center justify-center rounded-lg bg-blue-500/15 text-blue-600 dark:text-blue-400">
                  <CableIcon className="size-4" />
                </span>
                <div>
                  <div className="text-xl font-semibold tabular-nums">{workbench.stats.interfaces ?? 0}</div>
                  <div className="text-xs text-muted-foreground">接口总数</div>
                </div>
              </div>
              <div className="flex items-center gap-3 rounded-lg border bg-card p-3">
                <span className="flex size-9 items-center justify-center rounded-lg bg-red-500/15 text-red-600 dark:text-red-400">
                  <AlertTriangleIcon className="size-4" />
                </span>
                <div>
                  <div className="text-xl font-semibold tabular-nums">{workbench.stats.idor ?? 0}</div>
                  <div className="text-xs text-muted-foreground">越权(IDOR)候选</div>
                </div>
              </div>
              <div className="flex items-center gap-3 rounded-lg border bg-card p-3">
                <span className="flex size-9 items-center justify-center rounded-lg bg-amber-500/15 text-amber-600 dark:text-amber-400">
                  <ShieldCheckIcon className="size-4" />
                </span>
                <div>
                  <div className="text-xl font-semibold tabular-nums">{workbench.stats.unauth ?? 0}</div>
                  <div className="text-xs text-muted-foreground">未授权候选</div>
                </div>
              </div>
              <div className="flex items-center gap-3 rounded-lg border bg-card p-3">
                <span className="flex size-9 items-center justify-center rounded-lg bg-violet-500/15 text-violet-600 dark:text-violet-400">
                  <NetworkIcon className="size-4" />
                </span>
                <div>
                  <div className="text-xl font-semibold tabular-nums">{workbench.stats.domains ?? 0}</div>
                  <div className="text-xs text-muted-foreground">域名数</div>
                </div>
              </div>
            </div>
            <div className="flex flex-wrap gap-2">
              {[
                { k: "all" as const, label: `全部（${workbench.interfaces.length}）` },
                { k: "idor" as const, label: `越权候选（${workbench.interfaces.filter((i) => i.idorCandidate).length}）` },
                { k: "unauth" as const, label: `未授权候选（${workbench.interfaces.filter((i) => i.unauthCandidate).length}）` },
              ].map((f) => (
                <Button
                  key={f.k}
                  size="sm"
                  variant={workbenchFilter === f.k ? "default" : "outline"}
                  onClick={() => setWorkbenchFilter(f.k)}
                >
                  {f.label}
                </Button>
              ))}
            </div>
            <Card className="overflow-hidden">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="w-20">方法</TableHead>
                    <TableHead>接口</TableHead>
                    <TableHead className="w-44">来源文件</TableHead>
                    <TableHead className="w-16 text-right">热度</TableHead>
                    <TableHead className="w-44">参数</TableHead>
                    <TableHead className="w-40">标记</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {workbenchLoading ? (
                    <TableRow>
                      <TableCell colSpan={6}>
                        <Skeleton className="h-40 w-full" />
                      </TableCell>
                    </TableRow>
                  ) : filteredWorkbench.length === 0 ? (
                    <TableRow>
                      <TableCell colSpan={6} className="text-muted-foreground py-8 text-center">
                        未提取到接口
                      </TableCell>
                    </TableRow>
                  ) : (
                    filteredWorkbench.map((it) => (
                      <TableRow
                        key={it.id}
                        className="cursor-pointer"
                        onClick={() => {
                          if (it.sourceFile) {
                            setTab("source");
                            void loadFile(detail.id, it.sourceFile);
                          }
                        }}
                      >
                        <TableCell>{methodBadge(it.method)}</TableCell>
                        <TableCell>
                          <div className="flex flex-col gap-1">
                            <div className="font-mono text-xs">{it.url}</div>
                            {it.rawUrl && it.rawUrl !== it.url ? (
                              <div className="line-clamp-1 font-mono text-[10px] text-muted-foreground" title={it.rawUrl}>
                                {it.rawUrl}
                              </div>
                            ) : null}
                            {it.highlights.length > 0 ? (
                              <div className="flex flex-wrap gap-1">
                                {it.highlights.map((h) => (
                                  <span key={h} className="inline-flex rounded bg-slate-500/10 px-1.5 py-0.5 text-[10px] text-muted-foreground">
                                    {h}
                                  </span>
                                ))}
                              </div>
                            ) : null}
                          </div>
                        </TableCell>
                        <TableCell className="font-mono text-xs">{it.sourceFile}</TableCell>
                        <TableCell className="text-right text-xs tabular-nums">{it.refCount}</TableCell>
                        <TableCell className="text-xs">{it.params.length ? it.params.join(", ") : "—"}</TableCell>
                        <TableCell>
                          <div className="flex flex-wrap gap-1">
                            {it.idorCandidate ? (
                              <span className="inline-flex rounded bg-red-500/15 px-1.5 py-0.5 text-[10px] font-semibold text-red-600 dark:text-red-400">
                                越权
                              </span>
                            ) : null}
                            {it.unauthCandidate ? (
                              <span className="inline-flex rounded bg-amber-500/15 px-1.5 py-0.5 text-[10px] font-semibold text-amber-600 dark:text-amber-400">
                                未授权
                              </span>
                            ) : null}
                            {it.hasAuth ? (
                              <span className="inline-flex rounded bg-emerald-500/15 px-1.5 py-0.5 text-[10px] font-semibold text-emerald-600 dark:text-emerald-400">
                                带鉴权
                              </span>
                            ) : (
                              <span className="inline-flex rounded bg-slate-500/15 px-1.5 py-0.5 text-[10px] font-semibold text-slate-500">
                                无鉴权
                              </span>
                            )}
                          </div>
                        </TableCell>
                      </TableRow>
                    ))
                  )}
                </TableBody>
              </Table>
            </Card>
          </div>
        )}

        {deleteDialog()}
        {appidDialog()}
      </div>
    );
  }

  // ---------- 列表视图 ----------
  const filtered = keyword
    ? samples.filter(
        (s) =>
          s.name.toLowerCase().includes(keyword.toLowerCase()) ||
          (s.appid ?? "").toLowerCase().includes(keyword.toLowerCase()),
      )
    : samples;

  return (
    <div className="flex h-[calc(100dvh-6rem)] min-h-0 flex-col gap-4 md:h-[calc(100dvh-7rem)] md:gap-6">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h1 className="flex items-center gap-2 text-xl font-semibold tracking-tight">
            <SmartphoneIcon className="size-5" /> 小程序分析
          </h1>
          <p className="text-muted-foreground text-sm">
            wxapkg 上传解包 · 敏感信息扫描 · 共 {samples.length} 个样本
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button size="sm" onClick={() => setUploadOpen(true)}>
            <UploadIcon className="size-4" /> 上传 wxapkg
          </Button>
          <Button variant="outline" onClick={refresh} disabled={loading}>
            <RefreshCwIcon className={cn("size-4", loading && "animate-spin")} /> 刷新
          </Button>
        </div>
      </div>

      <Card className="border-dashed">
        <CardContent className="py-3">
          <div className="text-muted-foreground text-xs">
            支持 PC 微信缓存直取（agent 工具 <code>miniprogram_pc_fetch</code>）与手工上传；明文包直接解包，
            V1MMWX 加密包需在上传时填写 AppID（即缓存目录名，如 wx1234567890abcdef），缺省时样本会挂起，
            进入详情补填即可解密。敏感发现可交由 agent 工具{" "}
            <code>miniprogram_test_items</code> 走测试项账本闭环。
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
            placeholder="按名称 / AppID 过滤"
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
              <TableHead className="w-40">AppID</TableHead>
              <TableHead className="w-20">文件数</TableHead>
              <TableHead className="w-24">大小</TableHead>
              <TableHead className="w-28">来源</TableHead>
              <TableHead className="w-40">上传时间</TableHead>
              <TableHead className="w-28 text-right">操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading ? (
              Array.from({ length: 5 }).map((_, i) => (
                <TableRow key={i}>
                  <TableCell colSpan={7}>
                    <Skeleton className="h-5 w-full" />
                  </TableCell>
                </TableRow>
              ))
            ) : filtered.length === 0 ? (
              <TableRow>
                <TableCell colSpan={7} className="text-muted-foreground py-8 text-center">
                  <PackageIcon className="mx-auto mb-2 size-6 opacity-50" />
                  暂无样本 · 点击右上角「上传 wxapkg」开始
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
                      {!s.appid ? " · 待补 AppID" : ""}
                    </div>
                  </TableCell>
                  <TableCell className="font-mono text-xs">{s.appid || "—"}</TableCell>
                  <TableCell className="text-xs">{s.file_count}</TableCell>
                  <TableCell className="text-xs">{formatMiniprogramBytes(s.size)}</TableCell>
                  <TableCell className="text-xs">{s.source || "gui_upload"}</TableCell>
                  <TableCell className="text-muted-foreground text-xs">{formatTime(s.created_at)}</TableCell>
                  <TableCell className="text-right">
                    <div className="flex items-center justify-end gap-1">
                      {!s.appid ? (
                        <Button
                          size="icon"
                          variant="ghost"
                          className="size-8"
                          title="补填 AppID 解密"
                          onClick={() => {
                            setAppidTarget(s);
                            setAppidInput("");
                          }}
                        >
                          <UploadIcon className="size-4" />
                        </Button>
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
            <DialogTitle>上传 wxapkg 样本</DialogTitle>
            <DialogDescription>
              支持一次选择多个包（PC 微信 4.x 常拆成 __APP__ + _subpackages_* 分包，会合并解包为一个样本）。
              V1MMWX 加密包需填写 AppID 才能解密。
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-4 py-2">
            <div className="grid gap-2">
              <Label htmlFor="mp-files">wxapkg 文件（可多选）</Label>
              <Input
                id="mp-files"
                type="file"
                multiple
                accept=".wxapkg"
                onChange={(e) => onPickFiles(e.target.files)}
              />
              {uploadForm.files.length > 0 ? (
                <div className="text-muted-foreground text-xs">
                  已选 {uploadForm.files.length} 个包：
                  {uploadForm.files.map((f) => f.name).join("、")}
                </div>
              ) : null}
            </div>
            <div className="grid gap-2">
              <Label htmlFor="mp-appid">AppID（加密包必填，明文包可留空）</Label>
              <Input
                id="mp-appid"
                value={uploadForm.appid}
                onChange={(e) => setUploadForm((f) => ({ ...f, appid: e.target.value }))}
                placeholder="wx1234567890abcdef"
              />
            </div>
            <div className="grid gap-2">
              <Label>所属项目</Label>
              <Select
                value={uploadForm.project_id}
                onValueChange={(v) => setUploadForm((f) => ({ ...f, project_id: v }))}
              >
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
              {uploading ? <Loader2Icon className="size-4 animate-spin" /> : <UploadIcon className="size-4" />}{" "}
              上传并解包
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {deleteDialog()}
      {appidDialog()}
    </div>
  );

  function deleteDialog() {
    return (
      <Dialog open={Boolean(deleteTarget)} onOpenChange={(o) => !o && setDeleteTarget(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>删除样本</DialogTitle>
            <DialogDescription>
              样本 {deleteTarget?.name}（{deleteTarget?.file_count} 个文件）连同工作区源码将被删除，数据库行与
              findings 一并清除。此操作不可恢复。
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

  function appidDialog() {
    return (
      <Dialog open={Boolean(appidTarget)} onOpenChange={(o) => !o && setAppidTarget(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>补填 AppID</DialogTitle>
            <DialogDescription>
              样本 {appidTarget?.name} 是 V1MMWX 加密包（或未提供 AppID）。AppID 是 PC 微信缓存里包所在目录名，
              补填后将自动解密、解包并重新扫描。
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-2 py-2">
            <Label htmlFor="mp-fill-appid">AppID</Label>
            <Input
              id="mp-fill-appid"
              value={appidInput}
              onChange={(e) => setAppidInput(e.target.value)}
              placeholder="wx1234567890abcdef"
            />
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setAppidTarget(null)} disabled={appidSaving}>
              取消
            </Button>
            <Button onClick={() => void submitAppID()} disabled={appidSaving}>
              {appidSaving ? <Loader2Icon className="size-4 animate-spin" /> : null} 解密并重扫
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    );
  }
}
