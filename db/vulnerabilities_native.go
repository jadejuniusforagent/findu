package db

// Step 6d Phase B1：vulnerabilities 九条路由的原生 store。
//
// 数据面：共享 PG public schema 的 vulnerabilities 表（findu 漏洞库与宿主
// finding 证据链同表）。行为与 findu internal/database/vulnerability.go 的
// PG 等价实现逐字段对齐：
//   - list/get 派生 task_id/task_queue_id（batch_tasks 按 conversation 关联）
//   - create 时 conversation → project 自动回填（未显式指定 project_id 时）
//   - delete/batch-delete 先清 project_facts.related_vulnerability_id 再删
//   - 写路径后 best-effort 刷新关联资产的 risk cache
//
// 范围取舍（见 docs/refactor/STEP6D-AB-HANDOFF.md）：
//   - RBAC 细粒度访问过滤（owner/assignment 传播）暂不实现：宿主 Bearer +
//     scope=all 管理员行为与 findu 兼容路径一致；细粒度留 RBAC 域迁移。
//   - 通知 hook（NotifyVulnerabilityCreated → 告警投递）暂不接：属 findu
//     通知管道装配，后续 notify 域批次再接。
//   - filter-options 的任务/队列建议 PG 版用 bt.created_at 排序（findu
//     SQLite 版的 bt.rowid 在 PG 无对应）。

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// VulnerabilityStore operates on the shared-PG vulnerabilities table.
type VulnerabilityStore struct {
	db     *DB
	assets *AssetStore
}

// Vulnerabilities returns the vulnerability store.
func (d *DB) Vulnerabilities() *VulnerabilityStore {
	return &VulnerabilityStore{db: d, assets: d.Assets()}
}

