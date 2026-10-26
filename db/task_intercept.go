package db

// 任务级拦截/允许规则存储已迁到 domain/task（Step 3 批 3 第三刀）。
//
// 本文件只保留别名与方法转发。规则的值类型 task.InterceptRule 同时被全局规则表
// 复用 —— db.AssetInterceptRule 是它的类型别名（见 asset_intercept.go）。
// 删除语义：task_intercept_rules.task_id → tasks ON DELETE CASCADE，随任务消失。
//
// 原实现（190 行、7 个方法 + 1 个事务内批量插入）见 git 历史。

import "finduai/domain/task"

// ListTaskInterceptRules 返回一个任务的全部规则（block 与 allow）；
// taskID <= 0 返回空。
func (s *AssetStore) ListTaskInterceptRules(taskID int64) ([]task.InterceptRule, error) {
	return task.ListInterceptRules(s.db, taskID)
}

// TaskInterceptRulesSplit 把规则拆成 block / allow 两组，给执行判定用。
func (s *AssetStore) TaskInterceptRulesSplit(taskID int64) (block, allow []task.InterceptRule, err error) {
	return task.SplitInterceptRules(s.db, taskID)
}

func (s *AssetStore) CreateTaskInterceptRule(taskID int64, action, kind, pattern, note string, enabled bool) (task.InterceptRule, error) {
	return task.CreateInterceptRule(s.db, taskID, action, kind, pattern, note, enabled)
}

func (s *AssetStore) UpdateTaskInterceptRule(taskID, ruleID int64, action, kind, pattern, note string, enabled bool) (task.InterceptRule, error) {
	return task.UpdateInterceptRule(s.db, taskID, ruleID, action, kind, pattern, note, enabled)
}

func (s *AssetStore) DeleteTaskInterceptRule(taskID, ruleID int64) (bool, error) {
	return task.DeleteInterceptRule(s.db, taskID, ruleID)
}

func (s *AssetStore) ToggleTaskInterceptRule(taskID, ruleID int64, enabled bool) error {
	return task.ToggleInterceptRule(s.db, taskID, ruleID, enabled)
}
