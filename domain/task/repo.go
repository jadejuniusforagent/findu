// 本文件是任务域的存储层之一：任务分类（task_categories）与可复用任务模板
// （task_templates）。SQL 全部收敛在这里。
//
// 分层约定
//   - 只依赖 database/sql 与 core/store 的窄接口，不认识 db 包的 DB 类型；
//     迁移期 db 包保留类型别名 + 方法转发，调用点零改动。
//   - 事务用 store.Beginner 表达（迁移期还没有统一的事务边界），不直接依赖 *sql.DB。
//
// 删除语义（本刀重点，靠外键而不是靠代码里的级联）：
//   - tasks.category_id 与 task_templates.category_id 都是
//     REFERENCES task_categories(id) ON DELETE SET NULL；
//   - 所以删除分类只会把任务与模板退回「未分类」，既不删任务也不删模板。
//     这条由 db/task_config_store_test.go 的用例守着，改动 schema 时会立刻红。
package task

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"

	"finduai/core/store"
)

const (
	// MaxCategoryNameRunes 限制分类名长度（按 rune 计，不是字节）。
	MaxCategoryNameRunes = 80
	// MaxCategoryBatchSize 限制一次批量移动的任务数，避免单个请求锁住无上限的行。
	MaxCategoryBatchSize = 100

	MaxTemplateNameRunes = 120
	MaxTemplateTextRunes = 16000
)

var (
	ErrCategoryInvalid      = errors.New("invalid task category")
	ErrCategoryNameConflict = errors.New("task category name already exists")
	ErrCategoryNotFound     = errors.New("task category not found")
	ErrCategoryTaskNotFound = errors.New("task not found")

	ErrTemplateInvalid      = errors.New("invalid task template")
	ErrTemplateNameConflict = errors.New("task template name already exists")
	ErrTemplateNotFound     = errors.New("task template not found")
)

// Category 是全局复用的任务分组标签。TaskCount 是活任务数（软删的不算）。
type Category struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	NKey      string    `json:"-"`
	TaskCount int       `json:"task_count"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

const categoryCols = `category.id, category.name, category.nkey,
       count(task.id) FILTER (WHERE task.deleted_at IS NULL),
       category.created_at, category.updated_at`

func scanCategory(row interface{ Scan(...any) error }) (Category, error) {
	var c Category
	err := row.Scan(&c.ID, &c.Name, &c.NKey, &c.TaskCount, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}

// uniqueViolation 识别唯一键冲突（SQLSTATE 23505）。分类 nkey 与模板 nkey 都靠它
// 转成各自的 NameConflict 错误；不核对约束名，与原实现一致。
func uniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func normalizeCategoryName(name string) (string, string, error) {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return "", "", fmt.Errorf("%w: name is required", ErrCategoryInvalid)
	}
	if utf8.RuneCountInString(name) > MaxCategoryNameRunes {
		return "", "", fmt.Errorf("%w: name exceeds %d characters", ErrCategoryInvalid, MaxCategoryNameRunes)
	}
	return name, strings.ToLower(name), nil
}

// CreateCategory 插入一个分类；nkey 冲突时返回 ErrCategoryNameConflict。
func CreateCategory(q store.Querier, name string) (*Category, error) {
	name, nkey, err := normalizeCategoryName(name)
	if err != nil {
		return nil, err
	}
	category, err := scanCategory(q.QueryRow(`
WITH inserted AS (
    INSERT INTO task_categories(name, nkey)
    VALUES ($1,$2)
    ON CONFLICT (nkey) DO NOTHING
    RETURNING *
)
SELECT inserted.id, inserted.name, inserted.nkey, 0, inserted.created_at, inserted.updated_at
FROM inserted`, name, nkey))
	if err == sql.ErrNoRows {
		return nil, ErrCategoryNameConflict
	}
	if err != nil {
		return nil, err
	}
	return &category, nil
}

// ListCategories 按名字排序，附带每个分类下的活任务数。
func ListCategories(q store.Querier) ([]*Category, error) {
	rows, err := q.Query(`
