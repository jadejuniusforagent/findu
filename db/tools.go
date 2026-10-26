package db

import (
	"database/sql"
	"encoding/json"
)

// Tool is one row of the built-in tool catalog. key + handler live in code; this
// row carries only the page-editable surface: description, parameter schema
// (structure read-only, per-param description/default editable), agent binding,
// and the on/off switch. See schema.sql §H and agent/toolcatalog.go.
type Tool struct {
	Key         string          `json:"key"`
	System      bool            `json:"system"`
	Description string          `json:"description"`
	DisplayName string          `json:"display_name,omitempty"` // HTTP 层装饰的中文名,不落库
	Schema      json.RawMessage `json:"schema"`
	Agents      []string        `json:"agents"`
	Enabled     bool            `json:"enabled"`
	Kind        string          `json:"kind"`     // builtin | command | script | http
	Runtime     string          `json:"runtime"`  // findu | findu —— 统一工具目录里区分所属运行时
	Exec        json.RawMessage `json:"exec"`     // 自定义工具执行规格(kind!=builtin)
	Deferred    bool            `json:"deferred"` // schema 延迟(走 SearchExtraTools/ExecuteExtraTool)
	Calls       int             `json:"calls"`    // runtime ledger aggregate; not stored in tools
}

const toolCols = `key, system, description, schema, agents, enabled, kind, exec, deferred, runtime`

// SeedTool inserts a built-in tool's code-defined defaults ONCE. ON CONFLICT DO
// NOTHING: an existing row (possibly edited in the UI) is never overwritten on
// startup — that's what keeps page edits from being wiped every restart. Use
// UpsertToolForce for an explicit "reset to code default".
func (d *DB) SeedTool(key, desc string, schema, agents json.RawMessage) error {
	if len(schema) == 0 {
		schema = json.RawMessage("{}")
	}
	if len(agents) == 0 {
		agents = json.RawMessage("[]")
	}
	_, err := d.Exec(`
INSERT INTO tools(key, system, description, schema, agents, enabled)
VALUES ($1, true, $2, $3, $4, true)
ON CONFLICT (key) DO NOTHING`, key, desc, schema, agents)
	return err
}

// SeedFinduTool inserts a findu-runtime tool's code-defined defaults ONCE with
// runtime='findu'. SeedTool defaults the runtime column to 'findu'; role tool
// bindings are filtered by runtime='findu' (see boundFinduToolsNative in
// db/roles_native.go), so findu-subsystem MCP tools (android_*/miniprogram_*)
// must go through this entry to become bindable per-role. Same ON CONFLICT DO
// NOTHING semantics as SeedTool: UI edits survive restarts.
func (d *DB) SeedFinduTool(key, desc string, schema, agents json.RawMessage) error {
	if len(schema) == 0 {
		schema = json.RawMessage("{}")
	}
	if len(agents) == 0 {
		agents = json.RawMessage("[]")
	}
	_, err := d.Exec(`
INSERT INTO tools(key, system, description, schema, agents, enabled, runtime)
VALUES ($1, true, $2, $3, $4, true, 'findu')
ON CONFLICT (key) DO NOTHING`, key, desc, schema, agents)
	return err
}

// SeedRecipeTool inserts a CLI recipe tool's defaults ONCE with runtime='recipe',
// deferred=true (schema withheld via SearchExtraTools/ExecuteExtraTool — see
// finduapp/recipes.go) and agents='[]'. The empty agents array is the recipe
// convention for "visible to every agent" (ToolResolve/orchestration treat an
// empty recipe binding as all-agents, preserving pre-seed behaviour); a non-empty
// array narrows the deferred disclosure to the bound agents only.
func (d *DB) SeedRecipeTool(key, desc string, schema json.RawMessage) error {
	if len(schema) == 0 {
		schema = json.RawMessage("{}")
	}
	_, err := d.Exec(`
INSERT INTO tools(key, system, description, schema, agents, enabled, kind, deferred, runtime)
VALUES ($1, true, $2, $3, '[]'::jsonb, true, 'builtin', true, 'recipe')
ON CONFLICT (key) DO NOTHING`, key, desc, schema)
	return err
}

// AddAgentToToolBinding adds an agent key to the given tools' `agents` arrays if
// not already present (idempotent). Used to give the built-in Auto agent its
// default toolset on existing DBs.
func (d *DB) AddAgentToToolBinding(agentKey string, keys []string) error {
	for _, k := range keys {
		if _, err := d.Exec(`UPDATE tools SET agents = agents || to_jsonb($1::text) WHERE key=$2 AND NOT (agents ? $1)`, agentKey, k); err != nil {
			return err
		}
	}
	return nil
}

// RemoveAgentFromToolBindings strips an agent key from every tool's `agents`
// JSONB array — called when a custom agent is deleted so no tool keeps a dangling
// binding. Uses jsonb `-` (remove array element) guarded by `?` (membership).
func (d *DB) RemoveAgentFromToolBindings(agentKey string) error {
	_, err := d.Exec(`UPDATE tools SET agents = agents - $1 WHERE agents ? $1`, agentKey)
	return err
}

