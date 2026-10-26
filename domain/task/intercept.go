// 任务级拦截/允许规则（表 task_intercept_rules）。
//
// 与全局规则表 asset_intercept_rules 同构（kind/pattern/note/enabled），区别只在
// 归属：这里的行 task_id 指向 tasks 且 ON DELETE CASCADE，随任务一起消失。
//
// 值类型 InterceptRule 为什么放本包而不是 db/asset_intercept.go：
// 表属主是任务域，全局规则表只是复用同一套字段。db 包用类型别名对外，所以
// db.AssetInterceptRule 与 task.InterceptRule 是同一个类型，调用点无需改动。
// 若后续 asset 域迁出时出现反向依赖，再把共享值类型上提到 core/model。
package task

import (
	"fmt"
	"strings"
	"time"

	"finduai/core/store"
)

// InterceptRule 是一条拦截/允许规则。Action 仅任务级使用：block=拦截（默认），
// allow=白名单；全局规则不带该列，恒为空，按拦截处理。
type InterceptRule struct {
	ID        int64     `json:"id"`
	Enabled   bool      `json:"enabled"`
	Kind      string    `json:"kind"` // exact_domain|exact_ip|exact_url|fuzzy_domain|fuzzy_ip|fuzzy_url|cidr
	Pattern   string    `json:"pattern"`
	Note      string    `json:"note"`
	Builtin   bool      `json:"builtin"`
	Action    string    `json:"action,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// InterceptRuleInput 是创建任务时录入的单条规则（写入前统一 normalize）。
type InterceptRuleInput struct {
	Enabled bool   `json:"enabled"`
	Action  string `json:"action"`
	Kind    string `json:"kind"`
	Pattern string `json:"pattern"`
	Note    string `json:"note"`
}

const interceptRuleCols = `id, enabled, action, kind, pattern, note, created_at, updated_at`

func scanInterceptRule(row interface{ Scan(...any) error }) (InterceptRule, error) {
	var r InterceptRule
	err := row.Scan(&r.ID, &r.Enabled, &r.Action, &r.Kind, &r.Pattern, &r.Note, &r.CreatedAt, &r.UpdatedAt)
	return r, err
}

// KindLabel 返回 kind 的中文标签，用于给 agent 的说明消息。
func KindLabel(kind string) string {
	switch kind {
	case "exact_domain":
		return "域名(全等)"
	case "exact_ip":
		return "IP(全等)"
	case "exact_url":
		return "URL(全等)"
	case "fuzzy_domain":
		return "域名(模糊)"
	case "fuzzy_ip":
		return "IP(模糊)"
	case "fuzzy_url":
		return "URL(模糊)"
	case "cidr":
		return "CIDR 网段"
	}
	return kind
}

// Reason 返回一条可读的命中原因，形如：命中资产拦截规则 [域名(模糊): .gov.cn]（备注）。
func (r InterceptRule) Reason() string {
	s := fmt.Sprintf("命中资产拦截规则 [%s: %s]", KindLabel(r.Kind), r.Pattern)
	if note := strings.TrimSpace(r.Note); note != "" {
		s += "（" + note + "）"
	}
	return s
}

// normalizeRuleAction 只认 "allow"，其余一律归为 "block"。
func normalizeRuleAction(action string) string {
	if action == "allow" {
		return "allow"
	}
	return "block"
}

// ListInterceptRules 返回一个任务的全部规则（block 与 allow 都有）；
// taskID <= 0 直接返回空。
func ListInterceptRules(q store.Querier, taskID int64) ([]InterceptRule, error) {
	if taskID <= 0 {
		return nil, nil
	}
	rows, err := q.Query(`SELECT `+interceptRuleCols+` FROM task_intercept_rules WHERE task_id=$1 ORDER BY action, id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []InterceptRule
	for rows.Next() {
		r, err := scanInterceptRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SplitInterceptRules 把规则拆成 block / allow 两组，给执行判定用。
func SplitInterceptRules(q store.Querier, taskID int64) (block, allow []InterceptRule, err error) {
	rules, err := ListInterceptRules(q, taskID)
	if err != nil {
		return nil, nil, err
	}
	for _, r := range rules {
		if r.Action == "allow" {
			allow = append(allow, r)
		} else {
			block = append(block, r)
		}
	}
	return block, allow, nil
}

// CreateInterceptRule 在任务下插入一条规则。
func CreateInterceptRule(q store.Querier, taskID int64, action, kind, pattern, note string, enabled bool) (InterceptRule, error) {
	row := q.QueryRow(`
INSERT INTO task_intercept_rules(task_id, enabled, action, kind, pattern, note)
VALUES ($1,$2,$3,$4,$5,$6)
RETURNING `+interceptRuleCols,
		taskID, enabled, normalizeRuleAction(action), kind, pattern, note)
	return scanInterceptRule(row)
}

// UpdateInterceptRule 覆盖规则的编辑字段；用 task_id 兜住，规则只能经由它的任务改。
func UpdateInterceptRule(q store.Querier, taskID, ruleID int64, action, kind, pattern, note string, enabled bool) (InterceptRule, error) {
	row := q.QueryRow(`
UPDATE task_intercept_rules
   SET enabled=$3, action=$4, kind=$5, pattern=$6, note=$7
WHERE id=$1 AND task_id=$2
RETURNING `+interceptRuleCols,
		ruleID, taskID, enabled, normalizeRuleAction(action), kind, pattern, note)
	return scanInterceptRule(row)
}

// DeleteInterceptRule 删一条规则，返回是否删到。
func DeleteInterceptRule(q store.Querier, taskID, ruleID int64) (bool, error) {
	res, err := q.Exec(`DELETE FROM task_intercept_rules WHERE id=$1 AND task_id=$2`, ruleID, taskID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ToggleInterceptRule 翻转一条规则的启用状态。
func ToggleInterceptRule(q store.Querier, taskID, ruleID int64, enabled bool) error {
	_, err := q.Exec(`UPDATE task_intercept_rules SET enabled=$3 WHERE id=$1 AND task_id=$2`, ruleID, taskID, enabled)
	return err
}

// InsertInterceptRules 在创建任务的事务里批量写入规则（q 传 *sql.Tx 即可，
// 它满足 store.Querier），保证规则与任务同生同灭。
func InsertInterceptRules(q store.Querier, taskID int64, rules []InterceptRuleInput) error {
	for _, r := range rules {
		if _, err := q.Exec(`
INSERT INTO task_intercept_rules(task_id, enabled, action, kind, pattern, note)
VALUES ($1,$2,$3,$4,$5,$6)`, taskID, r.Enabled, normalizeRuleAction(r.Action), r.Kind, r.Pattern, r.Note); err != nil {
			return fmt.Errorf("insert task intercept rule %q: %w", r.Pattern, err)
		}
	}
	return nil
}
