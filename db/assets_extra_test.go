package db

import (
	"database/sql"
	"testing"
	"time"

	"finduai/domain/asset"
)

func TestAssetProjectBindingAndDelete(t *testing.T) {
	d, as, _ := testSetup(t)
	defer d.Close()
	id, err := as.UpsertRootDomain(asset.UpsertRootDomainReq{Domain: "bind-native-" + time.Now().Format("150405.000000000") + ".test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Exec(`DELETE FROM assets WHERE id=$1`, id) })
	if _, err := as.Stats(7); err != nil {
		t.Fatalf("asset stats: %v", err)
	}
	status := "inactive"
	if err := as.UpdateByID(id, AssetPatch{Status: &status, AddTags: []string{"native"}}); err != nil {
		t.Fatal(err)
	}
	var gotStatus string
	var tags string
	if err := d.QueryRow(`SELECT status, COALESCE(tags_json,'[]') FROM assets WHERE id=$1`, id).Scan(&gotStatus, &tags); err != nil {
		t.Fatal(err)
	}
	if gotStatus != "inactive" || tags != `["native"]` {
		t.Fatalf("status=%q tags=%q", gotStatus, tags)
	}
	if n, err := as.BulkUpdate([]string{id}, AssetPatch{AddTags: []string{"bulk"}, RemoveTags: []string{"native"}}); err != nil || n != 1 {
		t.Fatalf("bulk update n=%d err=%v", n, err)
	}
	if err := d.QueryRow(`SELECT COALESCE(tags_json,'[]') FROM assets WHERE id=$1`, id).Scan(&tags); err != nil {
		t.Fatal(err)
	}
	if tags != `["bulk"]` {
		t.Fatalf("bulk tags %q", tags)
	}
	p, err := d.Projects().Create(&Project{Name: "asset-bind-test", Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Exec(`DELETE FROM projects WHERE id=$1`, p.ID) })

	updated, err := as.UpdateProjectIDs([]string{id}, p.ID)
	if err != nil || updated != 1 {
		t.Fatalf("updated=%d err=%v", updated, err)
	}
	var pid sql.NullString
	if err := d.QueryRow(`SELECT project_id FROM assets WHERE id=$1`, id).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if !pid.Valid || pid.String != p.ID {
		t.Fatalf("project_id=%v want %s", pid, p.ID)
	}
	if _, err := as.UpdateProjectIDs([]string{id}, ""); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT project_id FROM assets WHERE id=$1`, id).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if pid.Valid {
		t.Fatalf("project_id=%v want NULL", pid)
	}
	deleted, err := as.DeleteByIDs([]string{id})
	if err != nil || deleted != 1 {
		t.Fatalf("deleted=%d err=%v", deleted, err)
	}
}