SELECT ` + categoryCols + `
FROM task_categories category
LEFT JOIN tasks task ON task.category_id=category.id
GROUP BY category.id
ORDER BY category.name, category.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	categories := []*Category{}
	for rows.Next() {
		category, err := scanCategory(rows)
		if err != nil {
			return nil, err
		}
		categories = append(categories, &category)
	}
	return categories, rows.Err()
}

// GetCategory 返回一个分类；不存在时返回 (nil, nil)。
func GetCategory(q store.Querier, id int64) (*Category, error) {
	category, err := scanCategory(q.QueryRow(`
SELECT `+categoryCols+`
FROM task_categories category
LEFT JOIN tasks task ON task.category_id=category.id
WHERE category.id=$1
GROUP BY category.id`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &category, nil
}

// RenameCategory 改名；返回的行里已带上最新任务数。
func RenameCategory(q store.Querier, id int64, name string) (*Category, error) {
	name, nkey, err := normalizeCategoryName(name)
	if err != nil {
		return nil, err
	}
	category, err := scanCategory(q.QueryRow(`
WITH updated AS (
    UPDATE task_categories SET name=$2, nkey=$3 WHERE id=$1 RETURNING *
)
SELECT updated.id, updated.name, updated.nkey,
       (SELECT count(*) FROM tasks WHERE category_id=updated.id AND deleted_at IS NULL),
       updated.created_at, updated.updated_at
FROM updated`, id, name, nkey))
	if err == sql.ErrNoRows {
		return nil, ErrCategoryNotFound
	}
	if uniqueViolation(err) {
		return nil, ErrCategoryNameConflict
	}
	if err != nil {
		return nil, err
	}
	return &category, nil
}

// DeleteCategory 删除分类。受影响的任务由 tasks.category_id 的 ON DELETE SET NULL
// 退回未分类，这里不做任何手工级联。
func DeleteCategory(q store.Querier, id int64) (bool, error) {
	result, err := q.Exec(`DELETE FROM task_categories WHERE id=$1`, id)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows > 0, err
}

// SetCategory 把单个活任务移到一个分类；categoryID 为 nil 表示退回未分类。
func SetCategory(q store.Querier, taskID int64, categoryID *int64) (*Category, error) {
	if categoryID == nil {
		result, err := q.Exec(`UPDATE tasks SET category_id=NULL WHERE id=$1 AND deleted_at IS NULL`, taskID)
		if err != nil {
			return nil, err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return nil, err
		}
		if rows == 0 {
			return nil, ErrCategoryTaskNotFound
		}
		return nil, nil
	}
	if *categoryID <= 0 {
		return nil, fmt.Errorf("%w: category id must be positive", ErrCategoryInvalid)
	}
	category, err := scanCategory(q.QueryRow(`
WITH selected AS (
    SELECT * FROM task_categories WHERE id=$2
), updated AS (
    UPDATE tasks SET category_id=$2
    WHERE id=$1 AND deleted_at IS NULL AND EXISTS (SELECT 1 FROM selected)
    RETURNING id
)
SELECT selected.id, selected.name, selected.nkey,
       (SELECT count(*) FROM tasks WHERE category_id=selected.id AND deleted_at IS NULL),
       selected.created_at, selected.updated_at
FROM selected, updated`, taskID, *categoryID))
	if err == sql.ErrNoRows {
		var taskExists bool
		if checkErr := q.QueryRow(`SELECT EXISTS(SELECT 1 FROM tasks WHERE id=$1 AND deleted_at IS NULL)`, taskID).Scan(&taskExists); checkErr != nil {
			return nil, checkErr
		}
		if !taskExists {
			return nil, ErrCategoryTaskNotFound
		}
		return nil, ErrCategoryNotFound
	}
	if err != nil {
		return nil, err
	}
	return &category, nil
}

// SetTasksCategory 在一个事务里把若干任务移进同一分类（nil = 未分类），
// 半途失败不会留下部分更新。返回值：真正被更新的 id（两次点击之间被删掉的不在其中）
// 与刷新后的分类行（task_count 已包含本次移动）。
func SetTasksCategory(q store.Beginner, taskIDs []int64, categoryID *int64) ([]int64, *Category, error) {
	if len(taskIDs) == 0 {
		return nil, nil, fmt.Errorf("%w: task ids are required", ErrCategoryInvalid)
	}
	if len(taskIDs) > MaxCategoryBatchSize {
		return nil, nil, fmt.Errorf("%w: at most %d tasks per request", ErrCategoryInvalid, MaxCategoryBatchSize)
	}
	for _, id := range taskIDs {
		if id <= 0 {
			return nil, nil, fmt.Errorf("%w: task id must be positive", ErrCategoryInvalid)
		}
	}
	if categoryID != nil && *categoryID <= 0 {
		return nil, nil, fmt.Errorf("%w: category id must be positive", ErrCategoryInvalid)
	}
	tx, err := q.Begin()
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback() //nolint:errcheck

	// 在事务内校验分类存在，避免并发删除把下面的 UPDATE 变成外键冲突。
	if categoryID != nil {
		var exists bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM task_categories WHERE id=$1)`, *categoryID).Scan(&exists); err != nil {
			return nil, nil, err
		}
		if !exists {
			return nil, nil, ErrCategoryNotFound
		}
	}
	rows, err := tx.Query(`
UPDATE tasks SET category_id=$2
WHERE id=ANY($1::bigint[]) AND deleted_at IS NULL
RETURNING id`, taskIDs, categoryID)
	if err != nil {
		return nil, nil, err
	}
	updated := make([]int64, 0, len(taskIDs))
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, nil, err
		}
		updated = append(updated, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	var category *Category
	if categoryID != nil {
		fetched, err := scanCategory(tx.QueryRow(`
SELECT `+categoryCols+`
FROM task_categories category
LEFT JOIN tasks task ON task.category_id=category.id
WHERE category.id=$1
GROUP BY category.id`, *categoryID))
		if err != nil {
			return nil, nil, err
		}
		category = &fetched
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	return updated, category, nil
}

// ---- 任务模板（table task_templates）----

// Template 是可复用的任务预设：描述 + 目标 + 可选分类 + 任务级拦截规则快照。
type Template struct {
	ID             int64                `json:"id"`
	Name           string               `json:"name"`
	NKey           string               `json:"-"`
	Description    string               `json:"description"`
	Goal           string               `json:"goal"`
	CategoryID     *int64               `json:"category_id"`
	InterceptRules []InterceptRuleInput `json:"intercept_rules"`
	CreatedAt      time.Time            `json:"created_at"`
	UpdatedAt      time.Time            `json:"updated_at"`
}

// TemplateInput 是规范化后的创建/覆盖载荷。
type TemplateInput struct {
	Name           string
	Description    string
	Goal           string
	CategoryID     *int64
	InterceptRules []InterceptRuleInput
}

// TemplatePatch 只改被标记为「已设置」的字段：Name/Description/Goal 用非 nil 指针，
// CategoryID/InterceptRules 用显式 Set 标志（这样 SetCategoryID=true 且 CategoryID=nil
// 表示「清空分类」，与「没传」区分开）。
type TemplatePatch struct {
	Name              *string
	Description       *string
	Goal              *string
	CategoryID        *int64
	SetCategoryID     bool
	InterceptRules    []InterceptRuleInput
	SetInterceptRules bool
}

const templateCols = `id, name, nkey, description, goal, category_id, intercept_rules, created_at, updated_at`

func scanTemplate(row interface{ Scan(...any) error }) (Template, error) {
	var t Template
	var rulesRaw []byte
	if err := row.Scan(&t.ID, &t.Name, &t.NKey, &t.Description, &t.Goal, &t.CategoryID, &rulesRaw, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return t, err
	}
	t.InterceptRules = []InterceptRuleInput{}
	if len(rulesRaw) > 0 {
		if err := json.Unmarshal(rulesRaw, &t.InterceptRules); err != nil {
			return t, err
		}
		if t.InterceptRules == nil {
			t.InterceptRules = []InterceptRuleInput{}
		}
	}
	return t, nil
}

// marshalTemplateRules 序列化规则快照，永远输出 JSON 数组（不会是 null）。
func marshalTemplateRules(rules []InterceptRuleInput) ([]byte, error) {
	if rules == nil {
		rules = []InterceptRuleInput{}
	}
	return json.Marshal(rules)
}

// templateName 规范化显示空白但保留用户的大小写。
func templateName(name string) string { return strings.Join(strings.Fields(name), " ") }

// templateNKey 是唯一索引用的、大小写无关的身份。
func templateNKey(name string) string { return strings.ToLower(templateName(name)) }

func normalizeTemplateInput(in TemplateInput) (TemplateInput, string, error) {
	in.Name = templateName(in.Name)
	in.Description = strings.TrimSpace(in.Description)
	in.Goal = strings.TrimSpace(in.Goal)
	switch {
	case in.Name == "":
		return in, "", fmt.Errorf("%w: name is required", ErrTemplateInvalid)
	case utf8.RuneCountInString(in.Name) > MaxTemplateNameRunes:
		return in, "", fmt.Errorf("%w: name exceeds %d characters", ErrTemplateInvalid, MaxTemplateNameRunes)
	case in.Description == "":
		return in, "", fmt.Errorf("%w: description is required", ErrTemplateInvalid)
	case utf8.RuneCountInString(in.Description) > MaxTemplateTextRunes:
		return in, "", fmt.Errorf("%w: description exceeds %d characters", ErrTemplateInvalid, MaxTemplateTextRunes)
	case in.Goal == "":
		return in, "", fmt.Errorf("%w: goal is required", ErrTemplateInvalid)
	case utf8.RuneCountInString(in.Goal) > MaxTemplateTextRunes:
		return in, "", fmt.Errorf("%w: goal exceeds %d characters", ErrTemplateInvalid, MaxTemplateTextRunes)
	}
	return in, templateNKey(in.Name), nil
}

func normalizeTemplatePatch(patch TemplatePatch) (TemplatePatch, *string, error) {
	if patch.Name == nil && patch.Description == nil && patch.Goal == nil && !patch.SetCategoryID && !patch.SetInterceptRules {
		return patch, nil, fmt.Errorf("%w: no fields supplied", ErrTemplateInvalid)
	}
	var nkey *string
	if patch.Name != nil {
		name := templateName(*patch.Name)
		if name == "" {
			return patch, nil, fmt.Errorf("%w: name is required", ErrTemplateInvalid)
		}
		if utf8.RuneCountInString(name) > MaxTemplateNameRunes {
			return patch, nil, fmt.Errorf("%w: name exceeds %d characters", ErrTemplateInvalid, MaxTemplateNameRunes)
		}
		key := templateNKey(name)
		patch.Name = &name
		nkey = &key
	}
	if patch.Description != nil {
		description := strings.TrimSpace(*patch.Description)
		if description == "" {
			return patch, nil, fmt.Errorf("%w: description is required", ErrTemplateInvalid)
		}
		if utf8.RuneCountInString(description) > MaxTemplateTextRunes {
			return patch, nil, fmt.Errorf("%w: description exceeds %d characters", ErrTemplateInvalid, MaxTemplateTextRunes)
		}
		patch.Description = &description
	}
	if patch.Goal != nil {
		goal := strings.TrimSpace(*patch.Goal)
		if goal == "" {
			return patch, nil, fmt.Errorf("%w: goal is required", ErrTemplateInvalid)
		}
		if utf8.RuneCountInString(goal) > MaxTemplateTextRunes {
			return patch, nil, fmt.Errorf("%w: goal exceeds %d characters", ErrTemplateInvalid, MaxTemplateTextRunes)
		}
		patch.Goal = &goal
	}
	return patch, nkey, nil
}

// CreateTemplate 插入一个全局复用的预设；名字大小写不敏感地重复时返回
// ErrTemplateNameConflict。
func CreateTemplate(q store.Querier, in TemplateInput) (*Template, error) {
	in, nkey, err := normalizeTemplateInput(in)
	if err != nil {
		return nil, err
	}
	rulesJSON, err := marshalTemplateRules(in.InterceptRules)
	if err != nil {
		return nil, err
	}
	t, err := scanTemplate(q.QueryRow(`
INSERT INTO task_templates(name, nkey, description, goal, category_id, intercept_rules)
VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (nkey) DO NOTHING
RETURNING `+templateCols, in.Name, nkey, in.Description, in.Goal, in.CategoryID, rulesJSON))
	if err == sql.ErrNoRows {
		return nil, ErrTemplateNameConflict
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// ListTemplates 最近维护的排在最前。
func ListTemplates(q store.Querier) ([]*Template, error) {
	rows, err := q.Query(`SELECT ` + templateCols + ` FROM task_templates ORDER BY updated_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Template{}
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, &t)
	}
	return out, rows.Err()
}

