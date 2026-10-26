package db

// 漏洞域存储用例。除跨域列表/取证锁（另有存量用例钉住）外，本用例补齐
// finding_enrichment / vuln_kb / vuln_intel（含只补缺富化语义）。
// vuln_kb / vuln_intel 自批 5 起由 plugins/intel/vulnstore 实现，这里做真实
// PostgreSQL 回归，确认 SQL 下沉后语义未变。

import (
	"encoding/json"
	"finduai/domain/exploration"
	"finduai/domain/finding"
	"finduai/plugins/intel/vulnstore"
	"fmt"
	"math"
	"testing"
	"time"
)

func TestFindingRepoPackageBridge(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	// ---- findings 单表桥（真实探索节点 → 节点寻址 → 状态 → 复现记录）----
	tk, err := d.CreateTask("bridge 第七刀", "目标", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(tk.ID) })
	es := d.Exploration(tk.ExplorationID)
	nodeID, err := es.AddNode(exploration.KindFinding, map[string]any{"summary": "x", "severity": "high"}, 5, "confirmed", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	const vc = "bridge_vuln"
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM findings WHERE vulnclass=$1`, vc) })
	fid, err := d.AddFinding(tk.ID, nodeID, vc, "bridge-name", "high", "bridge summary", "bridge evidence", "bridge-worker", []string{"a-1"})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := d.FindingIDByNodeID(nodeID); got != fid {
		t.Fatalf("FindingIDByNodeID: got %d want %d", got, fid)
	}
	if err = d.SetFindingReproduction(nodeID, json.RawMessage(`{"steps":["curl /"]}`)); err != nil {
		t.Fatal(err)
	}
	f, err := d.GetFindingByNodeID(nodeID)
	if err != nil || f == nil || f.Severity != "high" || len(f.Reproduction) == 0 {
		t.Fatalf("GetFindingByNodeID: %+v err=%v", f, err)
	}
	if n, err := d.SetFindingStatus(fid, finding.FindingConfirmed); err != nil || n != 1 {
		t.Fatalf("SetFindingStatus: %d %v", n, err)
	}
	metas, err := d.FindingMetaByNodeID(tk.ID)
	if err != nil || metas[nodeID].ID != fid || metas[nodeID].Status != finding.FindingConfirmed {
		t.Fatalf("FindingMetaByNodeID: %+v err=%v", metas, err)
	}

	// ---- finding_enrichment 桥（upsert → 读 → 局部补丁只补缺）----
	if err = d.UpsertFindingEnrichment(&finding.FindingEnrichment{
		NodeID: nodeID, Name: "ai-name", Target: "t.local", Severity: "high",
		Status: "open", Source: "ai", Remediation: json.RawMessage(`["fix"]`),
	}); err != nil {
		t.Fatal(err)
	}
	e, err := d.GetFindingEnrichment(nodeID)
	if err != nil || e == nil || e.Name != "ai-name" || rem(e.Remediation) != `["fix"]` {
		t.Fatalf("GetFindingEnrichment: %+v err=%v", e, err)
	}
	// 局部补丁：name 留空保留存量，status 变更生效，source 翻 manual。
	if err = d.UpdateFindingEnrichmentFields(nodeID, "", "", "medium", "confirmed"); err != nil {
		t.Fatal(err)
	}
	if e, _ = d.GetFindingEnrichment(nodeID); e.Name != "ai-name" || e.Severity != "medium" || e.Status != "confirmed" || e.Source != "manual" {
		t.Fatalf("partial patch clicked AI values: %+v", e)
	}
	fes, err := d.GetFindingEnrichments([]int64{nodeID, 987654321000})
	if err != nil || len(fes) != 1 {
		t.Fatalf("GetFindingEnrichments: %d %v", len(fes), err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM finding_enrichment WHERE node_id=$1`, nodeID) })

	// ---- vuln_kb 桥（插件存储：幂等 upsert → 列表分页 → 详情含 body → 统计）----
	vstore := vulnstore.New(d)
	kb := finding.VulnKBEntry{ContentHash: "hash-" + suffix, Title: "bridge-kb", Product: "bridge-app", VulnType: "rce", CVE: "CVE-2026-0001", Payload: "p", Fingerprint: "fp", Body: "full-body", SourcePath: "kb/bridge.md"}
	if inserted, err := vstore.UpsertVulnKB(kb); err != nil || !inserted {
		t.Fatalf("UpsertVulnKB first: %v %v", inserted, err)
	}
	if inserted, err := vstore.UpsertVulnKB(kb); err != nil || inserted {
		t.Fatalf("UpsertVulnKB dup: %v %v", inserted, err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM vuln_kb WHERE content_hash=$1`, kb.ContentHash) })
	items, total, err := vstore.ListVulnKB("bridge-kb", "rce", 0, 10)
	if err != nil || total != 1 || len(items) != 1 || items[0].Body != "" {
		t.Fatalf("ListVulnKB: total=%d n=%d body=%q err=%v", total, len(items), items[0].Body, err)
	}
	full, err := vstore.GetVulnKB(items[0].ID)
	if err != nil || full == nil || full.Body != "full-body" {
		t.Fatalf("GetVulnKB: %+v err=%v", full, err)
	}
	if _, err := vstore.VulnKBStats(); err != nil {
		t.Fatal(err)
	}

	// ---- vuln_intel 桥（幂等 upsert → 只补缺富化 → 待富化队列 → 列表富化字段）----
	intel := finding.VulnIntel{UniqueKey: "src-" + suffix, Title: "bridge-intel", Severity: "high", CVE: "CVE-2026-0002", Source: "brsrc-" + suffix, SourceURL: "https://x/" + suffix, Tags: "[]", Refs: "[]"}
	if inserted, err := vstore.UpsertVulnIntel(intel); err != nil || !inserted {
		t.Fatalf("UpsertVulnIntel first: %v %v", inserted, err)
	}
	if inserted, err := vstore.UpsertVulnIntel(intel); err != nil || inserted {
		t.Fatalf("UpsertVulnIntel dup: %v %v", inserted, err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM vuln_intel WHERE unique_key=$1`, intel.UniqueKey) })
	var intelID int64
	if err = d.QueryRow(`SELECT id FROM vuln_intel WHERE unique_key=$1`, intel.UniqueKey).Scan(&intelID); err != nil {
		t.Fatal(err)
	}
	if err = vstore.EnrichVulnIntel(finding.VulnIntelEnrich{ID: intelID, GithubPoc: "[]", CvssScore: 8.1, CvssVector: "AV:N", Enriched: true}); err != nil {
		t.Fatal(err)
	}
	// 再来一次空载荷富化，存量值不得被覆盖（只补缺）。
	if err = vstore.EnrichVulnIntel(finding.VulnIntelEnrich{ID: intelID, GithubPoc: "[]", Enriched: false}); err != nil {
		t.Fatal(err)
	}
	pending, err := vstore.PendingVulnIntel(20)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pending {
		if p.ID == intel.ID {
			t.Fatal("enriched-and-fresh row should not be pending")
		}
	}
	rows, total, err := vstore.ListVulnIntel(suffix, "", 0, 50)
	if err != nil || total != 1 || len(rows) != 1 || rows[0].CvssScore == nil ||
		math.Abs(*rows[0].CvssScore-8.1) > 0.01 || rows[0].CvssVector != "AV:N" {
		t.Fatalf("ListVulnIntel: total=%d %+v err=%v", total, rows, err)
	}
	_ = pending
}

func rem(r json.RawMessage) string { return string(r) }
