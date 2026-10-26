package asset

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"finduai/core/store"
)

// =====================================================================
// 宽列 SELECT / 扫描
// =====================================================================

// AssetSelectCols 统一资产表的宽列 SELECT 首部（覆盖六种 type 的全部列），
// 追加 WHERE / ORDER 后配 ScanAssets 使用。常量供 db 装配层（scope 查询）复用。
const AssetSelectCols = `SELECT id, type, company_id, array_to_json(task_ids)::text,
       COALESCE(domain,''), COALESCE(root_domain,''), COALESCE(ip,''),
       COALESCE(c_segment::text,''), port,
       COALESCE(icp,''), array_to_json(bound_domains)::text, array_to_json(open_ports)::text, COALESCE(record_type,''),
       array_to_json(record_value)::text, COALESCE(bundle_id,''), COALESCE(app_name,''),
       COALESCE(category,''), COALESCE(app_description,''), COALESCE(app_icp,''),
       COALESCE(url,''), COALESCE(service_type,''), COALESCE(service_name,''),
       COALESCE(favicon_mmh3,''), status_code, content_length,
       COALESCE(page_title,''), array_to_json(technologies)::text, array_to_json(auth)::text,
       COALESCE(method,''), array_to_json(params)::text,
       COALESCE(status,''), COALESCE(tags_json,''), extra, last_seen::text
FROM assets`

