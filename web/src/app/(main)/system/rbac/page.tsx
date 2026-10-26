"use client";

import * as React from "react";

import { KeyRoundIcon, PlusIcon, RefreshCwIcon, ShieldCheckIcon, Trash2Icon, UserRoundIcon } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
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
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import { finduDelete, finduErrorMessage, finduFetch, finduPost, finduPut, formatTime } from "@/lib/findu";

// ---------------------------------------------------------------------------
// 类型
// ---------------------------------------------------------------------------

type User = {
  id: string;
  username: string;
  display_name: string;
  enabled: boolean;
  is_builtin: boolean;
  roles: string[];
  created_at: string;
  updated_at: string;
};

type Role = {
  id: string;
  name: string;
  description: string;
  scope: string;
  is_system: boolean;
  permissions: string[];
  created_at: string;
  updated_at: string;
};

type Metadata = {
  permissions: Record<string, string>;
  scopes: string[];
};

const SCOPE_LABEL: Record<string, string> = {
  all: "全部资源",
  assigned: "被授权资源",
  own: "自己创建的资源",
};

// ---------------------------------------------------------------------------
// 权限矩阵
// ---------------------------------------------------------------------------

function groupPermissions(perms: Record<string, string>): { group: string; keys: string[] }[] {
  const buckets = new Map<string, string[]>();
  for (const key of Object.keys(perms).sort()) {
    const group = key.includes(":") ? key.split(":")[0] : "other";
    const arr = buckets.get(group) ?? [];
    arr.push(key);
    buckets.set(group, arr);
  }
  return [...buckets.entries()].sort((a, b) => a[0].localeCompare(b[0])).map(([group, keys]) => ({ group, keys }));
}

