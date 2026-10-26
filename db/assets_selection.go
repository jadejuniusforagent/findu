package db

// Step 6d Phase A：assets selection / scan-links / import / merge 原生实现。
//
// 数据面：共享 PG public schema 的统一 assets 表（findu 资产库视图 + 宿主六类
// 资产视图同表）。本文件提供 findu `internal/database/asset.go` 等价能力的 PG
// 原生版本，DTO 字段与 JSON 形状与 findu 兼容路径逐字段对齐，旧 UI 无感切换。
//
// 范围取舍（见 docs/refactor/STEP6D-AB-HANDOFF.md）：
//   - RBAC 细粒度（owner/project scope 过滤）暂不实现：宿主 Bearer 鉴权 +
//     scope=all 管理员行为与 findu 兼容路径一致；细粒度留待 RBAC 域迁移。
//   - import 的 normalize/validate 语义与 findu 一致（URL Host 解析、dedup key、
//     active/inactive 状态、标签上限 30）。

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/net/idna"
)

// AssetRowDTO 是 findu 资产库行的宿主原生 DTO；JSON 字段与 findu
// internal/database.Asset 逐字段一致（旧 UI 消费同一形状）。
type AssetRowDTO struct {
	ID                     string     `json:"id"`
	ProjectID              string     `json:"project_id,omitempty"`
	ProjectName            string     `json:"project_name,omitempty"`
	Host                   string     `json:"host"`
	IP                     string     `json:"ip"`
	Port                   int        `json:"port"`
	Domain                 string     `json:"domain"`
	Protocol               string     `json:"protocol"`
	Title                  string     `json:"title"`
	Server                 string     `json:"server"`
	Country                string     `json:"country"`
	Province               string     `json:"province"`
	City                   string     `json:"city"`
	ResponsiblePerson      string     `json:"responsible_person"`
	Department             string     `json:"department"`
	BusinessSystem         string     `json:"business_system"`
	Environment            string     `json:"environment"`
	Criticality            string     `json:"criticality"`
	Source                 string     `json:"source"`
	SourceQuery            string     `json:"source_query"`
	Status                 string     `json:"status"`
	Tags                   []string   `json:"tags"`
	FirstSeenAt            time.Time  `json:"first_seen_at"`
	LastSeenAt             time.Time  `json:"last_seen_at"`
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
	LastScanAt             *time.Time `json:"last_scan_at,omitempty"`
	LastScanConversationID string     `json:"last_scan_conversation_id,omitempty"`
	LastScanQueueID        string     `json:"last_scan_queue_id,omitempty"`
	LastScanTaskID         string     `json:"last_scan_task_id,omitempty"`
	VulnerabilityCount     int        `json:"vulnerability_count"`
	RiskLevel              string     `json:"risk_level"`
}

// AssetSelectionFilter 是 GET /api/assets/selection 的查询过滤（findu 参数子集 + type）。
type AssetSelectionFilter struct {
	Q         string
	Type      string
	Status    string
	Protocol  string
	ProjectID string
	Source    string
	Tag       string
	Host      string
	IP        string
	Domain    string
	SortBy    string
	SortOrder string
}

// assetRowSelectCols 与 findu assetSelectColumns 等价（PG 原生）。
const assetRowSelectCols = `assets.id,COALESCE(assets.project_id,''),COALESCE(p.name,''),
	COALESCE(assets.host,''),COALESCE(assets.ip,''),COALESCE(assets.port,0),COALESCE(assets.domain,''),
	COALESCE(assets.protocol,''),COALESCE(assets.title,''),COALESCE(assets.server,''),COALESCE(assets.country,''),
	COALESCE(assets.province,''),COALESCE(assets.city,''),COALESCE(assets.responsible_person,''),COALESCE(assets.department,''),
	COALESCE(assets.business_system,''),COALESCE(assets.environment,''),COALESCE(assets.criticality,''),
	COALESCE(assets.source,''),COALESCE(assets.source_query,''),COALESCE(assets.status,''),COALESCE(assets.tags_json,'[]'),
	assets.first_seen_at,assets.last_seen_at,assets.created_at,assets.updated_at,
	` + assetEffectiveLastScanExpr + `,COALESCE(assets.last_scan_conversation_id,''),COALESCE(assets.last_scan_queue_id,''),COALESCE(assets.last_scan_task_id,''),
	COALESCE(assets.vulnerability_count,0),COALESCE(NULLIF(assets.risk_level,''),'unassessed')`

