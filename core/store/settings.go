package store

import "database/sql"

// 平台级 KV 配置（表 settings）。UI 在运行时开关的功能（如 traffic_capture）
// 和只允许首次设置的密钥类配置（如 auth.password_hash）都落这里。
//
// 为什么放 core/store：它不属于任何单一领域，是平台基础设施；任何 domain 都可以
// 通过它读写，而不必反向依赖某个领域包。

// GetSetting 读取一个键；键不存在时 ok=false（调用方用自己的默认值）。
func GetSetting(q Querier, key string) (value string, ok bool, err error) {
	err = q.QueryRow(`SELECT value FROM settings WHERE key=$1`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

// SetSetting 写入或覆盖一个键。
func SetSetting(q Querier, key, value string) error {
	_, err := q.Exec(`
INSERT INTO settings(key, value) VALUES ($1, $2)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, key, value)
	return err
}

// InsertSettingIfAbsent 只在 key 尚未存在时写入，已存在则原样保留并返回 inserted=false。
//
// 给 auth.password_hash 这类「只允许首次设置」的键用：判定落在数据库的主键约束上，
// 调用方先 GetSetting 再写的那种检查只是快速失败路径——读出错或并发撞车时，
// 已有的值也不会被 ON CONFLICT DO UPDATE 顺手覆盖掉。
func InsertSettingIfAbsent(q Querier, key, value string) (inserted bool, err error) {
	res, err := q.Exec(`
INSERT INTO settings(key, value) VALUES ($1, $2)
ON CONFLICT (key) DO NOTHING`, key, value)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// GetBool 读布尔型设置；未设置或无法解析时返回 def。
// 只认 "true" 与 "1"，其余一律当 false —— 与既有行为一致。
func GetBool(q Querier, key string, def bool) bool {
	v, ok, err := GetSetting(q, key)
	if err != nil || !ok {
		return def
	}
	return v == "true" || v == "1"
}

// SetBool 把布尔值规范成 "true"/"false" 落库。
func SetBool(q Querier, key string, val bool) error {
	if val {
		return SetSetting(q, key, "true")
	}
	return SetSetting(q, key, "false")
}
