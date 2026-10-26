"use client";
"use no memo";

import * as React from "react";
import {
  Background,
  BackgroundVariant,
  Controls,
  Handle,
  MarkerType,
  MiniMap,
  Panel,
  Position,
  ReactFlow,
  ReactFlowProvider,
  useEdgesState,
  useNodesState,
  type Edge as RFEdge,
  type Node as RFNode,
  type NodeProps,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import {
  CloudIcon,
  FileCodeIcon,
  GlobeIcon,
  ListTreeIcon,
  NetworkIcon,
  ShieldCheckIcon,
  SmartphoneIcon,
  SplitIcon,
} from "lucide-react";

import { cn } from "@/lib/utils";
import type {
  MiniprogramTopology,
  MiniprogramTopologyEdge,
  MiniprogramTopologyNode,
} from "@/lib/miniprogram-api";

const NODE_W = 220;
const NODE_H = 64;
const COL_GAP = 120;
const ROW_GAP = 28;

type Meta = { label: string; icon: typeof SmartphoneIcon; iconBg: string; hex: string; chip: string };

const typeMeta: Record<string, Meta> = {
  page: { label: "页面", icon: SmartphoneIcon, iconBg: "bg-blue-500", hex: "#3b82f6", chip: "bg-blue-500/15 text-blue-600 dark:text-blue-400" },
  file: { label: "模块", icon: FileCodeIcon, iconBg: "bg-slate-500", hex: "#64748b", chip: "bg-slate-500/15 text-slate-600 dark:text-slate-300" },
  api: { label: "接口", icon: GlobeIcon, iconBg: "bg-orange-500", hex: "#f97316", chip: "bg-orange-500/15 text-orange-600 dark:text-orange-400" },
  cloudfn: { label: "云函数", icon: CloudIcon, iconBg: "bg-violet-500", hex: "#8b5cf6", chip: "bg-violet-500/15 text-violet-600 dark:text-violet-400" },
};

const edgeMeta: Record<string, { color: string; label: string; dashed: boolean }> = {
  navigate: { color: "#3b82f6", label: "跳转", dashed: false },
  api_call: { color: "#f97316", label: "接口调用", dashed: true },
  cloudfn: { color: "#8b5cf6", label: "云函数", dashed: true },
};

// 三种视图：endpoints(接口/云函数操作面，默认) | navigation(页面流转) | all(完整图)。
type TopoView = "endpoints" | "navigation" | "all";

type TopoData = { node: MiniprogramTopologyNode };
type TopoRFNode = RFNode<TopoData, "topo">;

// filterGraph 按视图过滤出要展示的节点与边。
function filterGraph(
  topo: MiniprogramTopology,
  view: TopoView,
  keyPagesOnly: boolean,
): { nodes: MiniprogramTopologyNode[]; edges: MiniprogramTopologyEdge[] } {
  if (view === "endpoints") {
    // 操作面：只保留接口/云函数节点 + 直接调用它们的页面/模块，边只含接口/云函数调用。
    const targets = new Set(
      topo.nodes.filter((n) => n.type === "api" || n.type === "cloudfn").map((n) => n.id),
    );
    const edges = topo.edges.filter((e) => e.type === "api_call" || e.type === "cloudfn");
    const sourceIds = new Set(edges.map((e) => e.source));
    const nodes = topo.nodes.filter((n) => targets.has(n.id) || sourceIds.has(n.id));
    return { nodes, edges };
  }
  if (view === "navigation") {
    // 页面流转：只保留页面节点与跳转边。
    const edges = topo.edges.filter((e) => e.type === "navigate");
    const deg = new Map<string, { in: number; out: number }>();
    edges.forEach((e) => {
      deg.set(e.source, { in: deg.get(e.source)?.in ?? 0, out: (deg.get(e.source)?.out ?? 0) + 1 });
      deg.set(e.target, { in: (deg.get(e.target)?.in ?? 0) + 1, out: deg.get(e.target)?.out ?? 0 });
    });
    let nodes = topo.nodes.filter((n) => n.type === "page" && deg.has(n.id));
    if (keyPagesOnly) {
      // 关键页：参与跳转 >=2 的枢纽页，避免全部页面淹没视图。
      nodes = nodes.filter((n) => {
        const d = deg.get(n.id)!;
        return d.in + d.out >= 2;
      });
    }
    const ids = new Set(nodes.map((n) => n.id));
    return { nodes, edges: edges.filter((e) => ids.has(e.source) && ids.has(e.target)) };
  }
  return { nodes: topo.nodes, edges: topo.edges };
}

// 布局入口：navigation 用分层 DAG，其余用两列（来源左 / 目标右）。
function layout(
  nodes: MiniprogramTopologyNode[],
  edges: MiniprogramTopologyEdge[],
  view: TopoView,
): { nodes: TopoRFNode[]; edges: RFEdge[] } {
  let pos: Map<string, { x: number; y: number }>;
  if (view === "navigation") {
    pos = layoutNavigation(nodes, edges);
  } else {
    pos = layoutColumns(nodes, edges);
  }

  const rfNodes: TopoRFNode[] = nodes.map((n) => ({
    id: n.id,
    type: "topo",
    position: pos.get(n.id) ?? { x: 0, y: 0 },
    data: { node: n },
    sourcePosition: Position.Right,
    targetPosition: Position.Left,
  }));

  const rfEdges: RFEdge[] = edges.map((e, i) => {
    const m = edgeMeta[e.type] ?? { color: "#94a3b8", label: e.type, dashed: false };
    return {
      id: `e${i}`,
      source: e.source,
      target: e.target,
      label: m.label,
      type: "default",
      animated: false,
      labelShowBg: false,
      labelStyle: { fontSize: 10, fontWeight: 600, fill: m.color },
      style: {
        stroke: m.color,
        strokeWidth: 1.6,
        strokeOpacity: 0.7,
        strokeDasharray: m.dashed ? "5 4" : undefined,
      },
      markerEnd: { type: MarkerType.ArrowClosed, color: m.color, width: 14, height: 14 },
    } satisfies RFEdge;
  });

  return { nodes: rfNodes, edges: rfEdges };
}

// layoutColumns 两列：来源（页面/模块）在左，目标（接口/云函数，按热度降序）在右。
function layoutColumns(
  nodes: MiniprogramTopologyNode[],
  edges: MiniprogramTopologyEdge[],
): Map<string, { x: number; y: number }> {
  const sources = nodes.filter((n) => n.type === "page" || n.type === "file");
  const targets = nodes
    .filter((n) => n.type === "api" || n.type === "cloudfn")
    .sort((a, b) => b.refCount - a.refCount);
  const pos = new Map<string, { x: number; y: number }>();
  sources.forEach((n, i) => pos.set(n.id, { x: 0, y: i * (NODE_H + ROW_GAP) }));
  targets.forEach((n, i) => pos.set(n.id, { x: NODE_W + COL_GAP, y: i * (NODE_H + ROW_GAP) }));
  return pos;
}

// layoutNavigation 页面流转：按跳转深度的最长路径分列（从左到右的 DAG），每层垂直居中。
function layoutNavigation(
  nodes: MiniprogramTopologyNode[],
  edges: MiniprogramTopologyEdge[],
): Map<string, { x: number; y: number }> {
  const layer = new Map<string, number>();
  nodes.forEach((n) => layer.set(n.id, 0));
  for (let k = 0; k < nodes.length; k++) {
    let changed = false;
    for (const e of edges) {
      const s = layer.get(e.source) ?? 0;
      if (s + 1 > (layer.get(e.target) ?? 0)) {
        layer.set(e.target, s + 1);
        changed = true;
      }
    }
    if (!changed) break;
  }
  const layerCount = new Map<number, number>();
  nodes.forEach((n) => {
    const l = layer.get(n.id) ?? 0;
    layerCount.set(l, (layerCount.get(l) ?? 0) + 1);
  });
  const layerIdx = new Map<number, number>();
  const pos = new Map<string, { x: number; y: number }>();
  nodes.forEach((n) => {
    const l = layer.get(n.id) ?? 0;
    const i = layerIdx.get(l) ?? 0;
    layerIdx.set(l, i + 1);
    const total = layerCount.get(l) ?? 1;
    const y = (i - (total - 1) / 2) * (NODE_H + ROW_GAP);
    pos.set(n.id, { x: l * (NODE_W + COL_GAP), y });
  });
  return pos;
}

function TopoNode({ data, selected }: NodeProps<TopoRFNode>) {
  const n = data.node;
  const meta = typeMeta[n.type] ?? typeMeta.file;
  const Icon = meta.icon;
  return (
    <div className={cn("w-[220px] rounded-xl border-2 transition-colors", selected ? "border-blue-500" : "border-transparent")}>
      <div className="relative rounded-[10px] border border-black/[0.06] bg-white shadow-sm hover:shadow-md dark:border-white/10 dark:bg-neutral-900">
        <Handle
          type="target"
          position={Position.Left}
          className="!size-2 !border-2 !border-neutral-300 !bg-white opacity-0 transition-opacity group-hover:opacity-100 dark:!border-neutral-600 dark:!bg-neutral-800"
        />
        <div className="flex items-center gap-2 px-3 pt-2.5 pb-1">
          <span className={cn("flex size-6 shrink-0 items-center justify-center rounded-lg shadow-sm", meta.iconBg)}>
            <Icon className="size-3.5 text-white" />
          </span>
          <span className="grow truncate text-[13px] font-semibold text-neutral-700 dark:text-neutral-100">
            {meta.label}
          </span>
          {n.refCount > 0 && (
            <span className="shrink-0 rounded-md px-1.5 py-0.5 text-[10px] font-semibold tabular-nums" style={{ backgroundColor: `${meta.hex}1f`, color: meta.hex }}>
              ×{n.refCount}
            </span>
          )}
        </div>
        <div className="line-clamp-2 px-3 pb-1 font-mono text-[11px] leading-snug text-neutral-600 break-all dark:text-neutral-300" title={n.label}>
          {n.label}
        </div>
        {n.type === "api" && n.detail?.url ? (
          <div className="line-clamp-1 px-3 pb-2.5 font-mono text-[10px] text-neutral-400 dark:text-neutral-500" title={n.detail.url}>
            {n.detail.url}
          </div>
        ) : n.file ? (
          <div className="line-clamp-1 px-3 pb-2.5 font-mono text-[10px] text-neutral-400 dark:text-neutral-500" title={n.file}>
            {n.file}
          </div>
        ) : (
          <div className="px-3 pb-2.5 text-[10px] text-neutral-400 dark:text-neutral-500">{meta.label}</div>
        )}
        <Handle
          type="source"
          position={Position.Right}
          className="!size-2 !border-2 !border-neutral-300 !bg-white opacity-0 transition-opacity group-hover:opacity-100 dark:!border-neutral-600 dark:!bg-neutral-800"
        />
      </div>
    </div>
  );
}

const nodeTypes = { topo: TopoNode };

const viewTabs: { k: TopoView; label: string; icon: typeof NetworkIcon }[] = [
  { k: "endpoints", label: "接口操作面", icon: GlobeIcon },
  { k: "navigation", label: "页面流转", icon: SplitIcon },
  { k: "all", label: "全部", icon: ListTreeIcon },
];

function TopologyInner({
  topology,
  onSelect,
}: {
  topology: MiniprogramTopology;
  onSelect: (n: MiniprogramTopologyNode) => void;
}) {
  const [rfNodes, setRfNodes, onNodesChange] = useNodesState<TopoRFNode>([]);
  const [rfEdges, setRfEdges, onEdgesChange] = useEdgesState<RFEdge>([]);
  const [view, setView] = React.useState<TopoView>("endpoints");
  const [keyPagesOnly, setKeyPagesOnly] = React.useState(true);

  const filtered = React.useMemo(
    () => filterGraph(topology, view, keyPagesOnly),
    [topology, view, keyPagesOnly],
  );
  const { nodes, edges } = React.useMemo(
    () => layout(filtered.nodes, filtered.edges, view),
    [filtered, view],
  );

  React.useEffect(() => {
    setRfNodes(nodes);
    setRfEdges(edges);
  }, [nodes, edges, setRfNodes, setRfEdges]);

  const isEmpty = filtered.nodes.length === 0;
  const apiCount = filtered.nodes.filter((n) => n.type === "api").length;
  const cfCount = filtered.nodes.filter((n) => n.type === "cloudfn").length;
  const pageCount = filtered.nodes.filter((n) => n.type === "page").length;

  return (
    <ReactFlow
      nodes={rfNodes}
      edges={rfEdges}
      onNodesChange={onNodesChange}
      onEdgesChange={onEdgesChange}
      onNodeClick={(_, node) => onSelect((node.data as TopoData).node)}
      nodeTypes={nodeTypes}
      fitView
      fitViewOptions={{ padding: 0.15 }}
      proOptions={{ hideAttribution: true }}
      onlyRenderVisibleElements
      minZoom={0.1}
      className="!bg-[#f0f2f7] dark:!bg-neutral-950"
    >
      <Background variant={BackgroundVariant.Dots} gap={16} size={1} className="text-neutral-400/50 dark:text-neutral-700/60" />
      <Controls showInteractive={false} className="!rounded-lg !border !shadow-sm [&>button]:!border-border [&>button]:!bg-card [&>button:hover]:!bg-accent [&_svg]:!fill-foreground" />
      <MiniMap
        pannable
        zoomable
        className="!bg-card !rounded-lg !border !shadow-sm"
        maskColor="rgb(148 163 184 / 0.18)"
        nodeColor={(node) => {
          const data = node.data as TopoData | undefined;
          return (data?.node ? typeMeta[data.node.type] : typeMeta.file)?.hex ?? "#94a3b8";
        }}
        nodeStrokeWidth={0}
        nodeBorderRadius={4}
      />
      <Panel position="top-left">
        <div className="bg-card/95 flex w-72 flex-col gap-2 rounded-lg border p-3 text-xs shadow-sm backdrop-blur">
          <div className="flex gap-1">
            {viewTabs.map((t) => (
              <button
                key={t.k}
                type="button"
                onClick={() => setView(t.k)}
                className={cn(
                  "inline-flex flex-1 items-center justify-center gap-1 rounded-md px-2 py-1 text-[11px] font-medium transition-colors",
                  view === t.k
                    ? "bg-primary text-primary-foreground"
                    : "text-muted-foreground hover:bg-accent",
                )}
              >
                <t.icon className="size-3.5" />
                {t.label}
              </button>
            ))}
          </div>

          {view === "navigation" ? (
            <div className="flex items-center justify-between">
              <span className="text-muted-foreground">关键页（参与跳转≥2）</span>
              <button
                type="button"
                onClick={() => setKeyPagesOnly((v) => !v)}
                className={cn(
                  "relative h-4 w-8 rounded-full transition-colors",
                  keyPagesOnly ? "bg-primary" : "bg-muted",
                )}
                aria-pressed={keyPagesOnly}
              >
                <span
                  className={cn(
                    "absolute top-0.5 size-3 rounded-full bg-white transition-all",
                    keyPagesOnly ? "left-4" : "left-0.5",
                  )}
                />
              </button>
            </div>
          ) : null}

          {isEmpty ? (
            <span className="text-muted-foreground">该视图下没有可展示的节点</span>
          ) : (
            <div className="flex items-center gap-3 text-muted-foreground">
              <span>
                节点 <b className="text-foreground tabular-nums">{filtered.nodes.length}</b>
              </span>
              <span>
                边 <b className="text-foreground tabular-nums">{filtered.edges.length}</b>
              </span>
            </div>
          )}

          <div className="border-border/60 flex flex-wrap gap-x-3 gap-y-1.5 border-t pt-2">
            {["page", "file", "api", "cloudfn"].map((k) => {
              const m = typeMeta[k];
              const Icon = m.icon;
              return (
                <span key={k} className="text-foreground inline-flex items-center gap-1.5">
                  <span className={cn("flex size-4 items-center justify-center rounded", m.iconBg)}>
                    <Icon className="size-2.5 text-white" />
                  </span>
                  {m.label}
                </span>
              );
            })}
          </div>
          <div className="border-border/60 flex flex-wrap gap-x-3 gap-y-1.5 border-t pt-2">
            {Object.entries(edgeMeta).map(([k, m]) => (
              <span key={k} className="inline-flex items-center gap-1.5">
                <span
                  className="h-0.5 w-4 rounded-full"
                  style={{ backgroundColor: m.color, ...(m.dashed ? { backgroundImage: `repeating-linear-gradient(90deg, ${m.color} 0 4px, transparent 4px 7px)` } : {}) }}
                />
                {m.label}
              </span>
            ))}
            <span className="text-muted-foreground">点击节点查看源码</span>
          </div>
          {view === "endpoints" ? (
            <div className="border-border/60 flex items-center gap-3 border-t pt-2 text-muted-foreground">
              <span>
                接口 <b className="text-orange-500 tabular-nums">{apiCount}</b>
              </span>
              <span>
                云函数 <b className="text-violet-500 tabular-nums">{cfCount}</b>
              </span>
              <span>
                调用页面 <b className="text-blue-500 tabular-nums">{pageCount}</b>
              </span>
              <ShieldCheckIcon className="size-3.5" />
            </div>
          ) : null}
        </div>
      </Panel>
    </ReactFlow>
  );
}

export function MiniprogramTopologyGraph({
  topology,
  onSelect,
  className,
}: {
  topology: MiniprogramTopology;
  onSelect: (n: MiniprogramTopologyNode) => void;
  className?: string;
}) {
  return (
    <div className={cn("w-full overflow-hidden rounded-xl", className)}>
      <ReactFlowProvider>
        <TopologyInner topology={topology} onSelect={onSelect} />
      </ReactFlowProvider>
    </div>
  );
}

export type { MiniprogramTopologyEdge, MiniprogramTopologyNode };