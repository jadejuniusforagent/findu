package db

import (
	"database/sql"
	"fmt"
	"testing"
	"time"
)

func TestWebShellStoreCRUD(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	defer d.Close()
	// These tables are owned by the findu subsystem in production; the host
	// test schema does not create them, so make the fixture self-contained.
	if _, err := d.Exec(`CREATE TABLE IF NOT EXISTS webshell_connections (
		id TEXT PRIMARY KEY, project_id TEXT, url TEXT NOT NULL,
		password TEXT NOT NULL DEFAULT '', type TEXT NOT NULL DEFAULT 'php',
		method TEXT NOT NULL DEFAULT 'post', cmd_param TEXT NOT NULL DEFAULT '',
		remark TEXT NOT NULL DEFAULT '', encoding TEXT NOT NULL DEFAULT '',
		os TEXT NOT NULL DEFAULT '', created_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`CREATE TABLE IF NOT EXISTS webshell_connection_states (
		connection_id TEXT PRIMARY KEY, state_json TEXT NOT NULL DEFAULT '{}',
		updated_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		t.Fatal(err)
	}
	ws := d.Webshells()
	c := &WebShellConnection{
		ID:        fmt.Sprintf("ws_test_%d", time.Now().UnixNano()),
		ProjectID: "",
		URL:       "http://127.0.0.1:1/test.php",
		Password:  "p",
		Type:      "php",
		Method:    "post",
		CmdParam:  "cmd",
		Remark:    "native test",
		Encoding:  "auto",
		OS:        "linux",
		CreatedAt: time.Now().UTC(),
	}
	if err := ws.Create(c); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Exec(`DELETE FROM webshell_connections WHERE id=$1`, c.ID) })

	got, err := ws.Get(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != c.URL || got.Remark != "native test" {
		t.Fatalf("got %+v", got)
	}
	list, err := ws.List("")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range list {
		if item.ID == c.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("created connection missing from list")
	}
	got.Remark = "updated"
	got.Password = "new"
	if err := ws.Update(got); err != nil {
		t.Fatal(err)
	}
	updated, err := ws.Get(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Remark != "updated" || updated.Password != "new" {
		t.Fatalf("updated %+v", updated)
	}
	if err := ws.Delete(c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.Get(c.ID); err != sql.ErrNoRows {
		t.Fatalf("get after delete = %v, want sql.ErrNoRows", err)
	}
}
