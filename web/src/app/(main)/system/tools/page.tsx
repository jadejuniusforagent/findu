"use client";

import * as React from "react";

import { PlayIcon, PlusIcon, RotateCcwIcon, SaveIcon, SearchIcon, Trash2Icon } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Separator } from "@/components/ui/separator";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/api";
import type { Agent, Tool } from "@/lib/types";

// Traffic tools are host tools gated by the global 流量捕获 switch: bindable, but
// only usable when capture is on. Keep in sync with traffic.SeedToolMetas.
const TRAFFIC_TOOL_KEYS = new Set(["traffic_search", "traffic_get"]);

// ---- 工具分层：核心（常驻挂载） vs 按需分类（前端批量挂载/卸载） ----
// 系统工具（runtime !== "recipe"）按前缀/名单归入按需分类；未匹配的落核心组兜底
// （引擎命脉工具宁多挂勿漏挂）。挂载语义 = enabled（配合已有的 agents 绑定）。
const SYSTEM_OPTIONAL_GROUPS: { key: string; title: string; desc: string; match: (k: string) => boolean }[] = [
  {
    key: "android",
    title: "安卓分析",
    desc: "APK 收编/画像/反编译/测试项——安卓样本任务才需要",
    match: (k) => k.startsWith("android_"),
  },
  {
    key: "miniprogram",
    title: "小程序分析",
    desc: "wxapkg 解包/敏感信息/云开发/调试端点——微信小程序任务才需要",
    match: (k) => k.startsWith("miniprogram_"),
  },
  {
    key: "webshell",
    title: "WebShell",
    desc: "WebShell 管理与文件读写——拿到 WebShell 落地后才需要",
    match: (k) => k.startsWith("webshell_") || k.startsWith("manage_webshell_"),
  },
  {
    key: "batch",
    title: "批量任务",
    desc: "批量任务队列管理——跑批场景才需要",
    match: (k) => k.startsWith("batch_task_"),
  },
  {
    key: "traffic",
    title: "流量取证",
    desc: "记录代理流量检索/读取（受全局流量捕获开关门控）",
    match: (k) => k.startsWith("traffic_"),
  },
  {
    key: "platform",
    title: "平台扩展",
    desc: "运行中创建/修改工具、MCP、技能——平台自举场景才需要",
    match: (k) =>
      [
        "create_custom_tool",
        "update_custom_tool",
        "create_mcp",
        "update_mcp",
        "create_skill",
        "update_skill_file",
        "analyze_image",
      ].includes(k),
  },
  {
    key: "execution",
    title: "工具执行管理",
    desc: "异步工具执行状态查询/等待/取消",
    match: (k) => ["get_tool_execution", "wait_tool_execution", "cancel_tool_execution"].includes(k),
  },
  {
    key: "retest",
    title: "漏洞复测",
    desc: "复测会话专用（记录修复结论回写漏洞状态）",
    match: (k) => k === "get_finding_retest_context" || k === "record_finding_retest_result",
  },
];

// ---- CLI 配方（runtime === "recipe"）分类：按功能域 ----
// 名单覆盖 configs/toolrecipes 全部 yaml 名；实际只渲染后端接入的（二进制存在的）
const RECIPE_CATEGORIES: { key: string; title: string; desc: string; names: string[] }[] = [
  {
    key: "recon",
    title: "侦察 · 信息收集",
    desc: "资产测绘/端口扫描/子域枚举/空间搜索",
    names: [
      "amass",
      "arp-scan",
      "dnsenum",
      "dnslog",
      "fierce",
      "fofa_search",
      "gau",
      "katana",
      "lightx",
      "masscan",
      "nbtscan",
      "nmap",
      "paramspider",
      "quake_search",
      "rustscan",
      "fscan",
      "subfinder",
      "waybackurls",
      "wafw00f",
      "x8",
      "bloodhound",
    ],
  },
  {
    key: "webscan",
    title: "Web 漏洞扫描",
    desc: "目录爆破/参数发现/漏洞扫描器/注入利用",
    names: [
      "arjun",
      "dalfox",
      "dirsearch",
      "dotdotpwn",
      "feroxbuster",
      "ffuf",
      "gobuster",
      "graphql-scanner",
      "http-framework-test",
      "jaeles",
      "nikto",
      "nuclei",
      "sqlmap",
      "wpscan",
    ],
  },
  {
    key: "exploit",
    title: "漏洞利用 · 后渗透",
    desc: "利用框架/密码爆破/内网枚举/提权与取证",
    names: [
      "metasploit",
      "msfvenom",
      "impacket",
      "netexec",
      "responder",
      "hydra",
      "hashcat",
      "john",
      "exiftool",
      "binwalk",
      "foremost",
      "enum4linux-ng",
      "linpeas",
      "rpcclient",
      "smbmap",
      "volatility3",
      "steghide",
    ],
  },
  {
    key: "binary",
    title: "二进制 · 逆向",
    desc: "调试器/反编译/Rop 链构建/隐写分析",
    names: [
      "gdb",
      "ghidra",
      "radare2",
      "objdump",
      "angr",
      "pwninit",
      "pwntools",
      "ropgadget",
      "ropper",
      "one-gadget",
      "libc-database",
      "checksec",
      "hashpump",
      "jwt-analyzer",
      "strings",
      "xxd",
      "zsteg",
    ],
  },
  {
    key: "cloud",
    title: "云安全",
    desc: "云配置审计/容器与 K8s 检测",
    names: ["checkov", "clair", "cloudmapper", "falco", "kube-bench", "kube-hunter", "pacu", "prowler"],
  },
  {
    key: "execenv",
    title: "执行环境",
    desc: "脚本执行/包安装/结果查询（通用底座）",
    names: ["exec", "execute-python-script", "install-python-package", "api-schema-analyzer", "query-execution-result"],
  },
];

