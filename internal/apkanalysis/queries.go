package apkanalysis

// queries — 列表分页与任务详情查询(工作台消费面)。

import (
	"database/sql"
	"encoding/json"
	"errors"
	"finduai/internal/strutil"
	"fmt"
	"strings"
)

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
	// 重复清单收敛：委托 strutil.Paginate
	return strutil.Paginate(page, pageSize, defaultPageSize, maxPageSize)
}

// ListSamplesPaged 样本分页列表(keyword 匹配 名称/包名/sha256 前缀)。
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
		where = append(where, fmt.Sprintf(`(lower(name) LIKE $%d OR lower(COALESCE(package_name,'')) LIKE $%d OR lower(sha256) LIKE $%d)`, n, n, n))
	}
	whereSQL := ""
	if len(where) > 0 {
		whereSQL = "WHERE " + strings.Join(where, " AND ")
	}
	p := &Paged[Sample]{Page: page, PageSize: pageSize}
	if err := s.db.QueryRow(`SELECT count(*) FROM apk_samples `+whereSQL, args...).Scan(&p.Total); err != nil {
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

// GetSampleByID 单样本(id 精确;样本详情页用,避免 name 撞 ID 的歧义)。
func (s *Service) GetSampleByID(id string) (*Sample, error) {
	rows, err := s.querySamples(`WHERE id = $1`, id)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("sample not found: %s", id)
	}
	return rows[0], nil
}

// ListTasksPaged 任务分页(可按样本/动作/状态过滤)。
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
	if err := s.db.QueryRow(`SELECT count(*) FROM apk_scan_tasks `+whereSQL, args...).Scan(&p.Total); err != nil {
		return nil, err
	}
	q := `SELECT id, sample_id, COALESCE(project_id,''), action, COALESCE(query_json,''), status,
		COALESCE(error,''), duration_ms, created_at
		FROM apk_scan_tasks ` + whereSQL + fmt.Sprintf(` ORDER BY created_at DESC LIMIT %d OFFSET %d`, pageSize, (page-1)*pageSize)
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

// ScanTaskDetail 任务详情(含查询与结果 JSON,结果行已在落库时截断)。
type ScanTaskDetail struct {
	ScanTask
	QueryJSON  string         `json:"query_json"`
	ResultJSON string         `json:"result_json,omitempty"`
	Result     map[string]any `json:"result,omitempty"` // result_json 解析失败时为空
	Progress   map[string]any `json:"progress,omitempty"`
}

// GetTask 任务详情。
func (s *Service) GetTask(taskID string) (*ScanTaskDetail, error) {
	d := &ScanTaskDetail{}
	var progressJSON string
	err := s.db.QueryRow(`SELECT id, sample_id, COALESCE(project_id,''), action, query_json, status,
		COALESCE(result_json,''), COALESCE(error,''), duration_ms, COALESCE(progress,''), created_at
		FROM apk_scan_tasks WHERE id = $1`, taskID).
		Scan(&d.ID, &d.SampleID, &d.ProjectID, &d.Action, &d.QueryJSON, &d.Status,
			&d.ResultJSON, &d.Error, &d.DurationMS, &progressJSON, &d.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}
	if err != nil {
		return nil, err
	}
	d.Result = parseJSONMap(d.ResultJSON)
	d.Progress = parseJSONMap(progressJSON)
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
