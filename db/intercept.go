package db

// 会话审批拦截的存储层已迁到 domain/conversation（Step 3 批 3 第五刀）：
// 全局规则表 intercept_rules 与审批流水表 intercept_pending 的全部 SQL。
//
// 本文件保留：
//  1. 类型别名 —— 调用点（server/*、intercept/*）零改动；
//  2. 方法转发 —— db.DB 天然满足 store.Querier，把 d 传下去即可。
//
// 注意两点：
//   - audit 相关类型与决策状态机在 db/intercept_detail.go；
//   - GetInterceptExecution（执行导航）留在 db 装配层（见该文件头），不在本包转发范围。
//
// ownership 元数据里这两张表是 plugin-hitl；Step 4 HITL 插件化时数据面归插件，
// 插件侧将直接复用 domain/conversation，不重复实现。

import (
	dconv "finduai/domain/conversation"
)

// ---- 全局规则（表 intercept_rules）----

func (d *DB) ListInterceptRules() ([]dconv.InterceptRule, error) {
	return dconv.ListInterceptRules(d)
}

func (d *DB) CreateInterceptRule(name, matchTarget, matchType, pattern, action, message string, priority int, enabled bool, timeoutEnabled bool, timeoutSeconds int, timeoutAction string) (dconv.InterceptRule, error) {
	return dconv.CreateInterceptRule(d, name, matchTarget, matchType, pattern, action, message, priority, enabled, timeoutEnabled, timeoutSeconds, timeoutAction)
}

func (d *DB) UpdateInterceptRule(id int64, name, matchTarget, matchType, pattern, action, message string, priority int, enabled bool, timeoutEnabled bool, timeoutSeconds int, timeoutAction string) (dconv.InterceptRule, error) {
	return dconv.UpdateInterceptRule(d, id, name, matchTarget, matchType, pattern, action, message, priority, enabled, timeoutEnabled, timeoutSeconds, timeoutAction)
}

func (d *DB) DeleteInterceptRule(id int64) error {
	return dconv.DeleteInterceptRule(d, id)
}

func (d *DB) ToggleInterceptRule(id int64, enabled bool) error {
	return dconv.ToggleInterceptRule(d, id, enabled)
}

// ---- 审批流水（表 intercept_pending）----

func (d *DB) CreateInterceptPending(ruleID int64, convID string, taskID, agentName, toolName string, input []byte, reason string, audits ...*dconv.InterceptAudit) (int64, error) {
	return dconv.CreateInterceptPending(d, ruleID, convID, taskID, agentName, toolName, input, reason, audits...)
}

func (d *DB) DecideInterceptPending(id int64, status string) error {
	return dconv.DecideInterceptPending(d, id, status)
}

func (d *DB) CreateDecidedIntercept(ruleID int64, convID string, taskID, agentName, toolName string, input []byte, status, reason string, audits ...*dconv.InterceptAudit) (int64, error) {
	return dconv.CreateDecidedIntercept(d, ruleID, convID, taskID, agentName, toolName, input, status, reason, audits...)
}

func (d *DB) ListPendingIntercepts() ([]dconv.InterceptPending, error) {
	return dconv.ListPendingIntercepts(d)
}

func (d *DB) GetInterceptPending(id int64) (*dconv.InterceptPending, error) {
	return dconv.GetInterceptPending(d, id)
}

func (d *DB) ListAllIntercepts(limit int) ([]dconv.InterceptApprovalRow, error) {
	return dconv.ListAllIntercepts(d, limit)
}

func (d *DB) ListAllInterceptsPage(page, size int, filter dconv.InterceptApprovalFilter) ([]dconv.InterceptApprovalRow, int, error) {
	return dconv.ListAllInterceptsPage(d, page, size, filter)
}

func (d *DB) ListTaskIntercepts(taskID string) ([]dconv.InterceptApprovalRow, error) {
	return dconv.ListTaskIntercepts(d, taskID)
}

func (d *DB) ListTaskInterceptsPage(taskID string, page, size int, filter dconv.InterceptApprovalFilter) ([]dconv.InterceptApprovalRow, int, error) {
	return dconv.ListTaskInterceptsPage(d, taskID, page, size, filter)
}
