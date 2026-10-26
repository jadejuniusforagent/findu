package miniprogram

// tasks.go —— 小程序分析记录(任务台账),列结构与消费面与 apkanalysis 对齐:
// 每次上传解包/敏感扫描/补填 AppID/跨文件检索落一条,Agent 对话内的调用同账。

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"finduai/internal/strutil"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// ScanTask 分析记录行。动作取值(前端 taskActionLabel 需同步):
// upload(上传解包+首扫) / scan(敏感信息扫描) / appid_fill(补填 AppID 解密) / findrefs(跨文件检索)。
type ScanTask struct {
	ID         string    `json:"id"`
	SampleID   string    `json:"sample_id"`
	ProjectID  string    `json:"project_id,omitempty"`
	Action     string    `json:"action"`
	QueryJSON  string    `json:"query_json,omitempty"` // 操作参数(前端详情抽屉展示)
	Status     string    `json:"status"`               // running | done | error
	ResultJSON string    `json:"-"`
	Error      string    `json:"error,omitempty"`
	DurationMS int64     `json:"duration_ms"`
	CreatedAt  time.Time `json:"created_at"`
}

// ScanTaskDetail 任务详情(含查询与结果 JSON;result 为解析后的对象,失败时为空)。
type ScanTaskDetail struct {
	ScanTask
	QueryJSON  string         `json:"query_json"`
	ResultJSON string         `json:"result_json,omitempty"`
	Result     map[string]any `json:"result,omitempty"`
}

// recordTask 落一条分析记录;sample 可为 nil(findrefs 等场景已解析样本时传入取项目归属)。
func (s *Service) recordTask(sample *Sample, action string, req any, result any, status, errText string, durationMS int64) {
	qj, _ := json.Marshal(req)
	rj, _ := json.Marshal(result)
	projectID := ""
	sampleID := ""
	if sample != nil {
		projectID = sample.ProjectID
		sampleID = sample.ID
	}
	_, err := s.db.Exec(`INSERT INTO miniprogram_scan_tasks
		(id, sample_id, project_id, action, query_json, status, result_json, error, duration_ms, created_at)
		VALUES ($1,$2,NULLIF($3,'')::text,$4,$5,$6,$7,NULLIF($8,'')::text,$9,$10)`,
		uuid.NewString(), sampleID, projectID, action, string(qj), status, string(rj), errText, durationMS, time.Now())
	if err != nil {
		s.log.Warn("记录小程序分析任务失败", zap.Error(err))
	}
}

// RecordTask 供外部(MCP 工具等)落一条分析记录到 miniprogram_scan_tasks。
func (s *Service) RecordTask(sample *Sample, action string, req any, result any, status, errText string, durationMS int64) {
	s.recordTask(sample, action, req, result, status, errText, durationMS)
}

// ScanWithTask 执行敏感信息扫描并落一条 scan 记录(GUI 手动重扫入口;
// 上传首扫/appid 补填各自的记录已覆盖扫描结果,不再重复落 scan)。
func (s *Service) ScanWithTask(sample *Sample) error {
	start := time.Now()
	err := s.RunScan(sample)
	status, errText := "done", ""
	if err != nil {
		status, errText = "error", err.Error()
	}
	s.recordTask(sample, "scan", map[string]any{"sample_id": sample.ID},
		scanResultSummary(s, sample.ID), status, errText, time.Since(start).Milliseconds())
	return err
}

// scanResultSummary 扫描结果摘要(按严重级计数,发现明细在 miniprogram_findings,不进台账)。
func scanResultSummary(s *Service, sampleID string) map[string]any {
	findings, err := s.ListFindings(sampleID, "")
	if err != nil {
		return map[string]any{"findings_total": 0}
	}
	bySeverity := map[string]int{}
	for _, f := range findings {
		bySeverity[f.Severity]++
	}
	return map[string]any{"findings_total": len(findings), "by_severity": bySeverity}
}

// Paged 标准分页结果。
type Paged[T any] struct {
	Items    []*T `json:"items"`
	Total    int  `json:"total"`
	Page     int  `json:"page"`
	PageSize int  `json:"page_size"`
}

const (
	defaultPageSize = 50
	maxPageSize     = 200
)

func clampPage(page, pageSize int) (int, int) {
	return strutil.Paginate(page, pageSize, defaultPageSize, maxPageSize)
}

