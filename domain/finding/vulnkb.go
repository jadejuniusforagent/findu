package finding

// vuln_kb（1day 打击库）的跨层类型。SQL 实现已随 intel 插件迁到
// plugins/intel/vulnstore（Step 4 批 5）；domain 只保留 API/JSON 契约。

import "time"

// VulnKBEntry 是漏洞知识库的一条结构化记录,来源为仓库 vulnkb/ 目录下的
// 1day 漏洞 markdown。导入按 content_hash 幂等。
type VulnKBEntry struct {
	ID          int64     `json:"id"`
	ContentHash string    `json:"-"`
	Title       string    `json:"title"`
	Product     string    `json:"product"`
	VulnType    string    `json:"vuln_type"`
	CVE         string    `json:"cve"`
	Fingerprint string    `json:"fingerprint"`
	RequestLine string    `json:"request_line"`
	Headers     string    `json:"headers"`
	Payload     string    `json:"payload"`
	ResponseSig string    `json:"response_sig"`
	Expected    string    `json:"expected"`
	Body        string    `json:"body,omitempty"`
	SourcePath  string    `json:"source_path"`
	CreatedAt   time.Time `json:"created_at"`
}

// VulnKBTypeCount 是统计接口里「按漏洞类型的条数」。
type VulnKBTypeCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// VulnKBStats 是漏洞库的概览统计。
type VulnKBStats struct {
	Total           int               `json:"total"`
	Products        int               `json:"products"`
	WithCVE         int               `json:"with_cve"`
	WithPayload     int               `json:"with_payload"`
	WithFingerprint int               `json:"with_fingerprint"`
	Types           []VulnKBTypeCount `json:"types"`
}
