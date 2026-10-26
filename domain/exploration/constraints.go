// 任务约束（表 task_constraints）。
//
// 每行属于一个 exploration（exploration_id REFERENCES explorations ON DELETE CASCADE），
// 所以归属探索域而不是任务域：任务删除会连带 exploration，约束随 exploration 一起消失。
// 操作者/分解器写入的 allow|deny 自由文本，注入 planner/worker 的系统提示。
package exploration

import (
	"fmt"
	"time"

	"finduai/core/store"
)

// Constraint 是一条操作约束：kind=allow（允许的操作）或 kind=deny（禁止的操作）。
type Constraint struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"` // allow | deny
	Text      string    `json:"text"`
	Origin    string    `json:"origin,omitempty"` // goals | human | system
	CreatedAt time.Time `json:"created_at"`
}

// ListConstraints 返回该探索的约束：allow 在前、deny 在后，组内按 id 升序
// （提示块与 UI 的渲染顺序要稳定）。
func ListConstraints(q store.Querier, expID int64) ([]Constraint, error) {
	rows, err := q.Query(`
SELECT id, kind, text, COALESCE(origin,''), created_at
FROM task_constraints WHERE exploration_id=$1
ORDER BY (kind='deny'), id`, expID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Constraint
	for rows.Next() {
		var c Constraint
		if err := rows.Scan(&c.ID, &c.Kind, &c.Text, &c.Origin, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// AddConstraint 插入一条约束（kind 必须是 allow|deny），返回 id。
func AddConstraint(q store.Querier, expID int64, kind, text, origin string) (int64, error) {
	if kind != "allow" && kind != "deny" {
		return 0, fmt.Errorf("kind 必须是 allow 或 deny")
	}
	if origin == "" {
		origin = "system"
	}
	var id int64
	err := q.QueryRow(`
INSERT INTO task_constraints(exploration_id, kind, text, origin)
VALUES ($1, $2, $3, $4) RETURNING id`, expID, kind, text, origin).Scan(&id)
	return id, err
}

// UpdateConstraint 改写 kind + text（限定在本探索内）。不存在时返回错误。
func UpdateConstraint(q store.Querier, expID, id int64, kind, text string) error {
	if kind != "allow" && kind != "deny" {
		return fmt.Errorf("kind 必须是 allow 或 deny")
	}
	res, err := q.Exec(`
UPDATE task_constraints SET kind=$1, text=$2, updated_at=now()
WHERE id=$3 AND exploration_id=$4`, kind, text, id, expID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("约束不存在")
	}
	return nil
}

// DeleteConstraint 删除一条约束（限定在本探索内）。不存在时返回错误。
func DeleteConstraint(q store.Querier, expID, id int64) error {
	res, err := q.Exec(`DELETE FROM task_constraints WHERE id=$1 AND exploration_id=$2`, id, expID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("约束不存在")
	}
	return nil
}
