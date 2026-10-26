"use client";

import * as React from "react";

import { Bot, PlusIcon, Trash2Icon } from "lucide-react";
import { toast } from "sonner";

import { AgentEditor } from "@/components/agent-editor";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/api";
import type { Agent } from "@/lib/types";

// AgentGridCard is one clickable tile opening the agent's editor drawer. Custom
// (non-builtin) agents get a delete button.
function AgentGridCard({ agent, onOpen, onDeleted }: { agent: Agent; onOpen: () => void; onDeleted: () => void }) {
  async function del() {
    try {
      await api.deleteAgent(agent.key);
      toast.success(`已删除 Agent「${agent.name}」`);
      onDeleted();
    } catch (e) {
      toast.error("删除失败：" + (e as Error).message);
    }
  }
  return (
    <div className="hover:border-primary/50 group relative flex flex-col gap-2 rounded-lg border p-4 transition-colors">
      <button type="button" onClick={onOpen} className="flex flex-col gap-2 text-left">
        <div className="flex flex-wrap items-center gap-2">
          <Bot className="text-muted-foreground size-4" />
          <span className="text-sm font-medium">{agent.name}</span>
          <span className="text-muted-foreground font-mono text-xs">{agent.key}</span>
          {agent.builtin ? (
            <Badge variant="secondary" className="px-1.5 py-0 text-[10px]">
              内置
            </Badge>
          ) : (
            <Badge variant="outline" className="px-1.5 py-0 text-[10px]">
              自定义
            </Badge>
          )}
          {!agent.enabled && (
            <Badge variant="outline" className="text-destructive px-1.5 py-0 text-[10px]">
              已停用
            </Badge>
          )}
        </div>
        <p className="text-muted-foreground line-clamp-2 min-h-8 text-xs">{agent.description || "（无描述）"}</p>
        <div className="text-muted-foreground flex flex-wrap gap-1.5 text-[10px]">
          <span className="rounded border px-1.5 py-0.5">MCP {agent.mcp_count ?? 0}</span>
          <span className="rounded border px-1.5 py-0.5">Skill {agent.skill_count ?? 0}</span>
          <span className="rounded border px-1.5 py-0.5">工具 {agent.tool_count ?? 0}</span>
        </div>
      </button>
      {!agent.builtin && (
        <AlertDialog>
          <AlertDialogTrigger asChild>
            <Button
              variant="ghost"
              size="icon-sm"
              className="text-muted-foreground hover:text-destructive absolute top-2 right-2 opacity-0 transition-opacity group-hover:opacity-100"
            >
              <Trash2Icon className="size-3.5" />
            </Button>
          </AlertDialogTrigger>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>删除 Agent「{agent.name}」？</AlertDialogTitle>
              <AlertDialogDescription>
                将一并删除它的提示词、变量、可见性与工具绑定。此操作不可撤销。
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>取消</AlertDialogCancel>
              <AlertDialogAction onClick={del}>删除</AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      )}
    </div>
  );
}

