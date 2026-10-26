package asset

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	"finduai/core/store"
	"finduai/domain/task"
)

// =====================================================================
// 全局资产封禁规则（表 asset_intercept_rules）
//
// 与 intercept_rules（按工具名/输入匹配）不同，这里匹配的是「目标资产」：
// 精确/模糊的域名·IP·URL，或 CIDR 段。值类型与任务级规则表同构，唯一定义在
// domain/task（表属主判定见 domain/task/intercept.go 的注释），签名仍以
// task.InterceptRule 表达；db 包用类型别名 db.AssetInterceptRule 对外。
// =====================================================================

const assetInterceptRuleCols = `id, enabled, kind, pattern, note, builtin, created_at, updated_at`

func scanAssetInterceptRule(row interface{ Scan(...any) error }) (task.InterceptRule, error) {
	var r task.InterceptRule
	err := row.Scan(&r.ID, &r.Enabled, &r.Kind, &r.Pattern, &r.Note, &r.Builtin, &r.CreatedAt, &r.UpdatedAt)
	return r, err
}

// ListAssetInterceptRules returns all rules, built-ins first then newest first.
func ListAssetInterceptRules(q store.Querier) ([]task.InterceptRule, error) {
	rows, err := q.Query(`SELECT ` + assetInterceptRuleCols + ` FROM asset_intercept_rules ORDER BY builtin DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []task.InterceptRule
	for rows.Next() {
		r, err := scanAssetInterceptRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CreateAssetInterceptRule inserts a new user rule (builtin is always false here).
func CreateAssetInterceptRule(q store.Querier, kind, pattern, note string, enabled bool) (task.InterceptRule, error) {
	row := q.QueryRow(`
INSERT INTO asset_intercept_rules(enabled, kind, pattern, note, builtin)
VALUES ($1, $2, $3, $4, false)
RETURNING `+assetInterceptRuleCols,
		enabled, kind, pattern, note)
	return scanAssetInterceptRule(row)
}

// UpdateAssetInterceptRule replaces the editable fields of an existing rule.
func UpdateAssetInterceptRule(q store.Querier, id int64, kind, pattern, note string, enabled bool) (task.InterceptRule, error) {
	row := q.QueryRow(`
UPDATE asset_intercept_rules
   SET enabled=$2, kind=$3, pattern=$4, note=$5
WHERE id=$1
RETURNING `+assetInterceptRuleCols,
		id, enabled, kind, pattern, note)
	return scanAssetInterceptRule(row)
}

// DeleteAssetInterceptRule removes a rule (built-in rules are deletable too).
func DeleteAssetInterceptRule(q store.Querier, id int64) error {
	_, err := q.Exec(`DELETE FROM asset_intercept_rules WHERE id=$1`, id)
	return err
}

// ToggleAssetInterceptRule flips the enabled state of a rule.
func ToggleAssetInterceptRule(q store.Querier, id int64, enabled bool) error {
	_, err := q.Exec(`UPDATE asset_intercept_rules SET enabled=$2 WHERE id=$1`, id, enabled)
	return err
}

// =====================================================================
// 匹配/执行层：把「目标资产」的域名/IP/URL 与启用中的规则做匹配。
// 供 agent 工具（add_intent、insert_assets）在下发意图 / 插入资产前调用，
// 命中则拒绝。本节纯计算，不触库。
// =====================================================================

// matchOne 判断单条启用规则是否命中给定的域名/IP/URL 候选串，返回命中的具体值。
func matchOne(r task.InterceptRule, domains, ips, urls []string) (string, bool) {
	p := strings.TrimSpace(r.Pattern)
	if p == "" {
		return "", false
	}
	switch r.Kind {
	case "exact_domain":
		for _, d := range domains {
			if strings.EqualFold(strings.TrimSpace(d), p) {
				return d, true
			}
		}
	case "exact_ip":
		for _, ip := range ips {
			if strings.TrimSpace(ip) == p {
				return ip, true
			}
		}
	case "exact_url":
		for _, u := range urls {
			if strings.TrimSpace(u) == p {
				return u, true
			}
		}
	case "fuzzy_domain":
		lp := strings.ToLower(p)
		for _, d := range domains {
			if d != "" && strings.Contains(strings.ToLower(d), lp) {
				return d, true
			}
		}
	case "fuzzy_ip":
		for _, ip := range ips {
			if ip != "" && strings.Contains(ip, p) {
				return ip, true
			}
		}
	case "fuzzy_url":
		lp := strings.ToLower(p)
		for _, u := range urls {
			if u != "" && strings.Contains(strings.ToLower(u), lp) {
				return u, true
			}
		}
	case "cidr":
		_, ipnet, err := net.ParseCIDR(p)
		if err != nil {
			return "", false
		}
		for _, ip := range ips {
			if pip := net.ParseIP(strings.TrimSpace(ip)); pip != nil && ipnet.Contains(pip) {
				return ip, true
			}
		}
	}
	return "", false
}

// MatchAssetInterceptRules 返回第一条命中给定 域名/IP/URL 候选串的启用规则，及命中的具体值。
// 供 insert_assets 用原始输入（尚未落库的 assetInputItem）匹配。
func MatchAssetInterceptRules(rules []task.InterceptRule, domains, ips, urls []string) (task.InterceptRule, string, bool) {
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		if v, ok := matchOne(r, domains, ips, urls); ok {
			return r, v, true
		}
	}
	return task.InterceptRule{}, "", false
}

// InterceptCandidates 提取一个已落库资产用于拦截匹配的 域名/IP/URL 候选串。
// URL 的 host 会被拆出并归类，使「只带 URL」的服务类资产也能被 域名/IP 规则命中。
func (a *Asset) InterceptCandidates() (domains, ips, urls []string) {
	add := func(dst *[]string, s string) {
		if s = strings.TrimSpace(s); s != "" {
			*dst = append(*dst, s)
		}
	}
	add(&domains, a.Domain)
	add(&domains, a.RootDomain)
	for _, d := range a.BoundDomains {
		add(&domains, d)
	}
	add(&ips, a.IP)
	add(&urls, a.URL)
	if a.URL != "" {
		if u, err := url.Parse(a.URL); err == nil {
			if h := u.Hostname(); h != "" {
				if net.ParseIP(h) != nil {
					add(&ips, h)
				} else {
					add(&domains, h)
				}
			}
		}
	}
	return domains, ips, urls
}

// InterceptLabel 返回资产的简短标识，用于给 agent 的说明消息。
func (a *Asset) InterceptLabel() string {
	var target string
	switch {
	case a.Domain != "":
		target = a.Domain
	case a.URL != "":
		target = a.URL
	case a.IP != "":
		target = a.IP
	default:
		target = fmt.Sprintf("#%s", a.ID)
	}
	return fmt.Sprintf("资产#%s[%s] %s", a.ID, a.Type, target)
}

// 判断规则集里是否存在任一启用规则。
func HasEnabledRule(rules []task.InterceptRule) bool {
	for _, r := range rules {
		if r.Enabled {
			return true
		}
	}
	return false
}

// AssetGateDecision 是「先拦截后允许」闸门对一组候选串的判定结果。
type AssetGateDecision struct {
	Allowed bool
	Reason  string // 被拒原因（不含资产标识）；Allowed=true 时为空
}

// EvaluateAssetGate 执行任务级闸门判定：
//  1. 命中任一启用的 blockRules → 拒绝（拦截原因）。
//  2. 否则若 allowRules 存在启用项且都不命中 → 拒绝（不在允许范围）。
//  3. 否则放行。
//
// allowRules 为空/无启用项时，允许闸门不生效（即不启用白名单，全部放行），
// 避免「未配置允许规则」把所有资产挡掉。
func EvaluateAssetGate(blockRules, allowRules []task.InterceptRule, domains, ips, urls []string) AssetGateDecision {
	if rule, _, ok := MatchAssetInterceptRules(blockRules, domains, ips, urls); ok {
		return AssetGateDecision{Allowed: false, Reason: rule.Reason()}
	}
	if HasEnabledRule(allowRules) {
		if _, _, ok := MatchAssetInterceptRules(allowRules, domains, ips, urls); !ok {
			return AssetGateDecision{Allowed: false, Reason: "不在任务允许(白名单)范围内，不允许测试"}
		}
	}
	return AssetGateDecision{Allowed: true}
}

// AssetInterceptHit 描述一个被闸门拒绝的资产（拦截命中 或 不在允许范围）。
type AssetInterceptHit struct {
	Asset  *Asset
	Reason string // 可读原因
}

// Describe 返回一条可读的说明：资产信息 + 原因。
func (h AssetInterceptHit) Describe() string {
	return fmt.Sprintf("%s → %s", h.Asset.InterceptLabel(), h.Reason)
}
