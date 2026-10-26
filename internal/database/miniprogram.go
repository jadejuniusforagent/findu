package database

import "fmt"

// initMiniprogramAnalysisTables 创建小程序分析相关表：
// miniprogram_samples（样本库）+ miniprogram_findings（敏感信息扫描发现）
// + miniprogram_scan_tasks（分析记录台账）。
// miniprogram_samples 外键引用 projects，必须在 projects 之后建。
func (db *DB) initMiniprogramAnalysisTables() error {
	createMiniprogramSamplesTable := `
CREATE TABLE IF NOT EXISTS miniprogram_samples (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    sha256 TEXT NOT NULL UNIQUE,
    size BIGINT NOT NULL DEFAULT 0,
    workspace_path TEXT NOT NULL DEFAULT '',
    appid TEXT,
    file_count INT NOT NULL DEFAULT 0,
    project_id TEXT,
    owner_user_id TEXT,
    source TEXT NOT NULL DEFAULT 'gui_upload',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE SET NULL
);`

	createMiniprogramFindingsTable := `
CREATE TABLE IF NOT EXISTS miniprogram_findings (
    id TEXT PRIMARY KEY,
    sample_id TEXT NOT NULL,
    category TEXT NOT NULL,
    severity TEXT NOT NULL DEFAULT 'medium',
    file_path TEXT NOT NULL DEFAULT '',
    line_number INT NOT NULL DEFAULT 0,
    content TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (sample_id) REFERENCES miniprogram_samples(id) ON DELETE CASCADE
);`

	// 分析记录台账：列结构与 tasks.go 的 INSERT/SELECT 严格对齐
	//（upload/scan/appid_fill/findrefs 每次落一条，GUI 轮询 + Agent 工具同账）。
	// error 允许 NULL：tasks.go 用 NULLIF($8,'') 把空错误转 NULL（对齐 apk_scan_tasks）。
	createMiniprogramScanTasksTable := `
CREATE TABLE IF NOT EXISTS miniprogram_scan_tasks (
    id TEXT PRIMARY KEY,
    sample_id TEXT NOT NULL DEFAULT '',
    project_id TEXT,
    action TEXT NOT NULL,
    query_json TEXT,
    status TEXT NOT NULL DEFAULT 'running',
    result_json TEXT,
    error TEXT,
    duration_ms BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);`

	// 测试项账本：扫描发现 → Agent 委派验证 → 结论回流（对齐 apk_test_items）。
	createMiniprogramTestItemsTable := `
CREATE TABLE IF NOT EXISTS miniprogram_test_items (
    id TEXT PRIMARY KEY,
    sample_id TEXT NOT NULL,
    source TEXT NOT NULL DEFAULT '',
    source_type TEXT NOT NULL DEFAULT 'finding',
    category TEXT NOT NULL DEFAULT '',
    severity TEXT NOT NULL DEFAULT 'medium',
    title TEXT NOT NULL,
    target TEXT NOT NULL DEFAULT '',
    instruction TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    conclusion TEXT NOT NULL DEFAULT '',
    evidence TEXT NOT NULL DEFAULT '',
    conversation_id TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (sample_id) REFERENCES miniprogram_samples(id) ON DELETE CASCADE
);`

	if _, err := db.Exec(createMiniprogramSamplesTable); err != nil {
		return fmt.Errorf("创建miniprogram_samples表失败: %w", err)
	}
	if _, err := db.Exec(createMiniprogramFindingsTable); err != nil {
		return fmt.Errorf("创建miniprogram_findings表失败: %w", err)
	}
	if _, err := db.Exec(createMiniprogramScanTasksTable); err != nil {
		return fmt.Errorf("创建miniprogram_scan_tasks表失败: %w", err)
	}
	if _, err := db.Exec(createMiniprogramTestItemsTable); err != nil {
		return fmt.Errorf("创建miniprogram_test_items表失败: %w", err)
	}

	// 索引
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_miniprogram_samples_project ON miniprogram_samples(project_id)`); err != nil {
		return fmt.Errorf("创建miniprogram_samples索引失败: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_miniprogram_samples_sha256 ON miniprogram_samples(sha256)`); err != nil {
		return fmt.Errorf("创建miniprogram_samples_sha256索引失败: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_miniprogram_findings_sample ON miniprogram_findings(sample_id)`); err != nil {
		return fmt.Errorf("创建miniprogram_findings索引失败: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_miniprogram_findings_category ON miniprogram_findings(category)`); err != nil {
		return fmt.Errorf("创建miniprogram_findings_category索引失败: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_miniprogram_scan_tasks_sample ON miniprogram_scan_tasks(sample_id)`); err != nil {
		return fmt.Errorf("创建miniprogram_scan_tasks索引失败: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_miniprogram_scan_tasks_created ON miniprogram_scan_tasks(created_at)`); err != nil {
		return fmt.Errorf("创建miniprogram_scan_tasks索引失败: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_miniprogram_test_items_sample ON miniprogram_test_items(sample_id)`); err != nil {
		return fmt.Errorf("创建miniprogram_test_items索引失败: %w", err)
	}

	return nil
}