// ScanAssets scans the wide SELECT that covers all type columns.
func ScanAssets(rows *sql.Rows) ([]*Asset, error) {
	var out []*Asset
	for rows.Next() {
		a := &Asset{}
		var taskIDsRaw, boundDomainsRaw, openPortsRaw, recordValueRaw, techsRaw, authRaw, paramsRaw, extraRaw, tagsRaw []byte
		var port, statusCode sql.NullInt64
		var contentLength sql.NullInt64
		var companyID sql.NullInt64
		if err := rows.Scan(
			&a.ID, &a.Type, &companyID, &taskIDsRaw,
			&a.Domain, &a.RootDomain, &a.IP, &a.CSegment, &port,
			&a.ICP, &boundDomainsRaw, &openPortsRaw, &a.RecordType,
			&recordValueRaw, &a.BundleID, &a.AppName,
			&a.Category, &a.AppDescription, &a.AppICP,
			&a.URL, &a.ServiceType, &a.ServiceName,
			&a.FaviconMMH3, &statusCode, &contentLength,
			&a.PageTitle, &techsRaw, &authRaw,
			&a.Method, &paramsRaw, &a.Status, &tagsRaw, &extraRaw, &a.LastSeen,
		); err != nil {
			return nil, err
		}
		if companyID.Valid {
			cid := companyID.Int64
			a.CompanyID = &cid
		}
		if port.Valid {
			p := int(port.Int64)
			a.Port = &p
		}
		if statusCode.Valid {
			sc := int(statusCode.Int64)
			a.StatusCode = &sc
		}
		if contentLength.Valid {
			cl := contentLength.Int64
			a.ContentLength = &cl
		}
		// parse arrays
		if len(taskIDsRaw) > 0 {
			_ = json.Unmarshal(taskIDsRaw, &a.TaskIDs)
		}
		if len(boundDomainsRaw) > 0 {
			_ = json.Unmarshal(boundDomainsRaw, &a.BoundDomains)
		}
		if len(openPortsRaw) > 0 {
			_ = json.Unmarshal(openPortsRaw, &a.OpenPorts)
		}
		if len(recordValueRaw) > 0 {
			_ = json.Unmarshal(recordValueRaw, &a.RecordValue)
		}
		if len(techsRaw) > 0 {
			_ = json.Unmarshal(techsRaw, &a.Technologies)
		}
		if len(authRaw) > 0 {
			_ = json.Unmarshal(authRaw, &a.Auth)
		}
		if len(paramsRaw) > 0 {
			_ = json.Unmarshal(paramsRaw, &a.Params)
		}
		// findu 共表扩展列（Step 6d R3）：标签为 JSON 数组文本。
		if len(tagsRaw) > 0 {
			_ = json.Unmarshal(tagsRaw, &a.Tags)
		}
		if len(extraRaw) > 0 {
			_ = json.Unmarshal(extraRaw, &a.Extra)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func ScanTypeCounts(rows *sql.Rows) (map[string]int, error) {
	out := map[string]int{}
	for rows.Next() {
		var typ string
		var cnt int
		if err := rows.Scan(&typ, &cnt); err != nil {
			return nil, err
		}
		out[typ] = cnt
	}
	return out, rows.Err()
}

// PageClause 追加统一排序与分页（limit<=0 不加 LIMIT，offset<=0 不加 OFFSET）。
func PageClause(args *[]any, limit, offset int) string {
	q := ` ORDER BY last_seen DESC, id DESC`
	if limit > 0 {
		*args = append(*args, limit)
		q += fmt.Sprintf(` LIMIT $%d`, len(*args))
	}
	if offset > 0 {
		*args = append(*args, offset)
		q += fmt.Sprintf(` OFFSET $%d`, len(*args))
	}
	return q
}

// =====================================================================
// 查询
// =====================================================================

// QueryByType returns assets rows of a given type, newest first.
func QueryByType(q store.Querier, typ string, limit, offset int) ([]*Asset, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := q.Query(AssetSelectCols+`
WHERE type = $1
ORDER BY last_seen DESC, id DESC
LIMIT $2 OFFSET $3`, typ, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return ScanAssets(rows)
}

// CountByType returns the total number of assets of a type (for server-side pagination).
func CountByType(q store.Querier, typ string) (int, error) {
	var n int
	err := q.QueryRow(`SELECT count(*) FROM assets WHERE type = $1`, typ).Scan(&n)
	return n, err
}

// QueryByCompany returns assets for a company, optionally filtered by type.
// limit <= 0 means no limit.
func QueryByCompany(q store.Querier, companyID int64, typ string, limit, offset int) ([]*Asset, error) {
	clause := AssetSelectCols + ` WHERE company_id = $1`
	args := []any{companyID}
	if typ != "" {
		args = append(args, typ)
		clause += fmt.Sprintf(` AND type = $%d`, len(args))
	}
	clause += PageClause(&args, limit, offset)
	rows, err := q.Query(clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return ScanAssets(rows)
}

func CountByCompany(q store.Querier, companyID int64, typ string) (int, error) {
	clause := `SELECT count(*) FROM assets WHERE company_id = $1`
	args := []any{companyID}
	if typ != "" {
		args = append(args, typ)
		clause += fmt.Sprintf(` AND type = $%d`, len(args))
	}
	var n int
	err := q.QueryRow(clause, args...).Scan(&n)
	return n, err
}

// QueryByTask returns assets rows that have a given task_id in task_ids.
// 注意：本函数只做查询，不做任务源注水（hydrateTaskAssetSources 属任务域，
// db 装配层的方法负责调用）。limit <= 0 意味着不分页。
func QueryByTask(q store.Querier, taskID int64, typ string, limit, offset int) ([]*Asset, error) {
	clause := AssetSelectCols + ` WHERE $1 = ANY(task_ids)`
	args := []any{taskID}
	if typ != "" {
		args = append(args, typ)
		clause += fmt.Sprintf(` AND type = $%d`, len(args))
	}
	clause += PageClause(&args, limit, offset)
	rows, err := q.Query(clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return ScanAssets(rows)
}

func CountByTask(q store.Querier, taskID int64, typ string) (int, error) {
	clause := `SELECT count(*) FROM assets WHERE $1 = ANY(task_ids)`
	args := []any{taskID}
	if typ != "" {
		args = append(args, typ)
		clause += fmt.Sprintf(` AND type = $%d`, len(args))
	}
	var n int
	err := q.QueryRow(clause, args...).Scan(&n)
	return n, err
}

func CountsByTypeForTask(q store.Querier, taskID int64) (map[string]int, error) {
	rows, err := q.Query(`SELECT COALESCE(type, 'unknown'), COUNT(*) FROM assets WHERE $1 = ANY(task_ids) GROUP BY type`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return ScanTypeCounts(rows)
}

// GetByIDs returns assets with the given ids (order preserved by id array order).
func GetByIDs(q store.Querier, ids []string) ([]*Asset, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}
	statement := AssetSelectCols + " WHERE id IN (" + strings.Join(placeholders, ",") + ") ORDER BY last_seen DESC"
	rows, err := q.Query(statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return ScanAssets(rows)
}

// =====================================================================
// 删除
// =====================================================================

// DeleteByTaskID removes assets owned only by taskID and detaches taskID from
// assets shared with other tasks. Full task deletion uses the coordinated
// transaction in DeleteTaskCascadePrepared; this method remains for callers
// that explicitly manage only asset associations.
func DeleteByTaskID(q store.Querier, taskID int64) (int64, error) {
	res, err := q.Exec(`DELETE FROM assets WHERE task_ids = ARRAY[$1]::bigint[]`, taskID)
	if err != nil {
		return 0, err
	}
	deleted, _ := res.RowsAffected()
	if _, err := q.Exec(`UPDATE assets SET task_ids = array_remove(task_ids, $1) WHERE $1 = ANY(task_ids)`, taskID); err != nil {
		return deleted, err
	}
	return deleted, nil
}

// DeleteByCompanyID hard-deletes all assets belonging to a company. Returns rows deleted.
func DeleteByCompanyID(q store.Querier, companyID int64) (int64, error) {
	res, err := q.Exec(`DELETE FROM assets WHERE company_id = $1`, companyID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DeleteByHost hard-deletes every asset whose host exactly matches the given value:
// root_domain / subdomain / service / endpoint (they carry the host in domain or
// root_domain) plus an ip asset and its services/endpoints (ip column). The host is
// normalized the same way it is stored (DomainKey: lowercase/trim/strip trailing dot)
// so matching is exact, not fuzzy. Passing a root domain also removes its subdomains
// and their services/endpoints (they carry root_domain = that host); passing a
// subdomain/IP removes only that host's own assets. Referencing exploration_anchors
// rows are cleaned by ON DELETE CASCADE. Returns rows deleted, grouped by type.
func DeleteByHost(q store.Querier, host string) (map[string]int64, error) {
	h := store.DomainKey(host)
	if h == "" {
		return nil, fmt.Errorf("host is required")
	}
	rows, err := q.Query(`
DELETE FROM assets
WHERE domain = $1 OR root_domain = $1 OR ip = $1
RETURNING type`, h)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int64{}
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		counts[t]++
	}
	return counts, rows.Err()
}

// DeleteByIDs hard-deletes assets by their IDs. Returns the number of rows deleted.
func DeleteByIDs(q store.Querier, ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}
	res, err := q.Exec("DELETE FROM assets WHERE id IN ("+strings.Join(placeholders, ",")+")", args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CountsByType returns asset counts per type.
func CountsByType(q store.Querier) (map[string]int, error) {
	rows, err := q.Query(`SELECT COALESCE(type, 'unknown'), COUNT(*) FROM assets GROUP BY type`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return ScanTypeCounts(rows)
}

// =====================================================================
// 任务相关 host 统计
// =====================================================================

// HostsByTask returns the exact HTTP host candidates attached to a task's
// assets. Domain/IP columns cover root domains, subdomains and non-HTTP
// services; URL covers HTTP services and endpoints.
func HostsByTask(q store.Querier, taskID int64) ([]string, error) {
	rows, err := q.Query(`
SELECT COALESCE(domain,''), COALESCE(ip,''), COALESCE(url,'')
FROM assets WHERE $1 = ANY(task_ids)`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	hosts := make(map[string]struct{})
	add := func(host string) {
		host = strings.TrimSpace(strings.ToLower(host))
		if host != "" {
			hosts[host] = struct{}{}
		}
	}
	for rows.Next() {
		var domain, ip, rawURL string
		if err := rows.Scan(&domain, &ip, &rawURL); err != nil {
			return nil, err
		}
		add(domain)
		add(ip)
		if u, err := url.Parse(rawURL); err == nil {
			add(u.Hostname())
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(hosts))
	for host := range hosts {
		out = append(out, host)
	}
	sort.Strings(out)
	return out, nil
}
