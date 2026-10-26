package db

// 企业域存储拆分（Step 3 批 3 第十刀）：
//   - 新增 domain/company：companies/company_scope 的单域读写（Up/Get/UpsertByName/
//     GetScope）、范围规则纯解析（company_scope 原件更名上移）、advisory 锁与
//     规则写入积木（LockScopeMutation / EnsureCompanyExists / InsertScopeRules）、
//     insert-time 归属解算 ResolveCompanyWithICP（只读本域两表）。
//   - 本文件保留「公司 × 资产」跨域事务作装配层：CreateCompanyWithScope /
//     AddScopeInputsChecked / UpdateScopeInputsChecked / DeleteCompanyWithAssets /
//     RecomputeAttribution（写 assets.company_id）与 MalformedIPAssetWarning
//     （读 assets）。其余为类型别名 + 方法转发，调用点零改动。

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"

	"finduai/domain/company"
)

// Companies returns the company store.
func (d *DB) Companies() *CompanyStore { return &CompanyStore{db: d} }

// CompanyStore operates on the companies + company_scope tables. 单域读写转发
// domain/company；跨域事务（归属重算）保留在本包。
type CompanyStore struct{ db *DB }

func (s *CompanyStore) UpsertCompany(name, logo string) (id int64, created bool, err error) {
	return company.UpsertCompany(s.db, name, logo)
}

func (s *CompanyStore) GetCompany(id int64) (*company.Company, error) {
	return company.GetCompany(s.db, id)
}

func (s *CompanyStore) GetCompanyByName(name string) (*company.Company, error) {
	return company.GetCompanyByName(s.db, name)
}

func (s *CompanyStore) UpsertByName(name string) (int64, error) {
	return company.UpsertByName(s.db, name)
}

func (s *CompanyStore) GetScope(companyID int64) ([]company.ScopeRule, error) {
	return company.GetScope(s.db, companyID)
}

func (s *CompanyStore) ResolveCompany(rootDomain, ipStr string) (*int64, error) {
	return company.ResolveCompany(s.db, rootDomain, ipStr)
}

func (s *CompanyStore) ResolveCompanyWithICP(rootDomain, ipStr, icp string) (*int64, error) {
	return company.ResolveCompanyWithICP(s.db, rootDomain, ipStr, icp)
}

// ---------------------------------------------------------------------------
// 跨域事务（企业 × 资产归属）—— 装配层。
// ---------------------------------------------------------------------------

// CreateCompanyWithScope creates a company without updating an existing row.
// The company, its valid initial scope rules, and derived asset attribution are
// committed atomically. Invalid inputs retain the legacy partial-validation
// contract and are reported without preventing valid rules from being stored.
func (s *CompanyStore) CreateCompanyWithScope(name, logo string, inputs []company.ScopeInput, reason string) (
	id int64, added, skipped, invalid int, validationErrors []string, err error,
) {
	if err := company.ValidateCompanyScopeInputBounds(inputs); err != nil {
		return 0, 0, 0, 0, nil, err
	}
	rules, invalid, validationErrors := company.ParseScopeInputs(inputs)
	if err := company.ValidateParsedScopeBounds(rules); err != nil {
		return 0, 0, 0, invalid, validationErrors, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, 0, 0, invalid, validationErrors, err
	}
	defer tx.Rollback() //nolint:errcheck
	if err := company.LockScopeMutation(tx); err != nil {
		return 0, 0, 0, invalid, validationErrors, err
	}

	nkey := companyNKey(name)
	var logoVal any
	if logo != "" {
		logoVal = logo
	}
	if err := tx.QueryRow(`
INSERT INTO companies(name, nkey, logo)
VALUES ($1, $2, $3)
ON CONFLICT (nkey) DO NOTHING
RETURNING id`, name, nkey, logoVal).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, 0, 0, invalid, validationErrors, company.ErrCompanyNameConflict
		}
		return 0, 0, 0, invalid, validationErrors, err
	}

	added, skipped, needsAttribution, err := company.InsertScopeRules(tx, id, rules, reason)
	if err != nil {
		return 0, 0, 0, invalid, validationErrors, err
	}
	if needsAttribution {
		warning, err := recomputeAttributionTx(tx)
		if err != nil {
			return 0, 0, 0, invalid, validationErrors, err
		}
		logAttributionWarning(warning)
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, 0, invalid, validationErrors, err
	}
	return id, added, skipped, invalid, validationErrors, nil
}

func companyNKey(name string) string {
	return strings.Join(strings.Fields(strings.ToLower(name)), " ")
}