// VulnerabilityDTO 是 findu database.Vulnerability 的宿主原生投影；JSON
// 字段名与 findu 逐字段一致（旧 UI 消费同一形状）。
type VulnerabilityDTO struct {
	ID              string    `json:"id"`
	ConversationID  string    `json:"conversation_id"`
	ProjectID       string    `json:"project_id,omitempty"`
	ConversationTag string    `json:"conversation_tag,omitempty"`
	TaskTag         string    `json:"task_tag,omitempty"`
	TaskID          string    `json:"task_id,omitempty"`
	TaskQueueID     string    `json:"task_queue_id,omitempty"`
	Title           string    `json:"title"`
	Description     string    `json:"description"`
	Severity        string    `json:"severity"`
	Status          string    `json:"status"`
	Type            string    `json:"type"`
	Target          string    `json:"target"`
	Preconditions   string    `json:"preconditions"`
	ReproSteps      string    `json:"reproduction_steps"`
	Evidence        string    `json:"evidence"`
	Impact          string    `json:"impact"`
	Recommendation  string    `json:"recommendation"`
	RetestNotes     string    `json:"retest_notes"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// VulnerabilityFilter 列表/统计/导出/批量删除共用的筛选条件（findu 同名语义）。
type VulnerabilityFilter struct {
	ID              string
	Search          string
	ConversationID  string
	ProjectID       string
	Severity        string
	Status          string
	TaskID          string
	ConversationTag string
	TaskTag         string
}

func escapeVulnerabilityLikePatternNative(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return "%" + s + "%"
}

// vulnerabilityWhere 构造 WHERE 子句（PG $n 占位），基于 WHERE 1=1 前缀。
func vulnerabilityWhere(query string, args []any, filter VulnerabilityFilter) (string, []any) {
	add := func(clause string, v any) {
		args = append(args, v)
		query += fmt.Sprintf(" AND %s $%d", clause, len(args))
	}
	if v := strings.TrimSpace(filter.ID); v != "" {
		add("id =", v)
	}
	if v := strings.TrimSpace(filter.ConversationID); v != "" {
		add("conversation_id =", v)
	}
	if v := strings.TrimSpace(filter.ProjectID); v != "" {
		add("project_id =", v)
	}
	if v := strings.TrimSpace(filter.TaskID); v != "" {
		args = append(args, v, v)
		n1, n2 := len(args)-1, len(args)
		query += fmt.Sprintf(" AND EXISTS (SELECT 1 FROM batch_tasks bt WHERE bt.conversation_id = vulnerabilities.conversation_id AND (bt.id = $%d OR bt.queue_id = $%d))", n1, n2)
	}
	if v := strings.TrimSpace(filter.ConversationTag); v != "" {
		add("conversation_tag =", v)
	}
	if v := strings.TrimSpace(filter.TaskTag); v != "" {
		add("task_tag =", v)
	}
	if v := strings.TrimSpace(filter.Severity); v != "" {
		add("severity =", v)
	}
	if v := strings.TrimSpace(filter.Status); v != "" {
		add("status =", v)
	}
	if search := strings.TrimSpace(filter.Search); search != "" {
		pattern := escapeVulnerabilityLikePatternNative(search)
		args = append(args, make([]any, 14)...)
		for i := range args[len(args)-14:] {
			args[len(args)-14+i] = pattern
		}
		first := len(args) - 14
		last := len(args)
		query += ` AND (
			LOWER(id) LIKE LOWER($` + strconv.Itoa(first+1) + `) OR
			LOWER(title) LIKE LOWER($` + strconv.Itoa(first+2) + `) OR
			LOWER(COALESCE(description, '')) LIKE LOWER($` + strconv.Itoa(first+3) + `) OR
			LOWER(COALESCE(vulnerability_type, '')) LIKE LOWER($` + strconv.Itoa(first+4) + `) OR
			LOWER(COALESCE(target, '')) LIKE LOWER($` + strconv.Itoa(first+5) + `) OR
			LOWER(COALESCE(preconditions, '')) LIKE LOWER($` + strconv.Itoa(first+6) + `) OR
			LOWER(COALESCE(reproduction_steps, '')) LIKE LOWER($` + strconv.Itoa(first+7) + `) OR
			LOWER(COALESCE(evidence, '')) LIKE LOWER($` + strconv.Itoa(first+8) + `) OR
			LOWER(COALESCE(impact, '')) LIKE LOWER($` + strconv.Itoa(first+9) + `) OR
			LOWER(COALESCE(recommendation, '')) LIKE LOWER($` + strconv.Itoa(first+10) + `) OR
			LOWER(COALESCE(retest_notes, '')) LIKE LOWER($` + strconv.Itoa(first+11) + `) OR
			LOWER(COALESCE(conversation_id, '')) LIKE LOWER($` + strconv.Itoa(first+12) + `) OR
			LOWER(COALESCE(conversation_tag, '')) LIKE LOWER($` + strconv.Itoa(first+13) + `) OR
			LOWER(COALESCE(task_tag, '')) LIKE LOWER($` + strconv.Itoa(last) + `)
		)`
	}
	return query, args
}

const vulnerabilitySelectCols = `id, COALESCE(conversation_id,''), COALESCE(project_id,''), title, description, severity, status,
	conversation_tag, task_tag, vulnerability_type, target,
	COALESCE(preconditions,''), COALESCE(reproduction_steps,''), COALESCE(evidence,''),
	COALESCE(impact,''), COALESCE(recommendation,''), COALESCE(retest_notes,''),
	COALESCE((SELECT bt.id FROM batch_tasks bt WHERE bt.conversation_id = vulnerabilities.conversation_id LIMIT 1), ''),
	COALESCE((SELECT bt.queue_id FROM batch_tasks bt WHERE bt.conversation_id = vulnerabilities.conversation_id LIMIT 1), ''),
	created_at, updated_at`

func scanVulnerability(row interface{ Scan(dest ...any) error }) (*VulnerabilityDTO, error) {
	var v VulnerabilityDTO
	err := row.Scan(&v.ID, &v.ConversationID, &v.ProjectID, &v.Title, &v.Description,
		&v.Severity, &v.Status, &v.ConversationTag, &v.TaskTag, &v.Type, &v.Target,
		&v.Preconditions, &v.ReproSteps, &v.Evidence, &v.Impact, &v.Recommendation, &v.RetestNotes,
		&v.TaskID, &v.TaskQueueID, &v.CreatedAt, &v.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// vulnerabilityInsertCols 是 create/update 共用的写列（update 同列集）。
const vulnerabilityWriteCols = `id, conversation_id, project_id, conversation_tag, task_tag, title, description, severity, status,
	vulnerability_type, target, preconditions, reproduction_steps, evidence, impact, recommendation, retest_notes,
	created_at, updated_at`

func vulnerabilityWriteArgs(v *VulnerabilityDTO) []any {
	return []any{
		v.ID, nullIfEmptyString(v.ConversationID), nullIfEmptyString(v.ProjectID),
		v.ConversationTag, v.TaskTag, v.Title, v.Description, v.Severity, v.Status,
		v.Type, v.Target, v.Preconditions, v.ReproSteps, v.Evidence, v.Impact, v.Recommendation, v.RetestNotes,
		v.CreatedAt, v.UpdatedAt,
	}
}

// CreateVulnerabilityNative 创建漏洞（B1）：默认 id/status/时间戳，未显式
// 指定 project_id 时按 conversation 回填，写后 best-effort 刷资产风险缓存。
func (s *VulnerabilityStore) CreateVulnerabilityNative(v *VulnerabilityDTO) (*VulnerabilityDTO, error) {
	if v.ID == "" {
		v.ID = uuid.NewString()
	}
	if v.Status == "" {
		v.Status = "open"
	}
	now := time.Now()
	if v.CreatedAt.IsZero() {
		v.CreatedAt = now
	}
	v.UpdatedAt = now
	if strings.TrimSpace(v.ProjectID) == "" && v.ConversationID != "" {
		var pid sql.NullString
		if err := s.db.QueryRow(`SELECT project_id FROM conversations WHERE id=$1`, v.ConversationID).Scan(&pid); err == nil && pid.Valid {
			v.ProjectID = strings.TrimSpace(pid.String)
		}
	}
	placeholders := make([]string, 19)
	for i := range placeholders {
		placeholders[i] = "$" + strconv.Itoa(i+1)
	}
	_, err := s.db.Exec(`INSERT INTO vulnerabilities (`+vulnerabilityWriteCols+`) VALUES (`+strings.Join(placeholders, ",")+`)`,
		vulnerabilityWriteArgs(v)...)
	if err != nil {
		return nil, fmt.Errorf("创建漏洞失败: %w", err)
	}
	s.assets.RefreshAssetRiskCacheForConversations(v.ConversationID)
	return v, nil
}

// GetVulnerabilityNative 按 id 取漏洞（含派生 task 字段）。
func (s *VulnerabilityStore) GetVulnerabilityNative(id string) (*VulnerabilityDTO, error) {
	v, err := scanVulnerability(s.db.QueryRow(`SELECT `+vulnerabilitySelectCols+` FROM vulnerabilities WHERE id = $1`, strings.TrimSpace(id)))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("漏洞不存在")
		}
		return nil, fmt.Errorf("获取漏洞失败: %w", err)
	}
	return v, nil
}

// ListVulnerabilitiesNative 分页列出（ORDER BY created_at DESC）。
func (s *VulnerabilityStore) ListVulnerabilitiesNative(limit, offset int, filter VulnerabilityFilter) ([]*VulnerabilityDTO, error) {
	query := `SELECT ` + vulnerabilitySelectCols + ` FROM vulnerabilities WHERE 1=1`
	args := []any{}
	query, args = vulnerabilityWhere(query, args, filter)
	args = append(args, limit, offset)
	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args))
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询漏洞列表失败: %w", err)
	}
	defer rows.Close()
	out := []*VulnerabilityDTO{}
	for rows.Next() {
		v, err := scanVulnerability(rows)
		if err != nil {
			continue
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// CountVulnerabilitiesNative 按筛选统计总数。
func (s *VulnerabilityStore) CountVulnerabilitiesNative(filter VulnerabilityFilter) (int, error) {
	query := "SELECT COUNT(*) FROM vulnerabilities WHERE 1=1"
	args := []any{}
	query, args = vulnerabilityWhere(query, args, filter)
	var count int
	if err := s.db.QueryRow(query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("统计漏洞总数失败: %w", err)
	}
	return count, nil
}

// UpdateVulnerabilityNative 全字段覆盖更新（findu UpdateVulnerability 语义：
// handler 先读现有行合并指针字段再整体写回），写后刷新新旧会话风险缓存。
func (s *VulnerabilityStore) UpdateVulnerabilityNative(id string, v *VulnerabilityDTO) error {
	v.UpdatedAt = time.Now()
	var oldConversationID string
	_ = s.db.QueryRow(`SELECT COALESCE(conversation_id,'') FROM vulnerabilities WHERE id = $1`, id).Scan(&oldConversationID)
	placeholders := make([]string, 18)
	for i := range placeholders {
		placeholders[i] = "$" + strconv.Itoa(i+1)
	}
	// 写列去掉 id（WHERE 用）：conversation_id..retest_notes + updated_at
	_, err := s.db.Exec(`UPDATE vulnerabilities SET
		conversation_id=$1, project_id=$2, conversation_tag=$3, task_tag=$4, title=$5, description=$6,
		severity=$7, status=$8, vulnerability_type=$9, target=$10, preconditions=$11, reproduction_steps=$12,
		evidence=$13, impact=$14, recommendation=$15, retest_notes=$16, updated_at=$17 WHERE id=$18`,
		nullIfEmptyString(v.ConversationID), nullIfEmptyString(v.ProjectID), v.ConversationTag, v.TaskTag, v.Title, v.Description,
		v.Severity, v.Status, v.Type, v.Target, v.Preconditions, v.ReproSteps, v.Evidence, v.Impact,
		v.Recommendation, v.RetestNotes, v.UpdatedAt, id)
	if err != nil {
		return fmt.Errorf("更新漏洞失败: %w", err)
	}
	s.assets.RefreshAssetRiskCacheForConversations(oldConversationID, v.ConversationID)
	return nil
}

// DeleteVulnerabilityNative 删除单条：事务内清 project_facts 关联 → 删除 →
// 刷风险缓存（findu DeleteVulnerability 同构）。
func (s *VulnerabilityStore) DeleteVulnerabilityNative(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	var conversationID string
	_ = tx.QueryRow(`SELECT COALESCE(conversation_id,'') FROM vulnerabilities WHERE id = $1`, id).Scan(&conversationID)
	if _, err := tx.Exec(`UPDATE project_facts SET related_vulnerability_id = NULL WHERE related_vulnerability_id = $1`, id); err != nil {
		return fmt.Errorf("清理事实漏洞关联失败: %w", err)
	}
	res, err := tx.Exec(`DELETE FROM vulnerabilities WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("删除漏洞失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交事务失败: %w", err)
	}
	s.assets.RefreshAssetRiskCacheForConversations(conversationID)
	return nil
}

