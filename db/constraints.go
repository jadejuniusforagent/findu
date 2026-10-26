package db

// 任务约束存储已迁到 domain/exploration（Step 3 批 3 第三刀）。
//
// 归属域判定：task_constraints.exploration_id → explorations ON DELETE CASCADE，
// 属主是探索域而不是任务域，所以放 domain/exploration；本文件只做转发，
// ExplorationStore 的方法签名（无参、用 s.expID）保持不变，调用点零改动。
//
// 原实现（85 行、4 个方法）见 git 历史。

import "finduai/domain/exploration"

func (s *ExplorationStore) ListConstraints() ([]exploration.Constraint, error) {
	return exploration.ListConstraints(s.db, s.expID)
}

func (s *ExplorationStore) AddConstraint(kind, text, origin string) (int64, error) {
	return exploration.AddConstraint(s.db, s.expID, kind, text, origin)
}

func (s *ExplorationStore) UpdateConstraint(id int64, kind, text string) error {
	return exploration.UpdateConstraint(s.db, s.expID, id, kind, text)
}

func (s *ExplorationStore) DeleteConstraint(id int64) error {
	return exploration.DeleteConstraint(s.db, s.expID, id)
}