// DeleteCompany deletes a company and re-evaluates automatic ownership against
// the remaining companies in the same transaction. Explicitly-owned assets are
// detached by the FK and may then fall back to a remaining scope match.
func (s *CompanyStore) DeleteCompany(id int64) error {
	_, err := s.DeleteCompanyWithAssets(id, false)
	return err
}

// DeleteCompanyWithAssets deletes a company and optionally all of its assets in
// one transaction, then re-evaluates ownership against the remaining companies.
func (s *CompanyStore) DeleteCompanyWithAssets(id int64, deleteAssets bool) (assetsDeleted int64, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck
	if err := company.LockScopeMutation(tx); err != nil {
		return 0, err
	}
	if deleteAssets {
		res, err := tx.Exec(`DELETE FROM assets WHERE company_id = $1`, id)
		if err != nil {
			return 0, err
		}
		assetsDeleted, err = res.RowsAffected()
		if err != nil {
			return 0, err
		}
	}
	res, err := tx.Exec(`DELETE FROM companies WHERE id = $1`, id)
	if err != nil {
		return 0, err
	}
	deleted, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if deleted == 0 {
		return 0, company.ErrCompanyNotFound
	}
	// This path has no per-request warning channel, so the log is the only place
	// the operator can learn about unparseable ip rows here.
	warning, err := recomputeAttributionTx(tx)
	if err != nil {
		return 0, err
	}
	logAttributionWarning(warning)
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return assetsDeleted, nil
}

