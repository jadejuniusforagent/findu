package db

import (
	"finduai/domain/asset"
	"finduai/domain/task"
)

// ListAssetInterceptRules returns all rules, built-ins first then newest first.
func (d *DB) ListAssetInterceptRules() ([]task.InterceptRule, error) {
	return asset.ListAssetInterceptRules(d)
}

// CreateAssetInterceptRule inserts a new user rule (builtin is always false here).
func (d *DB) CreateAssetInterceptRule(kind, pattern, note string, enabled bool) (task.InterceptRule, error) {
	return asset.CreateAssetInterceptRule(d, kind, pattern, note, enabled)
}

// UpdateAssetInterceptRule replaces the editable fields of an existing rule.
func (d *DB) UpdateAssetInterceptRule(id int64, kind, pattern, note string, enabled bool) (task.InterceptRule, error) {
	return asset.UpdateAssetInterceptRule(d, id, kind, pattern, note, enabled)
}

// DeleteAssetInterceptRule removes a rule (built-in rules are deletable too).
func (d *DB) DeleteAssetInterceptRule(id int64) error {
	return asset.DeleteAssetInterceptRule(d, id)
}

// ToggleAssetInterceptRule flips the enabled state of a rule.
func (d *DB) ToggleAssetInterceptRule(id int64, enabled bool) error {
	return asset.ToggleAssetInterceptRule(d, id, enabled)
}
