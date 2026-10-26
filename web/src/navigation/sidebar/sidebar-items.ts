import {
  Activity,
  BellRing,
  BookOpenText,
  Boxes,
  Bug,
  Cable,
  Database,
  FileText,
  FolderKanban,
  History,
  LayoutDashboard,
  ListChecks,
  type LucideIcon,
  MessageSquare,
  Network,
  PlugZap,
  Radar,
  Radio,
  ScanSearch,
  ScrollText,
  Server,
  Settings,
  Share2,
  ShieldAlert,
  ShieldCheck,
  ShieldQuestion,
  Smartphone,
  Sparkles,
  Swords,
  TerminalSquare,
  Users,
  UsersRound,
  Workflow,
  Wrench,
} from "lucide-react";

export type NavBadge = "new" | "soon";

export interface NavSubItem {
  id: string;
  title: string;
  url: string;
  icon?: LucideIcon;
  badge?: NavBadge;
  disabled?: boolean;
  newTab?: boolean;
}

interface NavItemBase {
  id: string;
  title: string;
  icon?: LucideIcon;
  badge?: NavBadge;
  disabled?: boolean;
  newTab?: boolean;
}

export interface NavMainLinkItem extends NavItemBase {
  url: string;
  subItems?: never;
}

export interface NavMainParentItem extends NavItemBase {
  subItems: NavSubItem[];
}

export type NavMainItem = NavMainLinkItem | NavMainParentItem;

export interface NavGroup {
  id: number;
  label?: string;
  items: NavMainItem[];
}

/**
 * 控制台菜单（2026-10-23 单 UI 定案）：
 *
 *  1. 主 UI 唯一：`/`（本控制台）。同一功能的交互只留一套实现，全部收敛在控制台；
 *     findu 工作台未迁交互（多代理对话/深任务板/人工审批面板等）按域逐批原生迁入。
 *  2. `/findu/` 降级为回滚逃生舱：URL 继续挂载（≥1 个发布周期），但控制台不再放
 *     「返回主界面/去 findu」类引导入口；仅保留功能性指针（HITL 审批、更新面板）直到
 *     控制台补齐对应能力。
 *  3. 双跑开关（6c 时代的写侧 UI 验证阀）已于 U2 退役：后端写侧早已宿主原生，
 *     控制台写入口默认开启，不再有「双跑：只读」阀门。
 *
 * 导航采用「精简三级域」组织（2026-10-26）：
 *  - 高频功能（仪表盘 / 对话）提为顶级标签，无需展开。
 *  - 其余按安全/渗透主流术语收敛为五大域：侦察、任务编排、移动逆向、模型观测、系统编排。
 *  - 对话（/chat）本身已内嵌会话历史侧栏，故「会话历史」不再单列入口。
 */
export const sidebarItems: NavGroup[] = [
  {
    id: 1,
    items: [
      { id: "dashboard", title: "仪表盘", url: "/", icon: LayoutDashboard },
      { id: "chat", title: "对话", url: "/chat", icon: MessageSquare },
    ],
  },
  {
    id: 2,
    label: "侦察",
    items: [
      { id: "projects", title: "项目", url: "/function/projects", icon: FolderKanban },
      { id: "assets", title: "资产", url: "/function/assets", icon: Boxes },
      { id: "info-collect", title: "空间测绘", url: "/function/info-collect", icon: Radar },
      { id: "webshell", title: "WebShell", url: "/function/webshell", icon: TerminalSquare },
      { id: "findings", title: "漏洞", url: "/function/findings", icon: Bug },
      { id: "vuln-kb", title: "漏洞库", url: "/function/vuln-kb", icon: BookOpenText },
      { id: "traffic", title: "流量", url: "/function/traffic", icon: Activity },
    ],
  },
  {
    id: 3,
    label: "任务编排",
    items: [
      { id: "plan-tasks", title: "任务看板", url: "/function/plan-tasks", icon: Swords },
      { id: "tasks", title: "任务", url: "/function/tasks", icon: ListChecks },
      { id: "hitl", title: "人工审批", url: "/function/hitl", icon: ShieldQuestion },
    ],
  },
  {
    id: 4,
    label: "移动逆向",
    items: [
      { id: "miniprogram", title: "小程序逆向", url: "/function/miniprogram", icon: ScanSearch },
      { id: "android", title: "APK 逆向", url: "/function/android", icon: Smartphone },
    ],
  },
  {
    id: 5,
    label: "模型观测",
    items: [{ id: "llm-records", title: "模型录制", url: "/function/llm-records", icon: Radio }],
  },
  {
    id: 6,
    label: "系统编排",
    items: [
      {
        id: "group-agents",
        title: "智能体",
        icon: Users,
        subItems: [
          { id: "agents", title: "智能体", url: "/system/agents", icon: Users },
          { id: "tools", title: "工具", url: "/system/tools", icon: Wrench },
          { id: "external-mcp", title: "外部 MCP", url: "/system/external-mcp", icon: Cable },
          { id: "workflows", title: "工作流", url: "/system/workflows", icon: Workflow },
          { id: "tool-guard", title: "工具防护", url: "/system/tool-guard", icon: ShieldAlert },
        ],
      },
      {
        id: "group-knowledge",
        title: "知识库与技能",
        icon: Database,
        subItems: [
          { id: "knowledge", title: "知识库", url: "/system/knowledge", icon: Database },
          { id: "fact-graph", title: "事实图谱", url: "/system/fact-graph", icon: Share2 },
          { id: "skills", title: "技能", url: "/system/skills", icon: Sparkles },
          { id: "skill-candidates", title: "技能候选", url: "/system/skill-candidates", icon: Sparkles },
        ],
      },
      {
        id: "group-access",
        title: "访问控制",
        icon: ShieldCheck,
        subItems: [
          { id: "roles", title: "角色", url: "/system/roles", icon: UsersRound },
          { id: "rbac", title: "权限管理", url: "/system/rbac", icon: ShieldCheck },
        ],
      },
      {
        id: "group-platform",
        title: "平台管理",
        icon: Settings,
        subItems: [
          { id: "services", title: "系统服务", url: "/system/services", icon: Server },
          { id: "plugins", title: "插件", url: "/system/plugins", icon: PlugZap },
          { id: "c2", title: "C2 会话", url: "/system/c2", icon: Network },
          { id: "notify", title: "通知推送", url: "/system/notify", icon: BellRing },
          { id: "reports", title: "报告", url: "/system/reports", icon: FileText },
          { id: "logs", title: "日志", url: "/system/logs", icon: ScrollText },
          { id: "changelog", title: "版本记录", url: "/system/changelog", icon: History },
          { id: "settings", title: "系统设置", url: "/system/settings", icon: Settings },
        ],
      },
    ],
  },
];
