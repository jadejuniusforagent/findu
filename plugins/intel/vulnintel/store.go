package vulnintel

import "finduai/domain/finding"

// Store 是抓取/富化管线需要的持久化能力。宿主注入 db.DB；插件不 import
// finduai/db，保持 plugins -> db 的分层红线。
type Store interface {
	UpsertVulnIntel(finding.VulnIntel) (bool, error)
	PendingVulnIntel(limit int) ([]finding.VulnIntel, error)
	EnrichVulnIntel(finding.VulnIntelEnrich) error
}

// Settings 是管线读取热配置的最小面（enabled / interval / github_token）。
// 与 Store 分开，避免插件存储为了 settings 复制核心配置 SQL。
type Settings interface {
	GetSetting(key string) (value string, ok bool, err error)
}