// ListSamplesPaged 样本分页列表(keyword 匹配 名称/appid/sha256 前段)。
func (s *Service) ListSamplesPaged(page, pageSize int, keyword, projectID string) (*Paged[Sample], error) {
	page, pageSize = clampPage(page, pageSize)
	where := []string{}
	args := []any{}
	if projectID != "" {
		args = append(args, projectID)
		where = append(where, fmt.Sprintf("project_id = $%d", len(args)))
	}
	if keyword != "" {
		args = append(args, "%"+strings.ToLower(keyword)+"%")
		n := len(args)
		where = append(where, fmt.Sprintf(`(lower(name) LIKE $%d OR lower(COALESCE(appid,'')) LIKE $%d OR lower(sha256) LIKE $%d)`, n, n, n))
	}
	whereSQL := ""
	if len(where) > 0 {
		whereSQL = "WHERE " + strings.Join(where, " AND ")
	}
	p := &Paged[Sample]{Page: page, PageSize: pageSize}
	if err := s.db.QueryRow(`SELECT count(*) FROM miniprogram_samples `+whereSQL, args...).Scan(&p.Total); err != nil {
		return nil, err
	}
	suffix := whereSQL + fmt.Sprintf(` ORDER BY created_at DESC LIMIT %d OFFSET %d`, pageSize, (page-1)*pageSize)
	samples, err := s.querySamples(suffix, args...)
	if err != nil {
		return nil, err
	}
	p.Items = samples
	return p, nil
}

// querySamples 按给定后缀(WHERE/ORDER/LIMIT)查询样本列表。
func (s *Service) querySamples(suffix string, args ...any) ([]*Sample, error) {
	rows, err := s.db.Query(`SELECT id, name, sha256, size, workspace_path, COALESCE(appid,''), file_count,
		COALESCE(project_id,''), COALESCE(owner_user_id,''), source, created_at, updated_at FROM miniprogram_samples `+suffix, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var samples []*Sample
	for rows.Next() {
		var sample Sample
		if err := rows.Scan(&sample.ID, &sample.Name, &sample.SHA256, &sample.Size, &sample.Workspace,
			&sample.AppID, &sample.FileCount, &sample.ProjectID, &sample.OwnerUserID,
			&sample.Source, &sample.CreatedAt, &sample.UpdatedAt); err != nil {
			return nil, err
		}
		sample.WorkspaceDisplay = displayWorkspacePath(sample.Workspace)
		samples = append(samples, &sample)
	}
	return samples, rows.Err()
}

// ListTasksPaged 分析记录分页(可按样本/动作/状态过滤)。
func (s *Service) ListTasksPaged(page, pageSize int, sampleID, action, status string) (*Paged[ScanTask], error) {
	page, pageSize = clampPage(page, pageSize)
	where := []string{}
	args := []any{}
	if sampleID != "" {
		args = append(args, sampleID)
		where = append(where, fmt.Sprintf("sample_id = $%d", len(args)))
	}
	if action != "" {
		args = append(args, action)
		where = append(where, fmt.Sprintf("action = $%d", len(args)))
	}
	if status != "" {
		args = append(args, status)
		where = append(where, fmt.Sprintf("status = $%d", len(args)))
	}
	whereSQL := ""
	if len(where) > 0 {
		whereSQL = "WHERE " + strings.Join(where, " AND ")
	}
	p := &Paged[ScanTask]{Page: page, PageSize: pageSize}
	if err := s.db.QueryRow(`SELECT count(*) FROM miniprogram_scan_tasks `+whereSQL, args...).Scan(&p.Total); err != nil {
		return nil, err
	}
	q := `SELECT id, sample_id, COALESCE(project_id,''), action, COALESCE(query_json,''), status,
		COALESCE(error,''), duration_ms, created_at
		FROM miniprogram_scan_tasks ` + whereSQL + fmt.Sprintf(` ORDER BY created_at DESC LIMIT %d OFFSET %d`, pageSize, (page-1)*pageSize)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		task := &ScanTask{}
		if err := rows.Scan(&task.ID, &task.SampleID, &task.ProjectID, &task.Action, &task.QueryJSON,
			&task.Status, &task.Error, &task.DurationMS, &task.CreatedAt); err != nil {
			return nil, err
		}
		p.Items = append(p.Items, task)
	}
	return p, rows.Err()
}

// GetTask 分析记录详情。
func (s *Service) GetTask(taskID string) (*ScanTaskDetail, error) {
	d := &ScanTaskDetail{}
	err := s.db.QueryRow(`SELECT id, sample_id, COALESCE(project_id,''), action, COALESCE(query_json,''), status,
		COALESCE(result_json,''), COALESCE(error,''), duration_ms, created_at
		FROM miniprogram_scan_tasks WHERE id = $1`, taskID).
		Scan(&d.ID, &d.SampleID, &d.ProjectID, &d.Action, &d.QueryJSON, &d.Status,
			&d.ResultJSON, &d.Error, &d.DurationMS, &d.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}
	if err != nil {
		return nil, err
	}
	d.Result = parseJSONMap(d.ResultJSON)
	return d, nil
}

func parseJSONMap(raw string) map[string]any {
	if raw == "" {
		return nil
	}
	m := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil
	}
	return m
}