const assetRowFrom = ` FROM assets LEFT JOIN projects p ON p.id=assets.project_id`

// assetEffectiveLastScanExpr 与 findu 同名表达式一致：批量任务完成时间 > 会话
// 最后助手消息时间 > 手工记录的 last_scan_at。
const assetEffectiveLastScanExpr = `COALESCE(
	(SELECT bt.completed_at FROM batch_tasks bt WHERE bt.id=assets.last_scan_task_id AND bt.completed_at IS NOT NULL LIMIT 1),
	(SELECT MAX(m.updated_at) FROM messages m WHERE m.conversation_id=assets.last_scan_conversation_id AND m.role='assistant'),
	assets.last_scan_at
)`

const assetVulnerabilityMatchExpr = `(
	(COALESCE(assets.last_scan_conversation_id,'')<>'' AND v.conversation_id=assets.last_scan_conversation_id)
	OR (COALESCE(assets.last_scan_task_id,'')<>'' AND EXISTS (
		SELECT 1 FROM batch_tasks bt WHERE bt.id=assets.last_scan_task_id AND bt.conversation_id=v.conversation_id
	))
)`

// assetVulnerabilityCountExpr / assetRiskScoreQueryExpr 与 findu 同名常量一致。
const assetVulnerabilityCountExpr = `(SELECT COUNT(DISTINCT v.id) FROM vulnerabilities v WHERE ` + assetVulnerabilityMatchExpr + `)`

const assetRiskScoreQueryExpr = `COALESCE((
	SELECT MAX(CASE LOWER(COALESCE(v.severity,'')) WHEN 'critical' THEN 5 WHEN 'high' THEN 4 WHEN 'medium' THEN 3 WHEN 'low' THEN 2 WHEN 'info' THEN 1 ELSE 0 END)
	FROM vulnerabilities v
	WHERE LOWER(COALESCE(v.status,'open')) NOT IN ('fixed','false_positive','ignored') AND ` + assetVulnerabilityMatchExpr + `
),0)`

// assetRiskLevelFromScoreNative 对齐 findu assetRiskLevelFromScore：未扫描
// 一律 unassessed；已扫描按最高严重度定级。
func assetRiskLevelFromScoreNative(score int, scanned bool) string {
	if !scanned {
		return "unassessed"
	}
	switch score {
	case 5:
		return "critical"
	case 4:
		return "high"
	case 3:
		return "medium"
	case 2:
		return "low"
	case 1:
		return "info"
	default:
		return "normal"
	}
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}

func assetSelectionWhere(filter AssetSelectionFilter) (string, []any) {
	query := " WHERE 1=1"
	args := []any{}
	add := func(clause string, value any) {
		args = append(args, value)
		query += fmt.Sprintf(" AND %s $%d", clause, len(args))
	}
	if q := strings.TrimSpace(filter.Q); q != "" {
		pattern := "%" + escapeLike(strings.ToLower(q)) + "%"
		query += ` AND (LOWER(assets.host) LIKE $1 ESCAPE '\' OR LOWER(assets.ip) LIKE $1 ESCAPE '\' OR LOWER(assets.domain) LIKE $1 ESCAPE '\'
			OR LOWER(assets.title) LIKE $1 ESCAPE '\' OR LOWER(assets.server) LIKE $1 ESCAPE '\' OR LOWER(assets.tags_json) LIKE $1 ESCAPE '\'
			OR LOWER(assets.responsible_person) LIKE $1 ESCAPE '\' OR LOWER(assets.department) LIKE $1 ESCAPE '\' OR LOWER(assets.business_system) LIKE $1 ESCAPE '\')`
		args = append(args, pattern)
	}
	if v := strings.TrimSpace(filter.Type); v != "" {
		add("assets.type =", v)
	}
	if v := strings.TrimSpace(filter.Status); v != "" {
		add("assets.status =", v)
	}
	if v := strings.TrimSpace(filter.Protocol); v != "" {
		add("assets.protocol =", v)
	}
	if v := strings.TrimSpace(filter.ProjectID); v != "" {
		add("assets.project_id =", v)
	}
	if v := strings.TrimSpace(filter.Source); v != "" {
		args = append(args, v)
		query += fmt.Sprintf(" AND LOWER(assets.source) = LOWER($%d)", len(args))
	}
	if tag := strings.TrimSpace(filter.Tag); tag != "" {
		pattern := "%\"" + escapeLike(strings.ToLower(tag)) + "\"%"
		add("LOWER(assets.tags_json) LIKE", pattern)
		query += " ESCAPE '\\'"
	}
	if v := strings.TrimSpace(filter.Host); v != "" {
		args = append(args, v)
		query += fmt.Sprintf(" AND LOWER(assets.host) = LOWER($%d)", len(args))
	}
	if v := strings.TrimSpace(filter.IP); v != "" {
		args = append(args, v)
		query += fmt.Sprintf(" AND LOWER(assets.ip) = LOWER($%d)", len(args))
	}
	if v := strings.TrimSpace(filter.Domain); v != "" {
		args = append(args, v)
		query += fmt.Sprintf(" AND LOWER(assets.domain) = LOWER($%d)", len(args))
	}
	return query, args
}