function systemGroupOf(t: Tool): { title: string; desc: string; optional: boolean } {
  for (const g of SYSTEM_OPTIONAL_GROUPS) {
    if (g.match(t.key)) return { title: g.title, desc: g.desc, optional: true };
  }
  return { title: "核心工具", desc: "探索图/资产/漏洞/任务管理——引擎命脉，默认挂载", optional: false };
}

function _recipeGroupOf(t: Tool): { title: string; desc: string } {
  for (const g of RECIPE_CATEGORIES) {
    if (g.names.includes(t.key)) return { title: g.title, desc: g.desc };
  }
  return { title: "其他配方", desc: "未归类 CLI 工具" };
}

// paramRows flattens schema.properties into an ordered row list for editing.
// name/type/required are read-only (welded to the Go handler); description/default
// are editable. Non-scalar params (array/object) can't carry an editable default.
// parentKey is set for rows that live inside an array param's items.properties.
type ParamRow = {
  name: string;
  type: string;
  required: boolean;
  description: string;
  defaultStr: string; // "" = no default declared
  scalar: boolean;
  parentKey?: string; // set when this is a sub-field of an array param's items
};

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function toRows(schema: Record<string, any>): ParamRow[] {
  const props = (schema?.properties ?? {}) as Record<string, Record<string, unknown>>;
  const required = new Set<string>((schema?.required as string[]) ?? []);
  const rows: ParamRow[] = [];
  for (const [name, p] of Object.entries(props)) {
    const type = String(p?.type ?? "");
    const hasDefault = p != null && "default" in p && (p as Record<string, unknown>).default != null;
    rows.push({
      name,
      type,
      required: required.has(name),
      description: String(p?.description ?? ""),
      defaultStr: hasDefault ? String((p as Record<string, unknown>).default) : "",
      scalar: ["string", "integer", "number", "boolean"].includes(type),
    });
    // Expand items.properties for array params so sub-fields are editable.
    if (type === "array") {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      const itemProps = (p as any)?.items?.properties as Record<string, Record<string, unknown>> | undefined;
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      const itemRequired = new Set<string>(((p as any)?.items?.required as string[]) ?? []);
      if (itemProps) {
        for (const [subName, subP] of Object.entries(itemProps)) {
          const subType = String((subP as Record<string, unknown>)?.type ?? "");
          const subHasDefault = subP != null && "default" in subP && subP.default != null;
          rows.push({
            name: subName,
            type: subType,
            required: itemRequired.has(subName),
            description: String(subP?.description ?? ""),
            defaultStr: subHasDefault ? String(subP.default) : "",
            scalar: ["string", "integer", "number", "boolean"].includes(subType),
            parentKey: name,
          });
        }
      }
    }
  }
  return rows;
}

// coerceDefault turns the edited default string back into a typed JSON value, or
// undefined to drop the "default" key entirely.
function coerceDefault(type: string, raw: string): unknown {
  const s = raw.trim();
  if (s === "") return undefined;
  if (type === "integer" || type === "number") {
    const n = Number(s);
    return Number.isNaN(n) ? undefined : n;
  }
  if (type === "boolean") return s === "true";
  return raw;
}

// applyRows writes edited rows back into a deep-copied schema (structure untouched).
// eslint-disable-next-line @typescript-eslint/no-explicit-any
function applyRows(schema: Record<string, any>, rows: ParamRow[]): Record<string, any> {
  const next = structuredClone(schema ?? {});
  const props = (next.properties ?? {}) as Record<string, Record<string, unknown>>;
  for (const r of rows) {
    if (r.parentKey) {
      // Sub-field of an array param's items.properties
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      const parent = props[r.parentKey] as any;
      const subP = parent?.items?.properties?.[r.name] as Record<string, unknown> | undefined;
      if (!subP) continue;
      subP.description = r.description;
      if (r.scalar) {
        const dv = coerceDefault(r.type, r.defaultStr);
        if (dv === undefined) delete subP.default;
        else subP.default = dv;
      }
    } else {
      const p = props[r.name];
      if (!p) continue;
      p.description = r.description;
      if (r.scalar) {
        const dv = coerceDefault(r.type, r.defaultStr);
        if (dv === undefined) delete p.default;
        else p.default = dv;
      }
    }
  }
  return next;
}