function PermissionMatrix({
  catalog,
  value,
  onChange,
  readOnly,
}: {
  catalog: Record<string, string>;
  value: string[];
  onChange: (next: string[]) => void;
  readOnly: boolean;
}) {
  const groups = React.useMemo(() => groupPermissions(catalog), [catalog]);
  const selected = React.useMemo(() => new Set(value), [value]);

  function toggle(key: string, checked: boolean) {
    const next = new Set(selected);
    if (checked) next.add(key);
    else next.delete(key);
    onChange([...next].sort());
  }

  return (
    <div className="max-h-[52vh] overflow-auto rounded-md border p-3">
      <div className="grid gap-4">
        {groups.map((g) => (
          <div key={g.group} className="grid gap-1.5">
            <div className="flex items-center gap-2">
              <span className="font-mono text-xs font-semibold">{g.group}</span>
              <Badge variant="outline" className="text-[10px]">
                {g.keys.filter((k) => selected.has(k)).length}/{g.keys.length}
              </Badge>
            </div>
            <div className="grid gap-1 md:grid-cols-2">
              {g.keys.map((key) => (
                <label key={key} className="flex items-start gap-2 rounded px-1 py-0.5 text-xs hover:bg-muted/60">
                  <Checkbox
                    checked={selected.has(key)}
                    disabled={readOnly}
                    onCheckedChange={(checked) => toggle(key, checked === true)}
                    className="mt-0.5"
                  />
                  <span className="min-w-0">
                    <span className="font-mono">{key}</span>
                    <span className="text-muted-foreground block text-[11px] leading-tight">{catalog[key] ?? ""}</span>
                  </span>
                </label>
              ))}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}

function RoleEditorDialog({
  role,
  catalog,
  scopes,
  onSaved,
  trigger,
}: {
  role: Role | null;
  catalog: Record<string, string>;
  scopes: string[];
  onSaved: () => void;
  trigger: React.ReactNode;
}) {
  const [open, setOpen] = React.useState(false);
  const [name, setName] = React.useState("");
  const [description, setDescription] = React.useState("");
  const [scope, setScope] = React.useState("all");
  const [permissions, setPermissions] = React.useState<string[]>([]);
  const [busy, setBusy] = React.useState(false);

  React.useEffect(() => {
    if (!open) return;
    setName(role?.name ?? "");
    setDescription(role?.description ?? "");
    setScope(role?.scope || "all");
    setPermissions(role?.permissions ?? []);
  }, [open, role]);

  const readOnly = role?.is_system === true;

  async function save() {
    if (name.trim() === "") {
      toast.error("请填写角色名称");
      return;
    }
    setBusy(true);
    try {
      const body = {
        name: name.trim(),
        description: description.trim(),
        scope,
        permissions,
      };
      if (role) {
        await finduPut(`/rbac/roles/${role.id}`, body);
        toast.success("角色已更新（已签发会话将失效，需重新登录）");
      } else {
        await finduPost("/rbac/roles", body);
        toast.success("角色已创建");
      }
      setOpen(false);
      onSaved();
    } catch (e) {
      toast.error("保存失败：" + finduErrorMessage(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>{trigger}</DialogTrigger>
      <DialogContent className="sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>{role ? `角色：${role.name}` : "新建角色"}</DialogTitle>
          <DialogDescription>
            {readOnly
              ? "系统内置角色只读（后端拒绝修改）；需要调整请新建自定义角色。"
              : "权限按前缀分组；保存后所有已签发会话将被吊销。"}
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-3">
          <div className="grid gap-3 md:grid-cols-3">
            <div className="grid gap-1.5">
              <Label htmlFor="r-name">名称</Label>
              <Input id="r-name" value={name} onChange={(e) => setName(e.target.value)} disabled={readOnly} />
            </div>
            <div className="grid gap-1.5">
              <Label>资源范围</Label>
              <Select value={scope} onValueChange={setScope} disabled={readOnly}>
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {scopes.map((s) => (
                    <SelectItem key={s} value={s}>
                      {SCOPE_LABEL[s] ?? s}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="grid gap-1.5">
              <Label>已选权限</Label>
              <div className="flex h-9 items-center rounded-md border px-3 text-sm">{permissions.length} 项</div>
            </div>
          </div>
          <div className="grid gap-1.5">
            <Label htmlFor="r-desc">描述</Label>
            <Input
              id="r-desc"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              disabled={readOnly}
            />
          </div>
          <PermissionMatrix catalog={catalog} value={permissions} onChange={setPermissions} readOnly={readOnly} />
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => setOpen(false)} disabled={busy}>
            关闭
          </Button>
          {!readOnly ? (
            <Button onClick={() => void save()} disabled={busy}>
              {busy ? "保存中…" : "保存"}
            </Button>
          ) : null}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ---------------------------------------------------------------------------
// 用户
// ---------------------------------------------------------------------------

function UserDialog({
  user,
  roles,
  onSaved,
  trigger,
}: {
  user: User | null;
  roles: Role[];
  onSaved: () => void;
  trigger: React.ReactNode;
}) {
  const [open, setOpen] = React.useState(false);
  const [username, setUsername] = React.useState("");
  const [displayName, setDisplayName] = React.useState("");
  const [password, setPassword] = React.useState("");
  const [enabled, setEnabled] = React.useState(true);
  const [picked, setPicked] = React.useState<string[]>([]);
  const [busy, setBusy] = React.useState(false);

  React.useEffect(() => {
    if (!open) return;
    setUsername(user?.username ?? "");
    setDisplayName(user?.display_name ?? "");
    setPassword("");
    setEnabled(user?.enabled ?? true);
    setPicked(user?.roles ?? []);
  }, [open, user]);

  async function save() {
    if (!user && (username.trim() === "" || password === "")) {
      toast.error("新建用户必须填用户名与初始密码");
      return;
    }
    setBusy(true);
    try {
      if (user) {
        const body: Record<string, unknown> = {
          display_name: displayName.trim(),
          enabled,
          roles: picked,
        };
        if (password !== "") body.password = password;
        await finduPut(`/rbac/users/${user.id}`, body);
        toast.success("用户已更新");
      } else {
        await finduPost("/rbac/users", {
          username: username.trim(),
          display_name: displayName.trim(),
          password,
          enabled,
          roles: picked,
        });
        toast.success("用户已创建");
      }
      setOpen(false);
      onSaved();
    } catch (e) {
      toast.error("保存失败：" + finduErrorMessage(e));
    } finally {
      setBusy(false);
    }
  }

  function toggleRole(id: string, checked: boolean) {
    setPicked((prev) => (checked ? [...new Set([...prev, id])] : prev.filter((r) => r !== id)));
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>{trigger}</DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{user ? `用户：${user.username}` : "新建用户"}</DialogTitle>
          <DialogDescription>
            {user ? "用户名不可修改；密码留空表示不改。" : "密码仅用于控制台登录，创建后可在编辑里重置。"}
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-3 py-2">
          <div className="grid gap-3 md:grid-cols-2">
            <div className="grid gap-1.5">
              <Label htmlFor="u-name">用户名</Label>
              <Input
                id="u-name"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                disabled={user !== null}
                className="font-mono"
              />
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor="u-display">显示名</Label>
              <Input id="u-display" value={displayName} onChange={(e) => setDisplayName(e.target.value)} />
            </div>
          </div>
          <div className="grid gap-1.5">
            <Label htmlFor="u-pass">{user ? "重置密码（留空不改）" : "初始密码"}</Label>
            <Input id="u-pass" type="password" value={password} onChange={(e) => setPassword(e.target.value)} />
          </div>
          <div className="flex items-center justify-between rounded-md border px-3 py-2">
            <div>
              <div className="text-sm font-medium">启用账号</div>
              <div className="text-muted-foreground text-xs">停用后无法登录，权限与会话一并失效。</div>
            </div>
            <Switch checked={enabled} onCheckedChange={setEnabled} />
          </div>
          <div className="grid gap-1.5">
            <Label>角色</Label>
            <div className="grid max-h-48 gap-1 overflow-auto rounded-md border p-2">
              {roles.length === 0 ? <span className="text-muted-foreground text-xs">还没有角色</span> : null}
              {roles.map((r) => (
                <label key={r.id} className="flex items-center gap-2 rounded px-1 py-1 text-sm hover:bg-muted/60">
                  <Checkbox checked={picked.includes(r.id)} onCheckedChange={(c) => toggleRole(r.id, c === true)} />
                  <span className="font-medium">{r.name}</span>
                  <span className="text-muted-foreground font-mono text-[11px]">{r.id}</span>
                  <Badge variant="outline" className="ml-auto text-[10px]">
                    {SCOPE_LABEL[r.scope] ?? r.scope}
                  </Badge>
                </label>
              ))}
            </div>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => setOpen(false)} disabled={busy}>
            取消
          </Button>
          <Button onClick={() => void save()} disabled={busy}>
            {busy ? "保存中…" : "保存"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ---------------------------------------------------------------------------
// 页面
// ---------------------------------------------------------------------------

export default function Page() {
  const [users, setUsers] = React.useState<User[]>([]);
  const [roles, setRoles] = React.useState<Role[]>([]);
  const [catalog, setCatalog] = React.useState<Record<string, string>>({});
  const [scopes, setScopes] = React.useState<string[]>(["all", "assigned", "own"]);
  const [loading, setLoading] = React.useState(true);

  const load = React.useCallback(async () => {
    setLoading(true);
    try {
      const [u, r, m] = await Promise.all([
        finduFetch<{ users: User[] }>("/rbac/users"),
        finduFetch<{ roles: Role[] }>("/rbac/roles"),
        finduFetch<Metadata>("/rbac/metadata"),
      ]);
      setUsers(u.users ?? []);
      setRoles(r.roles ?? []);
      setCatalog(m.permissions ?? {});
      if (Array.isArray(m.scopes) && m.scopes.length > 0) setScopes(m.scopes);
    } catch (e) {
      toast.error("读取权限数据失败：" + finduErrorMessage(e));
    } finally {
      setLoading(false);
    }
  }, []);

  React.useEffect(() => {
    void load();
  }, [load]);

  async function removeUser(u: User) {
    try {
      await finduDelete(`/rbac/users/${u.id}`);
      toast.success(`用户 ${u.username} 已删除`);
      await load();
    } catch (e) {
      toast.error("删除失败：" + finduErrorMessage(e));
    }
  }

  async function removeRole(r: Role) {
    try {
      await finduDelete(`/rbac/roles/${r.id}`);
      toast.success(`角色 ${r.name} 已删除`);
      await load();
    } catch (e) {
      toast.error("删除失败：" + finduErrorMessage(e));
    }
  }

  const roleName = React.useCallback((id: string) => roles.find((r) => r.id === id)?.name ?? id, [roles]);

  return (
    <div className="flex flex-1 flex-col gap-4 md:gap-6">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h1 className="flex items-center gap-2 text-xl font-semibold tracking-tight">
            <ShieldCheckIcon className="size-5" /> 权限管理
          </h1>
          <p className="text-muted-foreground text-sm">
            {users.length} 个用户 · {roles.length} 个角色 · {Object.keys(catalog).length} 项权限
          </p>
        </div>
        <Button variant="outline" onClick={() => void load()} disabled={loading}>
          <RefreshCwIcon className="size-4" /> 刷新
        </Button>
      </div>

      <Tabs defaultValue="users">
        <TabsList>
          <TabsTrigger value="users">用户</TabsTrigger>
          <TabsTrigger value="roles">角色与权限</TabsTrigger>
        </TabsList>

        <TabsContent value="users" className="mt-3">
          <Card>
            <CardHeader className="flex flex-row items-center justify-between gap-2">
              <div>
                <CardTitle className="flex items-center gap-2 text-base">
                  <UserRoundIcon className="size-4" /> 用户
                </CardTitle>
                <CardDescription>管理控制台的登录账号</CardDescription>
              </div>
              <UserDialog
                user={null}
                roles={roles}
                onSaved={() => void load()}
                trigger={
                  <Button size="sm">
                    <PlusIcon className="size-4" /> 新建用户
                  </Button>
                }
              />
            </CardHeader>
            <CardContent>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>用户名</TableHead>
                    <TableHead>显示名</TableHead>
                    <TableHead>角色</TableHead>
                    <TableHead>状态</TableHead>
                    <TableHead>更新时间</TableHead>
                    <TableHead className="text-right">操作</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {users.length === 0 ? (
                    <TableRow>
                      <TableCell colSpan={6} className="text-muted-foreground py-8 text-center text-sm">
                        {loading ? "加载中…" : "还没有用户"}
                      </TableCell>
                    </TableRow>
                  ) : null}
                  {users.map((u) => (
                    <TableRow key={u.id}>
                      <TableCell>
                        <div className="font-mono text-xs font-medium">{u.username}</div>
                        <div className="text-muted-foreground font-mono text-[10px]">{u.id}</div>
                      </TableCell>
                      <TableCell>{u.display_name || "-"}</TableCell>
                      <TableCell>
                        <div className="flex flex-wrap gap-1">
                          {(u.roles ?? []).length === 0 ? (
                            <span className="text-muted-foreground text-xs">-</span>
                          ) : null}
                          {(u.roles ?? []).map((rid) => (
                            <Badge key={rid} variant="secondary" className="text-[10px]">
                              {roleName(rid)}
                            </Badge>
                          ))}
                        </div>
                      </TableCell>
                      <TableCell>
                        <Badge variant={u.enabled ? "default" : "outline"} className="text-[10px]">
                          {u.enabled ? "启用" : "停用"}
                        </Badge>
                        {u.is_builtin ? (
                          <Badge variant="outline" className="ml-1 text-[10px]">
                            内置
                          </Badge>
                        ) : null}
                      </TableCell>
                      <TableCell className="text-muted-foreground text-xs">{formatTime(u.updated_at)}</TableCell>
                      <TableCell className="text-right">
                        <div className="flex justify-end gap-1">
                          <UserDialog
                            user={u}
                            roles={roles}
                            onSaved={() => void load()}
                            trigger={
                              <Button size="sm" variant="ghost">
                                <KeyRoundIcon className="size-3.5" /> 编辑
                              </Button>
                            }
                          />
                          {!u.is_builtin ? (
                            <Button
                              size="sm"
                              variant="ghost"
                              className="text-destructive hover:text-destructive"
                              onClick={() => void removeUser(u)}
                            >
                              <Trash2Icon className="size-3.5" />
                            </Button>
                          ) : null}
                        </div>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="roles" className="mt-3">
          <Card>
            <CardHeader className="flex flex-row items-center justify-between gap-2">
              <div>
                <CardTitle className="flex items-center gap-2 text-base">
                  <ShieldCheckIcon className="size-4" /> 角色
                </CardTitle>
                <CardDescription>系统角色只读；自定义角色可编辑权限矩阵。</CardDescription>
              </div>
              <RoleEditorDialog
                role={null}
                catalog={catalog}
                scopes={scopes}
                onSaved={() => void load()}
                trigger={
                  <Button size="sm">
                    <PlusIcon className="size-4" /> 新建角色
                  </Button>
                }
              />
            </CardHeader>
            <CardContent>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>角色</TableHead>
                    <TableHead>描述</TableHead>
                    <TableHead>范围</TableHead>
                    <TableHead>权限</TableHead>
                    <TableHead>类型</TableHead>
                    <TableHead className="text-right">操作</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {roles.length === 0 ? (
                    <TableRow>
                      <TableCell colSpan={6} className="text-muted-foreground py-8 text-center text-sm">
                        {loading ? "加载中…" : "还没有角色"}
                      </TableCell>
                    </TableRow>
                  ) : null}
                  {roles.map((r) => (
                    <TableRow key={r.id}>
                      <TableCell>
                        <div className="font-medium">{r.name}</div>
                        <div className="text-muted-foreground font-mono text-[10px]">{r.id}</div>
                      </TableCell>
                      <TableCell className="text-muted-foreground max-w-[260px] truncate text-xs">
                        {r.description || "-"}
                      </TableCell>
                      <TableCell className="text-xs">{SCOPE_LABEL[r.scope] ?? r.scope}</TableCell>
                      <TableCell>
                        <Badge variant="secondary" className="text-[10px]">
                          {(r.permissions ?? []).length} 项
                        </Badge>
                      </TableCell>
                      <TableCell>
                        <Badge variant={r.is_system ? "outline" : "default"} className="text-[10px]">
                          {r.is_system ? "系统" : "自定义"}
                        </Badge>
                      </TableCell>
                      <TableCell className="text-right">
                        <div className="flex justify-end gap-1">
                          <RoleEditorDialog
                            role={r}
                            catalog={catalog}
                            scopes={scopes}
                            onSaved={() => void load()}
                            trigger={
                              <Button size="sm" variant="ghost">
                                权限矩阵
                              </Button>
                            }
                          />
                          {!r.is_system ? (
                            <Button
                              size="sm"
                              variant="ghost"
                              className="text-destructive hover:text-destructive"
                              onClick={() => void removeRole(r)}
                            >
                              <Trash2Icon className="size-3.5" />
                            </Button>
                          ) : null}
                        </div>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>
    </div>
  );
}
