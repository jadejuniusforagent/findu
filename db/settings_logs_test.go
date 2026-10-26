package db

import (
	"fmt"
	"testing"
	"time"
)

// settings / server_logs 的 SQL 已迁到 core/store，db 包只剩转发。
// 下面两个用例走 db 包的方法打真 PG，覆盖迁移后的语句。

func TestSettingKVPackageBridge(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	t.Cleanup(func() { d.Close() }) // 先于其它 Cleanup 执行：注册得最早，LIFO 最后跑

	key := fmt.Sprintf("t-kv-%d", time.Now().UnixNano())
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM settings WHERE key=$1`, key) })

	// 未设置：ok=false，GetBool 用调用方默认值
	if _, ok, err := d.GetSetting(key); err != nil || ok {
		t.Fatalf("未设置时 ok 应为 false: ok=%v err=%v", ok, err)
	}
	if !d.GetBool(key, true) {
		t.Fatal("未设置时 GetBool 应返回传入的默认值 true")
	}

	// 写入后读回
	if err := d.SetSetting(key, "hello"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	v, ok, err := d.GetSetting(key)
	if err != nil || !ok || v != "hello" {
		t.Fatalf("读回不一致: v=%q ok=%v err=%v", v, ok, err)
	}

	// 布尔语义：只认 true / 1
	for _, tc := range []struct {
		raw  string
		want bool
	}{{"true", true}, {"1", true}, {"false", false}, {"0", false}, {"yes", false}} {
		if err := d.SetSetting(key, tc.raw); err != nil {
			t.Fatal(err)
		}
		if got := d.GetBool(key, !tc.want); got != tc.want {
			t.Fatalf("GetBool(%q) = %v, want %v", tc.raw, got, tc.want)
		}
	}
	if err := d.SetBool(key, true); err != nil {
		t.Fatal(err)
	}
	if raw, _, _ := d.GetSetting(key); raw != "true" {
		t.Fatalf("SetBool(true) 应落 \"true\"，实际 %q", raw)
	}

	// InsertSettingIfAbsent：首次 true，已存在则保留原值且返回 false
	key2 := key + "-absent"
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM settings WHERE key=$1`, key2) })
	if inserted, err := d.InsertSettingIfAbsent(key2, "first"); err != nil || !inserted {
		t.Fatalf("首次写入应 inserted=true: inserted=%v err=%v", inserted, err)
	}
	if inserted, err := d.InsertSettingIfAbsent(key2, "second"); err != nil || inserted {
		t.Fatalf("已存在应 inserted=false: inserted=%v err=%v", inserted, err)
	}
	if v, _, _ := d.GetSetting(key2); v != "first" {
		t.Fatalf("已存在的值被覆盖了: %q", v)
	}
}

func TestServiceLogsPackageBridge(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	t.Cleanup(func() { d.Close() }) // 先于其它 Cleanup 执行：注册得最早，LIFO 最后跑

	tag := fmt.Sprintf("t-log-%d", time.Now().UnixNano())
	ids := make([]int64, 0, 3)
	for i := 1; i <= 3; i++ {
		id, err := d.InsertLog("info", tag, fmt.Sprintf("line-%d", i))
		if err != nil {
			t.Fatalf("InsertLog 第 %d 行失败: %v", i, err)
		}
		ids = append(ids, id)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM server_logs WHERE tag=$1`, tag) })

	// RecentLogs 取最近 N 行（全局），必须包含我们刚写的三行
	rows, err := d.RecentLogs(200)
	if err != nil {
		t.Fatalf("RecentLogs: %v", err)
	}
	if len(rows) < 3 {
		t.Fatalf("RecentLogs 只返回 %d 行", len(rows))
	}
	// 输出必须按时间正序（最旧在前）
	for i := 1; i < len(rows); i++ {
		if rows[i-1].ID > rows[i].ID {
			t.Fatalf("RecentLogs 未按 id 正序: 位置 %d=%d > %d", i, rows[i-1].ID, rows[i].ID)
		}
	}
	seen := 0
	var lastTag string
	for _, r := range rows {
		if r.Tag == tag {
			seen++
			lastTag = r.Text
		}
	}
	if seen != 3 {
		t.Fatalf("最近日志里只找到 %d 条自建行（want 3）", seen)
	}
	if lastTag != "line-3" {
		t.Fatalf("正序最后一条应是 line-3，实际 %q", lastTag)
	}

	// ListLogsBefore：以最后一行 id 为界，不应再包含它
	before, err := d.ListLogsBefore(ids[2], 200)
	if err != nil {
		t.Fatalf("ListLogsBefore: %v", err)
	}
	for _, r := range before {
		if r.ID >= ids[2] {
			t.Fatalf("ListLogsBefore 返回了 id >= 边界值的行: %d >= %d", r.ID, ids[2])
		}
	}
}
