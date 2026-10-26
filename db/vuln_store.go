package db

import dfe "finduai/domain/finding"

// VulnKBStore 是 intel 插件对漏洞知识库/情报的读写面。实现位于
// plugins/intel/vulnstore；db 只保留这个注入槽位，不 import 插件。
type VulnKBStore interface {
	UpsertVulnKB(dfe.VulnKBEntry) (bool, error)
	ListVulnKB(q, vulnType string, page, size int) ([]dfe.VulnKBEntry, int, error)
	GetVulnKB(id int64) (*dfe.VulnKBEntry, error)
	VulnKBStats() (*dfe.VulnKBStats, error)

	ListVulnIntel(q, severity string, page, size int) ([]dfe.EnrichedVulnIntel, int, error)
	UpsertVulnIntel(dfe.VulnIntel) (bool, error)
	PendingVulnIntel(limit int) ([]dfe.VulnIntel, error)
	EnrichVulnIntel(dfe.VulnIntelEnrich) error
}

// SetVulnKBStore 在装配层注入插件存储。只应在启动阶段调用一次。
func (d *DB) SetVulnKBStore(s VulnKBStore) {
	if d != nil {
		d.vulnKB = s
	}
}

// VulnKBStore 返回插件存储；未注入时为 nil。
func (d *DB) VulnKBStore() VulnKBStore {
	if d == nil {
		return nil
	}
	return d.vulnKB
}