// ToolEditor is the full edit form for one tool, rendered inside the drawer.
function ToolEditor({
  tool,
  agents,
  captureOn,
  onSaved,
  onClose,
}: {
  tool: Tool;
  agents: Agent[];
  captureOn: boolean;
  onSaved: () => void;
  onClose: () => void;
}) {
  // traffic tools can't be bound/enabled until the global 流量捕获 switch is on.
  const trafficGated = TRAFFIC_TOOL_KEYS.has(tool.key) && !captureOn;
  const [description, setDescription] = React.useState(tool.description);
  const [bound, setBound] = React.useState<string[]>(tool.agents);
  const [enabled, setEnabled] = React.useState(tool.enabled);
  const [rows, setRows] = React.useState<ParamRow[]>(() => toRows(tool.schema));
  const [saving, setSaving] = React.useState(false);

  const setRow = (i: number, patch: Partial<ParamRow>) =>
    setRows((rs) => rs.map((r, j) => (j === i ? { ...r, ...patch } : r)));
  const toggleAgent = (k: string) => setBound((b) => (b.includes(k) ? b.filter((x) => x !== k) : [...b, k]));

  async function save() {
    setSaving(true);
    try {
      await api.saveTool(tool.key, {
        description,
        schema: applyRows(tool.schema, rows),
        agents: bound,
        enabled,
        deferred: tool.deferred, // 保持现值：recipe/custom 行消费它，内置行后端忽略
      });
      toast.success(`已保存工具「${tool.key}」`);
      onSaved();
      onClose();
    } catch (e) {
      toast.error(`保存失败：${(e as Error).message}`);
    } finally {
      setSaving(false);
    }
  }
  async function reset() {
    try {
      await api.resetTool(tool.key);
      toast.success(`已恢复「${tool.key}」为代码默认`);
      onSaved();
      onClose();
    } catch (e) {
      toast.error(`恢复失败：${(e as Error).message}`);
    }
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto px-4">
        {trafficGated && (
          <div className="rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-muted-foreground text-xs">
            该工具依赖<b>流量捕获</b>。请先在「系统配置」开启流量捕获，才能绑定给 Agent 并启用。
          </div>
        )}
        {/* binding + switch */}
        <div className="flex flex-wrap items-center gap-x-6 gap-y-3">
          <div className="grid gap-1.5">
            <Label className="text-muted-foreground text-xs">绑定 Agent（决定该工具给哪些 Agent）</Label>
            <div className="flex flex-wrap gap-3">
              {agents.map((ag) => (
                <label key={ag.key} className="flex items-center gap-2 text-sm">
                  <Checkbox
                    checked={bound.includes(ag.key)}
                    disabled={trafficGated}
                    onCheckedChange={() => toggleAgent(ag.key)}
                  />
                  {ag.name}
                  <span className="font-mono text-muted-foreground text-xs">{ag.key}</span>
                </label>
              ))}
              {agents.length === 0 && <span className="text-muted-foreground text-xs">（无 Agent）</span>}
            </div>
          </div>
          <div className="flex items-center gap-2">
            <Switch checked={enabled} onCheckedChange={setEnabled} id={`en-${tool.key}`} />
            <Label htmlFor={`en-${tool.key}`} className="text-sm">
              启用
            </Label>
          </div>
        </div>

        {/* description */}
        <div className="grid gap-1.5">
          <Label className="text-muted-foreground text-xs">工具描述（发送给模型）</Label>
          <Textarea
            className="font-mono text-xs"
            rows={6}
            value={description}
            onChange={(e) => setDescription(e.target.value)}
          />
        </div>

        {/* params */}
        <div className="grid gap-2">
          <Label className="text-muted-foreground text-xs">参数（名称 / 类型 / 必填只读；描述与默认值可改）</Label>
          {rows.length === 0 && <span className="text-muted-foreground text-xs">（无参数）</span>}
          {rows.map((r, i) => (
            <div
              key={`${r.parentKey ?? ""}.${r.name}`}
              className={
                r.parentKey ? "ml-3 grid gap-2 border-muted border-l-2 py-2 pl-3" : "grid gap-2 rounded-md border p-3"
              }
            >
              <div className="flex flex-wrap items-center gap-2">
                {r.parentKey && <span className="font-mono text-[10px] text-muted-foreground">↳</span>}
                <span className="font-mono text-sm">{r.name}</span>
                <Badge variant="secondary" className="px-1.5 py-0 text-[10px]">
                  {r.type || "?"}
                </Badge>
                {r.required && (
                  <Badge variant="outline" className="px-1.5 py-0 text-[10px]">
                    必填
                  </Badge>
                )}
                {r.parentKey && <span className="text-[10px] text-muted-foreground">items 子字段</span>}
              </div>
              <div className="grid gap-2">
                <div className="grid gap-1">
                  <Label className="text-[11px] text-muted-foreground">描述</Label>
                  <Input
                    className="text-xs"
                    value={r.description}
                    onChange={(e) => setRow(i, { description: e.target.value })}
                  />
                </div>
                <div className="grid gap-1">
                  <Label className="text-[11px] text-muted-foreground">默认值</Label>
                  <Input
                    className="text-xs"
                    placeholder={r.scalar ? "（空=无默认）" : "仅标量支持"}
                    disabled={!r.scalar}
                    value={r.defaultStr}
                    onChange={(e) => setRow(i, { defaultStr: e.target.value })}
                  />
                </div>
              </div>
            </div>
          ))}
        </div>
      </div>

      <Separator className="mt-4" />
      <div className="flex flex-wrap gap-2 p-4">
        <Button size="sm" onClick={save} disabled={saving}>
          <SaveIcon /> 保存
        </Button>
        <Button size="sm" variant="outline" onClick={reset}>
          <RotateCcwIcon /> 恢复默认
        </Button>
      </div>
    </div>
  );
}

