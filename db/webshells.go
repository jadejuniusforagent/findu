package db

import (
	"database/sql"
	"strings"
	"time"
)

// WebShellConnection is the host-side projection of a findu/shared-PG row.
type WebShellConnection struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id,omitempty"`
	URL       string    `json:"url"`
	Password  string    `json:"password"`
	Type      string    `json:"type"`
	Method    string    `json:"method"`
	CmdParam  string    `json:"cmdParam"`
	Remark    string    `json:"remark"`
	Encoding  string    `json:"encoding"`
	OS        string    `json:"os"`
	CreatedAt time.Time `json:"createdAt"`
}

// WebShellStore operates on the shared webshell_connections table.
type WebShellStore struct{ db *DB }

func (d *DB) Webshells() *WebShellStore { return &WebShellStore{db: d} }

const webshellSelectCols = `id, COALESCE(project_id,''), url, COALESCE(password,''),
	COALESCE(type,'php'), COALESCE(method,'post'), COALESCE(cmd_param,''),
	COALESCE(remark,''), COALESCE(encoding,''), COALESCE(os,''), created_at`

func scanWebshell(row interface{ Scan(...any) error }) (*WebShellConnection, error) {
	var c WebShellConnection
	if err := row.Scan(&c.ID, &c.ProjectID, &c.URL, &c.Password, &c.Type, &c.Method,
		&c.CmdParam, &c.Remark, &c.Encoding, &c.OS, &c.CreatedAt); err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *WebShellStore) List(projectID string) ([]*WebShellConnection, error) {
	args := []any{strings.TrimSpace(projectID)}
	where := `WHERE ($1 = '' OR project_id = $1)`
	rows, err := s.db.Query(`SELECT `+webshellSelectCols+` FROM webshell_connections `+where+`
		ORDER BY created_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*WebShellConnection{}
	for rows.Next() {
		c, err := scanWebshell(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *WebShellStore) Get(id string) (*WebShellConnection, error) {
	return scanWebshell(s.db.QueryRow(`SELECT `+webshellSelectCols+` FROM webshell_connections WHERE id=$1`, strings.TrimSpace(id)))
}

func (s *WebShellStore) Create(c *WebShellConnection) error {
	if c == nil {
		return sql.ErrNoRows
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.Exec(`INSERT INTO webshell_connections
		(id, project_id, url, password, type, method, cmd_param, remark, encoding, os, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		c.ID, strings.TrimSpace(c.ProjectID), c.URL, c.Password, c.Type, c.Method,
		c.CmdParam, c.Remark, c.Encoding, c.OS, c.CreatedAt)
	return err
}

func (s *WebShellStore) Update(c *WebShellConnection) error {
	if c == nil {
		return sql.ErrNoRows
	}
	res, err := s.db.Exec(`UPDATE webshell_connections SET
		project_id=$2, url=$3, password=$4, type=$5, method=$6,
		cmd_param=$7, remark=$8, encoding=$9, os=$10
		WHERE id=$1`,
		c.ID, strings.TrimSpace(c.ProjectID), c.URL, c.Password, c.Type, c.Method,
		c.CmdParam, c.Remark, c.Encoding, c.OS)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// GetState returns the persisted state JSON for a connection ("{}" when absent).
func (s *WebShellStore) GetState(id string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT state_json FROM webshell_connection_states WHERE connection_id=$1`, strings.TrimSpace(id)).Scan(&v)
	if err == sql.ErrNoRows {
		return "{}", nil
	}
	if err != nil {
		return "", err
	}
	if v == "" {
		v = "{}"
	}
	return v, nil
}

// UpsertState persists the frontend connection state JSON (upsert on connection_id).
func (s *WebShellStore) UpsertState(id, stateJSON string) error {
	if stateJSON == "" {
		stateJSON = "{}"
	}
	_, err := s.db.Exec(`INSERT INTO webshell_connection_states (connection_id, state_json, updated_at)
		VALUES ($1,$2,$3)
		ON CONFLICT(connection_id) DO UPDATE SET
			state_json = excluded.state_json,
			updated_at = excluded.updated_at`,
		strings.TrimSpace(id), stateJSON, time.Now())
	return err
}

func (s *WebShellStore) Delete(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.Exec(`DELETE FROM webshell_connection_states WHERE connection_id=$1`, id); err != nil {
		return err
	}
	res, err := tx.Exec(`DELETE FROM webshell_connections WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}
