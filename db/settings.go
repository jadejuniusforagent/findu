package db

// 平台级 KV 配置（表 settings）已迁到 core/store（Step 3 批 3 第二刀）。
//
// 这里只留方法转发：db 包内还有几十处 d.GetSetting/d.GetBool 调用，保留同名方法
// 让它们零改动；等批 4/5 收尾时再逐处换成 core/store 直调并删掉这层。
//
// 原实现见 git 历史（61 行，5 个方法）。

import "finduai/core/store"

func (d *DB) GetSetting(key string) (value string, ok bool, err error) {
	return store.GetSetting(d, key)
}

func (d *DB) SetSetting(key, value string) error { return store.SetSetting(d, key, value) }

func (d *DB) InsertSettingIfAbsent(key, value string) (inserted bool, err error) {
	return store.InsertSettingIfAbsent(d, key, value)
}

func (d *DB) GetBool(key string, def bool) bool { return store.GetBool(d, key, def) }

func (d *DB) SetBool(key string, val bool) error { return store.SetBool(d, key, val) }
