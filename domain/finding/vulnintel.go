package finding

// vuln_intel（自动抓取的漏洞情报）及富化字段的跨层类型。SQL 实现已随 intel
// 插件迁到 plugins/intel/vulnstore（Step 4 批 5）；domain 只保留 API/JSON 契约。
// 与 VulnKBEntry(1day 打击库)分表:情报回答「新披露了什么」,1day 回答
// 「指纹命中后怎么打」。

import "time"

// VulnIntel 是一条自动抓取的漏洞情报(标题/等级/CVE/披露时间/参考链接)。
type VulnIntel struct {
	ID          int64     `json:"id"`
	UniqueKey   string    `json:"-"`
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	Severity    string    `json:"severity"`
	CVE         string    `json:"cve"`
	Disclosure  string    `json:"disclosure"`
	Solutions   string    `json:"solutions,omitempty"`
	Source      string    `json:"source"`
	SourceURL   string    `json:"source_url"`
	Tags        string    `json:"tags"` // JSON 数组文本
	Refs        string    `json:"refs"` // JSON 数组文本
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// VulnIntelEnrich 一次富化的写回载荷。零值字段不覆盖(只补缺语义)。
type VulnIntelEnrich struct {
	ID              int64
	GithubPoc       string  // JSON 数组文本
	CvssScore       float64 // >0 才写
	CvssVector      string
	Cwes            string // JSON 数组文本
	Affected        string // JSON 数组文本
	LabOnly         bool
	HasLabOnly      bool // lab_only 显式置位才写
	SupplementTitle string
	HasTitle        bool
	Enriched        bool // true = 写 enriched_at(本次富化完成)
}

// VulnIntelPOC 是 github_poc JSON 数组里的一项(前端直接消费)。
type VulnIntelPOC struct {
	FullName    string   `json:"full_name"`
	URL         string   `json:"url"`
	Description string   `json:"description,omitempty"`
	Stars       int      `json:"stars"`
	PushedAt    string   `json:"pushed_at,omitempty"`
	Verdict     string   `json:"verdict,omitempty"`
	TriageScore int      `json:"triage_score,omitempty"`
	Reasons     []string `json:"triage_reasons,omitempty"`
}

// EnrichedVulnIntel 是带富化字段的完整情报记录(列表/详情接口)。
type EnrichedVulnIntel struct {
	VulnIntel
	GithubPoc  string     `json:"github_poc"`
	CvssScore  *float64   `json:"cvss_score"`
	CvssVector string     `json:"cvss_vector"`
	Cwes       string     `json:"cwes"`
	Affected   string     `json:"affected"`
	LabOnly    bool       `json:"lab_only"`
	EnrichedAt *time.Time `json:"enriched_at"`
}
