package db

import "testing"

// ensureFinduCompatFixture 把共享测试库对齐到生产「统一 assets 表 + findu 侧表」
// 的形态。宿主 schema.sql 建的 assets 带六类资产严格约束（type NOT NULL +
// type/port/service_type CHECK），而生产统一表由 findu DDL 先建、后续宿主列经
// ALTER 合并，无这些约束且 type 可空（findu 导入路径不写 type）。Phase A 的
// findu 兼容面（selection/import/merge、facts、stats、dashboard）还会触碰
// project_facts / batch_tasks / c2_listeners / rbac_* 等 findu 侧表——
// schema.sql 不建它们（见 webshells_test.go 同款先例）。
//
// 全部语句幂等；仅会在 _test 库执行（TestMain 强制库名带 _test），生产库
// 同名对象已由 findu DDL 建立、语句均为 no-op，且本文件不参与生产构建。
func ensureFinduCompatFixture(t *testing.T, d *DB) {
	t.Helper()
	_, err := d.Exec(`
ALTER TABLE assets ALTER COLUMN type DROP NOT NULL;
ALTER TABLE assets DROP CONSTRAINT IF EXISTS assets_type_check;
ALTER TABLE assets DROP CONSTRAINT IF EXISTS assets_port_check;
ALTER TABLE assets DROP CONSTRAINT IF EXISTS assets_service_type_check;
ALTER TABLE assets ADD COLUMN IF NOT EXISTS dedup_key TEXT;
ALTER TABLE assets ADD COLUMN IF NOT EXISTS project_id TEXT;
ALTER TABLE assets ADD COLUMN IF NOT EXISTS host TEXT;
ALTER TABLE assets ADD COLUMN IF NOT EXISTS protocol TEXT;
ALTER TABLE assets ADD COLUMN IF NOT EXISTS title TEXT;
ALTER TABLE assets ADD COLUMN IF NOT EXISTS server TEXT;
ALTER TABLE assets ADD COLUMN IF NOT EXISTS country TEXT;
ALTER TABLE assets ADD COLUMN IF NOT EXISTS province TEXT;
ALTER TABLE assets ADD COLUMN IF NOT EXISTS city TEXT;
ALTER TABLE assets ADD COLUMN IF NOT EXISTS responsible_person TEXT;
ALTER TABLE assets ADD COLUMN IF NOT EXISTS department TEXT;
ALTER TABLE assets ADD COLUMN IF NOT EXISTS business_system TEXT;
ALTER TABLE assets ADD COLUMN IF NOT EXISTS environment TEXT;
ALTER TABLE assets ADD COLUMN IF NOT EXISTS criticality TEXT;
ALTER TABLE assets ADD COLUMN IF NOT EXISTS source TEXT;
ALTER TABLE assets ADD COLUMN IF NOT EXISTS source_query TEXT;
ALTER TABLE assets ADD COLUMN IF NOT EXISTS status TEXT;
ALTER TABLE assets ADD COLUMN IF NOT EXISTS vulnerability_count INTEGER;
ALTER TABLE assets ADD COLUMN IF NOT EXISTS risk_score INTEGER;
ALTER TABLE assets ADD COLUMN IF NOT EXISTS risk_level TEXT;
ALTER TABLE assets ADD COLUMN IF NOT EXISTS tags_json TEXT;
ALTER TABLE assets ADD COLUMN IF NOT EXISTS first_seen_at TIMESTAMPTZ;
ALTER TABLE assets ADD COLUMN IF NOT EXISTS last_seen_at TIMESTAMPTZ;
ALTER TABLE assets ADD COLUMN IF NOT EXISTS owner_user_id TEXT;
ALTER TABLE assets ADD COLUMN IF NOT EXISTS last_scan_at TIMESTAMPTZ;
ALTER TABLE assets ADD COLUMN IF NOT EXISTS last_scan_conversation_id TEXT;
ALTER TABLE assets ADD COLUMN IF NOT EXISTS last_scan_queue_id TEXT;
ALTER TABLE assets ADD COLUMN IF NOT EXISTS last_scan_task_id TEXT;
CREATE INDEX IF NOT EXISTS idx_assets_dedup_key ON assets(dedup_key) WHERE dedup_key IS NOT NULL;
CREATE TABLE IF NOT EXISTS project_facts (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    fact_key TEXT NOT NULL,
    category TEXT NOT NULL DEFAULT 'note',
    summary TEXT NOT NULL DEFAULT '',
    body TEXT,
    confidence TEXT NOT NULL DEFAULT 'tentative',
    source_conversation_id TEXT,
    source_message_id TEXT,
    pinned INTEGER NOT NULL DEFAULT 0,
    related_vulnerability_id TEXT,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE(project_id, fact_key)
);
CREATE INDEX IF NOT EXISTS idx_project_facts_project ON project_facts(project_id);
CREATE TABLE IF NOT EXISTS batch_tasks (
    id TEXT PRIMARY KEY,
    queue_id TEXT,
    message TEXT,
    conversation_id TEXT,
    status TEXT,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    error TEXT,
    result TEXT
);
CREATE TABLE IF NOT EXISTS c2_listeners (
    id TEXT PRIMARY KEY,
    project_id TEXT,
    name TEXT,
    type TEXT,
    bind_host TEXT,
    bind_port INTEGER,
    profile_id TEXT,
    encryption_key TEXT,
    implant_token TEXT,
    status TEXT,
    config_json TEXT,
    remark TEXT,
    owner_user_id TEXT,
    created_at TIMESTAMPTZ,
    started_at TIMESTAMPTZ,
    last_error TEXT
);
CREATE TABLE IF NOT EXISTS rbac_users (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL,
    display_name TEXT,
    password_hash TEXT,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    is_builtin BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS rbac_resource_assignments (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);
CREATE TABLE IF NOT EXISTS hitl_interrupts (
    id TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL,
    message_id TEXT,
    mode TEXT NOT NULL,
    tool_name TEXT NOT NULL,
    tool_call_id TEXT,
    payload TEXT,
    status TEXT NOT NULL,
    reviewer TEXT NOT NULL DEFAULT 'human',
    decision TEXT,
    decision_comment TEXT,
    decided_by TEXT NOT NULL DEFAULT 'human',
    created_at TIMESTAMPTZ NOT NULL,
    decided_at TIMESTAMPTZ
);
CREATE TABLE IF NOT EXISTS hitl_conversation_configs (
    conversation_id TEXT PRIMARY KEY,
    enabled INTEGER NOT NULL DEFAULT 0,
    mode TEXT NOT NULL DEFAULT 'off',
    sensitive_tools TEXT NOT NULL DEFAULT '[]',
    timeout_seconds INTEGER NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL,
    reviewer TEXT NOT NULL DEFAULT 'human'
);
CREATE TABLE IF NOT EXISTS chat_upload_artifacts (
    relative_path TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL,
    owner_user_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_chat_upload_artifacts_conversation ON chat_upload_artifacts(conversation_id);
CREATE INDEX IF NOT EXISTS idx_chat_upload_artifacts_owner ON chat_upload_artifacts(owner_user_id);
CREATE TABLE IF NOT EXISTS workflow_definitions (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT,
    version INTEGER NOT NULL DEFAULT 1,
    graph_json TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
CREATE TABLE IF NOT EXISTS workflow_runs (
    id TEXT PRIMARY KEY,
    workflow_id TEXT NOT NULL,
    workflow_version INTEGER NOT NULL DEFAULT 1,
    conversation_id TEXT,
    project_id TEXT,
    role_id TEXT,
    status TEXT NOT NULL,
    input_json TEXT,
    output_json TEXT,
    error TEXT,
    pending_hitl_node_id TEXT,
    pending_hitl_json TEXT,
    started_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS workflow_node_runs (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES workflow_runs(id) ON DELETE CASCADE,
    node_id TEXT NOT NULL,
    status TEXT NOT NULL,
    input_json TEXT,
    output_json TEXT,
    error TEXT,
    started_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
`)
	if err != nil {
		t.Fatalf("findu compat fixture: %v", err)
	}
}
