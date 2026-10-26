// asset_import_bridge.go —— APK auto-analyze 导入依赖的资产导入桥。
//
// 内部桥接同语义的 UpsertAssets（PG 方言，经 DSN 方言层运行于同一 PG）。
// 差异：tags 整体覆盖、无返回 scored 清单——对 APK auto-analyze
// 的消费面（导入包内域名/IP 资产）行为足够。
package database

import "errors"

// ErrNilAsset 传入资产为 nil。
var ErrNilAsset = errors.New("asset is nil")

// UpsertAssetsNoRescore 批量 upsert 资产（auto-analyze 导入用，桥接 findu UpsertAssets）。
// 本桥整体覆盖 tags（不合并既有 tags、不跳过风险分重算）。
func (db *DB) UpsertAssetsNoRescore(assets []*Asset, ownerUserID string, allowGlobal ...bool) (AssetImportResult, error) {
	return db.UpsertAssets(assets, ownerUserID, allowGlobal...)
}

// ValidateAssetForImport 导入前校验单条资产（normalize 后验证）。
func ValidateAssetForImport(a *Asset) error {
	if a == nil {
		return ErrNilAsset
	}
	normalizeAsset(a)
	return validateAsset(a)
}
