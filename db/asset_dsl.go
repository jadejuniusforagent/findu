package db

import (
	"fmt"
	"strings"

	"finduai/domain/asset"
)

// 资产 DSL 查询 —— 装配层。
// DSL 解析/编译引擎（字段映射、tokeniser、parser、SQL 构造器）是纯文本→SQL
// 机械，已在 domain/asset/dsl.go（批 3 第八刀）。本文件保留：
//   - CountDSL / QueryDSL 的 *AssetStore 转发（后者在 taskID>0 时做任务源注水）；
//   - 依赖 task 域 scopeTargetCTE 的 InScope 方法（scope 谓词属任务域，等
//     task 域 SQL 下沉后一起搬）；
//   - server 直接引用的包级 ValidateDSL 转发。

// CountDSL returns the total number of assets matching a DSL expression (and optional
// type), for server-side pagination — same WHERE as QueryDSL, without LIMIT/OFFSET.
// taskID > 0 scopes the count to assets attached to that task.
func (s *AssetStore) CountDSL(dsl, typ string, taskID int64) (int, error) {
	return asset.CountDSL(s.db, dsl, typ, taskID)
}

// QueryDSL executes a DSL query string against the asset store.
// typ is an optional asset type filter applied independently of the DSL expression.
// taskID > 0 scopes results to assets attached to that task and hydrates each
// row's per-task source metadata (as QueryByTask does).
func (s *AssetStore) QueryDSL(dsl, typ string, taskID int64, limit, offset int) ([]*asset.Asset, error) {
	assets, err := asset.QueryDSL(s.db, dsl, typ, taskID, limit, offset)
	if err != nil {
		return nil, err
	}
	if taskID > 0 {
		if err := s.hydrateTaskAssetSources(taskID, assets); err != nil {
			return nil, err
		}
	}
	return assets, nil
}

// QueryDSLInScope is QueryDSL restricted to assets that BELONG to taskID's (and its
// direct source tasks') declared scope — membership, not literal value: a
// root_domain scope returns every subdomain / service / endpoint under it. This is
// the agent-facing list_assets path, so an agent queries the task's relevant assets
// instead of the whole shared库. taskID<=0 (non-task contexts: Auto / pentest / chat)
// has no scope to honor and falls back to the plain global QueryDSL. Rows carry the
// same per-task source metadata as QueryByTask.
func (s *AssetStore) QueryDSLInScope(dsl, typ string, taskID int64, limit, offset int) ([]*asset.Asset, error) {
	if taskID <= 0 {
		return s.QueryDSL(dsl, typ, 0, limit, offset)
	}
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	node, err := asset.ParseDSL(dsl)
	if err != nil {
		return nil, err
	}
	// $1 is reserved for taskID (scopeTargetCTE); DSL placeholders start at $2.
	where, dslArgs, err := asset.BuildDSLWhereBase(node, 1)
	if err != nil {
		return nil, err
	}
	args := []any{taskID}
	args = append(args, dslArgs...)
	if typ != "" {
		args = append(args, typ)
		where += fmt.Sprintf(" AND type = $%d", len(args))
	}
	where += " AND id IN (SELECT id FROM target)"
	args = append(args, limit, offset)
	q := `WITH ` + scopeTargetCTE + ` ` + asset.AssetSelectCols + " WHERE " + where +
		fmt.Sprintf(" ORDER BY last_seen DESC, id DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args))
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	assets, err := asset.ScanAssets(rows)
	if err != nil {
		return nil, err
	}
	if err := s.hydrateTaskAssetSources(taskID, assets); err != nil {
		return nil, err
	}
	return assets, nil
}

// GetByIDsInScope is GetByIDs restricted to ids that BELONG to taskID's (and its
// direct source tasks') declared scope, so an agent cannot reach out-of-scope
// assets by id. taskID<=0 (non-task contexts) falls back to the global GetByIDs.
// Out-of-scope ids are silently dropped from the result (not an error).
func (s *AssetStore) GetByIDsInScope(taskID int64, ids []string) ([]*asset.Asset, error) {
	if taskID <= 0 {
		return s.GetByIDs(ids)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	args := make([]any, 0, len(ids)+1)
	args = append(args, taskID) // $1 reserved for scopeTargetCTE
	placeholders := make([]string, len(ids))
	for i, id := range ids {
		placeholders[i] = fmt.Sprintf("$%d", i+2)
		args = append(args, id)
	}
	q := `WITH ` + scopeTargetCTE + ` ` + asset.AssetSelectCols +
		" WHERE id IN (" + strings.Join(placeholders, ",") + ")" +
		" AND id IN (SELECT id FROM target) ORDER BY last_seen DESC, id DESC"
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	assets, err := asset.ScanAssets(rows)
	if err != nil {
		return nil, err
	}
	if err := s.hydrateTaskAssetSources(taskID, assets); err != nil {
		return nil, err
	}
	return assets, nil
}
