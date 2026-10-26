package vulnstore

import (
	"strconv"

	"finduai/domain/finding"
)

// UpsertVulnIntel 按唯一键(源:源内ID)入库,返回是否为新记录(xmax=0 表示
// 本次 INSERT 而非 DO UPDATE 命中)。
func (s *Store) UpsertVulnIntel(v finding.VulnIntel) (bool, error) {
	var id int64
	var inserted bool
	err := s.q.QueryRow(`
INSERT INTO vuln_intel(unique_key, title, description, severity, cve, disclosure,
                       solutions, source, source_url, tags, refs)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
ON CONFLICT (unique_key) DO UPDATE SET
  title=EXCLUDED.title, description=EXCLUDED.description, severity=EXCLUDED.severity,
  cve=EXCLUDED.cve, disclosure=EXCLUDED.disclosure, solutions=EXCLUDED.solutions,
  tags=EXCLUDED.tags, refs=EXCLUDED.refs, updated_at=now()
RETURNING id, (xmax = 0)`,
		v.UniqueKey, v.Title, v.Description, v.Severity, v.CVE, v.Disclosure,
		v.Solutions, v.Source, v.SourceURL, v.Tags, v.Refs,
	).Scan(&id, &inserted)
	if err != nil {
		return false, err
	}
	return inserted, nil
}

// ListVulnIntel 分页列出情报(含富化字段),按入库时间倒序。severity 精确
// 过滤,q 模糊匹配标题/CVE/来源。
func (s *Store) ListVulnIntel(query, severity string, page, size int) ([]finding.EnrichedVulnIntel, int, error) {
	if size <= 0 {
		size = 50
	}
	if page < 0 {
		page = 0
	}
	where := " WHERE 1=1" // 列名不带前缀;dataQ 处统一替换表名
	var args []any
	if query != "" {
		args = append(args, "%"+query+"%")
		where += ` AND (v.title ILIKE $1 OR v.cve ILIKE $1 OR v.source ILIKE $1)`
	}
	if severity != "" {
		args = append(args, severity)
		where += ` AND v.severity = $` + strconv.Itoa(len(args))
	}
	var total int
	if err := s.q.QueryRow("SELECT COUNT(*) FROM vuln_intel v"+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	dataQ := `
SELECT v.id, v.title, COALESCE(v.description,''), COALESCE(v.severity,''), COALESCE(v.cve,''),
       COALESCE(v.disclosure,''), COALESCE(v.solutions,''), COALESCE(v.source,''),
       COALESCE(v.source_url,''), v.tags, v.refs, v.created_at, v.updated_at,
       v.github_poc, v.cvss_score, COALESCE(v.cvss_vector,''), v.cwes, v.affected,
       v.lab_only, v.enriched_at
FROM vuln_intel v` + where + `
ORDER BY v.created_at DESC, v.id DESC
LIMIT ` + strconv.Itoa(size) + " OFFSET " + strconv.Itoa(page*size)
	rows, err := s.q.Query(dataQ, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []finding.EnrichedVulnIntel{}
	for rows.Next() {
		v := finding.EnrichedVulnIntel{}
		if err := rows.Scan(&v.ID, &v.Title, &v.Description, &v.Severity, &v.CVE,
			&v.Disclosure, &v.Solutions, &v.Source, &v.SourceURL, &v.Tags, &v.Refs,
			&v.CreatedAt, &v.UpdatedAt,
			&v.GithubPoc, &v.CvssScore, &v.CvssVector, &v.Cwes, &v.Affected,
			&v.LabOnly, &v.EnrichedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, v)
	}
	return out, total, rows.Err()
}

// PendingVulnIntel 挑待富化条目:带 CVE 且(未富化或超 7 天)。
func (s *Store) PendingVulnIntel(limit int) ([]finding.VulnIntel, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.q.Query(`
SELECT id, unique_key, title, COALESCE(description,''), COALESCE(severity,''), COALESCE(cve,''),
       COALESCE(disclosure,''), COALESCE(solutions,''), COALESCE(source,''),
       COALESCE(source_url,''), tags, refs, created_at, updated_at
FROM vuln_intel
WHERE cve <> '' AND (enriched_at IS NULL OR enriched_at < now() - interval '7 days')
ORDER BY enriched_at NULLS FIRST, created_at DESC
LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []finding.VulnIntel{}
	for rows.Next() {
		var v finding.VulnIntel
		if err := rows.Scan(&v.ID, &v.UniqueKey, &v.Title, &v.Description, &v.Severity, &v.CVE,
			&v.Disclosure, &v.Solutions, &v.Source, &v.SourceURL, &v.Tags, &v.Refs,
			&v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// EnrichVulnIntel 富化写回。全部字段只补缺:已有非空值不被空值覆盖。
func (s *Store) EnrichVulnIntel(e finding.VulnIntelEnrich) error {
	_, err := s.q.Exec(`
UPDATE vuln_intel SET
  github_poc   = CASE WHEN $2 <> '[]' THEN $2 ELSE github_poc END,
  cvss_score   = COALESCE($3, cvss_score),
  cvss_vector  = CASE WHEN $4 <> '' THEN $4 ELSE cvss_vector END,
  cwes         = CASE WHEN $5 <> '[]' THEN $5 ELSE cwes END,
  affected     = CASE WHEN $6 <> '[]' THEN $6 ELSE affected END,
  lab_only     = CASE WHEN $7 THEN TRUE ELSE lab_only END,
  title        = CASE WHEN $8 <> '' AND (title = '' OR title = cve OR length(title) < 8) THEN $8 ELSE title END,
  enriched_at  = CASE WHEN $9 THEN now() ELSE enriched_at END
WHERE id = $1`,
		e.ID, e.GithubPoc, nullableFloat(e.CvssScore), e.CvssVector, e.Cwes, e.Affected,
		e.HasLabOnly && e.LabOnly, e.SupplementTitle, e.Enriched)
	return err
}

func nullableFloat(v float64) any {
	if v <= 0 {
		return nil
	}
	return v
}