// ToolGridCard is one clickable tile in the catalog grid.
function ToolGridCard({ tool, onClick }: { tool: Tool; onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="flex flex-col gap-2 rounded-lg border p-4 text-left transition-colors hover:border-primary/50 hover:bg-muted/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
    >
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-sm font-medium">{tool.display_name || tool.key}</span>
        {tool.display_name ? <span className="font-mono text-[10px] text-muted-foreground">{tool.key}</span> : null}
        {tool.system ? (
          <Badge variant="secondary" className="px-1.5 py-0 text-[10px]">
            系统
          </Badge>
        ) : (
          <Badge variant="outline" className="px-1.5 py-0 text-[10px]">
            自定义·{tool.kind}
          </Badge>
        )}
        {tool.deferred && (
          <Badge variant="outline" className="px-1.5 py-0 text-[10px]">
            deferred
          </Badge>
        )}
        {!tool.enabled && (
          <Badge variant="outline" className="px-1.5 py-0 text-[10px] text-destructive">
            已停用
          </Badge>
        )}
        <Badge
          variant="secondary"
          className="ml-auto px-1.5 py-0 text-[10px] tabular-nums"
          title={`累计调用 ${tool.calls ?? 0} 次`}
        >
          调用 {tool.calls ?? 0}
        </Badge>
      </div>
      <p className="line-clamp-1 h-4 text-muted-foreground text-xs">{tool.description || "（无描述）"}</p>
      <div className="mt-auto flex flex-wrap gap-1 pt-1">
        {tool.agents.length === 0 && <span className="text-[10px] text-muted-foreground">（未绑定 Agent）</span>}
        {tool.agents.map((a) => (
          <Badge key={a} variant="outline" className="px-1.5 py-0 text-[10px]">
            {a}
          </Badge>
        ))}
      </div>
    </button>
  );
}

// ToolGroupCard is one category card: header carries the mounted count and
// batch 挂载/卸载 buttons, body is the tool grid. 挂载语义 = enabled。
function ToolGroupCard({
  title,
  desc,
  tools,
  matchTool,
  busy,
  onToggleAll,
  onOpen,
}: {
  title: string;
  desc: string;
  tools: Tool[];
  busy: boolean;
  matchTool: (t: Tool) => boolean;
  onToggleAll: (list: Tool[], enabled: boolean) => void;
  onOpen: (t: Tool) => void;
}) {
  const shown = tools.filter(matchTool);
  const mounted = tools.filter((t) => t.enabled).length;
  if (shown.length === 0) return null;
  return (
    <Card>
      <CardHeader>
        <div className="flex flex-wrap items-start justify-between gap-2">
          <div>
            <CardTitle className="flex flex-wrap items-center gap-2 text-base">
              {title}
              <Badge variant="secondary" className="px-1.5 py-0 text-[10px] tabular-nums">
                {mounted}/{tools.length} 已挂载
              </Badge>
            </CardTitle>
            <CardDescription>{desc}</CardDescription>
          </div>
          <div className="flex gap-2">
            <Button
              size="sm"
              variant="outline"
              disabled={busy || mounted === tools.length}
              onClick={() => onToggleAll(tools, true)}
            >
              全部挂载
            </Button>
            <Button
              size="sm"
              variant="outline"
              className="text-destructive"
              disabled={busy || mounted === 0}
              onClick={() => onToggleAll(tools, false)}
            >
              全部卸载
            </Button>
          </div>
        </div>
      </CardHeader>
      <CardContent>
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {shown.map((t) => (
            <ToolGridCard key={t.key} tool={t} onClick={() => onOpen(t)} />
          ))}
        </div>
      </CardContent>
    </Card>
  );
}

