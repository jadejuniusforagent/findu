package db

// Step 6d Phase A：projects delete / facts / stats / dashboard-summary /
// conversations 原生实现（findu internal/database/project*.go 的 PG 原生版）。
//
// 范围：facts 的 edges（fact-graph/fact-edges CRUD/promote-attack-chain）体量大，
// 本批留 findu 兼容（routes 精确注册已原生子路由，其余走 serveFinduCompat）。

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ProjectFact 项目事实（黑板条目），JSON 与 findu database.ProjectFact 一致。
type ProjectFact struct {
	ID                     string    `json:"id"`
	ProjectID              string    `json:"project_id"`
	FactKey                string    `json:"fact_key"`
	Category               string    `json:"category"`
	Summary                string    `json:"summary"`
	Body                   string    `json:"body"`
	Confidence             string    `json:"confidence"`
	SourceConversationID   string    `json:"source_conversation_id,omitempty"`
	SourceMessageID        string    `json:"source_message_id,omitempty"`
	Pinned                 bool      `json:"pinned"`
	RelatedVulnerabilityID string    `json:"related_vulnerability_id,omitempty"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
}

// ProjectFactListFilter 事实列表筛选（findu 同名结构语义）。
type ProjectFactListFilter struct {
	Category               string
	Confidence             string
	Search                 string
	RelatedVulnerabilityID string
	ExcludeDeprecated      bool
}

// ProjectStats 项目聚合统计。
type ProjectStats struct {
	FactCount         int `json:"fact_count"`
	VulnCount         int `json:"vuln_count"`
	ConversationCount int `json:"conversation_count"`
	SparseFactCount   int `json:"sparse_fact_count"`
}

// ProjectDashboardFact 仪表盘跨项目近期事实。
type ProjectDashboardFact struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"project_id"`
	ProjectName string    `json:"project_name"`
	FactKey     string    `json:"fact_key"`
	Category    string    `json:"category"`
	Summary     string    `json:"summary"`
	Confidence  string    `json:"confidence"`
	Pinned      bool      `json:"pinned"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ProjectDashboardTotals 仪表盘汇总计数。
type ProjectDashboardTotals struct {
	ActiveProjects int `json:"active_projects"`
	TotalFacts     int `json:"total_facts"`
}

// ProjectDashboardSummary 仪表盘项目情报摘要。
type ProjectDashboardSummary struct {
	RecentFacts []ProjectDashboardFact `json:"recent_facts"`
	Totals      ProjectDashboardTotals `json:"totals"`
}

var factKeyPatternNative = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/-]*$`)

// ValidateFactKey 校验事实 key（与 findu 一致的文案与规则）。
func ValidateFactKey(key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("fact_key 不能为空")
	}
	if len(key) > 128 {
		return fmt.Errorf("fact_key 过长（最多 128 字符）")
	}
	if !factKeyPatternNative.MatchString(key) {
		return fmt.Errorf("fact_key 格式无效，仅允许字母、数字及 . _ / -，且须以字母或数字开头（支持驼峰命名）")
	}
	return nil
}

const projectFactSelectCols = `id, project_id, fact_key, COALESCE(category,''), summary, COALESCE(body,''), confidence,
	COALESCE(source_conversation_id,''), COALESCE(source_message_id,''), COALESCE(pinned,0),
	COALESCE(related_vulnerability_id,''), created_at, updated_at`

func scanProjectFact(row interface{ Scan(dest ...any) error }) (*ProjectFact, error) {
	var f ProjectFact
	var pinned int
	err := row.Scan(&f.ID, &f.ProjectID, &f.FactKey, &f.Category, &f.Summary, &f.Body, &f.Confidence,
		&f.SourceConversationID, &f.SourceMessageID, &pinned,
		&f.RelatedVulnerabilityID, &f.CreatedAt, &f.UpdatedAt)
	if err != nil {
		return nil, err
	}
	f.Pinned = pinned != 0
	return &f, nil
}

