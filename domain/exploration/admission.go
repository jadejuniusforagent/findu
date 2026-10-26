package exploration

// 意图准入的补偿删除。任务准入失败时由 task 域调用（gate 由调用方持有），
// 事务跨界的是删除副作用（activity 显式清、节点删除 CASCADE 走边与锚点），
// 表全部属于探索域，故随域走（原 db/intent_admission.go，批 3 第九刀上提）。

import (
	"fmt"

	"finduai/core/store"
)

// DiscardOpenIntent compensates a follow-up creation when task admission fails.
// The task execution gate must still be held by the caller, so the intent cannot
// be claimed between this check and deletion. Edges and anchors cascade with the
// node; activity is deleted explicitly because its node FK otherwise becomes NULL.
func DiscardOpenIntent(b store.Beginner, expID, id int64) error {
	tx, err := b.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM activity WHERE exploration_id=$1 AND node_id=$2`, expID, id); err != nil {
		return err
	}
	res, err := tx.Exec(`DELETE FROM exploration_nodes
		WHERE id=$1 AND exploration_id=$2 AND kind='intent' AND state='open'`, id, expID)
	if err != nil {
		return err
	}
	removed, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if removed != 1 {
		return fmt.Errorf("open intent %d was not available for admission rollback", id)
	}
	return tx.Commit()
}
