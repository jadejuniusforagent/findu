package db

import (
	"database/sql"
	"testing"
	"time"
)

func TestAuditStoreNative(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	defer d.Close()
	if _, err := d.Exec(`CREATE TABLE IF NOT EXISTS audit_logs (
		id TEXT PRIMARY KEY, created_at TIMESTAMPTZ NOT NULL, level TEXT NOT NULL DEFAULT 'info',
		category TEXT NOT NULL, action TEXT NOT NULL, result TEXT NOT NULL,
		actor TEXT NOT NULL DEFAULT 'admin', session_hint TEXT, client_ip TEXT, user_agent TEXT,
		resource_type TEXT, resource_id TEXT, message TEXT NOT NULL, detail_json TEXT)`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	id := "audit_native_test_" + now.Format("150405.000000000")
	_, err = d.Exec(`INSERT INTO audit_logs
		(id, created_at, level, category, action, result, actor, message, detail_json)
		VALUES ($1,$2,'info','test','native','success','admin','native audit test','{"k":"v"}')`, id, now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Exec(`DELETE FROM audit_logs WHERE id=$1`, id) })

	as := d.AuditLogs()
	log, err := as.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if log.Message != "native audit test" || log.Detail["k"] != "v" {
		t.Fatalf("got %+v", log)
	}
	logs, total, err := as.List(AuditFilter{Category: "test", Query: "native", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if total < 1 || len(logs) < 1 {
		t.Fatalf("total=%d len=%d", total, len(logs))
	}
	sum, failures, recent, err := as.Summary(AuditFilter{Category: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if sum < 1 || failures < 0 || recent < 1 {
		t.Fatalf("summary=%d failures=%d recent=%d", sum, failures, recent)
	}
	if _, err := as.Get("missing"); err != sql.ErrNoRows {
		t.Fatalf("missing get = %v", err)
	}
}
