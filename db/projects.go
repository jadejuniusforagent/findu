package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Project is the host-side projection of a findu/shared-PG project row.
type Project struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	ScopeJSON   string    `json:"scope_json,omitempty"`
	Status      string    `json:"status"`
	Pinned      bool      `json:"pinned"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ProjectStore operates on the shared projects table.
type ProjectStore struct{ db *DB }

// Projects returns the project store.
func (d *DB) Projects() *ProjectStore { return &ProjectStore{db: d} }

const projectSelectCols = `id, name, COALESCE(description,''), COALESCE(scope_json,''),
	COALESCE(status,'active'), COALESCE(pinned,0), created_at, updated_at`

func projectPinnedInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func scanProject(row interface{ Scan(...any) error }) (*Project, error) {
	var p Project
	var pinned int
	if err := row.Scan(&p.ID, &p.Name, &p.Description, &p.ScopeJSON, &p.Status, &pinned, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	p.Pinned = pinned != 0
	return &p, nil
}

// List returns projects with optional status/search filters.
func (s *ProjectStore) List(status, search string, limit, offset int) ([]*Project, int, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}
	args := []any{strings.TrimSpace(status), strings.TrimSpace(search)}
	where := `WHERE ($1 = '' OR status = $1)
		AND ($2 = '' OR name ILIKE '%' || $2 || '%' OR COALESCE(description,'') ILIKE '%' || $2 || '%')`
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM projects `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.Query(`SELECT `+projectSelectCols+` FROM projects `+where+`
		ORDER BY pinned DESC, updated_at DESC LIMIT $3 OFFSET $4`,
		append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []*Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, p)
	}
	return out, total, rows.Err()
}

// Get returns one project by id.
func (s *ProjectStore) Get(id string) (*Project, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, sql.ErrNoRows
	}
	p, err := scanProject(s.db.QueryRow(`SELECT `+projectSelectCols+` FROM projects WHERE id = $1`, id))
	if err != nil {
		return nil, err
	}
	return p, nil
}

// Create inserts a project. Status defaults to active.
func (s *ProjectStore) Create(p *Project) (*Project, error) {
	if p == nil {
		return nil, fmt.Errorf("project is nil")
	}
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return nil, fmt.Errorf("项目名称不能为空")
	}
	if strings.TrimSpace(p.ID) == "" {
		p.ID = uuid.NewString()
	}
	p.Name = name
	p.Status = strings.TrimSpace(p.Status)
	if p.Status == "" {
		p.Status = "active"
	}
	p.UpdatedAt = time.Now().UTC()
	p.CreatedAt = p.UpdatedAt
	_, err := s.db.Exec(`INSERT INTO projects
		(id, name, description, scope_json, status, pinned, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		p.ID, p.Name, p.Description, p.ScopeJSON, p.Status, projectPinnedInt(p.Pinned), p.CreatedAt, p.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("创建项目失败: %w", err)
	}
	return p, nil
}

// Update writes the mutable project fields.
func (s *ProjectStore) Update(p *Project) error {
	if p == nil || strings.TrimSpace(p.ID) == "" {
		return fmt.Errorf("项目 ID 不能为空")
	}
	p.UpdatedAt = time.Now().UTC()
	res, err := s.db.Exec(`UPDATE projects SET
		name=$2, description=$3, scope_json=$4, status=$5, pinned=$6, updated_at=$7
		WHERE id=$1`,
		p.ID, p.Name, p.Description, p.ScopeJSON, p.Status, projectPinnedInt(p.Pinned), p.UpdatedAt)
	if err != nil {
		return fmt.Errorf("更新项目失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// Delete removes a project row. Cascading project bindings are intentionally
// left to the findu compatibility handler until the host owns those domains.
func (s *ProjectStore) Delete(id string) error {
	res, err := s.db.Exec(`DELETE FROM projects WHERE id=$1`, strings.TrimSpace(id))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
