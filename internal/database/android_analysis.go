package database

import "fmt"

// initAndroidAnalysisTables — 安卓分析(ASC Go 引擎)的样本库与扫描任务表。
// project_id 沿用库内惯例:可空 + ON DELETE SET NULL。
func (db *DB) initAndroidAnalysisTables() error {
	createApkSamplesTable := `
	CREATE TABLE IF NOT EXISTS apk_samples (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		sha256 TEXT NOT NULL UNIQUE,
		size BIGINT NOT NULL DEFAULT 0,
		workspace_path TEXT NOT NULL DEFAULT '',
		package_name TEXT,
		version_name TEXT,
		dex_count INT NOT NULL DEFAULT 0,
		class_count INT NOT NULL DEFAULT 0,
		project_id TEXT,
		source TEXT NOT NULL DEFAULT 'gui_upload',
		created_at TIMESTAMPTZ NOT NULL,
		FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE SET NULL
	);`

	createApkScanTasksTable := `
	CREATE TABLE IF NOT EXISTS apk_scan_tasks (
		id TEXT PRIMARY KEY,
		sample_id TEXT NOT NULL,
		project_id TEXT,
		action TEXT NOT NULL,
		query_json TEXT,
		status TEXT NOT NULL,
		result_json TEXT,
		error TEXT,
		duration_ms INT NOT NULL DEFAULT 0,
		created_at TIMESTAMPTZ NOT NULL,
		FOREIGN KEY (sample_id) REFERENCES apk_samples(id) ON DELETE CASCADE
	);`

	if _, err := db.Exec(createApkSamplesTable); err != nil {
		return fmt.Errorf("创建apk_samples表失败: %w", err)
	}
	if _, err := db.Exec(createApkScanTasksTable); err != nil {
		return fmt.Errorf("创建apk_scan_tasks表失败: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_apk_samples_project ON apk_samples(project_id)`); err != nil {
		return fmt.Errorf("创建apk_samples索引失败: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_apk_scan_tasks_sample ON apk_scan_tasks(sample_id)`); err != nil {
		return fmt.Errorf("创建apk_scan_tasks索引失败: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_apk_scan_tasks_created ON apk_scan_tasks(created_at)`); err != nil {
		return fmt.Errorf("创建apk_scan_tasks时间索引失败: %w", err)
	}
	// 增量列:自动分析分阶段进度(JSON: stage/index/total),老库无此列时补齐
	if !db.columnExists("apk_scan_tasks", "progress") {
		if _, err := db.Exec(`ALTER TABLE apk_scan_tasks ADD COLUMN progress TEXT`); err != nil {
			return fmt.Errorf("apk_scan_tasks 增加 progress 列失败: %w", err)
		}
	}

	createApkTestItemsTable := `
	CREATE TABLE IF NOT EXISTS apk_test_items (
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
		created_at TIMESTAMPTZ NOT NULL,
		updated_at TIMESTAMPTZ NOT NULL,
		FOREIGN KEY (sample_id) REFERENCES apk_samples(id) ON DELETE CASCADE
	);`
	if _, err := db.Exec(createApkTestItemsTable); err != nil {
		return fmt.Errorf("创建apk_test_items表失败: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_apk_test_items_sample ON apk_test_items(sample_id)`); err != nil {
		return fmt.Errorf("创建apk_test_items索引失败: %w", err)
	}

	// 反编译后端注册表(jadx jar / 本地路径 / DAD 等)。kind 唯一:
	// 同 kind 重复注册走 UPSERT(更新路径与验证状态),而非堆多行。
	createApkBackendsTable := `
	CREATE TABLE IF NOT EXISTS apk_backends (
		id TEXT PRIMARY KEY,
		kind TEXT NOT NULL UNIQUE,
		name TEXT NOT NULL DEFAULT '',
		path TEXT NOT NULL DEFAULT '',
		java_path TEXT NOT NULL DEFAULT '',
		workers INT NOT NULL DEFAULT 2,
		verified_at TIMESTAMPTZ,
		smoke_ok BOOLEAN NOT NULL DEFAULT false,
		preferred BOOLEAN NOT NULL DEFAULT false,
		enabled BOOLEAN NOT NULL DEFAULT true,
		created_at TIMESTAMPTZ NOT NULL,
		updated_at TIMESTAMPTZ NOT NULL
	);`
	if _, err := db.Exec(createApkBackendsTable); err != nil {
		return fmt.Errorf("创建apk_backends表失败: %w", err)
	}
	return nil
}

// columnExists information_schema 探测列是否存在(Postgres 无 ADD COLUMN IF NOT EXISTS)。
func (db *DB) columnExists(table, column string) bool {
	var n int
	err := db.QueryRow(`SELECT count(*) FROM information_schema.columns
		WHERE table_name = $1 AND column_name = $2`, table, column).Scan(&n)
	return err == nil && n > 0
}
