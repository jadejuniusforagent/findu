// Package vulnstore 是 intel 插件的漏洞知识库/情报 SQL 存储层。
//
// 表归属仍是 plugin-intel（见 core/ownership），但查询实现不再留在 domain/finding：
// 插件通过 core/store.Querier 访问数据库，宿主只注入连接，不把 db 包暴露给插件。
package vulnstore

import (
	"database/sql"
	"errors"
	"fmt"

	"finduai/core/store"
	"finduai/domain/finding"
)

// Store 是 vuln_kb / vuln_intel 的 SQL 实现。零值不可用，必须用 New 构造。
type Store struct {
	q store.Querier
}

// New 用宿主提供的连接构造存储。
func New(q store.Querier) *Store { return &Store{q: q} }

// UpsertVulnKB 按 content_hash 幂等插入,返回是否为新记录。
func (s *Store) UpsertVulnKB(e finding.VulnKBEntry) (bool, error) {
	var id int64
	err := s.q.QueryRow(`
INSERT INTO vuln_kb(content_hash, title, product, vuln_type, cve, fingerprint,
                    request_line, headers, payload, response_sig, expected, body, source_path)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
ON CONFLICT (content_hash) DO NOTHING
RETURNING id`,
		e.ContentHash, e.Title, e.Product, e.VulnType, e.CVE, e.Fingerprint,
		e.RequestLine, e.Headers, e.Payload, e.ResponseSig, e.Expected, e.Body, e.SourcePath,
	).Scan(&id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// vulnKBFilter 组装检索条件。q 匹配 title/product/cve/fingerprint 的 ILIKE;
// vulnType 精确(取值来自统计接口,不猜)。
func vulnKBFilter(q, vulnType string) (string, []any, int) {
	where := " WHERE 1=1"
	var args []any
	if q != "" {
		args = append(args, "%"+q+"%")
		where += fmt.Sprintf(" AND (title ILIKE $%d OR product ILIKE $%d OR cve ILIKE $%d OR fingerprint ILIKE $%d)",
			len(args), len(args), len(args), len(args))
	}
	if vulnType != "" {
		args = append(args, vulnType)
		where += fmt.Sprintf(" AND vuln_type = $%d", len(args))
	}
	return where, args, len(args)
}

// ListVulnKB 分页检索,列表不带 body(大字段,详情接口才给)。
func (s *Store) ListVulnKB(query, vulnType string, page, size int) ([]finding.VulnKBEntry, int, error) {
	if size <= 0 {
		size = 50
	}
	if page < 0 {
		page = 0
	}
	where, args, argN := vulnKBFilter(query, vulnType)

	var total int
	if err := s.q.QueryRow("SELECT COUNT(*) FROM vuln_kb"+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	dataQ := `
SELECT id, title, product, vuln_type, COALESCE(cve,''), COALESCE(fingerprint,''),
       COALESCE(request_line,''), COALESCE(headers,''), COALESCE(payload,''),
       COALESCE(response_sig,''), COALESCE(expected,''), source_path, created_at
FROM vuln_kb` + where + fmt.Sprintf(`
ORDER BY id DESC
LIMIT $%d OFFSET $%d`, argN+1, argN+2)

	args = append(args, size, page*size)
	rows, err := s.q.Query(dataQ, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := []finding.VulnKBEntry{}
	for rows.Next() {
		var e finding.VulnKBEntry
		if err := rows.Scan(&e.ID, &e.Title, &e.Product, &e.VulnType, &e.CVE, &e.Fingerprint,
			&e.RequestLine, &e.Headers, &e.Payload, &e.ResponseSig, &e.Expected, &e.SourcePath, &e.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, e)
	}
	return out, total, rows.Err()
}

// GetVulnKB 返回单条完整记录(含 body)。
func (s *Store) GetVulnKB(id int64) (*finding.VulnKBEntry, error) {
	var e finding.VulnKBEntry
	err := s.q.QueryRow(`
SELECT id, title, product, vuln_type, COALESCE(cve,''), COALESCE(fingerprint,''),
       COALESCE(request_line,''), COALESCE(headers,''), COALESCE(payload,''),
       COALESCE(response_sig,''), COALESCE(expected,''), body, source_path, created_at
FROM vuln_kb WHERE id = $1`, id,
	).Scan(&e.ID, &e.Title, &e.Product, &e.VulnType, &e.CVE, &e.Fingerprint,
		&e.RequestLine, &e.Headers, &e.Payload, &e.ResponseSig, &e.Expected, &e.Body, &e.SourcePath, &e.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// VulnKBStats 返回库概览。types 取条数最多的前 30 类(前端下拉用)。
func (s *Store) VulnKBStats() (*finding.VulnKBStats, error) {
	st := &finding.VulnKBStats{Types: []finding.VulnKBTypeCount{}}
	if err := s.q.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT product),
	COUNT(*) FILTER (WHERE cve <> ''), COUNT(*) FILTER (WHERE payload <> ''),
	COUNT(*) FILTER (WHERE fingerprint <> '') FROM vuln_kb`,
	).Scan(&st.Total, &st.Products, &st.WithCVE, &st.WithPayload, &st.WithFingerprint); err != nil {
		return nil, err
	}
	rows, err := s.q.Query(`SELECT vuln_type, COUNT(*) FROM vuln_kb
	WHERE vuln_type <> '' GROUP BY vuln_type ORDER BY COUNT(*) DESC, vuln_type LIMIT 30`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var t finding.VulnKBTypeCount
		if err := rows.Scan(&t.Name, &t.Count); err != nil {
			return nil, err
		}
		st.Types = append(st.Types, t)
	}
	return st, rows.Err()
}
