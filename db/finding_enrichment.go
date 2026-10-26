package db

// finding_enrichment 存储层已迁 domain/finding（Step 3 批 3 第七刀），
// 本文件留别名与转发，调用点零改动。

import (
	dfe "finduai/domain/finding"
)

func (d *DB) GetFindingEnrichment(nodeID int64) (*dfe.FindingEnrichment, error) {
	return dfe.GetFindingEnrichment(d, nodeID)
}

func (d *DB) GetFindingEnrichments(nodeIDs []int64) (map[int64]*dfe.FindingEnrichment, error) {
	return dfe.GetFindingEnrichments(d, nodeIDs)
}

func (d *DB) UpsertFindingEnrichment(e *dfe.FindingEnrichment) error {
	return dfe.UpsertFindingEnrichment(d, e)
}

func (d *DB) UpdateFindingEnrichmentFields(nodeID int64, name, target, severity, status string) error {
	return dfe.UpdateFindingEnrichmentFields(d, nodeID, name, target, severity, status)
}
