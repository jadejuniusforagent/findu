package db

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ChatMention is a lightweight search result. Details are read again on send.
type ChatMention struct {
	Kind        string `json:"kind"`
	ID          string `json:"id"` // 资产是 UUID；findings/companies 是数字串
	Label       string `json:"label"`
	Description string `json:"description"`
}

type ChatMentionPage struct {
	Items      []ChatMention `json:"items"`
	NextCursor string        `json:"next_cursor,omitempty"`
	sk         []string      // 内部：每行稳定排序键，不进 JSON
}

var ErrInvalidChatMentionCursor = errors.New("分页位置无效，请重新搜索")

type chatMentionCursor struct {
	SK    string `json:"sk"`
	Kind  string `json:"kind"`
	Exact bool   `json:"exact"`
	Scope string `json:"scope"`
	Query string `json:"query"`
}

func ValidChatMentionKind(kind string) bool {
	switch kind {
	case "finding", "company", "asset", "endpoint", "ip", "app", "root_domain", "subdomain", "service":
		return true
	}
	return false
}

// SearchChatMentions searches the shared catalog, just like the asset/finding
// pages. Values remain SQL parameters; %, _ and backslash are literal search text.
func (d *DB) SearchChatMentions(ctx context.Context, kind, query string) ([]ChatMention, error) {
	page, err := d.SearchChatMentionsPage(ctx, kind, query, "")
	return page.Items, err
}

// SearchChatMentionsPage uses the last result's stable sort key rather than an
// offset, so loading later pages does not repeatedly skip all earlier results.
//
// 资产主键合并后是 UUID(text)，findings/companies 仍是 bigint：UNION 里统一
// 转 id::text，排序键 sk 用 lpad(id::text,20,'0')——数字 ID 补零后保持数值顺序，
// UUID 长 36 位不受影响。cursor 存 sk 而不是数字 ID。
func (d *DB) SearchChatMentionsPage(ctx context.Context, kind, query, cursor string) (ChatMentionPage, error) {
	page := ChatMentionPage{Items: make([]ChatMention, 0)}
	if kind != "" && !ValidChatMentionKind(kind) {
		return page, fmt.Errorf("不支持的引用类型")
	}
	var after chatMentionCursor
	if cursor != "" {
		if len(cursor) > 2048 {
			return page, ErrInvalidChatMentionCursor
		}
		blob, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || json.Unmarshal(blob, &after) != nil || after.SK == "" ||
			!ValidChatMentionKind(after.Kind) || after.Scope != kind || after.Query != query {
			return page, ErrInvalidChatMentionCursor
		}
	}
	pattern := "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(query) + "%"
	rows, err := d.QueryContext(ctx, `
SELECT kind, id, label, description, sk FROM (
 (SELECT 'finding' AS kind, id::text AS id,
         COALESCE(NULLIF(name,''), vulnclass) AS label,
         concat_ws(' · ', severity, status, left(summary, 160)) AS description,
         lpad(id::text, 20, '0') AS sk
  FROM findings WHERE ($1='' OR $1='finding') AND
    ($2='' OR id::text=$2 OR concat_ws(' ',name,vulnclass,summary) ILIKE $3)
    AND ($4='' OR (id::text<>$2 AND (lpad(id::text,20,'0')<$4 OR (lpad(id::text,20,'0')=$4 AND 'finding'>$5))))
  ORDER BY (id::text=$2) DESC, sk DESC LIMIT 21)
 UNION ALL
 (SELECT 'company' AS kind, id::text AS id, name AS label, nkey AS description,
         lpad(id::text, 20, '0') AS sk
  FROM companies
  WHERE ($1='' OR $1='company') AND ($2='' OR id::text=$2 OR name ILIKE $3 OR nkey ILIKE $3)
    AND ($4='' OR (id::text<>$2 AND (lpad(id::text,20,'0')<$4 OR (lpad(id::text,20,'0')=$4 AND 'company'>$5))))
  ORDER BY (id::text=$2) DESC, sk DESC LIMIT 21)
 UNION ALL
 (SELECT type AS kind, id,
    CASE WHEN type='endpoint' THEN concat_ws(' ',NULLIF(method,''),url)
         ELSE COALESCE(NULLIF(app_name,''),NULLIF(url,''),NULLIF(domain,''),NULLIF(ip,''),NULLIF(bundle_id,''),'资产 #'||id) END AS label,
    concat_ws(' · ',type,NULLIF(page_title,''),NULLIF(service_name,''),NULLIF(bundle_id,''),NULLIF(ip,''),port::text) AS description,
    id AS sk
  FROM assets WHERE ($1='' OR $1='asset' OR type=$1) AND
    ($2='' OR id=$2 OR concat_ws(' ',domain,root_domain,ip,url,app_name,bundle_id,page_title,service_name,method) ILIKE $3)
    AND ($4='' OR (id<>$2 AND (id<$4 OR (id=$4 AND type>$5))))
  ORDER BY (id=$2) DESC, sk DESC LIMIT 21)
) matches ORDER BY (id=$2) DESC, sk DESC, kind LIMIT 21`, kind, query, pattern, after.SK, after.Kind)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var item ChatMention
		var sk string
		if err := rows.Scan(&item.Kind, &item.ID, &item.Label, &item.Description, &sk); err != nil {
			return page, err
		}
		page.Items = append(page.Items, item)
		page.sk = append(page.sk, sk)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if len(page.Items) > 20 {
		page.Items = page.Items[:20]
		page.sk = page.sk[:20]
		last := page.Items[19]
		blob, _ := json.Marshal(chatMentionCursor{page.sk[19], last.Kind, last.ID == query, kind, query})
		page.NextCursor = base64.RawURLEncoding.EncodeToString(blob)
	}
	page.sk = nil
	return page, nil
}