func assetSelectionOrder(sortBy, sortOrder string) string {
	direction := "DESC"
	if strings.EqualFold(strings.TrimSpace(sortOrder), "asc") {
		direction = "ASC"
	}
	expression := "assets.last_seen_at"
	switch strings.ToLower(strings.TrimSpace(sortBy)) {
	case "created_at":
		expression = "assets.created_at"
	case "updated_at":
		expression = "assets.updated_at"
	case "first_seen_at":
		expression = "assets.first_seen_at"
	case "last_scan_at":
		return "CASE WHEN " + assetEffectiveLastScanExpr + " IS NULL THEN " + map[bool]string{true: "0", false: "1"}[direction == "ASC"] + " END ASC, " + assetEffectiveLastScanExpr + " " + direction + ", assets.id ASC"
	case "host":
		expression = "LOWER(assets.host)"
	case "port":
		expression = "assets.port"
	case "vulnerability_count":
		expression = "COALESCE(assets.vulnerability_count,0)"
	case "risk_level":
		expression = "COALESCE(assets.risk_score,0)"
	}
	return expression + " " + direction + ", assets.id ASC"
}

func scanAssetRow(scanner interface{ Scan(dest ...any) error }) (*AssetRowDTO, error) {
	var a AssetRowDTO
	var tags string
	var lastScanAt sql.NullTime
	err := scanner.Scan(&a.ID, &a.ProjectID, &a.ProjectName, &a.Host, &a.IP, &a.Port, &a.Domain, &a.Protocol, &a.Title, &a.Server, &a.Country,
		&a.Province, &a.City, &a.ResponsiblePerson, &a.Department, &a.BusinessSystem, &a.Environment, &a.Criticality,
		&a.Source, &a.SourceQuery, &a.Status, &tags, &a.FirstSeenAt, &a.LastSeenAt, &a.CreatedAt, &a.UpdatedAt,
		&lastScanAt, &a.LastScanConversationID, &a.LastScanQueueID, &a.LastScanTaskID, &a.VulnerabilityCount, &a.RiskLevel)
	if err != nil {
		return nil, err
	}
	if lastScanAt.Valid {
		t := lastScanAt.Time
		a.LastScanAt = &t
	}
	if tags != "" {
		_ = json.Unmarshal([]byte(tags), &a.Tags)
	}
	if a.Tags == nil {
		a.Tags = []string{}
	}
	return &a, nil
}

