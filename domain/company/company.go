package company

// 企业域的存储层：companies / company_scope 两表。批 3 第十刀新增本包
// （迁移图 §十一 对 companies.go/company_scope.go 的「domain/task*（待定）」
// 按外键归属改判：companies 是被 assets.company_id 与 task_scope.company_id
// 引用的父表，自成鉴权上下文，不塞进 task）。
//
// 分层约定（同其余 domain 包）：
//   - 只依赖 database/sql 与 core/store 的窄接口（DomainKey 在 store 上），
//     不认识 db 包的 DB 类型；迁移期 db 包保留类型别名 + 方法转发。
//   - 纯解析（scope.go）零 SQL；规则落入 company_scope 的写入在 company.go。
//
// 边界：本包不做「归属重算」（recomputeAttribution 写 assets 表，公司×资产
// 跨域）——那些事务仍在 db 装配层；本包只出被装配层复用的锁/存在性检查/
// 规则写入等单域积木（LockScopeMutation / EnsureCompanyExists / InsertScopeRules）。

import (
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strings"
	"unicode/utf8"

	"finduai/core/store"
)

// Company is a row in the companies table.
type Company struct {
	ID        int64   `json:"id"`
	Name      string  `json:"name"`
	NKey      string  `json:"nkey"`
	Logo      *string `json:"logo,omitempty"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
}

// CompanyWithScope extends Company with its scope rules and asset count.
// （AssetCount 由 db 装配层在跨域读里填入。）
type CompanyWithScope struct {
	Company
	Scope      []ScopeRule `json:"scope"`
	AssetCount int         `json:"asset_count"`
}

// ScopeRule is one company_scope row.
type ScopeRule struct {
	ID        int64  `json:"id"`
	CompanyID int64  `json:"company_id"`
	Kind      string `json:"kind"`
	Domain    string `json:"domain,omitempty"`
	Net       string `json:"net,omitempty"`
	Value     string `json:"value,omitempty"`
	Raw       string `json:"raw"`
	Reason    string `json:"reason,omitempty"`
}

var (
	ErrCompanyNameConflict = errors.New("company name already exists")
	ErrCompanyNotFound     = errors.New("company not found")
)

const (
	// 企业范围不限制规则条数:逐个 IP / 域名录入的范围动辄上千条,封顶只会逼用户
	// 拆成多个企业。请求体大小(server 侧 maxCompanyMutationBodyBytes)仍然兜底。
	//
	// Raw and normalized textual scope payloads are bounded by Unicode rune
	// count so multi-byte input is treated consistently by the API and DB layer.
	MaxCompanyScopeRawRunes   = 1024
	MaxCompanyScopeValueRunes = 1024
)

// CompanyScopeValidationError identifies a client-correctable scope error.
// Storage and transaction failures are returned as ordinary errors instead.
type CompanyScopeValidationError struct{ Message string }

func (e *CompanyScopeValidationError) Error() string { return e.Message }

// ValidateCompanyScopeInputBounds applies request-wide limits before parsing.
// Store methods call it again so non-HTTP callers cannot bypass the limits.
// 只约束单条规则的长度,不限制条数。
func ValidateCompanyScopeInputBounds(inputs []ScopeInput) error {
	for i, input := range inputs {
		if utf8.RuneCountInString(input.Value) > MaxCompanyScopeRawRunes {
			return &CompanyScopeValidationError{Message: fmt.Sprintf(
				"企业范围第 %d 条原始值过长: 最多 %d 个字符", i+1, MaxCompanyScopeRawRunes,
			)}
		}
	}
	return nil
}

// ParseScopeInputs parses structured scope rules, counting invalid entries and
// reporting per-entry validation errors without aborting the batch.
func ParseScopeInputs(inputs []ScopeInput) (rules []ParsedScope, invalid int, validationErrors []string) {
	rules = make([]ParsedScope, 0, len(inputs))
	for _, input := range inputs {
		rule, err := ParseScopeInput(input)
		if err != nil {
			invalid++
			validationErrors = append(validationErrors, fmt.Sprintf("%s: %v", input.Value, err))
			continue
		}
		rules = append(rules, rule)
	}
	return rules, invalid, validationErrors
}

// ValidateParsedScopeBounds enforces rune-count limits on parsed rules.
func ValidateParsedScopeBounds(rules []ParsedScope) error {
	for i, rule := range rules {
		if utf8.RuneCountInString(rule.Raw) > MaxCompanyScopeRawRunes {
			return &CompanyScopeValidationError{Message: fmt.Sprintf(
				"企业范围第 %d 条原始值过长: 最多 %d 个字符", i+1, MaxCompanyScopeRawRunes,
			)}
		}
		if utf8.RuneCountInString(rule.Value) > MaxCompanyScopeValueRunes {
			return &CompanyScopeValidationError{Message: fmt.Sprintf(
				"企业范围第 %d 条规范化值过长: 最多 %d 个字符", i+1, MaxCompanyScopeValueRunes,
			)}
		}
	}
	return nil
}

// Scope writes rebuild derived asset ownership globally, so serialize them to
// ensure the committed attribution always reflects the latest committed rules.
// This key is reserved for company mutations; 7337741001 is the schema lock and
// 7337741002 is the cross-package test-suite lock.
const ScopeMutationLock int64 = 7337741003

// LockScopeMutation takes the advisory company-scope mutation lock. 调用方既有
// 本域事务（写入规则前后重算归属），也有 db 装配层里跨域事务（资产写入、任务
// 创建快照）——统一用这个键，避免锁序漂移。
func LockScopeMutation(q store.Querier) error {
	_, err := q.Exec(`SELECT pg_advisory_xact_lock($1)`, ScopeMutationLock)
	return err
}

// UpsertCompany creates or updates a company by name. Returns the id and whether
// a new row was created.
func UpsertCompany(q store.Querier, name, logo string) (id int64, created bool, err error) {
	nkey := companyNKey(name)
	var logoVal any
	if logo != "" {
		logoVal = logo
	}
	err = q.QueryRow(`
INSERT INTO companies(name, nkey, logo)
VALUES ($1, $2, $3)
ON CONFLICT (nkey) DO UPDATE SET
    name = EXCLUDED.name,
    logo = COALESCE(EXCLUDED.logo, companies.logo),
    updated_at = now()
RETURNING id, (xmax = 0)`, name, nkey, logoVal).Scan(&id, &created)
	return
}

// GetCompany returns one company by id (nil if not found).
func GetCompany(q store.Querier, id int64) (*Company, error) {
	c := &Company{}
	err := q.QueryRow(`
SELECT id, name, nkey, logo, created_at::text, updated_at::text
FROM companies WHERE id = $1`, id).Scan(
		&c.ID, &c.Name, &c.NKey, &c.Logo, &c.CreatedAt, &c.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return c, err
}

// GetCompanyByName returns one company by normalized name (nil if not found).
func GetCompanyByName(q store.Querier, name string) (*Company, error) {
	nkey := companyNKey(name)
	c := &Company{}
	err := q.QueryRow(`
SELECT id, name, nkey, logo, created_at::text, updated_at::text
FROM companies WHERE nkey = $1`, nkey).Scan(
		&c.ID, &c.Name, &c.NKey, &c.Logo, &c.CreatedAt, &c.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return c, err
}

// UpsertByName creates the company if it doesn't exist, then returns its id.
func UpsertByName(q store.Querier, name string) (int64, error) {
	id, _, err := UpsertCompany(q, name, "")
	return id, err
}

// GetScope returns all scope rules for a company.
func GetScope(q store.Querier, companyID int64) ([]ScopeRule, error) {
	rows, err := q.Query(`
SELECT id, company_id, kind,
       COALESCE(domain,''), COALESCE(net::text,''), COALESCE(value,''), raw, COALESCE(reason,'')
FROM company_scope
WHERE company_id = $1
ORDER BY id`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ScopeRule, 0)
	for rows.Next() {
		var r ScopeRule
		if err := rows.Scan(&r.ID, &r.CompanyID, &r.Kind, &r.Domain, &r.Net, &r.Value, &r.Raw, &r.Reason); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// EnsureCompanyExists verifies the company row exists (raises ErrCompanyNotFound).
func EnsureCompanyExists(q store.Querier, companyID int64) error {
	var exists bool
	if err := q.QueryRow(`SELECT EXISTS(SELECT 1 FROM companies WHERE id = $1)`, companyID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrCompanyNotFound
	}
	return nil
}

// InsertScopeRules inserts a batch of parsed rules. inserted=false per rule means
// a duplicate was ignored by ON CONFLICT, not an error. needsAttribution reports
// whether any inserted rule (other than keyword) may shift derived asset
// ownership, i.e. the caller must recompute attribution in the same transaction.
func InsertScopeRules(q store.Querier, companyID int64, rules []ParsedScope, reason string) (
	added, skipped int, needsAttribution bool, err error,
) {
	for _, rule := range rules {
		inserted, insertErr := InsertScopeRule(q, companyID, rule, reason)
		if insertErr != nil {
			return 0, 0, false, insertErr
		}
		if !inserted {
			skipped++
			continue
		}
		added++
		needsAttribution = needsAttribution || rule.Kind != "keyword"
	}
	return added, skipped, needsAttribution, nil
}

// InsertScopeRule inserts one scope rule. inserted=false means a duplicate was
// ignored by ON CONFLICT, not an error.
func InsertScopeRule(q store.Querier, companyID int64, rule ParsedScope, reason string) (inserted bool, err error) {
	var res interface{ RowsAffected() (int64, error) }
	switch rule.Kind {
	case "domain":
		res, err = q.Exec(`
INSERT INTO company_scope(company_id, kind, domain, raw, reason)
VALUES ($1, 'domain', $2, $3, $4)
ON CONFLICT ON CONSTRAINT uq_sv2_domain DO NOTHING`,
			companyID, rule.Domain, rule.Raw, reason)
	case "ip", "cidr":
		res, err = q.Exec(`
INSERT INTO company_scope(company_id, kind, net, raw, reason)
VALUES ($1, $2, $3::cidr, $4, $5)
ON CONFLICT ON CONSTRAINT uq_sv2_net DO NOTHING`,
			companyID, rule.Kind, rule.Net, rule.Raw, reason)
	case "icp", "keyword":
		res, err = q.Exec(`
INSERT INTO company_scope(company_id, kind, value, raw, reason)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (company_id, kind, value) WHERE kind IN ('icp','keyword') DO NOTHING`,
			companyID, rule.Kind, rule.Value, rule.Raw, reason)
	default:
		return false, fmt.Errorf("unsupported company scope kind %q", rule.Kind)
	}
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ResolveCompany returns the company_id for a given root_domain and/or ip, or nil
// if no scope rule matches. Mirrors the attribution logic used at asset insert time.
func ResolveCompany(q store.Querier, rootDomain, ipStr string) (*int64, error) {
	return ResolveCompanyWithICP(q, rootDomain, ipStr, "")
}

// ResolveCompanyWithICP mirrors RecomputeAttribution (db 装配层的跨域重算事务)
// for insert-time ownership. ICP is consulted only after domain and IP/CIDR fail
// to match. 只读 companies/company_scope；被 db 装配层的资产 upsert 事务注入使用。
func ResolveCompanyWithICP(q store.Querier, rootDomain, ipStr, icp string) (*int64, error) {
	if rootDomain != "" {
		var cid int64
		err := q.QueryRow(`
SELECT company_id FROM company_scope
WHERE kind = 'domain'
  AND domain = $1
ORDER BY length(domain) DESC, company_id
LIMIT 1`, rootDomain).Scan(&cid)
		if err == nil {
			return &cid, nil
		}
		if err != sql.ErrNoRows {
			return nil, err
		}
	}
	if ipStr != "" {
		if net.ParseIP(ipStr) != nil {
			var cid int64
			err := q.QueryRow(`
SELECT company_id FROM company_scope
WHERE kind IN ('ip','cidr')
  AND net >>= $1::inet
ORDER BY masklen(net) DESC, company_id
LIMIT 1`, ipStr).Scan(&cid)
			if err == nil {
				return &cid, nil
			}
			if err != sql.ErrNoRows {
				return nil, err
			}
		}
	}
	if normalized := NormalizeICP(icp); normalized != "" {
		var cid int64
		err := q.QueryRow(`
SELECT company_id FROM company_scope
WHERE kind = 'icp' AND value = $1
ORDER BY company_id
LIMIT 1`, normalized).Scan(&cid)
		if err == nil {
			return &cid, nil
		}
		if err != sql.ErrNoRows {
			return nil, err
		}
	}
	return nil, nil
}

// companyNKey normalises a company name: lowercase + trim + collapse whitespace.
func companyNKey(name string) string {
	return strings.Join(strings.Fields(strings.ToLower(name)), " ")
}