export default function ToolsPage() {
  const [tools, setTools] = React.useState<Tool[]>([]);
  const [agents, setAgents] = React.useState<Agent[]>([]);
  const [captureOn, setCaptureOn] = React.useState(false);
  const [selectedKey, setSelectedKey] = React.useState<string | null>(null);
  const [customEdit, setCustomEdit] = React.useState<Tool | "new" | null>(null);
  const [batchBusy, setBatchBusy] = React.useState<string | null>(null);

  const reload = React.useCallback(() => {
    api
      .tools()
      .then(setTools)
      .catch(() => setTools([]));
  }, []);
  React.useEffect(() => {
    reload();
    api
      .agents()
      .then(setAgents)
      .catch(() => {});
    api
      .settings()
      .then((s) => setCaptureOn(!!s.traffic_capture))
      .catch(() => {});
  }, [reload]);

  const [query, setQuery] = React.useState("");

  const selected = tools.find((t) => t.key === selectedKey) ?? null;
  const matchTool = React.useCallback(
    (t: Tool) => {
      const q = query.trim().toLowerCase();
      if (!q) return true;
      return (
        t.key.toLowerCase().includes(q) ||
        t.description.toLowerCase().includes(q) ||
        t.agents.some((a) => a.toLowerCase().includes(q))
      );
    },
    [query],
  );

  // 分桶：系统工具（runtime !== 'recipe'）与 CLI 配方（runtime === 'recipe'）、自定义工具
  const sysTools = tools.filter((t) => t.system && t.runtime !== "recipe");
  const recipeTools = tools.filter((t) => t.runtime === "recipe");
  const customTools = tools.filter((t) => !t.system);
  const allSystemCount = sysTools.length;
  const allRecipeCount = recipeTools.length;
  const allCustomCount = customTools.length;

  // 系统工具分层：核心组（引擎命脉，兜底收纳未匹配项）+ 按需分类组
  const coreGroup = sysTools.filter((t) => !systemGroupOf(t).optional);
  const optionalGroups = SYSTEM_OPTIONAL_GROUPS.map((g) => ({
    ...g,
    items: sysTools.filter((t) => g.match(t.key)),
  })).filter((g) => g.items.length > 0);

  // CLI 配方按功能域分组；未归类落「其他配方」
  const recipeGroups = RECIPE_CATEGORIES.map((g) => ({
    title: g.title,
    desc: g.desc,
    items: recipeTools.filter((t) => g.names.includes(t.key)),
  })).filter((g) => g.items.length > 0);
  const recipeOther = recipeTools.filter((t) => !RECIPE_CATEGORIES.some((g) => g.names.includes(t.key)));

  // 批量挂载/卸载：串行 PUT 只动目标行，结束 reload + toast
  const toggleAll = React.useCallback(
    async (list: Tool[], enabled: boolean) => {
      const targets = list.filter((t) => t.enabled !== enabled);
      if (targets.length === 0) return;
      setBatchBusy(enabled ? "mount" : "unmount");
      try {
        await api.saveToolsEnabled(targets, enabled);
        toast.success(`${enabled ? "已挂载" : "已卸载"} ${targets.length} 个工具`);
      } catch (e) {
        toast.error(`批量操作失败：${(e as Error).message}`);
      } finally {
        setBatchBusy(null);
        reload();
      }
    },
    [reload],
  );

  // clicking a built-in tool opens the binding drawer; a custom tool opens its full editor.
  const openTool = (t: Tool) => (t.system ? setSelectedKey(t.key) : setCustomEdit(t));

  return (
    <div className="flex flex-1 flex-col gap-4 md:gap-6">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="font-semibold text-xl tracking-tight">工具</h1>
          <p className="text-muted-foreground text-sm">系统工具的描述/绑定，以及自定义工具(command/script/http)</p>
        </div>
        <div className="relative w-64">
          <SearchIcon className="absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            className="h-8 pl-8 text-sm"
            placeholder="搜索工具名、描述、Agent…"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
        </div>
      </div>

      <Tabs defaultValue="system">
        <TabsList>
          <TabsTrigger value="system">系统工具（{allSystemCount}）</TabsTrigger>
          <TabsTrigger value="recipes">CLI 配方（{allRecipeCount}）</TabsTrigger>
          <TabsTrigger value="custom">自定义工具（{allCustomCount}）</TabsTrigger>
        </TabsList>

        <TabsContent value="system" className="flex flex-col gap-4">
          <p className="text-muted-foreground text-sm">
            核心工具默认挂载给绑定的 Agent；按需分类可整组挂载/卸载——卸载的分组不占 Agent 上下文。
            点击卡片可编辑描述、参数默认值与 Agent 绑定。
          </p>
          <ToolGroupCard
            title="核心工具"
            desc="探索图 / 资产 / 漏洞 / 任务管理——引擎命脉，默认挂载"
            tools={coreGroup}
            busy={!!batchBusy}
            matchTool={matchTool}
            onToggleAll={toggleAll}
            onOpen={openTool}
          />
          {optionalGroups.map((g) => (
            <ToolGroupCard
              key={g.key}
              title={g.title}
              desc={g.desc}
              tools={g.items}
              busy={!!batchBusy}
              matchTool={matchTool}
              onToggleAll={toggleAll}
              onOpen={openTool}
            />
          ))}
        </TabsContent>

        <TabsContent value="recipes" className="flex flex-col gap-4">
          <p className="rounded-md border border-dashed px-3 py-2 text-muted-foreground text-sm">
            CLI 配方（nmap / nuclei / ffuf…）走 <b>延迟披露</b>：名字常驻 Agent 上下文，完整参数 Schema 按需经
            SearchExtraTools 获取——不挂载也只占一行名字。
            <b>卸载</b>会把名字也撤下（Agent 完全不可见），按任务类型整组控制即可。
          </p>
          {recipeGroups.map((g) => (
            <ToolGroupCard
              key={g.title}
              title={g.title}
              desc={g.desc}
              tools={g.items}
              busy={!!batchBusy}
              matchTool={matchTool}
              onToggleAll={toggleAll}
              onOpen={openTool}
            />
          ))}
          {recipeOther.length > 0 && (
            <ToolGroupCard
              title="其他配方"
              desc="未归入功能域的 CLI 工具"
              tools={recipeOther}
              busy={!!batchBusy}
              matchTool={matchTool}
              onToggleAll={toggleAll}
              onOpen={openTool}
            />
          )}
          {allRecipeCount === 0 && (
            <p className="py-6 text-center text-muted-foreground text-sm">
              （暂无 CLI 配方——容器未接入，检查镜像内工具二进制）
            </p>
          )}
        </TabsContent>

        <TabsContent value="custom">
          {(() => {
            const customShown = customTools.filter(matchTool);
            return (
              <Card>
                <CardHeader>
                  <div className="flex items-start justify-between gap-3">
                    <div>
                      <CardTitle>自定义工具</CardTitle>
                      <CardDescription>
                        {query.trim()
                          ? `shell/command/script/http，${customShown.length} / ${allCustomCount} 个匹配，点击卡片编辑`
                          : `shell(bash 声明) / command(命令) / script(Python) / http(API)，共 ${allCustomCount} 个，点击卡片编辑`}
                      </CardDescription>
                    </div>
                    <Button size="sm" onClick={() => setCustomEdit("new")}>
                      <PlusIcon /> 新建自定义工具
                    </Button>
                  </div>
                </CardHeader>
                <CardContent>
                  {customShown.length === 0 ? (
                    <p className="py-6 text-center text-muted-foreground text-sm">
                      {query.trim() ? "没有匹配的自定义工具" : "（暂无自定义工具，点击右上角「新建自定义工具」）"}
                    </p>
                  ) : (
                    <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
                      {customShown.map((t) => (
                        <ToolGridCard key={t.key} tool={t} onClick={() => openTool(t)} />
                      ))}
                    </div>
                  )}
                </CardContent>
              </Card>
            );
          })()}
        </TabsContent>
      </Tabs>

      <Sheet open={!!selected} onOpenChange={(o) => !o && setSelectedKey(null)}>
        <SheetContent side="right" className="gap-0 p-0 data-[side=right]:w-[30vw] data-[side=right]:sm:max-w-[30vw]">
          {selected && (
            <>
              <SheetHeader className="px-4">
                <SheetTitle className="font-mono">{selected.key}</SheetTitle>
                <SheetDescription>编辑描述、参数默认值与 Agent 绑定</SheetDescription>
              </SheetHeader>
              <ToolEditor
                key={selected.key}
                tool={selected}
                agents={agents}
                captureOn={captureOn}
                onSaved={reload}
                onClose={() => setSelectedKey(null)}
              />
            </>
          )}
        </SheetContent>
      </Sheet>

      <CustomToolDialog
        edit={customEdit}
        agents={agents}
        onClose={() => setCustomEdit(null)}
        onSaved={() => {
          setCustomEdit(null);
          reload();
        }}
      />
    </div>
  );
}

