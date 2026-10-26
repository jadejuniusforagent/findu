// 本文件是报告域的存储层：组织级报告设置（单例）、已生成报告记录、
// 可复用报告模板三块，SQL 全部收敛在这里。
//
// 分层约定
//   - 只依赖 database/sql，不认识 db 包的 DB 类型，也不 import core/*、plugins/*。
//   - 取连接用窄接口 Querier：*sql.DB 与 db.DB（struct{ *sql.DB }）都满足，
//     这样迁移期 db 包可以薄转发而调用点零改动。
//
// 迁移背景（Step 3 批 3 第一刀）：原实现散在 db/report_store.go，与 737 处
// SQL 一起挤在 20.5k 行的 db 包里。本文件是"按域切 db 包"的样板。
package report

import (
	"database/sql"
	"encoding/json"
	"time"

	"finduai/core/store"
)

// Settings 是组织级报告设置（表 report_settings，恒为 id=1 的单例行）。
type Settings struct {
	CompanyName   string          `json:"company_name"`
	CompanyNameEn string          `json:"company_name_en"`
	LogoPath      string          `json:"logo_path"`
	Personnel     json.RawMessage `json:"personnel"` // [{name,role,contact}]
	CVSSVersion   string          `json:"cvss_version"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

// 默认值：未落库时调用方拿到的就是这套（CVSS 3.1 + 空人员表）。
const (
	defaultCVSSVersion = "3.1"
	defaultLocale      = "zh-CN"
)

// GetSettings 返回单例行；未落库时返回默认值而不是错误 —— 报告管线需要
// 在全新部署上直接可用。
func GetSettings(q store.Querier) (*Settings, error) {
	s := &Settings{CVSSVersion: defaultCVSSVersion, Personnel: json.RawMessage("[]")}
	var personnel string
	err := q.QueryRow(`SELECT company_name, company_name_en, logo_path, personnel_json::text, cvss_version, updated_at
		FROM report_settings WHERE id=1`).
		Scan(&s.CompanyName, &s.CompanyNameEn, &s.LogoPath, &personnel, &s.CVSSVersion, &s.UpdatedAt)
	if err == sql.ErrNoRows {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	s.Personnel = json.RawMessage(personnel)
	return s, nil
}

// SaveSettings 覆盖单例行。personnel 必须是 JSON 数组。
func SaveSettings(q store.Querier, s *Settings) error {
	_, err := q.Exec(`INSERT INTO report_settings (id, company_name, company_name_en, logo_path, personnel_json, cvss_version, updated_at)
		VALUES (1, $1, $2, $3, $4::jsonb, $5, now())
		ON CONFLICT (id) DO UPDATE SET company_name=EXCLUDED.company_name, company_name_en=EXCLUDED.company_name_en,
			logo_path=EXCLUDED.logo_path, personnel_json=EXCLUDED.personnel_json, cvss_version=EXCLUDED.cvss_version, updated_at=now()`,
		s.CompanyName, s.CompanyNameEn, s.LogoPath, personnelJSON(s.Personnel), defStr(s.CVSSVersion, defaultCVSSVersion))
	return err
}

// SetLogo 只更新组织默认 logo 路径（设置页单独换图时用，不动其余字段）。
func SetLogo(q store.Querier, path string) error {
	_, err := q.Exec(`INSERT INTO report_settings (id, logo_path, updated_at) VALUES (1, $1, now())
		ON CONFLICT (id) DO UPDATE SET logo_path=EXCLUDED.logo_path, updated_at=now()`, path)
	return err
}

// Record 是一条已生成报告的元数据（文件本体在磁盘，路径见 Path）。
type Record struct {
	ID        int64     `json:"id"`
	TaskID    *int64    `json:"task_id,omitempty"`
	TaskDesc  string    `json:"task_desc"`
	Filename  string    `json:"filename"`
	Path      string    `json:"-"`
	Size      int64     `json:"size"`
	Company   string    `json:"company"`
	CreatedAt time.Time `json:"created_at"`
}

// AddRecord 记录一条生成的报告；taskID <= 0 落 NULL（表示不挂在某个任务下）。
func AddRecord(q store.Querier, taskID int64, taskDesc, filename, path string, size int64, company string) (int64, error) {
	var tid *int64
	if taskID > 0 {
		tid = &taskID
	}
	var id int64
	err := q.QueryRow(`INSERT INTO reports (task_id, task_desc, filename, path, size, company)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`, tid, taskDesc, filename, path, size, company).Scan(&id)
	return id, err
}

// ListRecords 按生成时间倒序列出报告；limit <= 0 取 200。
func ListRecords(q store.Querier, limit int) ([]*Record, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := q.Query(`SELECT id, task_id, task_desc, filename, path, size, company, created_at
		FROM reports ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Record{}
	for rows.Next() {
		r := &Record{}
		if err := rows.Scan(&r.ID, &r.TaskID, &r.TaskDesc, &r.Filename, &r.Path, &r.Size, &r.Company, &r.CreatedAt); err != nil {
			return out, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetRecord 取一条报告记录；不存在返回 (nil, nil)。
func GetRecord(q store.Querier, id int64) (*Record, error) {
	r := &Record{}
	err := q.QueryRow(`SELECT id, task_id, task_desc, filename, path, size, company, created_at FROM reports WHERE id=$1`, id).
		Scan(&r.ID, &r.TaskID, &r.TaskDesc, &r.Filename, &r.Path, &r.Size, &r.Company, &r.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return r, err
}

// DeleteRecord 只删记录，磁盘上的 .docx 由调用方删（它才持有 dataDir 能解析可移植路径）。
func DeleteRecord(q store.Querier, id int64) error {
	_, err := q.Exec(`DELETE FROM reports WHERE id=$1`, id)
	return err
}

// Template 是可复用的报告封面/组织预设（表 report_templates）。
type Template struct {
	ID            int64           `json:"id"`
	Name          string          `json:"name"`
	CompanyName   string          `json:"company_name"`
	CompanyNameEn string          `json:"company_name_en"`
	LogoPath      string          `json:"logo_path"`
	Personnel     json.RawMessage `json:"personnel"` // [{name,role,contact}]
	CVSSVersion   string          `json:"cvss_version"`
	Locale        string          `json:"locale"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

const templateCols = `id, name, company_name, company_name_en, logo_path, personnel_json::text, cvss_version, locale, created_at, updated_at`

func scanTemplate(sc interface{ Scan(...any) error }) (*Template, error) {
	t := &Template{}
	var personnel string
	if err := sc.Scan(&t.ID, &t.Name, &t.CompanyName, &t.CompanyNameEn, &t.LogoPath, &personnel,
		&t.CVSSVersion, &t.Locale, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil, err
	}
	t.Personnel = json.RawMessage(personnel)
	return t, nil
}

// ListTemplates 按创建时间倒序列出全部模板。
func ListTemplates(q store.Querier) ([]*Template, error) {
	rows, err := q.Query(`SELECT ` + templateCols + ` FROM report_templates ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Template{}
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return out, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetTemplate 取一个模板；不存在返回 (nil, nil)。
func GetTemplate(q store.Querier, id int64) (*Template, error) {
	t, err := scanTemplate(q.QueryRow(`SELECT `+templateCols+` FROM report_templates WHERE id=$1`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return t, err
}

// AddTemplate 插入模板并返回 id；CVSS 与 locale 为空时落默认值。
func AddTemplate(q store.Querier, t *Template) (int64, error) {
	var id int64
	err := q.QueryRow(`INSERT INTO report_templates (name, company_name, company_name_en, logo_path, personnel_json, cvss_version, locale)
		VALUES ($1,$2,$3,$4,$5::jsonb,$6,$7) RETURNING id`,
		t.Name, t.CompanyName, t.CompanyNameEn, t.LogoPath, personnelJSON(t.Personnel),
		defStr(t.CVSSVersion, defaultCVSSVersion), defStr(t.Locale, defaultLocale)).Scan(&id)
	return id, err
}

// UpdateTemplate 覆盖已有模板（按 t.ID）。
func UpdateTemplate(q store.Querier, t *Template) error {
	_, err := q.Exec(`UPDATE report_templates SET name=$1, company_name=$2, company_name_en=$3, logo_path=$4,
		personnel_json=$5::jsonb, cvss_version=$6, locale=$7, updated_at=now() WHERE id=$8`,
		t.Name, t.CompanyName, t.CompanyNameEn, t.LogoPath, personnelJSON(t.Personnel),
		defStr(t.CVSSVersion, defaultCVSSVersion), defStr(t.Locale, defaultLocale), t.ID)
	return err
}

// DeleteTemplate 按 id 删除模板。
func DeleteTemplate(q store.Querier, id int64) error {
	_, err := q.Exec(`DELETE FROM report_templates WHERE id=$1`, id)
	return err
}

// ---- 内部辅助 ----

// personnelJSON 把可能为空的 personnel 归一化成 "[]"，避免落库 NULL。
func personnelJSON(p json.RawMessage) string {
	if len(p) == 0 {
		return "[]"
	}
	return string(p)
}

// defStr 给空字符串兜默认值。
func defStr(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
