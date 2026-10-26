package db

// 任务分类存储已迁到 domain/task（Step 3 批 3 第三刀）。
//
// 本文件只保留类型/常量别名与方法转发，调用点（server/*、内部各包）零改动。
// 删除语义：删分类不动任务 —— 由 tasks.category_id 的 ON DELETE SET NULL 保证。
//
// 原实现（242 行、7 个方法、2 张表）见 git 历史。

import "finduai/domain/task"

func (d *DB) CreateTaskCategory(name string) (*task.Category, error) {
	return task.CreateCategory(d, name)
}

func (d *DB) ListTaskCategories() ([]*task.Category, error) { return task.ListCategories(d) }

func (d *DB) GetTaskCategory(id int64) (*task.Category, error) { return task.GetCategory(d, id) }

func (d *DB) RenameTaskCategory(id int64, name string) (*task.Category, error) {
	return task.RenameCategory(d, id, name)
}

func (d *DB) DeleteTaskCategory(id int64) (bool, error) { return task.DeleteCategory(d, id) }

func (d *DB) SetTaskCategory(taskID int64, categoryID *int64) (*task.Category, error) {
	return task.SetCategory(d, taskID, categoryID)
}

func (d *DB) SetTasksCategory(taskIDs []int64, categoryID *int64) ([]int64, *task.Category, error) {
	return task.SetTasksCategory(d, taskIDs, categoryID)
}
