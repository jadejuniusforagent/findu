package db

import (
	"context"
	"testing"
)

// TestSchemaHashRecorded 验证 schema.sql 只在内容变化时重放：首次 Open 记录
// 内容哈希，之后的 Open 靠 schemaUpToDate 命中并跳过 DDL。
func TestSchemaHashRecorded(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	t.Cleanup(func() { d.Close() })

	var got string
	if err := d.QueryRow(`SELECT COALESCE((SELECT value FROM settings WHERE key = $1), '')`, schemaHashKey).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != schemaHash {
		t.Fatalf("settings[%s]=%q，期望当前 schema 哈希 %q", schemaHashKey, got, schemaHash)
	}

	ctx := context.Background()
	conn, err := d.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	ok, err := schemaUpToDate(ctx, conn, schemaHash)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("当前 schema 应判定为已应用")
	}
	ok, err = schemaUpToDate(ctx, conn, "stale-hash")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("哈希不一致时不应判定为已应用")
	}
}
