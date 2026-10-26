package db

// 审批的审计证据与决策状态机已迁到 domain/conversation（Step 3 批 3 第五刀）。
// 本文件只保留类型别名与三个方法转发；GetInterceptExecution（执行导航）因为
// 跨读 activity / conversation_activities / tasks 三个外部域的表，按规范留在
// db 装配层，见 db/intercept_execution.go。

import (
	dconv "finduai/domain/conversation"
)

func (d *DB) GetInterceptDetail(id int64) (*dconv.InterceptDetail, error) {
	return dconv.GetInterceptDetail(d, id)
}

func (d *DB) ResolveIntercept(id int64, status, action, reason string) (bool, error) {
	return dconv.ResolveIntercept(d, id, status, action, reason)
}

func (d *DB) CompleteIntercept(id int64, runID, toolUseID, status, output string, truncated bool) error {
	return dconv.CompleteIntercept(d, id, runID, toolUseID, status, output, truncated)
}