function CreateAgentDialog({ onCreated }: { onCreated: (key: string) => void }) {
  const [open, setOpen] = React.useState(false);
  const [key, setKey] = React.useState("");
  const [name, setName] = React.useState("");
  const [description, setDescription] = React.useState("");
  const [busy, setBusy] = React.useState(false);

  async function create() {
    setBusy(true);
    try {
      const a = await api.createAgent(key.trim(), name.trim(), description.trim());
      toast.success(`已创建 Agent「${a.name}」`);
      setOpen(false);
      setKey("");
      setName("");
      setDescription("");
      onCreated(a.key);
    } catch (e) {
      toast.error("创建失败：" + (e as Error).message);
    } finally {
      setBusy(false);
    }
  }

  const keyOk = /^[a-z][a-z0-9_]*$/.test(key.trim());
  const canCreate = keyOk && name.trim().length > 0 && !busy;

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="sm">
          <PlusIcon /> 新建 Agent
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>新建自定义 Agent</DialogTitle>
          <DialogDescription>
            创建一个会话型助手。key 用于内部标识，创建后不可更改；名称与描述用于识别。
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-4 py-2">
          <div className="grid gap-1.5">
            <Label htmlFor="agent-key">Key</Label>
            <Input
              id="agent-key"
              placeholder="如 research_helper"
              value={key}
              onChange={(e) => setKey(e.target.value)}
              className="font-mono"
            />
            {key.length > 0 && !keyOk && (
              <span className="text-destructive text-xs">小写字母开头，仅含小写字母/数字/下划线</span>
            )}
          </div>
          <div className="grid gap-1.5">
            <Label htmlFor="agent-name">名称</Label>
            <Input id="agent-name" placeholder="如 研究助手" value={name} onChange={(e) => setName(e.target.value)} />
          </div>
          <div className="grid gap-1.5">
            <Label htmlFor="agent-desc">描述</Label>
            <Textarea
              id="agent-desc"
              placeholder="一句话说明这个 Agent 是干什么的"
              rows={2}
              value={description}
              onChange={(e) => setDescription(e.target.value)}
            />
          </div>
        </div>
        <DialogFooter>
          <Button onClick={create} disabled={!canCreate}>
            创建
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export default function AgentsPage() {
  const [agents, setAgents] = React.useState<Agent[]>([]);
  const [editKey, setEditKey] = React.useState<string | null>(null);

  const reload = React.useCallback(() => {
    api
      .agents()
      .then(setAgents)
      .catch(() => setAgents([]));
  }, []);
  React.useEffect(() => {
    reload();
  }, [reload]);

  const editing = agents.find((a) => a.key === editKey) ?? null;

  // 角色与引擎编排分开展示：role 为唯一真源，kind 已归一化为 role 推导（不再读库中 kind 列）。
  const kindOf = (a: Agent) => (a.role === "assistant" ? "role" : "engine");
  const roleAgents = agents.filter((a) => kindOf(a) === "role");
  const engineAgents = agents.filter((a) => kindOf(a) === "engine");

  return (
    <div className="flex flex-1 flex-col gap-4 md:gap-6">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold tracking-tight">Agent 智能体</h1>
          <p className="text-muted-foreground text-sm">
            角色＝会话里可选的人设与工具绑定；引擎编排＝自主渗透运行时内部件，只改提示词
          </p>
        </div>
        <CreateAgentDialog
          onCreated={(key) => {
            reload();
            setEditKey(key);
          }}
        />
      </div>

      <Tabs defaultValue="role" className="gap-4">
        <TabsList>
          <TabsTrigger value="role">角色（{roleAgents.length}）</TabsTrigger>
          <TabsTrigger value="engine">引擎编排（{engineAgents.length}）</TabsTrigger>
        </TabsList>

        <TabsContent value="role">
          <Card>
            <CardHeader>
              <CardTitle>会话可选角色</CardTitle>
              <CardDescription>
                在对话里可被选中的角色。提示词按版本保存，工具绑定走 tools.agents（白名单），MCP/技能走可见性配置。
              </CardDescription>
            </CardHeader>
            <CardContent>
              {roleAgents.length === 0 ? (
                <p className="text-muted-foreground py-6 text-center text-sm">（暂无角色）</p>
              ) : (
                <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
                  {roleAgents.map((a) => (
                    <AgentGridCard key={a.key} agent={a} onOpen={() => setEditKey(a.key)} onDeleted={reload} />
                  ))}
                </div>
              )}
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="engine">
          <Card>
            <CardHeader>
              <CardTitle>引擎编排</CardTitle>
              <CardDescription>
                目标拆解 / 规划 / 主 / 执行四个内部件，由自主渗透引擎按 key
                调用，不出现在会话角色列表里。一般只改提示词。
              </CardDescription>
            </CardHeader>
            <CardContent>
              {engineAgents.length === 0 ? (
                <p className="text-muted-foreground py-6 text-center text-sm">（暂无引擎件）</p>
              ) : (
                <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
                  {engineAgents.map((a) => (
                    <AgentGridCard key={a.key} agent={a} onOpen={() => setEditKey(a.key)} onDeleted={reload} />
                  ))}
                </div>
              )}
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>

      <Sheet open={!!editing} onOpenChange={(o) => !o && setEditKey(null)}>
        <SheetContent
          side="right"
          className="flex flex-col gap-0 p-0 data-[side=right]:w-[45vw] data-[side=right]:sm:max-w-[45vw]"
        >
          {editing && (
            <>
              <SheetHeader className="px-4">
                <SheetTitle className="flex items-center gap-2">
                  {editing.name}
                  <span className="text-muted-foreground font-mono text-xs">{editing.key}</span>
                  {!editing.builtin && (
                    <Badge variant="outline" className="px-1.5 py-0 text-[10px]">
                      自定义
                    </Badge>
                  )}
                </SheetTitle>
                <SheetDescription>{editing.description || "提示词、配置、可见资源与工具绑定"}</SheetDescription>
              </SheetHeader>
              <AgentEditor agentKey={editing.key} onSaved={reload} />
            </>
          )}
        </SheetContent>
      </Sheet>
    </div>
  );
}