// RemoveAgentFromTool strips one agent key from a SINGLE tool's `agents` array —
// used by one-time migrations that change a tool's default binding on existing DBs
// (SeedTool is first-insert-only, so a changed default never reaches a seeded row).
func (d *DB) RemoveAgentFromTool(agentKey, toolKey string) error {
	_, err := d.Exec(`UPDATE tools SET agents = agents - $1 WHERE key=$2 AND agents ? $1`, agentKey, toolKey)
	return err
}

// UpsertToolForce overwrites a tool row with the given code-default values (used by
// the per-tool "reset" action). It resets description/schema/agents and re-enables
// the tool, but keeps system=true.
func (d *DB) UpsertToolForce(key, desc string, schema, agents json.RawMessage) error {
	if len(schema) == 0 {
		schema = json.RawMessage("{}")
	}
	if len(agents) == 0 {
		agents = json.RawMessage("[]")
	}
	_, err := d.Exec(`
INSERT INTO tools(key, system, description, schema, agents, enabled)
VALUES ($1, true, $2, $3, $4, true)
ON CONFLICT (key) DO UPDATE
  SET description = EXCLUDED.description,
      schema      = EXCLUDED.schema,
      agents      = EXCLUDED.agents,
      enabled     = true`, key, desc, schema, agents)
	return err
}

// RefreshToolDefaults updates a system tool's model-facing description + schema to the
// code defaults, PRESERVING the user's agent binding + enabled flag. Used by one-time
// migrations to propagate a code schema change (SeedTool is first-insert-only, so a new
// parameter added in code otherwise never reaches an already-seeded row). No-op for
// custom tools or unknown keys.
func (d *DB) RefreshToolDefaults(key, desc string, schema json.RawMessage) error {
	if len(schema) == 0 {
		schema = json.RawMessage("{}")
	}
	_, err := d.Exec(`UPDATE tools SET description=$2, schema=$3, updated_at=now() WHERE key=$1 AND system`, key, desc, schema)
	return err
}

func scanTool(rows interface{ Scan(...any) error }) (*Tool, error) {
	var t Tool
	var agents []byte
	// 注意：toolCols 自 p6.2 起多了 runtime 一列（统一工具目录区分 findu/findu），
	// 这里必须同步追加 &t.Runtime，否则 Scan 报 "expected 10 destination arguments in Scan, not 9"，
	// 整个 /api/tools 500 → 工具页空白、对话里的工具列表也拿不到。
	if err := rows.Scan(&t.Key, &t.System, &t.Description, &t.Schema, &agents, &t.Enabled, &t.Kind, &t.Exec, &t.Deferred, &t.Runtime); err != nil {
		return nil, err
	}
	if len(agents) > 0 {
		_ = json.Unmarshal(agents, &t.Agents)
	}
	if t.Agents == nil {
		t.Agents = []string{}
	}
	return &t, nil
}

