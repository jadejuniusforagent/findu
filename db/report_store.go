package db

// 报告域存储已迁到 domain/report（Step 3 批 3 第一刀）。
//
// 本文件只保留类型别名与方法转发，调用点（server/*、内部各包）零改动。
// 这样一次只搬一个域的 SQL，任何一批编译失败都能立刻定位到该域；
// 等批 4/5 减薄 handler 时再逐处删别名、让调用方直接依赖 domain/report。
//
// 原实现（219 行、11 个方法、3 张表）见 git 历史：本文件上一版。

import "finduai/domain/report"

// ---- 组织级报告设置（表 report_settings，单例 id=1）----

func (d *DB) GetReportSettings() (*report.Settings, error) { return report.GetSettings(d) }

func (d *DB) SaveReportSettings(rs *report.Settings) error { return report.SaveSettings(d, rs) }

func (d *DB) SetReportLogo(path string) error { return report.SetLogo(d, path) }

// ---- 已生成报告记录（表 reports）----

func (d *DB) AddReport(taskID int64, taskDesc, filename, path string, size int64, company string) (int64, error) {
	return report.AddRecord(d, taskID, taskDesc, filename, path, size, company)
}

func (d *DB) ListReports(limit int) ([]*report.Record, error) { return report.ListRecords(d, limit) }

func (d *DB) GetReport(id int64) (*report.Record, error) { return report.GetRecord(d, id) }

func (d *DB) DeleteReport(id int64) error { return report.DeleteRecord(d, id) }

// ---- 可复用报告模板（表 report_templates）----

func (d *DB) ListReportTemplates() ([]*report.Template, error) { return report.ListTemplates(d) }

func (d *DB) GetReportTemplate(id int64) (*report.Template, error) { return report.GetTemplate(d, id) }

func (d *DB) AddReportTemplate(rt *report.Template) (int64, error) { return report.AddTemplate(d, rt) }

func (d *DB) UpdateReportTemplate(rt *report.Template) error { return report.UpdateTemplate(d, rt) }

func (d *DB) DeleteReportTemplate(id int64) error { return report.DeleteTemplate(d, id) }
