package store

// 本文件是 canonical 五实体的 pgx 实现。
// 注意：这些实现尚未接线（Step 3 由 adapter 注入），因此当前没有集成测试覆盖，
// 只保证编译通过与 SQL 形状与 0001 迁移一致。

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PG 持有连接池，提供五个仓储。
type PG struct {
	Pool *pgxpool.Pool
}

// NewPG 用给定连接池构造仓储集合。
func NewPG(pool *pgxpool.Pool) Repos {
	p := &PG{Pool: pool}
	return Repos{
		Session:  &pgSession{p},
		Turn:     &pgTurn{p},
		Message:  &pgMessage{p},
		ToolCall: &pgToolCall{p},
		Artifact: &pgArtifact{p},
	}
}

func notFound(err error) error {
	if err == pgx.ErrNoRows {
		return ErrNotFound
	}
	return err
}

// ---------- session ----------

type pgSession struct{ p *PG }

func (r *pgSession) Create(ctx context.Context, s Session) error {
	_, err := r.p.Pool.Exec(ctx, `INSERT INTO canonical_session (session_id, project_id, adapter, title, status, meta, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		s.SessionID, s.ProjectID, s.Adapter, s.Title, s.Status, s.Meta, s.CreatedAt, s.UpdatedAt)
	return err
}

func (r *pgSession) Get(ctx context.Context, id string) (Session, error) {
	var s Session
	err := r.p.Pool.QueryRow(ctx, `SELECT session_id, project_id, adapter, title, status, meta, created_at, updated_at FROM canonical_session WHERE session_id = $1`, id).Scan(
		&s.SessionID, &s.ProjectID, &s.Adapter, &s.Title, &s.Status, &s.Meta, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return s, notFound(err)
	}
	return s, nil
}

func (r *pgSession) List(ctx context.Context, projectID string, p Page) ([]Session, string, error) {
	limit := p.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.p.Pool.Query(ctx, `SELECT session_id, project_id, adapter, title, status, meta, created_at, updated_at FROM canonical_session WHERE ($1 = '' OR project_id = $1) AND ($2 = '' OR created_at < $2::timestamptz) ORDER BY created_at DESC, session_id DESC LIMIT $3`, projectID, p.Cursor, limit)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var s Session
		if err := rows.Scan(&s.SessionID, &s.ProjectID, &s.Adapter, &s.Title, &s.Status, &s.Meta, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, "", err
		}
		out = append(out, s)
	}
	if len(out) == limit {
		return out, out[len(out)-1].CreatedAt.Format(time.RFC3339Nano), rows.Err()
	}
	return out, "", rows.Err()
}

func (r *pgSession) UpdateStatus(ctx context.Context, id, status string) error {
	_, err := r.p.Pool.Exec(ctx, `UPDATE canonical_session SET status = $2, updated_at = now() WHERE session_id = $1`, id, status)
	return err
}

func (r *pgSession) Close(ctx context.Context, id string) error {
	return r.UpdateStatus(ctx, id, "closed")
}

// ---------- turn ----------

type pgTurn struct{ p *PG }

func (r *pgTurn) Append(ctx context.Context, t Turn) (string, error) {
	_, err := r.p.Pool.Exec(ctx, `INSERT INTO canonical_turn (turn_id, session_id, role, status, seq, created_at) VALUES ($1,$2,$3,$4, COALESCE((SELECT max(seq)+1 FROM canonical_turn WHERE session_id = $2), 0), $5)`,
		t.TurnID, t.SessionID, t.Role, t.Status, t.CreatedAt)
	if err != nil {
		return "", err
	}
	return t.TurnID, nil
}

func (r *pgTurn) ListBySession(ctx context.Context, sessionID string, p Page) ([]Turn, string, error) {
	limit := p.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := r.p.Pool.Query(ctx, `SELECT turn_id, session_id, role, status, seq, created_at FROM canonical_turn WHERE session_id = $1 ORDER BY seq ASC LIMIT $2`, sessionID, limit)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []Turn
	for rows.Next() {
		var t Turn
		if err := rows.Scan(&t.TurnID, &t.SessionID, &t.Role, &t.Status, &t.Seq, &t.CreatedAt); err != nil {
			return nil, "", err
		}
		out = append(out, t)
	}
	return out, "", rows.Err()
}

func (r *pgTurn) GetLatest(ctx context.Context, sessionID string) (Turn, error) {
	var t Turn
	err := r.p.Pool.QueryRow(ctx, `SELECT turn_id, session_id, role, status, seq, created_at FROM canonical_turn WHERE session_id = $1 ORDER BY seq DESC LIMIT 1`, sessionID).Scan(
		&t.TurnID, &t.SessionID, &t.Role, &t.Status, &t.Seq, &t.CreatedAt)
	if err != nil {
		return t, notFound(err)
	}
	return t, nil
}

// ---------- message ----------

type pgMessage struct{ p *PG }

func (r *pgMessage) Append(ctx context.Context, m Message) (string, error) {
	_, err := r.p.Pool.Exec(ctx, `INSERT INTO canonical_message (message_id, turn_id, role, content, content_parts, created_at) VALUES ($1,$2,$3,$4,$5,$6)`,
		m.MessageID, m.TurnID, m.Role, m.Content, m.Parts, m.CreatedAt)
	if err != nil {
		return "", err
	}
	return m.MessageID, nil
}

func (r *pgMessage) ListByTurn(ctx context.Context, turnID string) ([]Message, error) {
	rows, err := r.p.Pool.Query(ctx, `SELECT message_id, turn_id, role, content, content_parts, created_at FROM canonical_message WHERE turn_id = $1 ORDER BY created_at ASC`, turnID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.MessageID, &m.TurnID, &m.Role, &m.Content, &m.Parts, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *pgMessage) ListBySession(ctx context.Context, sessionID string, p Page) ([]Message, string, error) {
	limit := p.Limit
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := r.p.Pool.Query(ctx, `SELECT m.message_id, m.turn_id, m.role, m.content, m.content_parts, m.created_at FROM canonical_message m JOIN canonical_turn t ON t.turn_id = m.turn_id WHERE t.session_id = $1 ORDER BY m.created_at ASC LIMIT $2`, sessionID, limit)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.MessageID, &m.TurnID, &m.Role, &m.Content, &m.Parts, &m.CreatedAt); err != nil {
			return nil, "", err
		}
		out = append(out, m)
	}
	return out, "", rows.Err()
}

// ---------- tool call ----------

type pgToolCall struct{ p *PG }

func (r *pgToolCall) Start(ctx context.Context, c ToolCall) error {
	_, err := r.p.Pool.Exec(ctx, `INSERT INTO canonical_tool_call (call_id, turn_id, tool_name, input, status, started_at) VALUES ($1,$2,$3,$4,'running',$5)`,
		c.CallID, c.TurnID, c.ToolName, c.Input, c.StartedAt)
	return err
}

func (r *pgToolCall) Finish(ctx context.Context, callID, status, errMsg string, output map[string]any, durationMS int) error {
	_, err := r.p.Pool.Exec(ctx, `UPDATE canonical_tool_call SET status = $2, error = $3, output = $4, finished_at = now(), duration_ms = $5 WHERE call_id = $1`,
		callID, status, errMsg, output, durationMS)
	return err
}

func (r *pgToolCall) ListByTurn(ctx context.Context, turnID string) ([]ToolCall, error) {
	rows, err := r.p.Pool.Query(ctx, `SELECT call_id, turn_id, tool_name, input, output, status, COALESCE(error,''), started_at, finished_at, COALESCE(duration_ms,0) FROM canonical_tool_call WHERE turn_id = $1 ORDER BY started_at ASC`, turnID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ToolCall
	for rows.Next() {
		var c ToolCall
		if err := rows.Scan(&c.CallID, &c.TurnID, &c.ToolName, &c.Input, &c.Output, &c.Status, &c.Error, &c.StartedAt, &c.FinishedAt, &c.DurationMS); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *pgToolCall) Stats(ctx context.Context, since time.Time) (map[string]int, error) {
	rows, err := r.p.Pool.Query(ctx, `SELECT tool_name, count(*) FROM canonical_tool_call WHERE started_at >= $1 GROUP BY tool_name`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var name string
		var n int
		if err := rows.Scan(&name, &n); err != nil {
			return nil, err
		}
		out[name] = n
	}
	return out, rows.Err()
}

// ---------- artifact ----------

type pgArtifact struct{ p *PG }

func (r *pgArtifact) Put(ctx context.Context, a Artifact) error {
	_, err := r.p.Pool.Exec(ctx, `INSERT INTO canonical_artifact (artifact_id, session_id, turn_id, kind, path, size, sha256, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		a.ArtifactID, a.SessionID, a.TurnID, a.Kind, a.Path, a.Size, a.SHA256, a.CreatedAt)
	return err
}

func (r *pgArtifact) Get(ctx context.Context, id string) (Artifact, error) {
	var a Artifact
	err := r.p.Pool.QueryRow(ctx, `SELECT artifact_id, COALESCE(session_id,''), COALESCE(turn_id,''), kind, path, COALESCE(size,0), COALESCE(sha256,''), created_at FROM canonical_artifact WHERE artifact_id = $1`, id).Scan(
		&a.ArtifactID, &a.SessionID, &a.TurnID, &a.Kind, &a.Path, &a.Size, &a.SHA256, &a.CreatedAt)
	if err != nil {
		return a, notFound(err)
	}
	return a, nil
}

func (r *pgArtifact) ListBySession(ctx context.Context, sessionID string) ([]Artifact, error) {
	rows, err := r.p.Pool.Query(ctx, `SELECT artifact_id, COALESCE(session_id,''), COALESCE(turn_id,''), kind, path, COALESCE(size,0), COALESCE(sha256,''), created_at FROM canonical_artifact WHERE session_id = $1 ORDER BY created_at DESC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Artifact
	for rows.Next() {
		var a Artifact
		if err := rows.Scan(&a.ArtifactID, &a.SessionID, &a.TurnID, &a.Kind, &a.Path, &a.Size, &a.SHA256, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