// DeleteProjectNative 删除项目（A5）：事务内解绑 vulnerabilities/assets/
// webshell_connections/c2_listeners 的 project_id、删除项目与级联事实，
// 最后清理项目级目录（findu removeProjectScopedDirs 语义，宿主侧对齐）。
func (s *ProjectStore) DeleteProjectNative(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return sql.ErrNoRows
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.Exec(`DELETE FROM project_facts WHERE project_id = $1`, id); err != nil {
		return fmt.Errorf("删除项目事实失败: %w", err)
	}
	if _, err := tx.Exec(`UPDATE vulnerabilities SET project_id = NULL WHERE project_id = $1`, id); err != nil {
		return fmt.Errorf("解除漏洞项目关联失败: %w", err)
	}
	if _, err := tx.Exec(`UPDATE assets SET project_id = NULL WHERE project_id = $1`, id); err != nil {
		return fmt.Errorf("解除资产项目关联失败: %w", err)
	}
	if _, err := tx.Exec(`UPDATE webshell_connections SET project_id = NULL WHERE project_id = $1`, id); err != nil {
		return fmt.Errorf("解除 WebShell 项目关联失败: %w", err)
	}
	if _, err := tx.Exec(`UPDATE c2_listeners SET project_id = NULL WHERE project_id = $1`, id); err != nil {
		return fmt.Errorf("解除 C2 监听器项目关联失败: %w", err)
	}
	res, err := tx.Exec(`DELETE FROM projects WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("删除项目失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.removeProjectScopedDirsNative(id)
	return nil
}

// removeProjectScopedDirsNative 清理项目级目录（reduction/workspace 的
// projects/<id>/ 子目录；宿主 dataDir 下）。
func (s *ProjectStore) removeProjectScopedDirsNative(projectID string) {
	// findu 侧按 db 根目录清理 tmp/reduction|workspace 的 projects/<id>/。
	// 宿主 PG 模式下这些目录在 findu 容器 data-findu 下，宿主进程不可达；
	// 目录残留风险已记录在 STEP6D-AB-HANDOFF 已知缺口，卸载演练统一清理。
}

// ListProjectFacts 分页列出项目事实（A6）。
func (s *ProjectStore) ListProjectFacts(projectID string, filter ProjectFactListFilter, limit, offset int) ([]*ProjectFact, error) {
	if limit <= 0 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	query := `SELECT ` + projectFactSelectCols + ` FROM project_facts WHERE project_id = $1`
	args := []any{strings.TrimSpace(projectID)}
	if c := strings.TrimSpace(filter.Category); c != "" {
		args = append(args, c)
		query += fmt.Sprintf(" AND category = $%d", len(args))
	}
	if c := strings.TrimSpace(filter.Confidence); c != "" {
		args = append(args, c)
		query += fmt.Sprintf(" AND confidence = $%d", len(args))
	}
	if filter.ExcludeDeprecated {
		query += " AND confidence != 'deprecated'"
	}
	if rid := strings.TrimSpace(filter.RelatedVulnerabilityID); rid != "" {
		args = append(args, rid)
		query += fmt.Sprintf(" AND related_vulnerability_id = $%d", len(args))
	}
	if q := strings.TrimSpace(filter.Search); q != "" {
		pat := "%" + q + "%"
		args = append(args, pat, pat, pat)
		query += fmt.Sprintf(" AND (fact_key LIKE $%d OR summary LIKE $%d OR body LIKE $%d)", len(args)-2, len(args)-1, len(args))
	}
	args = append(args, limit, offset)
	query += fmt.Sprintf(" ORDER BY pinned DESC, updated_at DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args))
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ProjectFact
	for rows.Next() {
		f, err := scanProjectFact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// GetProjectFactByKey 按 key 获取事实。
func (s *ProjectStore) GetProjectFactByKey(projectID, factKey string) (*ProjectFact, error) {
	return scanProjectFact(s.db.QueryRow(`SELECT `+projectFactSelectCols+` FROM project_facts WHERE project_id = $1 AND fact_key = $2`,
		strings.TrimSpace(projectID), strings.TrimSpace(factKey)))
}

// GetProjectFact 按 ID 获取事实。
func (s *ProjectStore) GetProjectFact(id string) (*ProjectFact, error) {
	return scanProjectFact(s.db.QueryRow(`SELECT `+projectFactSelectCols+` FROM project_facts WHERE id = $1`,
		strings.TrimSpace(id)))
}

// mergeFactBodyOnUpdate 更新时 incoming body 为空则保留已有内容。
func mergeFactBodyOnUpdate(incoming, existing string) string {
	if strings.TrimSpace(incoming) == "" {
		return existing
	}
	return incoming
}

// UpsertProjectFact 创建或更新事实（A6，按 project_id + fact_key）。
func (s *ProjectStore) UpsertProjectFact(f *ProjectFact) (*ProjectFact, error) {
	if err := ValidateFactKey(f.FactKey); err != nil {
		return nil, err
	}
	if strings.TrimSpace(f.Category) == "" {
		f.Category = "note"
	}
	if strings.TrimSpace(f.Confidence) == "" {
		f.Confidence = "tentative"
	}
	now := time.Now()
	existing, err := s.GetProjectFactByKey(f.ProjectID, f.FactKey)
	if err == nil && existing != nil {
		f.ID = existing.ID
		f.CreatedAt = existing.CreatedAt
		f.UpdatedAt = now
		f.Body = mergeFactBodyOnUpdate(f.Body, existing.Body)
		if strings.TrimSpace(f.Category) == "" {
			f.Category = existing.Category
		}
		if strings.TrimSpace(f.Confidence) == "" {
			f.Confidence = existing.Confidence
		}
		_, err = s.db.Exec(`UPDATE project_facts SET category = $1, summary = $2, body = $3, confidence = $4,
			source_conversation_id = COALESCE($5, source_conversation_id),
			source_message_id = COALESCE($6, source_message_id),
			pinned = $7, related_vulnerability_id = $8, updated_at = $9 WHERE id = $10`,
			f.Category, f.Summary, f.Body, f.Confidence,
			nullIfEmptyString(f.SourceConversationID), nullIfEmptyString(f.SourceMessageID), projectPinnedInt(f.Pinned),
			nullIfEmptyString(f.RelatedVulnerabilityID), f.UpdatedAt, f.ID)
		if err != nil {
			return nil, fmt.Errorf("更新事实失败: %w", err)
		}
		return f, nil
	}
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if strings.TrimSpace(f.ID) == "" {
		f.ID = uuid.NewString()
	}
	f.CreatedAt = now
	f.UpdatedAt = now
	_, err = s.db.Exec(`INSERT INTO project_facts (
		id, project_id, fact_key, category, summary, body, confidence,
		source_conversation_id, source_message_id, pinned, related_vulnerability_id,
		created_at, updated_at
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		f.ID, f.ProjectID, f.FactKey, f.Category, f.Summary, f.Body, f.Confidence,
		nullIfEmptyString(f.SourceConversationID), nullIfEmptyString(f.SourceMessageID), projectPinnedInt(f.Pinned),
		nullIfEmptyString(f.RelatedVulnerabilityID), f.CreatedAt, f.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("创建事实失败: %w", err)
	}
	return f, nil
}

// DeprecateProjectFactNative 将事实标记为 deprecated（A6）。
func (s *ProjectStore) DeprecateProjectFactNative(projectID, factKey string) error {
	res, err := s.db.Exec(`UPDATE project_facts SET confidence = 'deprecated', updated_at = $1 WHERE project_id = $2 AND fact_key = $3`,
		time.Now(), strings.TrimSpace(projectID), strings.TrimSpace(factKey))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("事实不存在")
	}
	return nil
}