// DeleteVulnerabilitiesByFilterNative 按筛选批量删除，返回实际删除条数。
func (s *VulnerabilityStore) DeleteVulnerabilitiesByFilterNative(filter VulnerabilityFilter) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("开启事务失败: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	where := "WHERE 1=1"
	args := []any{}
	where, args = vulnerabilityWhere(where, args, filter)

	affectedConversations, err := collectVulnerabilityConversationIDsNative(tx, where, args)
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`UPDATE project_facts SET related_vulnerability_id = NULL
		WHERE related_vulnerability_id IN (SELECT id FROM vulnerabilities `+where+`)`, args...); err != nil {
		return 0, fmt.Errorf("清理事实漏洞关联失败: %w", err)
	}
	res, err := tx.Exec(`DELETE FROM vulnerabilities `+where, args...)
	if err != nil {
		return 0, fmt.Errorf("批量删除漏洞失败: %w", err)
	}
	deleted, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("获取删除条数失败: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("提交事务失败: %w", err)
	}
	s.assets.RefreshAssetRiskCacheForConversations(affectedConversations...)
	return deleted, nil
}

func collectVulnerabilityConversationIDsNative(tx *sql.Tx, where string, args []any) ([]string, error) {
	rows, err := tx.Query(`SELECT DISTINCT COALESCE(conversation_id,'') FROM vulnerabilities `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("查询受影响漏洞会话失败: %w", err)
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		if strings.TrimSpace(id) != "" {
			ids = append(ids, id)
		}
	}
	return ids, rows.Err()
}

