package exploration

// 探索根（explorations 表）的读写。任务域在建任务事务里调用它们（经 db 转发）。
import (
	"database/sql"

	"finduai/core/store"
)

// CreateExploration creates a new exploration root and returns its id.
func CreateExploration(q store.Querier, description, goal string) (int64, error) {
	var id int64
	err := q.QueryRow(`INSERT INTO explorations(description, goal) VALUES ($1, $2) RETURNING id`, description, goal).Scan(&id)
	return id, err
}

// Root returns the exploration's description and goal.
func Root(q store.Querier, expID int64) (description, goal string, err error) {
	var d sql.NullString
	err = q.QueryRow(`SELECT description, goal FROM explorations WHERE id=$1`, expID).Scan(&d, &goal)
	return d.String, goal, err
}
