package db

import (
	"database/sql"
	"fmt"
	"testing"
	"time"
)

func TestProjectStoreCRUD(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	defer d.Close()
	ps := d.Projects()
	name := fmt.Sprintf("native-project-test-%d", time.Now().UnixNano())
	p, err := ps.Create(&Project{Name: name, Description: "native host CRUD", Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Exec(`DELETE FROM projects WHERE id=$1`, p.ID) })

	got, err := ps.Get(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != name || got.Status != "active" {
		t.Fatalf("got %+v", got)
	}
	list, total, err := ps.List("active", name, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total < 1 || len(list) < 1 {
		t.Fatalf("list total=%d len=%d", total, len(list))
	}
	got.Description = "updated"
	got.Pinned = true
	got.Status = "archived"
	if err := ps.Update(got); err != nil {
		t.Fatal(err)
	}
	updated, err := ps.Get(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Description != "updated" || !updated.Pinned || updated.Status != "archived" {
		t.Fatalf("updated %+v", updated)
	}
	if err := ps.Delete(p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := ps.Get(p.ID); err != sql.ErrNoRows {
		t.Fatalf("get after delete = %v, want sql.ErrNoRows", err)
	}
}