// ListTools returns the whole tool catalog, ordered by key.
func (d *DB) ListTools() ([]*Tool, error) {
	rows, err := d.Query(`SELECT ` + toolCols + ` FROM tools ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Tool
	for rows.Next() {
		t, err := scanTool(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetTool fetches one tool row (nil if absent).
func (d *DB) GetTool(key string) (*Tool, error) {
	row := d.QueryRow(`SELECT `+toolCols+` FROM tools WHERE key=$1`, key)
	t, err := scanTool(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return t, err
}

// CreateCustomTool inserts a user-defined tool (system=false) with an execution
// spec. Fails if the key already exists.
func (d *DB) CreateCustomTool(t *Tool) error {
	schema := t.Schema
	if len(schema) == 0 {
		schema = json.RawMessage("{}")
	}
	exec := t.Exec
	if len(exec) == 0 {
		exec = json.RawMessage("{}")
	}
	agents, _ := json.Marshal(t.Agents)
	if len(agents) == 0 {
		agents = json.RawMessage("[]")
	}
	_, err := d.Exec(`
INSERT INTO tools(key, system, description, schema, agents, enabled, kind, exec, deferred)
VALUES ($1, false, $2, $3, $4, $5, $6, $7, $8)`,
		t.Key, t.Description, schema, agents, t.Enabled, t.Kind, exec, t.Deferred)
	return err
}

// UpdateCustomTool updates a custom tool's editable fields (kind/exec/deferred +
// desc/schema/agents/enabled). Only touches system=false rows.
func (d *DB) UpdateCustomTool(t *Tool) error {
	schema := t.Schema
	if len(schema) == 0 {
		schema = json.RawMessage("{}")
	}
	exec := t.Exec
	if len(exec) == 0 {
		exec = json.RawMessage("{}")
	}
	agents, _ := json.Marshal(t.Agents)
	if len(agents) == 0 {
		agents = json.RawMessage("[]")
	}
	_, err := d.Exec(`
UPDATE tools SET description=$2, schema=$3, agents=$4, enabled=$5, kind=$6, exec=$7, deferred=$8
WHERE key=$1 AND system=false`,
		t.Key, t.Description, schema, agents, t.Enabled, t.Kind, exec, t.Deferred)
	return err
}

// DeleteCustomTool removes a custom tool (system=false only; built-ins protected).
func (d *DB) DeleteCustomTool(key string) error {
	_, err := d.Exec(`DELETE FROM tools WHERE key=$1 AND system=false`, key)
	return err
}

// ListCustomTools returns only the user-defined (system=false) tools.
func (d *DB) ListCustomTools() ([]*Tool, error) {
	rows, err := d.Query(`SELECT ` + toolCols + ` FROM tools WHERE system=false ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Tool
	for rows.Next() {
		t, err := scanTool(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// UpdateTool saves the page-editable fields. key is never changed (it is welded to
// the Go handler). system tools: the caller must keep the schema structure — only
// per-param description/default and the agent binding are meant to move.
// deferred moves with the row for recipe/custom rows (schema withheld); built-in
// findu rows ignore it at injection time, so storing it is still harmless.
func (d *DB) UpdateTool(key, desc string, schema, agents json.RawMessage, enabled, deferred bool) error {
	if len(schema) == 0 {
		schema = json.RawMessage("{}")
	}
	if len(agents) == 0 {
		agents = json.RawMessage("[]")
	}
	_, err := d.Exec(`
UPDATE tools SET description=$2, schema=$3, agents=$4, enabled=$5, deferred=$6 WHERE key=$1`,
		key, desc, schema, agents, enabled, deferred)
	return err
}

// =====================================================================
// 系统工具一次性升级（原 server/finding_workflow.go、finding_traffic.go 直连 SQL）
//
// 这些 UPDATE 的共同语义：只替换「仍是代码默认值」的行，用户改过的描述/绑定
// 必须存活（因此都带旧默认值匹配条件）；changed=false 表示并发下的用户编辑获胜。
// 批 4 第一刀把 SQL 从 server 收到工具注册表属主（db），调用点只做标志位判断。
// =====================================================================

// UpdateSystemToolDescriptionIfUnchanged replaces a built-in tool's description
// only while it still carries the legacy code default.
func (d *DB) UpdateSystemToolDescriptionIfUnchanged(key, description, legacy string) (bool, error) {
	res, err := d.Exec(`UPDATE tools SET description=$2,updated_at=now() WHERE key=$1 AND system AND description=$3`,
		key, description, legacy)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// UpdateSystemToolSchemaIfUnchanged replaces a built-in tool's schema only while
// it still equals the code default (oldSchema). changed=false means a concurrent
// user edit won and the caller must preserve it.
func (d *DB) UpdateSystemToolSchemaIfUnchanged(key string, oldSchema, newSchema json.RawMessage) (bool, error) {
	res, err := d.Exec(`UPDATE tools SET schema=$2::jsonb,updated_at=now() WHERE key=$1 AND system AND schema=$3::jsonb`,
		key, newSchema, oldSchema)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// UpdateTrafficToolAgentsIfDefault upgrades one traffic tool's agent binding only
// when it still carries a known legacy default (["worker"] or the five-reader
// list). Customized binding lists survive.
func (d *DB) UpdateTrafficToolAgentsIfDefault(key string, newBinding json.RawMessage) error {
	_, err := d.Exec(`UPDATE tools SET agents=$2::jsonb WHERE key=$1 AND system AND (agents='["worker"]'::jsonb OR (agents @> '["worker","planner","mainagent","auto","pentest"]'::jsonb AND jsonb_array_length(agents)=5))`,
		key, newBinding)
	return err
}

// UpdateFindingTrafficToolAgentsIfDefault upgrades get_finding_traffic's binding
// only when it still equals the legacy ["auto","reporter"] default.
func (d *DB) UpdateFindingTrafficToolAgentsIfDefault(newBinding json.RawMessage) error {
	_, err := d.Exec(`UPDATE tools SET agents=$1::jsonb WHERE key='get_finding_traffic' AND system AND agents @> '["auto","reporter"]'::jsonb AND jsonb_array_length(agents)=2`,
		newBinding)
	return err
}

// UpdateBindFindingTrafficToolAgentsIfDefault upgrades bind_finding_traffic's
// binding only when it still carries the five-reader legacy default.
func (d *DB) UpdateBindFindingTrafficToolAgentsIfDefault() error {
	_, err := d.Exec(`UPDATE tools SET agents='["reporter"]'::jsonb WHERE key='bind_finding_traffic' AND system AND agents @> '["worker","planner","mainagent","auto","pentest"]'::jsonb AND jsonb_array_length(agents)=5`)
	return err
}

// EnsureSystemToolSchemaProperty adds a missing property to a built-in tool's
// schema (jsonb_set at properties.<propName>) without overwriting an existing
// property. Returns changed=false when the property was already present.
func (d *DB) EnsureSystemToolSchemaProperty(key, propName string, propValue json.RawMessage) (bool, error) {
	res, err := d.Exec(`UPDATE tools SET schema=jsonb_set(schema,ARRAY['properties',$2::text],$3::jsonb,true),updated_at=now()
WHERE key=$1 AND system AND NOT(COALESCE(schema->'properties','{}'::jsonb) ? $2)`,
		key, propName, propValue)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}
