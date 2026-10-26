package db

import (
	"os"
	"strings"
	"testing"
)

// TestAssetSelectionImportMergeNative 覆盖 A1/A2/A4：导入去重 → selection 过滤 →
// merge 合并 → 清理。全程用临时数据，结束后删除。
func TestAssetSelectionImportMergeNative(t *testing.T) {
	dsn := os.Getenv("FINDU_PG_DSN")
	if dsn == "" {
		t.Skip("FINDU_PG_DSN not set")
	}
	d, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	as := d.Assets()
	ensureFinduCompatFixture(t, d)

	// A2：导入两条同域资产 + 一条独立 IP 资产
	domain := "step6d-a-canary.example.com"
	result, err := as.ImportAssetsNative([]*AssetImportInput{
		{Host: domain, Domain: domain, Title: "canary one", Tags: []string{"step6d"}},
		{Host: domain, Domain: domain, Title: "canary two", Tags: []string{"extra"}},
		{Host: "192.0.2.77", IP: "192.0.2.77", Source: "manual"},
	}, "")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if result.Created != 2 || result.Updated != 1 {
		t.Fatalf("import counts: created=%d updated=%d (want 2/1, dedup expected)", result.Created, result.Updated)
	}
	t.Cleanup(func() {
		_, _ = as.DeleteAssetRowsByIDs(nil)
	})

	// 回收清理：按域/IP 删除
	t.Cleanup(func() {
		rows, _, _ := as.SelectAssetsForOperation(100, AssetSelectionFilter{Domain: domain})
		ids := []string{}
		for _, a := range rows {
			ids = append(ids, a.ID)
		}
		rowsIP, _, _ := as.SelectAssetsForOperation(100, AssetSelectionFilter{IP: "192.0.2.77"})
		for _, a := range rowsIP {
			ids = append(ids, a.ID)
		}
		if len(ids) > 0 {
			if _, err := as.DeleteAssetRowsByIDs(ids); err != nil {
				t.Errorf("cleanup delete: %v", err)
			}
		}
	})

	// A1：selection 按域过滤
	sel, total, err := as.SelectAssetsForOperation(100, AssetSelectionFilter{Domain: domain})
	if err != nil {
		t.Fatalf("selection: %v", err)
	}
	if total != 1 || len(sel) != 1 {
		t.Fatalf("selection domain=%d rows=%d (want 1/1)", total, len(sel))
	}
	primary := sel[0]
	// findu 语义：同 dedup_key 再导入，非空字段覆盖（title=CASE WHEN <>'' THEN …）。
	if primary.Title != "canary two" || primary.Status != "active" {
		t.Fatalf("primary shape: title=%q status=%q", primary.Title, primary.Status)
	}

	// A1：q 关键词过滤
	_, qTotal, err := as.SelectAssetsForOperation(100, AssetSelectionFilter{Q: "192.0.2.77"})
	if err != nil {
		t.Fatalf("selection q: %v", err)
	}
	if qTotal != 1 {
		t.Fatalf("selection q total=%d (want 1)", qTotal)
	}

	// A4：merge —— 需要两条同域资产；先再导入一条带端口的同域资产
	if _, err := as.ImportAssetsNative([]*AssetImportInput{
		{Host: domain + ":8443", Domain: domain, Protocol: "https", Port: 8443, Title: "canary svc"},
	}, ""); err != nil {
		t.Fatalf("import merge candidate: %v", err)
	}
	sel2, _, err := as.SelectAssetsForOperation(100, AssetSelectionFilter{Domain: domain})
	if err != nil {
		t.Fatalf("selection2: %v", err)
	}
	if len(sel2) != 2 {
		t.Fatalf("want 2 same-domain assets, got %d", len(sel2))
	}
	other := sel2[0]
	if other.ID == primary.ID {
		other = sel2[1]
	}
	merged, err := as.MergeAssetRowsNative(primary, []string{other.ID})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if merged != 1 {
		t.Fatalf("merged=%d (want 1)", merged)
	}
	// 合并后：主资产仍在、标题为 merge 时主资产 DTO 当前值（findu 全字段覆盖写）、重复资产消失
	after, err := as.GetAssetRow(primary.ID)
	if err != nil {
		t.Fatalf("get primary after merge: %v", err)
	}
	if after.Title != "canary two" {
		t.Fatalf("primary title after merge=%q", after.Title)
	}
	if _, err := as.GetAssetRow(other.ID); err == nil {
		t.Fatalf("duplicate asset still exists after merge")
	}

	// A3：MarkAssetScannedNative → risk cache 重算（无漏洞时 count=0/score=0，
	// last_scan_at 已写 → 已扫描 → level=normal，对齐 findu assetRiskLevelFromScore）。
	if err := as.MarkAssetScannedNative(primary.ID, "canary-conv-native-test", "", ""); err != nil {
		t.Fatalf("mark scanned: %v", err)
	}
	scanned, err := as.GetAssetRow(primary.ID)
	if err != nil {
		t.Fatalf("get scanned: %v", err)
	}
	if scanned.LastScanConversationID != "canary-conv-native-test" {
		t.Fatalf("last_scan_conversation_id=%q", scanned.LastScanConversationID)
	}
	if scanned.VulnerabilityCount != 0 || scanned.RiskLevel != "normal" {
		t.Fatalf("risk cache: vuln_count=%d risk_level=%q (want 0/normal)",
			scanned.VulnerabilityCount, scanned.RiskLevel)
	}
}