// ---- 自定义工具编辑器 ----

type ExecState = {
  command: string;
  code: string;
  method: string;
  url: string;
  headers: string;
  body: string;
  timeout_ms: string;
  proxy: string;
  use_recording_proxy: boolean;
};

function CustomToolDialog({
  edit,
  agents,
  onClose,
  onSaved,
}: {
  edit: Tool | "new" | null;
  agents: Agent[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const isNew = edit === "new";
  const tool = edit && edit !== "new" ? edit : null;
  const [key, setKey] = React.useState("");
  const [description, setDescription] = React.useState("");
  const [kind, setKind] = React.useState<"shell" | "command" | "script" | "http">("shell");
  const [ex, setEx] = React.useState<ExecState>({
    command: "",
    code: "",
    method: "GET",
    url: "",
    headers: "",
    body: "",
    timeout_ms: "",
    proxy: "",
    use_recording_proxy: false,
  });
  const [schemaText, setSchemaText] = React.useState("");
  const [bound, setBound] = React.useState<string[]>([]);
  const [deferred, setDeferred] = React.useState(false);
  const [enabled, setEnabled] = React.useState(true);
  const [saving, setSaving] = React.useState(false);
  const [paramsText, setParamsText] = React.useState("");
  const [testing, setTesting] = React.useState(false);
  const [testResult, setTestResult] = React.useState<{ output: string; is_error: boolean } | null>(null);

  // (re)load form state when opening.
  React.useEffect(() => {
    if (!edit) return;
    setParamsText("");
    setTestResult(null);
    if (edit === "new") {
      setKey("");
      setDescription("");
      setKind("shell");
      setEx({
        command: "",
        code: "",
        method: "GET",
        url: "",
        headers: "",
        body: "",
        timeout_ms: "",
        proxy: "",
        use_recording_proxy: false,
      });
      setSchemaText("");
      setBound([]);
      setDeferred(false);
      setEnabled(true);
      return;
    }
    const t = edit;
    const e = (t.exec ?? {}) as Record<string, unknown>;
    setKey(t.key);
    setDescription(t.description);
    setKind((t.kind as "shell" | "command" | "script" | "http") ?? "shell");
    setEx({
      command: String(e.command ?? ""),
      code: String(e.code ?? ""),
      method: String(e.method ?? "GET"),
      url: String(e.url ?? ""),
      headers: e.headers ? JSON.stringify(e.headers, null, 2) : "",
      body: String(e.body ?? ""),
      timeout_ms: e.timeout_ms ? String(e.timeout_ms) : "",
      proxy: String(e.proxy ?? ""),
      use_recording_proxy: !!e.use_recording_proxy,
    });
    setSchemaText(t.schema && Object.keys(t.schema).length ? JSON.stringify(t.schema, null, 2) : "");
    setBound(t.agents ?? []);
    setDeferred(!!t.deferred);
    setEnabled(t.enabled);
  }, [edit]);

  const toggleAgent = (k: string) => setBound((b) => (b.includes(k) ? b.filter((x) => x !== k) : [...b, k]));

  function buildExec(): Record<string, unknown> {
    if (kind === "shell") return {};
    const t = ex.timeout_ms ? Number(ex.timeout_ms) : undefined;
    if (kind === "command") return { command: ex.command, timeout_ms: t };
    if (kind === "script") return { code: ex.code, timeout_ms: t };
    let headers: Record<string, string> = {};
    if (ex.headers.trim()) {
      try {
        headers = JSON.parse(ex.headers);
      } catch {
        /* validated on save */
      }
    }
    return {
      method: ex.method,
      url: ex.url,
      headers,
      body: ex.body,
      timeout_ms: t,
      proxy: ex.proxy,
      use_recording_proxy: ex.use_recording_proxy,
    };
  }

  async function save() {
    if (isNew && !/^[a-z][a-z0-9_]*$/.test(key.trim())) {
      toast.error("key 需小写字母开头，仅含小写字母/数字/下划线");
      return;
    }
    let schema: Record<string, unknown> = {};
    if (schemaText.trim()) {
      try {
        schema = JSON.parse(schemaText);
      } catch {
        toast.error("参数 JSON Schema 格式错误");
        return;
      }
    }
    if (kind === "http" && ex.headers.trim()) {
      try {
        JSON.parse(ex.headers);
      } catch {
        toast.error("headers JSON 格式错误");
        return;
      }
    }
    if (kind === "http") {
      const props = (schema as { properties?: Record<string, unknown> }).properties;
      if (!props || Object.keys(props).length === 0) {
        toast.error("http 工具必须提供参数 JSON Schema（含 properties，不能留空）");
        return;
      }
    }
    setSaving(true);
    const payload = {
      description,
      schema: kind === "shell" ? {} : schema,
      agents: bound,
      enabled,
      kind,
      exec: buildExec(),
      deferred: kind === "shell" ? false : deferred,
    };
    try {
      if (isNew) await api.createCustomTool({ key: key.trim(), ...payload });
      else await api.updateCustomTool(tool!.key, payload);
      toast.success(isNew ? "已创建自定义工具" : "已保存");
      onSaved();
    } catch (e) {
      toast.error(`保存失败：${(e as Error).message}`);
    } finally {
      setSaving(false);
    }
  }
  async function del() {
    if (!tool) return;
    try {
      await api.deleteCustomTool(tool.key);
      toast.success("已删除");
      onSaved();
    } catch (e) {
      toast.error(`删除失败：${(e as Error).message}`);
    }
  }
  // runTest dry-runs the CURRENT form (unsaved) with the sample params, so a
  // template/script/request can be debugged before saving.
  async function runTest() {
    let params: Record<string, unknown> = {};
    if (paramsText.trim()) {
      try {
        params = JSON.parse(paramsText);
      } catch {
        toast.error("测试参数 JSON 格式错误");
        return;
      }
    }
    if (kind === "http" && ex.headers.trim()) {
      try {
        JSON.parse(ex.headers);
      } catch {
        toast.error("headers JSON 格式错误");
        return;
      }
    }
    setTesting(true);
    setTestResult(null);
    try {
      const r = await api.testCustomTool({ kind, exec: buildExec(), params });
      setTestResult(r);
    } catch (e) {
      setTestResult({ output: (e as Error).message, is_error: true });
    } finally {
      setTesting(false);
    }
  }

  return (
    <Sheet open={!!edit} onOpenChange={(o) => !o && onClose()}>
      <SheetContent
        side="right"
        className="flex flex-col gap-0 p-0 data-[side=right]:w-[45vw] data-[side=right]:min-w-[480px] data-[side=right]:sm:max-w-[45vw]"
      >
        <SheetHeader className="px-4">
          <SheetTitle>{isNew ? "新建自定义工具" : `编辑 ${tool?.key}`}</SheetTitle>
          <SheetDescription>
            shell=bash 环境声明(只需名称+描述，告知模型可用 bash 调用)；command/script/http 需写执行规格。
          </SheetDescription>
        </SheetHeader>

        <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto px-4 pb-4">
          <div className="grid gap-1.5">
            <Label className="text-xs">Key</Label>
            <Input
              className="font-mono"
              placeholder="如 nmap_scan"
              value={key}
              disabled={!isNew}
              onChange={(e) => setKey(e.target.value)}
            />
          </div>
          <div className="grid gap-1.5">
            <Label className="text-xs">描述(发给模型)</Label>
            <Textarea rows={2} value={description} onChange={(e) => setDescription(e.target.value)} />
          </div>

          <div className="grid gap-1.5">
            <Label className="text-xs">类型</Label>
            <Select value={kind} onValueChange={(v) => setKind(v as "shell" | "command" | "script" | "http")}>
              <SelectTrigger className="w-56">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="shell">shell（bash 环境声明）</SelectItem>
                <SelectItem value="command">command（shell 命令模板）</SelectItem>
                <SelectItem value="script">script（Python 脚本）</SelectItem>
                <SelectItem value="http">http（API 请求）</SelectItem>
              </SelectContent>
            </Select>
            {kind === "shell" && (
              <p className="text-muted-foreground text-xs">
                适合 nmap、sqlmap、ffuf 等出名工具——模型已知用法，只需声明"可在 bash 中调用"即可。名称+描述会追加到 Bash
                工具描述里。
              </p>
            )}
          </div>

          {kind === "command" && (
            <div className="grid gap-1.5">
              <Label className="text-xs">
                命令模板（占位符 {"{param}"}，如 nmap -p {"{ports}"} {"{target}"}）
              </Label>
              <Textarea
                className="font-mono text-xs"
                rows={2}
                value={ex.command}
                onChange={(e) => setEx({ ...ex, command: e.target.value })}
              />
            </div>
          )}
          {kind === "script" && (
            <div className="grid gap-1.5">
              <Label className="text-xs">Python 正文（参数走 stdin JSON / os.environ["TOOL_X"]）</Label>
              <Textarea
                className="font-mono text-xs"
                rows={10}
                value={ex.code}
                placeholder={"import json,sys\nargs=json.load(sys.stdin)\nprint(...)"}
                onChange={(e) => setEx({ ...ex, code: e.target.value })}
              />
            </div>
          )}
          {kind === "http" && (
            <div className="grid gap-2">
              <div className="flex gap-2">
                <div className="grid gap-1.5">
                  <Label className="text-xs">Method</Label>
                  <Input
                    className="w-24"
                    value={ex.method}
                    onChange={(e) => setEx({ ...ex, method: e.target.value })}
                  />
                </div>
                <div className="grid flex-1 gap-1.5">
                  <Label className="text-xs">URL（可含 {"{param}"}）</Label>
                  <Input
                    className="font-mono text-xs"
                    value={ex.url}
                    onChange={(e) => setEx({ ...ex, url: e.target.value })}
                  />
                </div>
              </div>
              <div className="grid gap-1.5">
                <Label className="text-xs">Headers（JSON，可含 {"{param}"}）</Label>
                <Textarea
                  className="font-mono text-xs"
                  rows={2}
                  value={ex.headers}
                  placeholder={'{"Authorization": "Bearer {token}"}'}
                  onChange={(e) => setEx({ ...ex, headers: e.target.value })}
                />
              </div>
              <div className="grid gap-1.5">
                <Label className="text-xs">Body（可含 {"{param}"}）</Label>
                <Textarea
                  className="font-mono text-xs"
                  rows={2}
                  value={ex.body}
                  onChange={(e) => setEx({ ...ex, body: e.target.value })}
                />
              </div>
              <div className="flex items-center gap-4">
                <div className="grid gap-1.5">
                  <Label className="text-xs">代理 URL（空=直连）</Label>
                  <Input
                    className="w-56 font-mono text-xs"
                    value={ex.proxy}
                    onChange={(e) => setEx({ ...ex, proxy: e.target.value })}
                  />
                </div>
                <label className="mt-4 flex items-center gap-2 text-sm">
                  <Checkbox
                    checked={ex.use_recording_proxy}
                    onCheckedChange={(v) => setEx({ ...ex, use_recording_proxy: !!v })}
                  />
                  走记录代理
                </label>
              </div>
            </div>
          )}

          {kind !== "shell" && (
            <div className="flex items-center gap-3">
              <div className="grid gap-1.5">
                <Label className="text-xs">超时(ms，空=默认)</Label>
                <Input
                  type="number"
                  className="w-32"
                  value={ex.timeout_ms}
                  onChange={(e) => setEx({ ...ex, timeout_ms: e.target.value })}
                />
              </div>
            </div>
          )}

          {kind !== "shell" && (
            <div className="grid gap-1.5">
              <Label className="text-xs">
                参数 JSON Schema
                {kind === "http" ? "（http 工具必填，需含 properties）" : "（留空 = 自动给 {args} 薄壳）"}
              </Label>
              <Textarea
                className="font-mono text-xs"
                rows={4}
                value={schemaText}
                placeholder={'{"type":"object","properties":{"target":{"type":"string"}},"required":["target"]}'}
                onChange={(e) => setSchemaText(e.target.value)}
              />
            </div>
          )}

          <div className="grid gap-1.5">
            <Label className="text-muted-foreground text-xs">绑定 Agent</Label>
            <div className="flex flex-wrap gap-3">
              {agents.map((a) => (
                <label key={a.key} className="flex items-center gap-2 text-sm">
                  <Checkbox checked={bound.includes(a.key)} onCheckedChange={() => toggleAgent(a.key)} />
                  {a.name}
                  <span className="font-mono text-muted-foreground text-xs">{a.key}</span>
                </label>
              ))}
            </div>
          </div>

          <div className="flex items-center gap-6">
            <label className="flex items-center gap-2 text-sm">
              <Switch checked={enabled} onCheckedChange={setEnabled} /> 启用
            </label>
            {kind !== "shell" && (
              <label className="flex items-center gap-2 text-sm">
                <Switch checked={deferred} onCheckedChange={setDeferred} /> deferred（大量不常用工具才开）
              </label>
            )}
          </div>

          {kind !== "shell" && (
            <div className="grid gap-1.5 rounded-md border p-3">
              <Label className="font-medium text-xs">测试运行（用当前表单，不会保存）</Label>
              <Textarea
                className="font-mono text-xs"
                rows={2}
                value={paramsText}
                placeholder={'示例参数 JSON，如 {"target":"example.com"}'}
                onChange={(e) => setParamsText(e.target.value)}
              />
              <div>
                <Button size="sm" variant="outline" onClick={runTest} disabled={testing}>
                  <PlayIcon /> {testing ? "运行中…" : "测试运行"}
                </Button>
              </div>
              {testResult && (
                <pre
                  className={
                    "max-h-64 overflow-auto whitespace-pre-wrap break-words rounded bg-muted p-2 font-mono text-xs" +
                    (testResult.is_error ? "text-destructive" : "")
                  }
                >
                  {testResult.output || "（无输出）"}
                </pre>
              )}
            </div>
          )}
        </div>

        <Separator />
        <div className="flex items-center gap-2 p-4">
          <Button size="sm" onClick={save} disabled={saving}>
            <SaveIcon /> {isNew ? "创建" : "保存"}
          </Button>
          {!isNew && (
            <Button size="sm" variant="outline" className="text-destructive" onClick={del}>
              <Trash2Icon /> 删除
            </Button>
          )}
        </div>
      </SheetContent>
    </Sheet>
  );
}