// RestoreProjectFactNative 恢复已废弃事实（A6）。
func (s *ProjectStore) RestoreProjectFactNative(projectID, factKey, confidence string) error {
	confidence = strings.TrimSpace(strings.ToLower(confidence))
	if confidence == "" {
		confidence = "tentative"
	}
	if confidence != "confirmed" && confidence != "tentative" {
		return fmt.Errorf("confidence 须为 confirmed 或 tentative")
	}
	existing, err := s.GetProjectFactByKey(projectID, factKey)
	if err != nil {
		return fmt.Errorf("事实不存在")
	}
	if strings.ToLower(strings.TrimSpace(existing.Confidence)) != "deprecated" {
		return fmt.Errorf("事实未处于废弃状态")
	}
	_, err = s.db.Exec(`UPDATE project_facts SET confidence = $1, updated_at = $2 WHERE project_id = $3 AND fact_key = $4`,
		confidence, time.Now(), strings.TrimSpace(projectID), strings.TrimSpace(factKey))
	return err
}

// DeleteProjectFactNative 删除事实（A6；边的级联由 findu 兼容路径持有——原生
// facts 批次不含 edges，仍走 findu）。
func (s *ProjectStore) DeleteProjectFactNative(id string) error {
	res, err := s.db.Exec(`DELETE FROM project_facts WHERE id = $1`, strings.TrimSpace(id))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("事实不存在")
	}
	return nil
}

