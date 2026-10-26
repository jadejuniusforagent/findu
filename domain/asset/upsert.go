package asset

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strings"

	"finduai/core/store"
)

// =====================================================================
// upsert 家族（本域 SQL）+ 企业归属解析注入
//
// 归属解析（company_scope 表上的宿主解析）的属主仍在 db 装配层
//（companies 域未迁出），所以整套函数都注入 CompanyResolver。所有函数都假定
// 已经处于「资产写入事务」中（db 侧 withCompanyScopeMutation 负责开事务并锁
// company_scope），对 q 的写入要么全成功要么全回滚。
// =====================================================================

// CompanyResolver 由装配层注入的企业归属解析：rootDomain → ip/cidr → ICP 三级
// 归属判定，与资产插入时的 RecomputeAttribution 逻辑同源。
type CompanyResolver interface {
	ResolveCompany(q store.Querier, rootDomain, ipStr, icp string) (*int64, error)
}

// =====================================================================
// Helpers（upsert 家族共用）
// =====================================================================

// CalcCSegment computes the /24 (IPv4) or /48 (IPv6) network for an IP string.
func CalcCSegment(ipStr string) string {
	if ipStr == "" {
		return ""
	}
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return ""
	}
	if ip.To4() != nil {
		// IPv4 /24
		parts := strings.Split(ipStr, ".")
		if len(parts) == 4 {
			return parts[0] + "." + parts[1] + "." + parts[2] + ".0/24"
		}
		return ""
	}
	// IPv6 /48
	_, ipnet, err := net.ParseCIDR(ipStr + "/48")
	if err != nil {
		return ""
	}
	return ipnet.String()
}

// NormalizeURL lowercases scheme and host, strips trailing slash from bare roots.
func NormalizeURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	s := u.String()
	if strings.HasSuffix(s, "/") && u.Path == "/" && u.RawQuery == "" {
		s = strings.TrimSuffix(s, "/")
	}
	return s
}

// ParseURL extracts domain, port, service_name from a URL.
func ParseURL(raw string) (domain string, port int, serviceName string) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", 0, ""
	}
	domain = strings.ToLower(u.Hostname())
	port = store.DefaultPort(strings.ToLower(u.Scheme), u.Port())
	switch strings.ToLower(u.Scheme) {
	case "https":
		serviceName = "HTTPS"
	case "http":
		serviceName = "HTTP"
	default:
		serviceName = strings.ToUpper(u.Scheme)
	}
	return
}

