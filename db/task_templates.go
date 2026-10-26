package db

// 任务模板存储已迁到 domain/task（Step 3 批 3 第三刀）。
//
// 本文件只保留类型/常量别名与方法转发，调用点零改动。
// 规则快照 intercept_rules 的 JSONB 序列化/反序列化也一并搬走（永远写数组不写 null）。
//
// 原实现（217 行、6 个方法、1 张表）见 git 历史。

import "finduai/domain/task"

func (d *DB) CreateTaskTemplate(in task.TemplateInput) (*task.Template, error) {
	return task.CreateTemplate(d, in)
}

func (d *DB) ListTaskTemplates() ([]*task.Template, error) { return task.ListTemplates(d) }

func (d *DB) GetTaskTemplate(id int64) (*task.Template, error) { return task.GetTemplate(d, id) }

func (d *DB) UpdateTaskTemplate(id int64, in task.TemplateInput) (*task.Template, error) {
	return task.UpdateTemplate(d, id, in)
}

func (d *DB) PatchTaskTemplate(id int64, patch task.TemplatePatch) (*task.Template, error) {
	return task.PatchTemplate(d, id, patch)
}

func (d *DB) DeleteTaskTemplate(id int64) (bool, error) { return task.DeleteTemplate(d, id) }
