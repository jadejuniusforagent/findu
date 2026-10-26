package store

import (
	"context"
	"database/sql"
)

// 迁移期数据库能力接口。
//
// 背景：canonical 五实体用 pgx（见 pg.go），而 Step 3 从 db 包搬出来的存量 SQL 走
// database/sql。两套驱动在一个包里并存是有意的过渡形态——先把 SQL 按域搬出 20.5k 行的
// db 包，等全部搬完、事务边界收口到 core 后，再统一驱动。
//
// 取窄接口而不是直接依赖 *sql.DB 的原因：db.DB 是 struct{ *sql.DB }，天然满足，
// 于是迁移期可以在 db 包留一层转发而调用点零改动；同时单测可传假实现。

// Querier 是无 context 的最小能力集（存量 SQL 大多没带 ctx）。
type Querier interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// ContextQuerier 在上面基础上加 context 版本，给需要超时/取消的调用点用。
type ContextQuerier interface {
	Querier
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Beginner 给需要事务的存量 SQL 用。迁移期事务边界还没上收，先把「能开事务」
// 这件事表达成一个窄接口，而不是让 domain 包依赖 *sql.DB。
// db.DB（struct{ *sql.DB }）与 *sql.DB 都满足它。
type Beginner interface {
	Querier
	Begin() (*sql.Tx, error)
}

// ContextBeginner 在上面基础上加 BeginTx，给必须用 context 取消/超时的事务用
// （如通知投递的领取：整轮分派要在一个事务里领取事件并展开投递）。
type ContextBeginner interface {
	ContextQuerier
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}