func MarshalJSONBArray(items []map[string]any) (string, error) {
	if len(items) == 0 {
		return "{}", nil
	}
	parts := make([]string, len(items))
	for i, m := range items {
		b, err := json.Marshal(m)
		if err != nil {
			return "", err
		}
		// PostgreSQL array literal for jsonb[]: each element must be
		// double-quoted with internal backslashes and double-quotes escaped.
		s := strings.ReplaceAll(string(b), `\`, `\\`)
		s = strings.ReplaceAll(s, `"`, `\"`)
		parts[i] = `"` + s + `"`
	}
	return "{" + strings.Join(parts, ",") + "}", nil
}

func MarshalStringArray(items []string) string {
	if len(items) == 0 {
		return "{}"
	}
	escaped := make([]string, len(items))
	for i, s := range items {
		escaped[i] = `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return "{" + strings.Join(escaped, ",") + "}"
}

func MarshalPortServices(ps []PortService) (string, error) {
	items := make([]map[string]any, len(ps))
	for i, p := range ps {
		items[i] = map[string]any{"port": p.Port, "service": p.Service}
	}
	return MarshalJSONBArray(items)
}

// NullableInt returns nil for zero, the value otherwise (port 列空值语义).
func NullableInt(v int) interface{} {
	if v == 0 {
		return nil
	}
	return v
}

// =====================================================================
// UpsertRootDomain
// =====================================================================

// UpsertRootDomain idempotently inserts or merges a root domain asset.
func UpsertRootDomain(q store.Querier, r CompanyResolver, req UpsertRootDomainReq) (string, error) {
	domain := store.DomainKey(req.Domain)
	if domain == "" {
		return "", fmt.Errorf("domain is required")
	}
	companyID, err := r.ResolveCompany(q, domain, "", req.ICP)
	if err != nil {
		return "", err
	}

	var taskIDs string
	if req.TaskID > 0 {
		taskIDs = fmt.Sprintf("{%d}", req.TaskID)
	} else {
		taskIDs = "{}"
	}

	var icpVal any
	if req.ICP != "" {
		icpVal = req.ICP
	}

	var id string
	err = q.QueryRow(`
INSERT INTO assets(type, domain, root_domain, icp, company_id, company_source, task_ids)
VALUES ('root_domain', $1, $1, $2, $3, 'scope', $4::bigint[])
ON CONFLICT (domain) WHERE type = 'root_domain' DO UPDATE SET
    icp        = COALESCE(EXCLUDED.icp, assets.icp),
    company_id = CASE
        WHEN assets.company_source = 'explicit' AND assets.company_id IS NOT NULL THEN assets.company_id
        ELSE COALESCE(EXCLUDED.company_id, assets.company_id)
    END,
    company_source = CASE
        WHEN assets.company_source = 'explicit' AND assets.company_id IS NOT NULL THEN 'explicit'
        WHEN EXCLUDED.company_id IS NOT NULL THEN 'scope'
        ELSE assets.company_source
    END,
    task_ids   = (SELECT ARRAY(SELECT DISTINCT unnest(assets.task_ids || EXCLUDED.task_ids))),
    extra      = assets.extra || EXCLUDED.extra,
    last_seen  = now()
RETURNING id`, domain, icpVal, companyID, taskIDs).Scan(&id)
	return id, err
}

// =====================================================================
// UpsertIP / Append*
// =====================================================================

// UpsertIP idempotently inserts or merges an IP asset.
func UpsertIP(q store.Querier, r CompanyResolver, req UpsertIPReq) (string, error) {
	if req.IP == "" {
		return "", fmt.Errorf("ip is required")
	}
	if err := ValidateAssetIP(req.IP); err != nil {
		return "", err
	}
	cseg := CalcCSegment(req.IP)
	companyID, err := r.ResolveCompany(q, "", req.IP, "")
	if err != nil {
		return "", err
	}

	var taskIDs string
	if req.TaskID > 0 {
		taskIDs = fmt.Sprintf("{%d}", req.TaskID)
	} else {
		taskIDs = "{}"
	}

	boundDomains := MarshalStringArray(req.BoundDomains)
	openPortsJSON, err := MarshalPortServices(req.OpenPorts)
	if err != nil {
		return "", err
	}

	var csegVal any
	if cseg != "" {
		csegVal = cseg
	}

	var id string
	err = q.QueryRow(`
INSERT INTO assets(type, ip, c_segment, bound_domains, open_ports, company_id, company_source, task_ids)
VALUES ('ip', $1, $2::cidr, $3::text[], $4::jsonb[], $5, 'scope', $6::bigint[])
ON CONFLICT (ip) WHERE type = 'ip' DO UPDATE SET
    bound_domains = (SELECT ARRAY(SELECT DISTINCT unnest(assets.bound_domains || EXCLUDED.bound_domains))),
    open_ports    = (
        SELECT ARRAY(
            SELECT DISTINCT ON ((elem->>'port')::int) elem
            FROM unnest(assets.open_ports || EXCLUDED.open_ports) AS elem
            ORDER BY (elem->>'port')::int,
                     CASE WHEN elem->>'service' IS NOT NULL AND elem->>'service' <> '' THEN 0 ELSE 1 END
        )
    ),
    c_segment  = COALESCE(assets.c_segment, EXCLUDED.c_segment),
    company_id = CASE
        WHEN assets.company_source = 'explicit' AND assets.company_id IS NOT NULL THEN assets.company_id
        ELSE COALESCE(EXCLUDED.company_id, assets.company_id)
    END,
    company_source = CASE
        WHEN assets.company_source = 'explicit' AND assets.company_id IS NOT NULL THEN 'explicit'
        WHEN EXCLUDED.company_id IS NOT NULL THEN 'scope'
        ELSE assets.company_source
    END,
    task_ids   = (SELECT ARRAY(SELECT DISTINCT unnest(assets.task_ids || EXCLUDED.task_ids))),
    last_seen  = now()
RETURNING id`, req.IP, csegVal, boundDomains, openPortsJSON, companyID, taskIDs).Scan(&id)
	return id, err
}

// AppendIPPort appends a {port, service} entry to an existing IP asset's open_ports.
func AppendIPPort(q store.Querier, ipStr string, port int, serviceName string) error {
	if ipStr == "" || port == 0 {
		return nil
	}
	entry, _ := json.Marshal(map[string]any{"port": port, "service": serviceName})
	_, err := q.Exec(`
UPDATE assets SET
    open_ports = (
        SELECT ARRAY(
            SELECT DISTINCT ON ((elem->>'port')::int) elem
            FROM unnest(open_ports || ARRAY[$1::jsonb]) AS elem
            ORDER BY (elem->>'port')::int,
                     CASE WHEN elem->>'service' IS NOT NULL AND elem->>'service' <> '' THEN 0 ELSE 1 END
        )
    ),
    last_seen = now()
WHERE type = 'ip' AND ip = $2`, string(entry), ipStr)
	return err
}

// AppendIPBoundDomain appends a domain to an existing IP asset's bound_domains.
func AppendIPBoundDomain(q store.Querier, ipStr, domain string) error {
	if ipStr == "" || domain == "" {
		return nil
	}
	_, err := q.Exec(`
UPDATE assets SET
    bound_domains = (SELECT ARRAY(SELECT DISTINCT unnest(bound_domains || ARRAY[$1::text]))),
    last_seen = now()
WHERE type = 'ip' AND ip = $2`, domain, ipStr)
	return err
}

// =====================================================================
// UpsertSubdomain
// =====================================================================

// UpsertSubdomain idempotently inserts or merges a subdomain asset and triggers
// side effects: root_domain upsert + IP bound_domains update.
func UpsertSubdomain(q store.Querier, r CompanyResolver, req UpsertSubdomainReq) (id string, err error) {
	domain := store.DomainKey(req.Domain)
	if domain == "" {
		return "", fmt.Errorf("domain is required")
	}

	rootDomain, _ := store.RootDomain(domain)
	if rootDomain == "" {
		rootDomain = domain
	}

	// Resolve by root domain first, then the asset's exact normalized ICP.
	companyID, err := r.ResolveCompany(q, rootDomain, "", req.ICP)
	if err != nil {
		return "", err
	}

	// side effect 1: ensure root domain exists
	_, _ = UpsertRootDomain(q, r, UpsertRootDomainReq{
		Domain: rootDomain,
		TaskID: req.TaskID,
	})

	// side effect 2: if A/AAAA record, upsert each IP + bind domain.
	// RecordValue is []string; each element may itself be comma-separated (legacy).
	var ipStr string // first valid IP, used for the subdomain row itself
	if req.RecordType == "A" || req.RecordType == "AAAA" {
		for _, rv := range req.RecordValue {
			for _, part := range strings.Split(rv, ",") {
				candidate := strings.TrimSpace(part)
				if candidate == "" || net.ParseIP(candidate) == nil {
					continue
				}
				if ipStr == "" {
					ipStr = candidate
				}
				_, _ = UpsertIP(q, r, UpsertIPReq{
					IP:           candidate,
					BoundDomains: []string{domain},
					TaskID:       req.TaskID,
				})
			}
		}
	}

	cseg := CalcCSegment(ipStr)
	if companyID == nil && ipStr != "" {
		companyID, _ = r.ResolveCompany(q, "", ipStr, "")
	}

	var taskIDs string
	if req.TaskID > 0 {
		taskIDs = fmt.Sprintf("{%d}", req.TaskID)
	} else {
		taskIDs = "{}"
	}

	var icpVal, csegVal, ipVal any
	if req.ICP != "" {
		icpVal = req.ICP
	}
	if cseg != "" {
		csegVal = cseg
	}
	if ipStr != "" {
		ipVal = ipStr
	}
	recordType := req.RecordType
	recordValueArr := MarshalStringArray(req.RecordValue)

	err = q.QueryRow(`
INSERT INTO assets(type, domain, root_domain, record_type, record_value, ip, c_segment, icp, company_id, company_source, task_ids)
VALUES ('subdomain', $1, $2, $3, $4::text[], $5, $6::cidr, $7, $8, 'scope', $9::bigint[])
ON CONFLICT (domain, COALESCE(record_type,'')) WHERE type = 'subdomain' DO UPDATE SET
    ip           = COALESCE(EXCLUDED.ip, assets.ip),
    c_segment    = COALESCE(EXCLUDED.c_segment, assets.c_segment),
    icp          = COALESCE(EXCLUDED.icp, assets.icp),
    company_id   = CASE
        WHEN assets.company_source = 'explicit' AND assets.company_id IS NOT NULL THEN assets.company_id
        ELSE COALESCE(EXCLUDED.company_id, assets.company_id)
    END,
    company_source = CASE
        WHEN assets.company_source = 'explicit' AND assets.company_id IS NOT NULL THEN 'explicit'
        WHEN EXCLUDED.company_id IS NOT NULL THEN 'scope'
        ELSE assets.company_source
    END,
    task_ids     = (SELECT ARRAY(SELECT DISTINCT unnest(assets.task_ids || EXCLUDED.task_ids))),
    record_value = (SELECT ARRAY(SELECT DISTINCT unnest(assets.record_value || EXCLUDED.record_value))),
    extra        = assets.extra || EXCLUDED.extra,
    last_seen    = now()
RETURNING id`, domain, rootDomain, recordType, recordValueArr, ipVal, csegVal, icpVal, companyID, taskIDs).Scan(&id)
	return id, err
}

// =====================================================================
// UpsertApp
// =====================================================================

// UpsertApp idempotently inserts or merges an app asset.
func UpsertApp(q store.Querier, r CompanyResolver, req UpsertAppReq) (string, error) {
	if req.Name == "" {
		return "", fmt.Errorf("app name is required")
	}

	var taskIDs string
	if req.TaskID > 0 {
		taskIDs = fmt.Sprintf("{%d}", req.TaskID)
	} else {
		taskIDs = "{}"
	}

	var bundleVal, catVal, descVal, icpVal, companyIDVal any
	companySource := "scope"
	if req.BundleID != "" {
		bundleVal = req.BundleID
	}
	if req.Category != "" {
		catVal = req.Category
	}
	if req.Description != "" {
		descVal = req.Description
	}
	if req.ICP != "" {
		icpVal = req.ICP
	}
	if req.CompanyID != nil {
		companyIDVal = *req.CompanyID
		companySource = "explicit"
	} else {
		companyID, err := r.ResolveCompany(q, "", "", req.ICP)
		if err != nil {
			return "", err
		}
		if companyID != nil {
			companyIDVal = *companyID
		}
	}

	var id string
	var err error

	if req.BundleID != "" {
		err = q.QueryRow(`
INSERT INTO assets(type, bundle_id, app_name, category, app_description, app_icp, company_id, company_source, task_ids)
VALUES ('app', $1, $2, $3, $4, $5, $6, $7, $8::bigint[])
ON CONFLICT (bundle_id) WHERE type = 'app' AND bundle_id IS NOT NULL DO UPDATE SET
    app_name        = COALESCE(EXCLUDED.app_name, assets.app_name),
    category        = COALESCE(EXCLUDED.category, assets.category),
    app_description = COALESCE(EXCLUDED.app_description, assets.app_description),
    app_icp         = COALESCE(EXCLUDED.app_icp, assets.app_icp),
    company_id      = CASE
        WHEN EXCLUDED.company_source = 'explicit' THEN EXCLUDED.company_id
        WHEN assets.company_source = 'explicit' AND assets.company_id IS NOT NULL THEN assets.company_id
        ELSE COALESCE(EXCLUDED.company_id, assets.company_id)
    END,
    company_source  = CASE
        WHEN EXCLUDED.company_source = 'explicit' THEN 'explicit'
        WHEN assets.company_source = 'explicit' AND assets.company_id IS NOT NULL THEN 'explicit'
        WHEN EXCLUDED.company_id IS NOT NULL THEN 'scope'
        ELSE assets.company_source
    END,
    task_ids        = (SELECT ARRAY(SELECT DISTINCT unnest(assets.task_ids || EXCLUDED.task_ids))),
    last_seen       = now()
RETURNING id`, bundleVal, req.Name, catVal, descVal, icpVal, companyIDVal, companySource, taskIDs).Scan(&id)
	} else {
		err = q.QueryRow(`
INSERT INTO assets(type, bundle_id, app_name, category, app_description, app_icp, company_id, company_source, task_ids)
VALUES ('app', NULL, $1, $2, $3, $4, $5, $6, $7::bigint[])
ON CONFLICT (app_name) WHERE type = 'app' AND bundle_id IS NULL DO UPDATE SET
    category        = COALESCE(EXCLUDED.category, assets.category),
    app_description = COALESCE(EXCLUDED.app_description, assets.app_description),
    app_icp         = COALESCE(EXCLUDED.app_icp, assets.app_icp),
    company_id      = CASE
        WHEN EXCLUDED.company_source = 'explicit' THEN EXCLUDED.company_id
        WHEN assets.company_source = 'explicit' AND assets.company_id IS NOT NULL THEN assets.company_id
        ELSE COALESCE(EXCLUDED.company_id, assets.company_id)
    END,
    company_source  = CASE
        WHEN EXCLUDED.company_source = 'explicit' THEN 'explicit'
        WHEN assets.company_source = 'explicit' AND assets.company_id IS NOT NULL THEN 'explicit'
        WHEN EXCLUDED.company_id IS NOT NULL THEN 'scope'
        ELSE assets.company_source
    END,
    task_ids        = (SELECT ARRAY(SELECT DISTINCT unnest(assets.task_ids || EXCLUDED.task_ids))),
    last_seen       = now()
RETURNING id`, req.Name, catVal, descVal, icpVal, companyIDVal, companySource, taskIDs).Scan(&id)
	}
	return id, err
}

// =====================================================================
// UpsertHTTPService / UpsertOtherService / linkHostAssets
// =====================================================================

// UpsertHTTPService inserts or merges an HTTP service asset. Domain, port,
// service_name, and root_domain are auto-extracted from URL.
func UpsertHTTPService(q store.Querier, r CompanyResolver, req UpsertHTTPServiceReq) (string, error) {
	if req.URL == "" {
		return "", fmt.Errorf("url is required")
	}
	if err := ValidateAssetIP(req.IP); err != nil {
		return "", err
	}
	normURL := NormalizeURL(req.URL)
	domain, port, serviceName := ParseURL(normURL)
	rootDomain, _ := store.RootDomain(domain)
	if rootDomain == "" {
		rootDomain = domain
	}

	cseg := CalcCSegment(req.IP)
	companyID, err := r.ResolveCompany(q, rootDomain, req.IP, "")
	if err != nil {
		return "", err
	}

	var taskIDs string
	if req.TaskID > 0 {
		taskIDs = fmt.Sprintf("{%d}", req.TaskID)
	} else {
		taskIDs = "{}"
	}

	techsArr := MarshalStringArray(req.Technologies)
	authJSON, err := MarshalJSONBArray(req.Auth)
	if err != nil {
		return "", err
	}

	var domainVal, ipVal, csegVal, titleVal, faviconVal any
	if domain != "" {
		domainVal = domain
	}
	if req.IP != "" {
		ipVal = req.IP
	}
	if cseg != "" {
		csegVal = cseg
	}
	if req.PageTitle != "" {
		titleVal = req.PageTitle
	}
	if req.FaviconMMH3 != "" {
		faviconVal = req.FaviconMMH3
	}

	var id string
	err = q.QueryRow(`
INSERT INTO assets(
    type, url, service_type, service_name, domain, ip, port, root_domain,
    c_segment, favicon_mmh3, technologies, status_code, content_length, page_title,
    auth, company_id, company_source, task_ids
)
VALUES (
    'service', $1, 'http', $2, $3, $4, $5, $6,
    $7::cidr, $8, $9::text[], $10, $11, $12,
    $13::jsonb[], $14, 'scope', $15::bigint[]
)
ON CONFLICT (url) WHERE type = 'service' AND service_type = 'http' DO UPDATE SET
    status_code    = COALESCE(EXCLUDED.status_code,    assets.status_code),
    content_length = COALESCE(EXCLUDED.content_length, assets.content_length),
    page_title     = COALESCE(EXCLUDED.page_title,     assets.page_title),
    favicon_mmh3   = COALESCE(EXCLUDED.favicon_mmh3,   assets.favicon_mmh3),
    ip             = COALESCE(EXCLUDED.ip,             assets.ip),
    c_segment      = COALESCE(EXCLUDED.c_segment,      assets.c_segment),
    technologies   = (SELECT ARRAY(SELECT DISTINCT unnest(assets.technologies || EXCLUDED.technologies))),
    auth           = (
        SELECT ARRAY(
            SELECT DISTINCT ON (elem::text) elem
            FROM unnest(assets.auth || EXCLUDED.auth) AS elem
        )
    ),
    task_ids       = (SELECT ARRAY(SELECT DISTINCT unnest(assets.task_ids || EXCLUDED.task_ids))),
    company_id     = CASE
        WHEN assets.company_source = 'explicit' AND assets.company_id IS NOT NULL THEN assets.company_id
        ELSE COALESCE(EXCLUDED.company_id, assets.company_id)
    END,
    company_source = CASE
        WHEN assets.company_source = 'explicit' AND assets.company_id IS NOT NULL THEN 'explicit'
        WHEN EXCLUDED.company_id IS NOT NULL THEN 'scope'
        ELSE assets.company_source
    END,
    last_seen      = now()
RETURNING id`,
		normURL, serviceName, domainVal, ipVal, NullableInt(port), rootDomain,
		csegVal, faviconVal, techsArr, req.StatusCode, req.ContentLength, titleVal,
		authJSON, companyID, taskIDs,
	).Scan(&id)
	if err != nil {
		return "", err
	}

	// side effects: register root_domain + subdomain as their own assets too
	linkHostAssets(q, r, domain, rootDomain, req.TaskID)
	if req.IP != "" {
		var boundDomains []string
		if domain != "" {
			boundDomains = []string{domain}
		}
		var openPorts []PortService
		if port > 0 {
			openPorts = []PortService{{Port: port, Service: serviceName}}
		}
		_, _ = UpsertIP(q, r, UpsertIPReq{
			IP:           req.IP,
			BoundDomains: boundDomains,
			OpenPorts:    openPorts,
			TaskID:       req.TaskID,
		})
	}
	return id, nil
}

// UpsertOtherService inserts or merges a non-HTTP service asset.
func UpsertOtherService(q store.Querier, r CompanyResolver, req UpsertOtherServiceReq) (string, error) {
	if req.Domain == "" && req.IP == "" {
		return "", fmt.Errorf("domain or ip is required")
	}
	if err := ValidateAssetIP(req.IP); err != nil {
		return "", err
	}
	if req.Port == 0 {
		return "", fmt.Errorf("port is required")
	}
	if req.ServiceName == "" {
		return "", fmt.Errorf("service_name is required")
	}
	// normalize service_name (lowercase) so the (domain,ip,port,service_name)
	// dedup key doesn't split "SSH" and "ssh" into separate rows.
	serviceName := strings.ToLower(strings.TrimSpace(req.ServiceName))

	domain := store.DomainKey(req.Domain)
	var rootDomain string
	if domain != "" {
		rootDomain, _ = store.RootDomain(domain)
		if rootDomain == "" {
			rootDomain = domain
		}
	}

	cseg := CalcCSegment(req.IP)
	companyID, err := r.ResolveCompany(q, rootDomain, req.IP, "")
	if err != nil {
		return "", err
	}

	var taskIDs string
	if req.TaskID > 0 {
		taskIDs = fmt.Sprintf("{%d}", req.TaskID)
	} else {
		taskIDs = "{}"
	}

	authJSON, err := MarshalJSONBArray(req.Auth)
	if err != nil {
		return "", err
	}

	var domainVal, rootDomainVal, ipVal, csegVal any
	if domain != "" {
		domainVal = domain
	}
	if rootDomain != "" {
		rootDomainVal = rootDomain
	}
	if req.IP != "" {
		ipVal = req.IP
	}
	if cseg != "" {
		csegVal = cseg
	}

	var id string
	err = q.QueryRow(`
INSERT INTO assets(
    type, service_type, service_name, domain, ip, port, root_domain,
    c_segment, auth, company_id, company_source, task_ids
)
VALUES ('service', 'other', $1, $2, $3, $4, $5, $6::cidr, $7::jsonb[], $8, 'scope', $9::bigint[])
ON CONFLICT (COALESCE(domain,''), COALESCE(ip,''), port, service_name) WHERE type = 'service' AND service_type = 'other' DO UPDATE SET
    domain     = COALESCE(EXCLUDED.domain,     assets.domain),
    ip         = COALESCE(EXCLUDED.ip,         assets.ip),
    c_segment  = COALESCE(EXCLUDED.c_segment,  assets.c_segment),
    auth       = (
        SELECT ARRAY(
            SELECT DISTINCT ON (elem::text) elem
            FROM unnest(assets.auth || EXCLUDED.auth) AS elem
        )
    ),
    task_ids   = (SELECT ARRAY(SELECT DISTINCT unnest(assets.task_ids || EXCLUDED.task_ids))),
    company_id = CASE
        WHEN assets.company_source = 'explicit' AND assets.company_id IS NOT NULL THEN assets.company_id
        ELSE COALESCE(EXCLUDED.company_id, assets.company_id)
    END,
    company_source = CASE
        WHEN assets.company_source = 'explicit' AND assets.company_id IS NOT NULL THEN 'explicit'
        WHEN EXCLUDED.company_id IS NOT NULL THEN 'scope'
        ELSE assets.company_source
    END,
    last_seen  = now()
RETURNING id`,
		serviceName, domainVal, ipVal, req.Port, rootDomainVal,
		csegVal, authJSON, companyID, taskIDs,
	).Scan(&id)
	if err != nil {
		return "", err
	}

	// side effects
	if req.IP != "" && req.Port > 0 {
		var boundDomains []string
		if domain != "" {
			boundDomains = []string{domain}
		}
		_, _ = UpsertIP(q, r, UpsertIPReq{
			IP:           req.IP,
			BoundDomains: boundDomains,
			OpenPorts:    []PortService{{Port: req.Port, Service: serviceName}},
			TaskID:       req.TaskID,
		})
	}
	linkHostAssets(q, r, domain, rootDomain, req.TaskID)
	return id, nil
}

// linkHostAssets ensures a service/endpoint's host is also registered as its own
// root_domain and (when it's a real subdomain, not the apex or an IP) subdomain
// asset — so those asset types stay populated and can anchor task scope. Best-effort.
func linkHostAssets(q store.Querier, r CompanyResolver, domain, rootDomain string, taskID int64) {
	if rootDomain != "" {
		_, _ = UpsertRootDomain(q, r, UpsertRootDomainReq{Domain: rootDomain, TaskID: taskID})
	}
	if domain != "" && domain != rootDomain && net.ParseIP(domain) == nil {
		_, _ = UpsertSubdomain(q, r, UpsertSubdomainReq{Domain: domain, TaskID: taskID})
	}
}

// =====================================================================
// UpsertEndpoint
// =====================================================================

// UpsertEndpoint inserts or merges an endpoint asset. Domain, port, root_domain
// are auto-extracted from the URL.
func UpsertEndpoint(q store.Querier, r CompanyResolver, req UpsertEndpointReq) (string, error) {
	if req.URL == "" {
		return "", fmt.Errorf("url is required")
	}
	if req.Method == "" {
		return "", fmt.Errorf("method is required")
	}
	if err := ValidateAssetIP(req.IP); err != nil {
		return "", err
	}
	method := strings.ToUpper(req.Method)
	normURL := NormalizeURL(req.URL)
	domain, port, _ := ParseURL(normURL)
	rootDomain, _ := store.RootDomain(domain)
	if rootDomain == "" {
		rootDomain = domain
	}

	cseg := CalcCSegment(req.IP)
	companyID, err := r.ResolveCompany(q, rootDomain, req.IP, "")
	if err != nil {
		return "", err
	}

	var taskIDs string
	if req.TaskID > 0 {
		taskIDs = fmt.Sprintf("{%d}", req.TaskID)
	} else {
		taskIDs = "{}"
	}

	paramsJSON, err := MarshalJSONBArray(req.Params)
	if err != nil {
		return "", err
	}

	var domainVal, rootDomainVal, ipVal, csegVal any
	if domain != "" {
		domainVal = domain
	}
	if rootDomain != "" {
		rootDomainVal = rootDomain
	}
	if req.IP != "" {
		ipVal = req.IP
	}
	if cseg != "" {
		csegVal = cseg
	}

	var id string
	err = q.QueryRow(`
INSERT INTO assets(
    type, url, method, domain, ip, port, root_domain,
    params, company_id, company_source, task_ids, c_segment
)
VALUES ('endpoint', $1, $2, $3, $4, $5, $6, $7::jsonb[], $8, 'scope', $9::bigint[], $10::cidr)
ON CONFLICT (url, method) WHERE type = 'endpoint' DO UPDATE SET
    params     = (
        SELECT ARRAY(
            SELECT DISTINCT ON ((elem->>'location'), (elem->>'name')) elem
            FROM unnest(assets.params || EXCLUDED.params) AS elem
            ORDER BY (elem->>'location'), (elem->>'name'), elem::text DESC
        )
    ),
    ip         = COALESCE(EXCLUDED.ip,        assets.ip),
    c_segment  = COALESCE(EXCLUDED.c_segment, assets.c_segment),
    task_ids   = (SELECT ARRAY(SELECT DISTINCT unnest(assets.task_ids || EXCLUDED.task_ids))),
    company_id = CASE
        WHEN assets.company_source = 'explicit' AND assets.company_id IS NOT NULL THEN assets.company_id
        ELSE COALESCE(EXCLUDED.company_id, assets.company_id)
    END,
    company_source = CASE
        WHEN assets.company_source = 'explicit' AND assets.company_id IS NOT NULL THEN 'explicit'
        WHEN EXCLUDED.company_id IS NOT NULL THEN 'scope'
        ELSE assets.company_source
    END,
    last_seen  = now()
RETURNING id`,
		normURL, method, domainVal, ipVal, NullableInt(port), rootDomainVal,
		paramsJSON, companyID, taskIDs, csegVal,
	).Scan(&id)
	if err != nil {
		return "", err
	}
	// side effects: endpoint previously registered none — register its host as
	// root_domain + subdomain(+IP) so those asset types get populated too.
	linkHostAssets(q, r, domain, rootDomain, req.TaskID)
	if req.IP != "" {
		_, _ = UpsertIP(q, r, UpsertIPReq{IP: req.IP, TaskID: req.TaskID})
	}
	return id, nil
}