// SelectAssetsForOperation 解析当前过滤命中的完整资产集（跨页批量操作用）。
// 上限与 findu 一致：匹配超过 limit 条时报错让前端缩小范围。
func (s *AssetStore) SelectAssetsForOperation(limit int, filter AssetSelectionFilter) ([]*AssetRowDTO, int, error) {
	if limit < 1 || limit > 10000 {
		limit = 10000
	}
	where, args := assetSelectionWhere(filter)
	var total int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM assets"+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if total > limit {
		return nil, total, fmt.Errorf("匹配资产超过 %d 条，请缩小筛选范围", limit)
	}
	rows, err := s.db.Query("SELECT "+assetRowSelectCols+assetRowFrom+where+" ORDER BY "+assetSelectionOrder(filter.SortBy, filter.SortOrder), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]*AssetRowDTO, 0, total)
	for rows.Next() {
		item, err := scanAssetRow(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

// GetAssetRow 按 id 返回 findu 兼容资产行。
func (s *AssetStore) GetAssetRow(id string) (*AssetRowDTO, error) {
	row := s.db.QueryRow("SELECT "+assetRowSelectCols+assetRowFrom+" WHERE assets.id = $1", strings.TrimSpace(id))
	return scanAssetRow(row)
}

// MarkAssetScannedNative 记录扫描链路（A3 scan-links）：绑定会话/队列/子任务
// 并刷新风险缓存。
func (s *AssetStore) MarkAssetScannedNative(id, conversationID, queueID, taskID string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return sql.ErrNoRows
	}
	res, err := s.db.Exec(`UPDATE assets SET last_scan_at=$1,last_scan_conversation_id=$2,last_scan_queue_id=$3,last_scan_task_id=$4,updated_at=$5 WHERE id=$6`,
		time.Now(), strings.TrimSpace(conversationID), strings.TrimSpace(queueID), strings.TrimSpace(taskID), time.Now(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return s.refreshAssetRiskCache(id)
}

// refreshAssetRiskCache 重算单资产漏洞计数与风险等级（findu RefreshAssetRiskCache
// 的 PG 版：三条查询 + Go 侧定级，与 findu assetRiskLevelFromScore 一致）。
func (s *AssetStore) refreshAssetRiskCache(assetID string) error {
	assetID = strings.TrimSpace(assetID)
	if assetID == "" {
		return nil
	}
	var count int
	if err := s.db.QueryRow("SELECT "+assetVulnerabilityCountExpr+" FROM assets WHERE assets.id=$1", assetID).Scan(&count); err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return fmt.Errorf("刷新资产漏洞数量失败: %w", err)
	}
	var score int
	if err := s.db.QueryRow("SELECT "+assetRiskScoreQueryExpr+" FROM assets WHERE assets.id=$1", assetID).Scan(&score); err != nil {
		return fmt.Errorf("刷新资产风险分数失败: %w", err)
	}
	var lastScan sql.NullTime
	if err := s.db.QueryRow("SELECT "+assetEffectiveLastScanExpr+" FROM assets WHERE assets.id=$1", assetID).Scan(&lastScan); err != nil {
		return fmt.Errorf("刷新资产扫描状态失败: %w", err)
	}
	level := assetRiskLevelFromScoreNative(score, lastScan.Valid)
	_, err := s.db.Exec(`UPDATE assets SET vulnerability_count=$1, risk_score=$2, risk_level=$3 WHERE id=$4`,
		count, score, level, assetID)
	return err
}

// AssetImportInput 是 POST /api/assets/import 的单条输入（字段与 findu database.Asset 一致）。
type AssetImportInput struct {
	ProjectID         string   `json:"project_id"`
	Host              string   `json:"host"`
	IP                string   `json:"ip"`
	Port              int      `json:"port"`
	Domain            string   `json:"domain"`
	Protocol          string   `json:"protocol"`
	Title             string   `json:"title"`
	Server            string   `json:"server"`
	Country           string   `json:"country"`
	Province          string   `json:"province"`
	City              string   `json:"city"`
	ResponsiblePerson string   `json:"responsible_person"`
	Department        string   `json:"department"`
	BusinessSystem    string   `json:"business_system"`
	Environment       string   `json:"environment"`
	Criticality       string   `json:"criticality"`
	Source            string   `json:"source"`
	SourceQuery       string   `json:"source_query"`
	Status            string   `json:"status"`
	Tags              []string `json:"tags"`
}

// AssetImportResult 与 findu AssetImportResult 同形。
type AssetImportResult struct {
	Created int `json:"created"`
	Updated int `json:"updated"`
	Skipped int `json:"skipped"`
}

var assetProtocolPatternNative = regexp.MustCompile(`^[a-z][a-z0-9+.-]{0,31}$`)

func normalizeImportAsset(a *AssetImportInput) {
	a.Host = strings.TrimSpace(a.Host)
	a.IP = strings.ToLower(strings.TrimSpace(a.IP))
	a.Domain = strings.ToLower(strings.TrimSpace(a.Domain))
	a.Protocol = strings.ToLower(strings.TrimSpace(a.Protocol))
	a.Title = strings.TrimSpace(a.Title)
	a.Server = strings.TrimSpace(a.Server)
	a.Country = strings.TrimSpace(a.Country)
	a.Province = strings.TrimSpace(a.Province)
	a.City = strings.TrimSpace(a.City)
	a.ResponsiblePerson = strings.TrimSpace(a.ResponsiblePerson)
	a.Department = strings.TrimSpace(a.Department)
	a.BusinessSystem = strings.TrimSpace(a.BusinessSystem)
	a.Environment = strings.ToLower(strings.TrimSpace(a.Environment))
	a.Criticality = strings.ToLower(strings.TrimSpace(a.Criticality))
	a.Source = strings.TrimSpace(a.Source)
	a.SourceQuery = strings.TrimSpace(a.SourceQuery)
	a.ProjectID = strings.TrimSpace(a.ProjectID)
	a.Status = strings.ToLower(strings.TrimSpace(a.Status))
	if a.Status == "" {
		a.Status = "active"
	}
	if a.Source == "" {
		a.Source = "manual"
	}
	seen := map[string]bool{}
	tags := make([]string, 0, len(a.Tags))
	for _, tag := range a.Tags {
		tag = strings.TrimSpace(tag)
		if tag != "" && !seen[tag] {
			seen[tag] = true
			tags = append(tags, tag)
		}
	}
	a.Tags = tags
	// URL 型 Host：服务端补齐结构化字段，保证与 findu 一致的去重键。
	if strings.Contains(a.Host, "://") {
		if parsed, err := url.Parse(a.Host); err == nil && parsed.Hostname() != "" && parsed.User == nil {
			hostname := strings.Trim(strings.ToLower(parsed.Hostname()), "[]")
			if net.ParseIP(hostname) != nil && a.IP == "" {
				a.IP = hostname
			} else if a.Domain == "" {
				if ascii, err := idna.Lookup.ToASCII(hostname); err == nil {
					a.Domain = strings.ToLower(ascii)
				}
			}
			if a.Protocol == "" {
				a.Protocol = strings.ToLower(parsed.Scheme)
			}
			if a.Port == 0 {
				if parsed.Port() != "" {
					a.Port, _ = strconv.Atoi(parsed.Port())
				} else if a.Protocol == "https" {
					a.Port = 443
				} else if a.Protocol == "http" {
					a.Port = 80
				}
			}
		}
	}
}

func validateImportAsset(a *AssetImportInput) error {
	if a.Host == "" && a.IP == "" && a.Domain == "" {
		return fmt.Errorf("资产目标不能为空")
	}
	if a.Port < 0 || a.Port > 65535 {
		return fmt.Errorf("端口必须在 0-65535 之间")
	}
	if a.IP != "" && net.ParseIP(strings.Trim(a.IP, "[]")) == nil {
		return fmt.Errorf("IP 地址格式无效")
	}
	if a.Status != "active" && a.Status != "inactive" {
		return fmt.Errorf("资产状态必须为 active 或 inactive")
	}
	if len(a.Tags) > 30 {
		return fmt.Errorf("标签不能超过 30 个")
	}
	return nil
}

func importDedupKey(a *AssetImportInput) string {
	target := a.Domain
	if target == "" {
		target = a.IP
	}
	if target == "" {
		target = strings.ToLower(a.Host)
	}
	return strings.Join([]string{target, strconv.Itoa(a.Port), a.Protocol}, "|")
}

func nullIfEmptyString(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}

// ImportAssetsNative 批量导入（A2）：dedup_key 命中则更新非空字段，否则新建；
// 新资产授权给 owner（rbac_resource_assignments，PG 幂等写法）。
func (s *AssetStore) ImportAssetsNative(assets []*AssetImportInput, ownerUserID string) (AssetImportResult, error) {
	result := AssetImportResult{}
	tx, err := s.db.Begin()
	if err != nil {
		return result, err
	}
	defer tx.Rollback() //nolint:errcheck
	now := time.Now()
	for _, asset := range assets {
		if asset == nil {
			result.Skipped++
			continue
		}
		normalizeImportAsset(asset)
		if err := validateImportAsset(asset); err != nil {
			return result, fmt.Errorf("第 %d 个资产无效: %w", result.Created+result.Updated+result.Skipped+1, err)
		}
		key := importDedupKey(asset)
		if key == "|0|" {
			result.Skipped++
			continue
		}
		var existingID string
		var existingOwner sql.NullString
		err := tx.QueryRow(`SELECT id,owner_user_id FROM assets WHERE dedup_key = $1`, key).Scan(&existingID, &existingOwner)
		tagsJSON, _ := json.Marshal(asset.Tags)
		if err == sql.ErrNoRows {
			id := uuid.NewString()
			_, err = tx.Exec(`INSERT INTO assets (
				id,dedup_key,project_id,host,ip,port,domain,protocol,title,server,country,province,city,source,source_query,status,tags_json,
				responsible_person,department,business_system,environment,criticality,
				first_seen_at,last_seen_at,created_at,updated_at,owner_user_id
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$23,$23,$23,$24)`,
				id, key, nullIfEmptyString(asset.ProjectID), asset.Host, asset.IP, asset.Port, asset.Domain, asset.Protocol, asset.Title, asset.Server,
				asset.Country, asset.Province, asset.City, asset.Source, asset.SourceQuery, asset.Status, string(tagsJSON),
				asset.ResponsiblePerson, asset.Department, asset.BusinessSystem, asset.Environment, asset.Criticality,
				now, nullIfEmptyString(ownerUserID))
			if err != nil {
				return result, fmt.Errorf("创建资产失败: %w", err)
			}
			if ownerUserID != "" {
				if _, err := tx.Exec(`INSERT INTO rbac_resource_assignments (id,user_id,resource_type,resource_id,created_at)
					SELECT $1,id,'asset',$2,$3 FROM rbac_users WHERE id=$4
					AND NOT EXISTS (SELECT 1 FROM rbac_resource_assignments WHERE user_id=$4 AND resource_type='asset' AND resource_id=$2)`,
					uuid.NewString(), id, now, ownerUserID); err != nil {
					return result, fmt.Errorf("授权新资产失败: %w", err)
				}
			}
			result.Created++
			continue
		}
		if err != nil {
			return result, fmt.Errorf("检查资产去重键失败: %w", err)
		}
		_, err = tx.Exec(`UPDATE assets SET
			host=CASE WHEN $1<>'' THEN $1 ELSE host END, ip=CASE WHEN $2<>'' THEN $2 ELSE ip END,
			domain=CASE WHEN $3<>'' THEN $3 ELSE domain END, protocol=CASE WHEN $4<>'' THEN $4 ELSE protocol END,
			title=CASE WHEN $5<>'' THEN $5 ELSE title END, server=CASE WHEN $6<>'' THEN $6 ELSE server END,
			country=CASE WHEN $7<>'' THEN $7 ELSE country END, province=CASE WHEN $8<>'' THEN $8 ELSE province END,
			city=CASE WHEN $9<>'' THEN $9 ELSE city END, source=CASE WHEN $10<>'' THEN $10 ELSE source END,
			source_query=CASE WHEN $11<>'' THEN $11 ELSE source_query END, project_id=CASE WHEN $12<>'' THEN $12 ELSE project_id END,
			responsible_person=CASE WHEN $13<>'' THEN $13 ELSE responsible_person END,
			department=CASE WHEN $14<>'' THEN $14 ELSE department END,
			business_system=CASE WHEN $15<>'' THEN $15 ELSE business_system END,
			environment=CASE WHEN $16<>'' THEN $16 ELSE environment END,
			criticality=CASE WHEN $17<>'' THEN $17 ELSE criticality END,
			tags_json=CASE WHEN $18<>'[]' THEN $18 ELSE tags_json END,
			last_seen_at=$19, updated_at=$19 WHERE id=$20`,
			asset.Host, asset.IP, asset.Domain, asset.Protocol, asset.Title, asset.Server,
			asset.Country, asset.Province, asset.City, asset.Source, asset.SourceQuery, asset.ProjectID,
			asset.ResponsiblePerson, asset.Department, asset.BusinessSystem, asset.Environment, asset.Criticality,
			string(tagsJSON), now, existingID)
		if err != nil {
			return result, fmt.Errorf("更新资产失败: %w", err)
		}
		if ownerUserID != "" && (!existingOwner.Valid || strings.TrimSpace(existingOwner.String) == ownerUserID) {
			if _, err := tx.Exec(`INSERT INTO rbac_resource_assignments (id,user_id,resource_type,resource_id,created_at)
				SELECT $1,id,'asset',$2,$3 FROM rbac_users WHERE id=$4
				AND NOT EXISTS (SELECT 1 FROM rbac_resource_assignments WHERE user_id=$4 AND resource_type='asset' AND resource_id=$2)`,
				uuid.NewString(), existingID, now, ownerUserID); err != nil {
				return result, fmt.Errorf("授权资产失败: %w", err)
			}
		}
		result.Updated++
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}
	return result, nil
}

// DeleteAssetRowsByIDs 供 merge canary 清理与 A4 复用。
func (s *AssetStore) DeleteAssetRowsByIDs(ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	res, err := s.db.Exec(`DELETE FROM assets WHERE id = ANY($1::text[])`, ids)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// UpdateAssetRow 覆盖式更新 findu 兼容行的可写列（A4 merge 的主资产落盘）。
func (s *AssetStore) UpdateAssetRow(a *AssetRowDTO) error {
	tagsJSON, _ := json.Marshal(a.Tags)
	res, err := s.db.Exec(`UPDATE assets SET
		project_id=$1,host=$2,ip=$3,port=$4,domain=$5,protocol=$6,title=$7,server=$8,
		country=$9,province=$10,city=$11,responsible_person=$12,department=$13,
		business_system=$14,environment=$15,criticality=$16,tags_json=$17,updated_at=$18
		WHERE id=$19`,
		nullIfEmptyString(a.ProjectID), a.Host, a.IP, a.Port, a.Domain, a.Protocol, a.Title, a.Server,
		a.Country, a.Province, a.City, a.ResponsiblePerson, a.Department,
		a.BusinessSystem, a.Environment, a.Criticality, string(tagsJSON), time.Now(), a.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// MergeAssetRowsNative 合并重复资产（A4 store 侧）：主资产更新 + 重复资产删除 +
// 风险缓存重算，单事务。
func (s *AssetStore) MergeAssetRowsNative(primary *AssetRowDTO, duplicateIDs []string) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck
	tagsJSON, _ := json.Marshal(primary.Tags)
	res, err := tx.Exec(`UPDATE assets SET
		project_id=$1,host=$2,ip=$3,port=$4,domain=$5,protocol=$6,title=$7,server=$8,
		country=$9,province=$10,city=$11,responsible_person=$12,department=$13,
		business_system=$14,environment=$15,criticality=$16,tags_json=$17,updated_at=$18
		WHERE id=$19`,
		nullIfEmptyString(primary.ProjectID), primary.Host, primary.IP, primary.Port, primary.Domain, primary.Protocol, primary.Title, primary.Server,
		primary.Country, primary.Province, primary.City, primary.ResponsiblePerson, primary.Department,
		primary.BusinessSystem, primary.Environment, primary.Criticality, string(tagsJSON), time.Now(), primary.ID)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, sql.ErrNoRows
	}
	del, err := tx.Exec(`DELETE FROM assets WHERE id = ANY($1::text[])`, duplicateIDs)
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`UPDATE assets SET
		vulnerability_count = `+assetVulnerabilityCountExpr+`,
		risk_score = `+assetRiskScoreQueryExpr+`
		WHERE id=$1`, primary.ID); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	merged, _ := del.RowsAffected()
	return int(merged), nil
}