// ListCompanies returns all companies with scope and asset count.
// （AssetCount 对 assets.count 是跨域读，按惯例留装配层。）
func (s *CompanyStore) ListCompanies() ([]*company.CompanyWithScope, error) {
	rows, err := s.db.Query(`
SELECT c.id, c.name, c.nkey, c.logo, c.created_at::text, c.updated_at::text,
       COUNT(DISTINCT a.id) AS asset_count
FROM companies c
LEFT JOIN assets a ON a.company_id = c.id
GROUP BY c.id
ORDER BY c.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*company.CompanyWithScope
	for rows.Next() {
		cws := &company.CompanyWithScope{}
		if err := rows.Scan(&cws.ID, &cws.Name, &cws.NKey, &cws.Logo,
			&cws.CreatedAt, &cws.UpdatedAt, &cws.AssetCount); err != nil {
			return nil, err
		}
		out = append(out, cws)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// fetch scope rules for each company
	for _, cws := range out {
		cws.Scope, err = s.GetScope(cws.ID)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// AddScope parses and inserts scope lines for a company, then reattributes assets.
// Returns counts of added, skipped, and invalid lines.
func (s *CompanyStore) AddScope(companyID int64, lines []string, reason string) (added, skipped, invalid int, errors []string) {
	inputs := make([]company.ScopeInput, 0, len(lines))
	for _, line := range lines {
		inputs = append(inputs, company.ScopeInput{Value: line})
	}
	return s.AddScopeInputs(companyID, inputs, reason)
}

// AddScopeInputs inserts structured scope rules. Empty kinds use the automatic
// CIDR/IP/ICP/domain/keyword classification used by AddScope.
func (s *CompanyStore) AddScopeInputs(companyID int64, inputs []company.ScopeInput, reason string) (added, skipped, invalid int, errors []string) {
	added, skipped, invalid, validationErrors, err := s.AddScopeInputsChecked(companyID, inputs, reason)
	if err != nil {
		validationErrors = append(validationErrors, err.Error())
	}
	return added, skipped, invalid, validationErrors
}

// AddScopeInputsChecked inserts structured scope rules while keeping input
// validation separate from storage and transaction errors.
func (s *CompanyStore) AddScopeInputsChecked(companyID int64, inputs []company.ScopeInput, reason string) (
	added, skipped, invalid int, validationErrors []string, err error,
) {
	if err := company.ValidateCompanyScopeInputBounds(inputs); err != nil {
		return 0, 0, 0, nil, err
	}
	rules, invalid, errs := company.ParseScopeInputs(inputs)
	if err := company.ValidateParsedScopeBounds(rules); err != nil {
		return 0, 0, invalid, errs, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, 0, invalid, errs, err
	}
	defer tx.Rollback() //nolint:errcheck
	if err := company.LockScopeMutation(tx); err != nil {
		return 0, 0, invalid, errs, err
	}
	if err := company.EnsureCompanyExists(tx, companyID); err != nil {
		return 0, 0, invalid, errs, err
	}
	if len(rules) == 0 {
		if err := tx.Commit(); err != nil {
			return 0, 0, invalid, errs, err
		}
		return 0, 0, invalid, errs, nil
	}
	added, skipped, needsAttribution, err := company.InsertScopeRules(tx, companyID, rules, reason)
	if err != nil {
		return 0, 0, invalid, errs, err
	}
	if needsAttribution {
		warning, err := recomputeAttributionTx(tx)
		if err != nil {
			return 0, 0, invalid, errs, fmt.Errorf("重新计算企业归属失败: %w", err)
		}
		logAttributionWarning(warning)
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, invalid, errs, err
	}
	return added, skipped, invalid, errs, nil
}

// ---------------------------------------------------------------------------
// 归属重算与告警（读/写 assets，跨域本体）。
// ---------------------------------------------------------------------------

// RecomputeAttribution rebuilds only scope-derived ownership. Explicit company
// links are immutable under scope edits. Precedence is domain, IP/CIDR, then
// normalized exact ICP; keyword rules never attribute assets.
func (s *CompanyStore) RecomputeAttribution() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if err := company.LockScopeMutation(tx); err != nil {
		return err
	}
	warning, err := recomputeAttributionTx(tx)
	if err != nil {
		return err
	}
	logAttributionWarning(warning)
	return tx.Commit()
}

// recomputeAttributionTx rebuilds scope-derived ownership. It returns a warning
// for assets whose ip column cannot be parsed: try_inet skips them instead of
// aborting the statement, so without this they would silently never receive a
// network-based company. Callers surface the warning and it is always logged.
func recomputeAttributionTx(tx *sql.Tx) (string, error) {
	// Only derived rows are cleared. Historical rows migrated without provenance
	// are marked explicit by schema.sql, which is the non-destructive default.
	if _, err := tx.Exec(`
UPDATE assets
SET company_id = NULL, company_source = 'scope'
WHERE company_source = 'scope'`); err != nil {
		return "", err
	}

	// Domain-based attribution (root_domain exact match).
	if _, err := tx.Exec(`
WITH matched AS (
    SELECT DISTINCT ON (a.id) a.id AS asset_id, cs.company_id
    FROM assets a
    JOIN company_scope cs ON cs.kind = 'domain' AND a.root_domain = cs.domain
    WHERE a.company_id IS NULL
      AND a.type IN ('root_domain','subdomain','service','endpoint')
      AND a.root_domain IS NOT NULL
    ORDER BY a.id, length(cs.domain) DESC, cs.company_id
)
UPDATE assets a
SET company_id = matched.company_id, company_source = 'scope'
FROM matched
WHERE a.id = matched.asset_id`); err != nil {
		return "", err
	}

	// IP/CIDR attribution for still-unowned assets.
	if _, err := tx.Exec(`
WITH matched AS (
    SELECT DISTINCT ON (a.id) a.id AS asset_id, cs.company_id
    FROM assets a
    JOIN company_scope cs ON cs.kind IN ('ip','cidr') AND cs.net >>= try_inet(a.ip)
    WHERE a.company_id IS NULL
      AND a.type IN ('ip','subdomain','service','endpoint')
      AND a.ip IS NOT NULL
    ORDER BY a.id, masklen(cs.net) DESC, cs.company_id
)
UPDATE assets a
SET company_id = matched.company_id, company_source = 'scope'
FROM matched
WHERE a.id = matched.asset_id`); err != nil {
		return "", err
	}

	// Exact normalized ICP attribution after domain/network precedence.
	if _, err := tx.Exec(`
WITH matched AS (
    SELECT DISTINCT ON (a.id) a.id AS asset_id, cs.company_id
    FROM assets a
    JOIN company_scope cs ON cs.kind = 'icp'
      AND (
        lower(regexp_replace(COALESCE(a.icp,''), '[[:space:]]+', '', 'g')) = cs.value
        OR lower(regexp_replace(COALESCE(a.app_icp,''), '[[:space:]]+', '', 'g')) = cs.value
      )
	WHERE a.company_id IS NULL
      AND (COALESCE(a.icp,'') <> '' OR COALESCE(a.app_icp,'') <> '')
    ORDER BY a.id, cs.company_id
)
UPDATE assets a
SET company_id = matched.company_id, company_source = 'scope'
FROM matched
WHERE a.id = matched.asset_id`); err != nil {
		return "", err
	}
	return malformedIPAssetWarning(tx)
}

// malformedIPAssetsSampled bounds how many offending ids one warning names, so a
// large batch of bad rows stays readable in a toast and in the log.
const malformedIPAssetsSampled = 5

// logAttributionWarning records a recompute warning in the server log. Every
// recompute path calls it, so the warning is reported even for triggers with no
// per-request response (company deletion, scopesentry sync, agent asset writes).
func logAttributionWarning(warning string) {
	if warning != "" {
		log.Printf("[assets] %s", warning)
	}
}

// malformedIPAssetQueryer is satisfied by both *sql.Tx and *DB so the warning
// can be produced inside a recompute transaction or read standalone by the API.
type malformedIPAssetQueryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

// MalformedIPAssetWarning reports assets with an unparseable ip outside of any
// mutation, letting the API attach the warning to a scope response without
// widening the mutation signatures — an unrelated data problem is not one of
// this request's validation errors.
func (s *CompanyStore) MalformedIPAssetWarning() (string, error) {
	return malformedIPAssetWarning(s.db)
}

// malformedIPAssetWarning describes assets whose ip column is not a valid
// address. They are invisible to network attribution, so the operator has to be
// told which rows to fix — silently skipping them would look like scope rules
// that simply do not work.
func malformedIPAssetWarning(q malformedIPAssetQueryer) (string, error) {
	rows, err := q.Query(`
SELECT id, ip, count(*) OVER () AS total
FROM assets
WHERE ip IS NOT NULL AND ip <> '' AND try_inet(ip) IS NULL
  AND type IN ('ip','subdomain','service','endpoint')
ORDER BY id
LIMIT $1`, malformedIPAssetsSampled)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var total int
	samples := make([]string, 0, malformedIPAssetsSampled)
	for rows.Next() {
		var id int64
		var ip string
		if err := rows.Scan(&id, &ip, &total); err != nil {
			return "", err
		}
		samples = append(samples, fmt.Sprintf("#%d %s", id, ip))
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if total == 0 {
		return "", nil
	}
	warning := fmt.Sprintf(
		"%d 条资产的 ip 字段不是合法 IP，已跳过 IP/CIDR 范围匹配（这些资产不会被网段规则归属到企业）：%s",
		total, strings.Join(samples, "、"),
	)
	if total > len(samples) {
		warning += fmt.Sprintf(" 等 %d 条", total)
	}
	return warning, nil
}

// UpdateScope replaces all scope rules for a company and reattributes.
func (s *CompanyStore) UpdateScope(companyID int64, lines []string, reason string) (added, invalid int, errs []string) {
	inputs := make([]company.ScopeInput, 0, len(lines))
	for _, line := range lines {
		inputs = append(inputs, company.ScopeInput{Value: line})
	}
	return s.UpdateScopeInputs(companyID, inputs, reason)
}

// UpdateScopeInputs replaces all rules with a structured set.
func (s *CompanyStore) UpdateScopeInputs(companyID int64, inputs []company.ScopeInput, reason string) (added, invalid int, errs []string) {
	added, invalid, validationErrors, err := s.UpdateScopeInputsChecked(companyID, inputs, reason)
	if err != nil {
		validationErrors = append(validationErrors, err.Error())
	}
	return added, invalid, validationErrors
}

// UpdateScopeInputsChecked replaces all rules while separating validation
// feedback from storage and transaction failures.
func (s *CompanyStore) UpdateScopeInputsChecked(companyID int64, inputs []company.ScopeInput, reason string) (
	added, invalid int, validationErrors []string, err error,
) {
	if err := company.ValidateCompanyScopeInputBounds(inputs); err != nil {
		return 0, 0, nil, err
	}
	rules, invalid, errs := company.ParseScopeInputs(inputs)
	if invalid > 0 {
		return 0, invalid, errs, &company.CompanyScopeValidationError{Message: fmt.Sprintf(
			"企业范围包含 %d 条无效规则，未覆盖原有范围", invalid,
		)}
	}
	if err := company.ValidateParsedScopeBounds(rules); err != nil {
		return 0, invalid, errs, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, invalid, errs, err
	}
	defer tx.Rollback() //nolint:errcheck
	if err := company.LockScopeMutation(tx); err != nil {
		return 0, invalid, errs, err
	}
	if err := company.EnsureCompanyExists(tx, companyID); err != nil {
		return 0, invalid, errs, err
	}
	if _, err := tx.Exec(`DELETE FROM company_scope WHERE company_id = $1`, companyID); err != nil {
		return 0, invalid, errs, err
	}
	added, _, _, err = company.InsertScopeRules(tx, companyID, rules, reason)
	if err != nil {
		return 0, invalid, errs, err
	}
	// Rebuild even for an empty replacement because removing the old rules may
	// detach scope-derived assets or expose a lower-precedence company match.
	warning, err := recomputeAttributionTx(tx)
	if err != nil {
		return 0, invalid, errs, fmt.Errorf("重新计算企业归属失败: %w", err)
	}
	logAttributionWarning(warning)
	if err := tx.Commit(); err != nil {
		return 0, invalid, errs, err
	}
	return added, invalid, errs, nil
}

// ---------------------------------------------------------------------------
// 包内薄包装 —— 命名兼容（db/tasks.go 与测试直接引用）。
// ---------------------------------------------------------------------------
