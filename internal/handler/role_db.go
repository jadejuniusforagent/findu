package handler

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"finduai/internal/config"
	"finduai/internal/database"
)

// 角色统一真源：PostgreSQL public.agents(kind='role') + agent_prompts(当前版本)。
//
// 为什么不再读 yaml：findu 原先的 roles/*.yaml 只能表达「名称/描述/prompt/工具名列表」，
// 没有版本、没有 MCP/技能绑定、没有启用位与来源标记。迁移后角色的唯一真源在 findu 侧，
// findu 只做消费方（读 DB + 运行时按角色提示词与工具集装配），写入口统一到控制台 /system/agents。
//
// 工具绑定的分工（两边运行时各有自己的工具注册表，名字不通用）：
//   - findu 内置工具：public.tools.agents 里包含角色 key 即绑定（白名单语义）
//   - MCP / 技能：public.agent_visibility(agent_id, resource_kind, resource_id)
//   - findu 内置工具：来自 findu 自己的注册表，不走 findu 的 tools 表；这里只回传 MCP 绑定，
//     Tools 留空表示「不额外收窄」，与 findu 旧行为（role.Tools 为空=不限制）一致。
func rolesFromDB(ctx context.Context, db *sql.DB) (map[string]config.RoleConfig, error) {
	if db == nil {
		return nil, nil
	}
	rows, err := db.QueryContext(ctx, `
		SELECT a.key, a.name, COALESCE(a.description, ''), COALESCE(a.icon, ''), a.enabled,
		       COALESCE(p.template_text, ''), a.id
		  FROM public.agents a
		  LEFT JOIN public.agent_prompts p ON p.id = a.current_prompt_id
		 WHERE a.role = 'assistant'
		   AND a.kind = 'role'
		 ORDER BY a.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]config.RoleConfig)
	ids := make(map[string]int64)
	keys := make(map[string]string)
	for rows.Next() {
		var key, name, desc, icon, prompt string
		var enabled bool
		var id int64
		if err := rows.Scan(&key, &name, &desc, &icon, &enabled, &prompt, &id); err != nil {
			return nil, err
		}
		out[name] = config.RoleConfig{
			Name:        name,
			Description: desc,
			UserPrompt:  prompt,
			Icon:        icon,
			Enabled:     enabled,
		}
		ids[name] = id
		keys[name] = key
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for name, id := range ids {
		r := out[name]
		changed := false
		if mcps := visibleMCPNames(ctx, db, id); len(mcps) > 0 {
			r.MCPs = mcps
			changed = true
		}
		// 工具绑定：统一目录 public.tools 里 runtime='findu' 且 agents 含该角色 key 的行。
		// 一条都没绑 → Tools 留空（= 不额外收窄，等价于 findu 旧行为）。
		if tl := boundFinduTools(ctx, db, keys[name]); len(tl) > 0 {
			r.Tools = tl
			changed = true
		}
		if changed {
			out[name] = r
		}
	}
	return out, nil
}

// boundFinduTools 读该角色在统一工具目录里绑定的 findu 运行时工具（按 agent key）。
// 注意：这里必须用 jsonb_exists() 而不是 `agents ? 'key'` —— findu 的方言层会把 SQL 里的
// `?` 当 SQLite 占位符重写成 `$n`，`?` 运算符会被改坏（实测绑定了也读不出来）。
func boundFinduTools(ctx context.Context, db *sql.DB, agentKey string) []string {
	if db == nil || agentKey == "" {
		return nil
	}
	rows, err := db.QueryContext(ctx, `
		SELECT key FROM public.tools
		 WHERE runtime = 'findu' AND enabled AND jsonb_exists(agents, $1)
		 ORDER BY key`, agentKey)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err == nil && k != "" {
			out = append(out, k)
		}
	}
	return out
}

// visibleMCPNames 读该角色绑定的 MCP 服务器名；表结构缺失时静默返回空（不阻断角色读取）。
func visibleMCPNames(ctx context.Context, db *sql.DB, agentID int64) []string {
	rows, err := db.QueryContext(ctx, `
		SELECT COALESCE(m.name, '')
		  FROM public.agent_visibility v
		  JOIN public.mcp_servers m ON m.id = v.resource_id
		 WHERE v.agent_id = $1 AND v.resource_kind = 'mcp' AND v.enabled`, agentID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err == nil && name != "" {
			out = append(out, name)
		}
	}
	return out
}

// roleCache 极短缓存：一条消息内多次解析角色，避免重复打库。
type roleCache struct {
	mu      sync.Mutex
	at      time.Time
	roles   map[string]config.RoleConfig
	lastErr error
}

var chatRoleCache = &roleCache{}

func (c *roleCache) get(ctx context.Context, db *sql.DB) (map[string]config.RoleConfig, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.at) < 30*time.Second && c.roles != nil {
		return c.roles, c.lastErr
	}
	rs, err := rolesFromDB(ctx, db)
	if err == nil && len(rs) > 0 {
		c.roles, c.at, c.lastErr = rs, time.Now(), nil
		return rs, nil
	}
	c.lastErr = err
	return rs, err
}

// rolesMapFor 是角色真源的唯一解析入口：PG 优先，DB 不可用时回落配置文件。
// 机器人、技能页、工作流等非 AgentHandler 调用方也走这里，不再各读一遍 yaml。
func rolesMapFor(db *database.DB, cfg *config.Config) map[string]config.RoleConfig {
	if db != nil {
		if rs, err := chatRoleCache.get(context.Background(), db.DB); err == nil && len(rs) > 0 {
			return rs
		}
	}
	if cfg != nil {
		return cfg.Roles
	}
	return nil
}

// rolesMap 供对话/批量任务解析角色。
func (h *AgentHandler) rolesMap() map[string]config.RoleConfig {
	return rolesMapFor(h.db, h.config)
}

// resolveRole 按名字取单个角色。
func (h *AgentHandler) resolveRole(name string) (config.RoleConfig, bool) {
	rs := h.rolesMap()
	if rs == nil {
		return config.RoleConfig{}, false
	}
	r, ok := rs[name]
	return r, ok
}

// ----- RoleHandler 侧的读接口（管理写入口已收口到控制台）-----

func (h *RoleHandler) rolesFromDB(ctx context.Context) ([]config.RoleConfig, error) {
	if h.db == nil {
		return nil, nil
	}
	rs, err := chatRoleCache.get(ctx, h.db.DB)
	if err != nil || len(rs) == 0 {
		return nil, err
	}
	out := make([]config.RoleConfig, 0, len(rs))
	for _, r := range rs {
		out = append(out, r)
	}
	return out, nil
}

func (h *RoleHandler) roleFromDB(ctx context.Context, name string) (config.RoleConfig, bool, error) {
	if h.db == nil {
		return config.RoleConfig{}, false, nil
	}
	rs, err := chatRoleCache.get(ctx, h.db.DB)
	if err != nil {
		return config.RoleConfig{}, false, err
	}
	r, ok := rs[name]
	return r, ok, nil
}