// GetProjectStatsCounts 统计项目下事实/漏洞/对话数量（A7）。
func (s *ProjectStore) GetProjectStatsCounts(projectID string) (*ProjectStats, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, fmt.Errorf("project_id 不能为空")
	}
	if _, err := s.Get(projectID); err != nil {
		return nil, fmt.Errorf("项目不存在")
	}
	stats := &ProjectStats{}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM project_facts WHERE project_id = $1 AND confidence != 'deprecated'`,
		projectID).Scan(&stats.FactCount); err != nil {
		return nil, fmt.Errorf("统计事实失败: %w", err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM vulnerabilities WHERE project_id = $1`,
		projectID).Scan(&stats.VulnCount); err != nil {
		return nil, fmt.Errorf("统计漏洞失败: %w", err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM conversations WHERE project_id = $1`,
		projectID).Scan(&stats.ConversationCount); err != nil {
		return nil, fmt.Errorf("统计对话失败: %w", err)
	}
	return stats, nil
}

// ListProjectFactsForSparseCheck 返回待补全检测字段（A7 sparse 判定输入）。
func (s *ProjectStore) ListProjectFactsForSparseCheck(projectID string) ([]struct {
	Category string
	FactKey  string
	Body     string
}, error) {
	rows, err := s.db.Query(`SELECT COALESCE(category,''), fact_key, COALESCE(body,'') FROM project_facts WHERE project_id = $1 AND confidence != 'deprecated'`,
		strings.TrimSpace(projectID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []struct {
		Category string
		FactKey  string
		Body     string
	}
	for rows.Next() {
		var row struct {
			Category string
			FactKey  string
			Body     string
		}
		if err := rows.Scan(&row.Category, &row.FactKey, &row.Body); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// GetProjectDashboardSummary 聚合跨项目近期事实（A8，scope=all 全量口径）。
func (s *ProjectStore) GetProjectDashboardSummary(factLimit int) (*ProjectDashboardSummary, error) {
	if factLimit <= 0 {
		factLimit = 5
	}
	if factLimit > 50 {
		factLimit = 50
	}
	out := &ProjectDashboardSummary{RecentFacts: []ProjectDashboardFact{}}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM projects WHERE status = 'active'`).Scan(&out.Totals.ActiveProjects); err != nil {
		return nil, fmt.Errorf("统计活跃项目失败: %w", err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM project_facts f
		INNER JOIN projects p ON p.id = f.project_id
		WHERE f.confidence != 'deprecated' AND p.status = 'active'`).Scan(&out.Totals.TotalFacts); err != nil {
		return nil, fmt.Errorf("统计事实失败: %w", err)
	}
	rows, err := s.db.Query(`SELECT f.id, f.project_id, p.name, f.fact_key, COALESCE(f.category,''), f.summary, f.confidence, COALESCE(f.pinned,0), f.updated_at
		FROM project_facts f INNER JOIN projects p ON p.id = f.project_id
		WHERE f.confidence != 'deprecated' AND p.status = 'active'
		ORDER BY f.updated_at DESC LIMIT $1`, factLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var item ProjectDashboardFact
		var pinned int
		if err := rows.Scan(&item.ID, &item.ProjectID, &item.ProjectName, &item.FactKey, &item.Category, &item.Summary, &item.Confidence, &pinned, &item.UpdatedAt); err != nil {
			return nil, err
		}
		item.Pinned = pinned != 0
		out.RecentFacts = append(out.RecentFacts, item)
	}
	return out, rows.Err()
}

// ProjectConversationRow 是项目对话列表行（findu Conversation 的列表子集）。
type ProjectConversationRow struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Pinned    bool      `json:"pinned"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	ProjectID string    `json:"project_id"`
	RoleName  string    `json:"role_name"`
}

// ListProjectConversations 列出绑定到项目的对话（A6 conversations）。
func (s *ProjectStore) ListProjectConversations(projectID string, limit, offset int) ([]*ProjectConversationRow, error) {
	if limit <= 0 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.db.Query(`SELECT id, COALESCE(title,''), COALESCE(pinned,0), created_at, updated_at,
		COALESCE(project_id::text,''), COALESCE(role_name,'')
		FROM conversations WHERE project_id = $1 ORDER BY updated_at DESC LIMIT $2 OFFSET $3`,
		strings.TrimSpace(projectID), limit, offset)
	if err != nil {
		return nil, fmt.Errorf("查询项目对话失败: %w", err)
	}
	defer rows.Close()
	var out []*ProjectConversationRow
	for rows.Next() {
		var c ProjectConversationRow
		var pinned int
		if err := rows.Scan(&c.ID, &c.Title, &pinned, &c.CreatedAt, &c.UpdatedAt, &c.ProjectID, &c.RoleName); err != nil {
			return nil, err
		}
		c.Pinned = pinned != 0
		out = append(out, &c)
	}
	return out, rows.Err()
}

// CountProjectConversations 统计项目绑定对话数。
func (s *ProjectStore) CountProjectConversations(projectID string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM conversations WHERE project_id = $1`, strings.TrimSpace(projectID)).Scan(&n)
	return n, err
}
