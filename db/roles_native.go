package db

// Step 6d Phase B2：roles 只读原生 store。
//
// 角色统一真源：PG public.agents(kind='role') + agent_prompts(当前版本)。
// MCP 绑定走 agent_visibility(resource_kind='mcp')；findu 运行时工具绑定走
// 统一目录 tools(runtime='findu', agents jsonb 含角色 key)。写入口已收口到
// 控制台 /system/agents（宿主 agents 管理接口），本域只读 + 409。
//
// 与 findu role_db.go 的差异：不回落 YAML（findu 兼容旧部署才回落；宿主侧
// agents 表即真源）；不做 30s 缓存（读路径直查，量小）。

import (
	"sort"
	"strings"
)

// RoleDTO 与 findu config.RoleConfig 的 JSON 形状逐字段一致。
type RoleDTO struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	UserPrompt  string   `json:"user_prompt"`
	Icon        string   `json:"icon,omitempty"`
	Tools       []string `json:"tools,omitempty"`
	MCPs        []string `json:"mcps,omitempty"`
	Enabled     bool     `json:"enabled"`
}

// RoleStore operates on agents(kind='role') 角色真源。
type RoleStore struct {
	db *DB
}

// Roles returns the role store.
func (d *DB) Roles() *RoleStore { return &RoleStore{db: d} }

// listRoleRows 读 agents(kind='role') 基础行（id/key/name/描述/图标/启用/prompt）。
func (s *RoleStore) listRoleRows() ([]struct {
	ID          int64
	Key         string
	Name        string
	Description string
	Icon        string
	Prompt      string
	Enabled     bool
}, error) {
	rows, err := s.db.Query(`
		SELECT a.id, a.key, a.name, COALESCE(a.description, ''), COALESCE(a.icon, ''), a.enabled,
		       COALESCE(p.template_text, '')
		  FROM agents a
		  LEFT JOIN agent_prompts p ON p.id = a.current_prompt_id
		 WHERE a.role = 'assistant'
		 ORDER BY a.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []struct {
		ID          int64
		Key         string
		Name        string
		Description string
		Icon        string
		Prompt      string
		Enabled     bool
	}{}
	for rows.Next() {
		var r struct {
			ID          int64
			Key         string
			Name        string
			Description string
			Icon        string
			Prompt      string
			Enabled     bool
		}
		if err := rows.Scan(&r.ID, &r.Key, &r.Name, &r.Description, &r.Icon, &r.Enabled, &r.Prompt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// visibleMCPNamesNative 读角色绑定的 MCP 服务器名（表缺失静默空）。
func (s *RoleStore) visibleMCPNamesNative(agentID int64) []string {
	rows, err := s.db.Query(`
		SELECT COALESCE(m.name, '')
		  FROM agent_visibility v
		  JOIN mcp_servers m ON m.id = v.resource_id
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

// boundFinduToolsNative 读角色在统一工具目录绑定的 findu 运行时工具
// （agents jsonb 包含角色 key）。用 jsonb_exists 而非 ? 操作符。
func (s *RoleStore) boundFinduToolsNative(agentKey string) []string {
	if agentKey == "" {
		return nil
	}
	rows, err := s.db.Query(`
		SELECT key FROM tools
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

// ListRolesNative 返回全部角色（按 id 升序，与 findu ORDER BY a.id 一致）。
func (s *RoleStore) ListRolesNative() ([]*RoleDTO, error) {
	rows, err := s.listRoleRows()
	if err != nil {
		return nil, err
	}
	out := make([]*RoleDTO, 0, len(rows))
	for _, r := range rows {
		role := &RoleDTO{
			Name:        r.Name,
			Description: r.Description,
			UserPrompt:  r.Prompt,
			Icon:        r.Icon,
			Enabled:     r.Enabled,
		}
		if mcps := s.visibleMCPNamesNative(r.ID); len(mcps) > 0 {
			role.MCPs = mcps
		}
		if tools := s.boundFinduToolsNative(r.Key); len(tools) > 0 {
			role.Tools = tools
		}
		out = append(out, role)
	}
	return out, nil
}

// GetRoleNative 按角色名取单个（name 精确匹配）。
func (s *RoleStore) GetRoleNative(name string) (*RoleDTO, bool, error) {
	name = strings.TrimSpace(name)
	roles, err := s.ListRolesNative()
	if err != nil {
		return nil, false, err
	}
	for _, r := range roles {
		if r.Name == name {
			return r, true, nil
		}
	}
	return nil, false, nil
}

// SortRoleNames 返回排序后的角色名（canary/调试用）。
func SortRoleNames(roles []*RoleDTO) []string {
	names := make([]string, 0, len(roles))
	for _, r := range roles {
		names = append(names, r.Name)
	}
	sort.Strings(names)
	return names
}