// GetTemplate 不存在时返回 (nil, nil)。
func GetTemplate(q store.Querier, id int64) (*Template, error) {
	t, err := scanTemplate(q.QueryRow(`SELECT `+templateCols+` FROM task_templates WHERE id=$1`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// UpdateTemplate 覆盖预设的可编辑字段（分类与规则不动）。
func UpdateTemplate(q store.Querier, id int64, in TemplateInput) (*Template, error) {
	in, _, err := normalizeTemplateInput(in)
	if err != nil {
		return nil, err
	}
	return PatchTemplate(q, id, TemplatePatch{
		Name: &in.Name, Description: &in.Description, Goal: &in.Goal,
	})
}

// PatchTemplate 原子地只改传入的字段——合并在一条 UPDATE 里做，避免两个并发
// PATCH 互相覆盖。
func PatchTemplate(q store.Querier, id int64, patch TemplatePatch) (*Template, error) {
	patch, nkey, err := normalizeTemplatePatch(patch)
	if err != nil {
		return nil, err
	}
	rulesJSON, err := marshalTemplateRules(patch.InterceptRules)
	if err != nil {
		return nil, err
	}
	t, err := scanTemplate(q.QueryRow(`UPDATE task_templates
SET name=CASE WHEN $2 THEN $3::text ELSE name END,
    nkey=CASE WHEN $2 THEN $4::text ELSE nkey END,
    description=CASE WHEN $5 THEN $6::text ELSE description END,
    goal=CASE WHEN $7 THEN $8::text ELSE goal END,
    category_id=CASE WHEN $9 THEN $10::bigint ELSE category_id END,
    intercept_rules=CASE WHEN $11 THEN $12::jsonb ELSE intercept_rules END
WHERE id=$1
RETURNING `+templateCols,
		id,
		patch.Name != nil, patch.Name, nkey,
		patch.Description != nil, patch.Description,
		patch.Goal != nil, patch.Goal,
		patch.SetCategoryID, patch.CategoryID,
		patch.SetInterceptRules, rulesJSON,
	))
	if err == sql.ErrNoRows {
		return nil, ErrTemplateNotFound
	}
	if uniqueViolation(err) {
		return nil, ErrTemplateNameConflict
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// DeleteTemplate 删除一个预设并报告它是否存在过。
func DeleteTemplate(q store.Querier, id int64) (bool, error) {
	result, err := q.Exec(`DELETE FROM task_templates WHERE id=$1`, id)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n > 0, err
}
