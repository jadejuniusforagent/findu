package db

import "strings"

// AssetFinduRow 前端 /function/assets 所需的 findu 共表资产行（读 PG assets 表
// 的 findu 扩展列，形状与 findu 旧接口对齐）。
type AssetFinduRow struct {
	ID                 string `json:"id"`
	Host               string `json:"host,omitempty"`
	IP                 string `json:"ip,omitempty"`
	Port               int    `json:"port,omitempty"`
	Domain             string `json:"domain,omitempty"`
	Protocol           string `json:"protocol,omitempty"`
	Title              string `json:"title,omitempty"`
	Server             string `json:"server,omitempty"`
	Status             string `json:"status,omitempty"`
	Source             string `json:"source,omitempty"`
	SourceQuery        string `json:"source_query,omitempty"`
	VulnerabilityCount int    `json:"vulnerability_count,omitempty"`
	RiskLevel          string `json:"risk_level,omitempty"`
	FirstSeenAt        string `json:"first_seen_at,omitempty"`
	LastSeenAt         string `json:"last_seen_at,omitempty"`
}

const assetFinduCols = `id, host, COALESCE(ip,''), COALESCE(port,0), COALESCE(domain,''),
	COALESCE(protocol,''), COALESCE(title,''), COALESCE(server,''),
	COALESCE(status,'active'), COALESCE(source,'manual'), COALESCE(source_query,''),
	COALESCE(vulnerability_count,0), COALESCE(risk_level,'unassessed'),
	COALESCE(first_seen_at::text,''), COALESCE(last_seen_at::text,'')`

// ListAssetsFinduNative 分页查询 assets 表（q 对 host/ip/domain/title 模糊匹配）。
func (s *AssetStore) ListAssetsFinduNative(q, status string, limit, offset int) ([]AssetFinduRow, error) {
	q = strings.TrimSpace(q)
	status = strings.TrimSpace(status)
	rows, err := s.db.Query(`
SELECT `+assetFinduCols+`
  FROM assets
 WHERE ($1 = '' OR host ILIKE '%'||$1||'%' OR ip ILIKE '%'||$1||'%'
        OR domain ILIKE '%'||$1||'%' OR title ILIKE '%'||$1||'%')
   AND ($2 = '' OR status = $2)
 ORDER BY last_seen_at DESC
 LIMIT $3 OFFSET $4`, q, status, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AssetFinduRow{}
	for rows.Next() {
		var r AssetFinduRow
		if err := rows.Scan(&r.ID, &r.Host, &r.IP, &r.Port, &r.Domain, &r.Protocol,
			&r.Title, &r.Server, &r.Status, &r.Source, &r.SourceQuery,
			&r.VulnerabilityCount, &r.RiskLevel, &r.FirstSeenAt, &r.LastSeenAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CountAssetsFinduNative 返回匹配资产总数。
func (s *AssetStore) CountAssetsFinduNative(q, status string) (int, error) {
	q = strings.TrimSpace(q)
	status = strings.TrimSpace(status)
	var n int
	err := s.db.QueryRow(`
SELECT count(*) FROM assets
 WHERE ($1 = '' OR host ILIKE '%'||$1||'%' OR ip ILIKE '%'||$1||'%'
        OR domain ILIKE '%'||$1||'%' OR title ILIKE '%'||$1||'%')
   AND ($2 = '' OR status = $2)`, q, status).Scan(&n)
	return n, err
}