// TestProjectFactsNativeDeleteStats 覆盖 A5/A6/A7：建临时项目 → facts upsert/
// update/deprecate/restore → stats 计数 → 删除项目级联清理。
func TestProjectFactsNativeDeleteStats(t *testing.T) {
	dsn := os.Getenv("FINDU_PG_DSN")
	if dsn == "" {
		t.Skip("FINDU_PG_DSN not set")
	}
	d, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	ps := d.Projects()
	ensureFinduCompatFixture(t, d)

	created, err := ps.Create(&Project{Name: "step6d-a-projects-canary"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() { _ = ps.DeleteProjectNative(created.ID) })

	// A6：fact upsert（同 key 两次 → 更新路径）
	fact, err := ps.UpsertProjectFact(&ProjectFact{
		ProjectID: created.ID, FactKey: "canary.fact", Summary: "第一步", Category: "note",
	})
	if err != nil {
		t.Fatalf("upsert fact: %v", err)
	}
	updated, err := ps.UpsertProjectFact(&ProjectFact{
		ProjectID: created.ID, FactKey: "canary.fact", Summary: "第二步", Category: "note", Body: "正文",
	})
	if err != nil {
		t.Fatalf("upsert fact again: %v", err)
	}
	if updated.ID != fact.ID || updated.Summary != "第二步" || updated.Body != "正文" {
		t.Fatalf("fact update shape: id same=%v summary=%q body=%q", updated.ID == fact.ID, updated.Summary, updated.Body)
	}

	// A6：deprecate → restore → delete
	if err := ps.DeprecateProjectFactNative(created.ID, "canary.fact"); err != nil {
		t.Fatalf("deprecate: %v", err)
	}
	if err := ps.RestoreProjectFactNative(created.ID, "canary.fact", "confirmed"); err != nil {
		t.Fatalf("restore: %v", err)
	}
	got, err := ps.GetProjectFactByKey(created.ID, "canary.fact")
	if err != nil || got.Confidence != "confirmed" {
		t.Fatalf("after restore: err=%v confidence=%q", err, got.Confidence)
	}
	if err := ps.DeleteProjectFactNative(got.ID); err != nil {
		t.Fatalf("delete fact: %v", err)
	}

	// A7：stats（再插一条非 deprecated fact 验证计数）
	if _, err := ps.UpsertProjectFact(&ProjectFact{
		ProjectID: created.ID, FactKey: "stats.fact", Summary: "计数用", Category: "note", Confidence: "confirmed",
	}); err != nil {
		t.Fatalf("upsert stats fact: %v", err)
	}
	stats, err := ps.GetProjectStatsCounts(created.ID)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.FactCount != 1 || stats.VulnCount != 0 || stats.ConversationCount != 0 {
		t.Fatalf("stats: %+v", stats)
	}

	// A5：delete project（级联删 facts）
	if err := ps.DeleteProjectNative(created.ID); err != nil {
		t.Fatalf("delete project: %v", err)
	}
	if _, err := ps.Get(created.ID); err == nil {
		t.Fatalf("project still exists after delete")
	}
	if _, err := ps.GetProjectFactByKey(created.ID, "stats.fact"); err == nil {
		t.Fatalf("fact still exists after project delete")
	}
}

// TestProjectDashboardSummaryNative 覆盖 A8：空过滤聚合不报错且形状正确。
func TestProjectDashboardSummaryNative(t *testing.T) {
	dsn := os.Getenv("FINDU_PG_DSN")
	if dsn == "" {
		t.Skip("FINDU_PG_DSN not set")
	}
	d, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	ensureFinduCompatFixture(t, d)
	summary, err := d.Projects().GetProjectDashboardSummary(5)
	if err != nil {
		t.Fatal(err)
	}
	if summary == nil || summary.Totals.ActiveProjects < 0 || len(summary.RecentFacts) > 5 {
		t.Fatalf("summary shape: %+v", summary)
	}
	for _, f := range summary.RecentFacts {
		if strings.TrimSpace(f.ProjectID) == "" || strings.TrimSpace(f.FactKey) == "" {
			t.Fatalf("summary fact shape: %+v", f)
		}
	}
}