// VulnerabilityStatsNative 漏洞统计（total/by_severity/by_status）。
type VulnerabilityStatsNative struct {
	Total      int            `json:"total"`
	BySeverity map[string]int `json:"by_severity"`
	ByStatus   map[string]int `json:"by_status"`
}

func (s *VulnerabilityStore) GetVulnerabilityStatsNative(filter VulnerabilityFilter) (*VulnerabilityStatsNative, error) {
	where := "WHERE 1=1"
	args := []any{}
	where, args = vulnerabilityWhere(where, args, filter)
	out := &VulnerabilityStatsNative{BySeverity: map[string]int{}, ByStatus: map[string]int{}}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM vulnerabilities `+where, args...).Scan(&out.Total); err != nil {
		return nil, fmt.Errorf("获取总漏洞数失败: %w", err)
	}
	rows, err := s.db.Query(`SELECT severity, COUNT(*) FROM vulnerabilities `+where+` GROUP BY severity`, args...)
	if err != nil {
		return nil, fmt.Errorf("获取严重程度统计失败: %w", err)
	}
	for rows.Next() {
		var key string
		var n int
		if err := rows.Scan(&key, &n); err == nil {
			out.BySeverity[key] = n
		}
	}
	rows.Close()
	rows, err = s.db.Query(`SELECT status, COUNT(*) FROM vulnerabilities `+where+` GROUP BY status`, args...)
	if err != nil {
		return nil, fmt.Errorf("获取状态统计失败: %w", err)
	}
	for rows.Next() {
		var key string
		var n int
		if err := rows.Scan(&key, &n); err == nil {
			out.ByStatus[key] = n
		}
	}
	rows.Close()
	return out, rows.Err()
}

// GetVulnerabilityFilterOptionsNative 返回 7 组筛选建议项（各上限 500/200）。
func (s *VulnerabilityStore) GetVulnerabilityFilterOptionsNative() (map[string][]string, error) {
	collect := func(query string, args ...any) ([]string, error) {
		rows, err := s.db.Query(query, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		items := []string{}
		for rows.Next() {
			var val string
			if err := rows.Scan(&val); err == nil && val != "" {
				items = append(items, val)
			}
		}
		return items, rows.Err()
	}
	vulnIDs, err := collect(`SELECT id FROM vulnerabilities GROUP BY id ORDER BY MAX(created_at) DESC LIMIT 500`)
	if err != nil {
		return nil, fmt.Errorf("查询漏洞ID建议失败: %w", err)
	}
	// PG 的 SELECT DISTINCT 不允许 ORDER BY 未投影列（SQLite 允许）；改 GROUP BY + 聚合排序。
	conversationIDs, err := collect(`SELECT conversation_id FROM vulnerabilities WHERE conversation_id IS NOT NULL AND conversation_id <> '' GROUP BY conversation_id ORDER BY MAX(created_at) DESC LIMIT 500`)
	if err != nil {
		return nil, fmt.Errorf("查询会话ID建议失败: %w", err)
	}
	// findu SQLite 版按 bt.rowid 排序；PG 无 rowid，batch_tasks 也无 created_at，
	// 改按 bt.started_at 聚合排序。
	taskIDs, err := collect(`SELECT bt.id FROM batch_tasks bt JOIN vulnerabilities ON bt.conversation_id = vulnerabilities.conversation_id WHERE bt.id <> '' GROUP BY bt.id ORDER BY MAX(bt.started_at) DESC NULLS LAST LIMIT 500`)
	if err != nil {
		return nil, fmt.Errorf("查询任务ID建议失败: %w", err)
	}
	queueIDs, err := collect(`SELECT bt.queue_id FROM batch_tasks bt JOIN vulnerabilities ON bt.conversation_id = vulnerabilities.conversation_id WHERE bt.queue_id <> '' GROUP BY bt.queue_id ORDER BY MAX(bt.started_at) DESC NULLS LAST LIMIT 500`)
	if err != nil {
		return nil, fmt.Errorf("查询队列ID建议失败: %w", err)
	}
	conversationTags, err := collect(`SELECT DISTINCT conversation_tag FROM vulnerabilities WHERE conversation_tag IS NOT NULL AND conversation_tag <> '' ORDER BY conversation_tag LIMIT 500`)
	if err != nil {
		return nil, fmt.Errorf("查询对话标签建议失败: %w", err)
	}
	taskTags, err := collect(`SELECT DISTINCT task_tag FROM vulnerabilities WHERE task_tag IS NOT NULL AND task_tag <> '' ORDER BY task_tag LIMIT 500`)
	if err != nil {
		return nil, fmt.Errorf("查询任务标签建议失败: %w", err)
	}
	projectIDs, err := collect(`SELECT project_id FROM vulnerabilities WHERE project_id IS NOT NULL AND project_id <> '' GROUP BY project_id ORDER BY MAX(created_at) DESC LIMIT 200`)
	if err != nil {
		return nil, fmt.Errorf("查询项目ID建议失败: %w", err)
	}
	return map[string][]string{
		"vulnerability_ids": vulnIDs,
		"conversation_ids":  conversationIDs,
		"project_ids":       projectIDs,
		"task_ids":          taskIDs,
		"queue_ids":         queueIDs,
		"conversation_tags": conversationTags,
		"task_tags":         taskTags,
	}, nil
}

// AssetIDsForVulnerabilityConversations 返回与给定会话关联（扫描链路）的
// 资产 id 集合（findu AssetIDsForVulnerabilityConversations 的 PG 版）。
func (s *AssetStore) AssetIDsForVulnerabilityConversations(conversationIDs []string) ([]string, error) {
	seen := map[string]struct{}{}
	cleaned := make([]string, 0, len(conversationIDs))
	for _, id := range conversationIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		cleaned = append(cleaned, id)
	}
	if len(cleaned) == 0 {
		return nil, nil
	}
	ph := make([]string, len(cleaned))
	args := make([]any, 0, len(cleaned)*2)
	for i, id := range cleaned {
		ph[i] = "$" + strconv.Itoa(i+1)
		args = append(args, id)
	}
	inConv := strings.Join(ph, ",")
	ph2 := make([]string, len(cleaned))
	for i, id := range cleaned {
		ph2[i] = "$" + strconv.Itoa(len(cleaned)+i+1)
		args = append(args, id)
	}
	inConv2 := strings.Join(ph2, ",")
	rows, err := s.db.Query(`SELECT DISTINCT assets.id FROM assets
		WHERE assets.last_scan_conversation_id IN (`+inConv+`)
		OR assets.last_scan_task_id IN (SELECT bt.id FROM batch_tasks bt WHERE bt.conversation_id IN (`+inConv2+`))`, args...)
	if err != nil {
		return nil, fmt.Errorf("查询受影响资产失败: %w", err)
	}
	defer rows.Close()
	assetIDs := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		assetIDs = append(assetIDs, id)
	}
	return assetIDs, rows.Err()
}

// RefreshAssetRiskCacheForConversations 遍历会话关联资产逐个刷新风险缓存
// （findu RefreshAssetRiskCacheForConversations 的宿主导出版，漏洞写路径调用）。
func (s *AssetStore) RefreshAssetRiskCacheForConversations(conversationIDs ...string) error {
	assetIDs, err := s.AssetIDsForVulnerabilityConversations(conversationIDs)
	if err != nil {
		return err
	}
	for _, id := range assetIDs {
		if err := s.refreshAssetRiskCache(id); err != nil {
			return err
		}
	}
	return nil
}
