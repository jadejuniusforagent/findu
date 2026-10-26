package server

import (
	"encoding/json"
	"finduai/core/model"
	"finduai/domain/exploration"
	"testing"
	"time"
)

func TestInheritedDTOProvenance(t *testing.T) {
	createdAt := time.Date(2026, time.August, 19, 12, 0, 0, 0, time.UTC)
	node := &exploration.Node{
		ID:           41,
		Kind:         exploration.KindFinding,
		Payload:      json.RawMessage(`{"vulnclass":"xss","severity":"high","summary":"source finding"}`),
		State:        "confirmed",
		CreatedAt:    createdAt,
		SourceTaskID: 7,
		Inherited:    true,
	}

	nodeDTO := taskNodeDTO(node)
	if !nodeDTO.Inherited || nodeDTO.SourceTaskID != "7" {
		t.Fatalf("node provenance lost: %+v", nodeDTO)
	}
	finding := findingDTOsForOwner("7", "source", []*exploration.Node{node}, nil, nil)
	if len(finding) != 1 || !finding[0].Inherited || finding[0].SourceTaskID != "7" || finding[0].TaskID != "7" {
		t.Fatalf("finding provenance lost: %+v", finding)
	}

	intentID := int64(44)
	activity := activityDTO(model.Activity{
		ID:           45,
		NodeID:       &intentID,
		CreatedAt:    createdAt,
		SourceTaskID: 7,
		Inherited:    true,
	})
	if !activity.Inherited || activity.SourceTaskID != "7" || activity.IntentID != "44" {
		t.Fatalf("activity provenance lost: %+v", activity)
	}
	assetRef := coverageAssetRefDTO(exploration.AssetRef{
		ID: 46, Kind: exploration.KindFact, State: "confirmed", Summary: "source fact", SourceTaskID: 7, Inherited: true,
	})
	if !assetRef.Inherited || assetRef.SourceTaskID != "7" {
		t.Fatalf("asset ref provenance lost: %+v", assetRef)
	}
}

func TestInheritedIntentResultOnlyAllowsHistory(t *testing.T) {
	for _, state := range []string{"done", "blocked", "exhausted", "stopped"} {
		if !inheritedIntentResult(state) {
			t.Errorf("terminal state %q should be inherited history", state)
		}
	}
	for _, state := range []string{"open", "running", ""} {
		if inheritedIntentResult(state) {
			t.Errorf("live state %q must not enter inherited history", state)
		}
	}
}

func TestInheritedGraphSnapshotFiltersLiveIntentsAndEdges(t *testing.T) {
	nodes := []*exploration.Node{
		{ID: 1, Kind: exploration.KindFact, State: "confirmed"},
		{ID: 2, Kind: exploration.KindIntent, State: "running"},
		{ID: 3, Kind: exploration.KindIntent, State: "done"},
		{ID: 4, Kind: exploration.KindFinding, State: "confirmed"},
	}
	edges := []exploration.Edge{
		{From: 1, Rel: exploration.RelDerivedFrom, To: 2},
		{From: 1, Rel: exploration.RelDerivedFrom, To: 3},
		{From: 3, Rel: exploration.RelYields, To: 4},
	}

	gotNodes, gotEdges := inheritedGraphSnapshot(nodes, edges, 77)
	if len(gotNodes) != 3 {
		t.Fatalf("expected fact, terminal intent and finding; got %+v", gotNodes)
	}
	for _, node := range gotNodes {
		if node.ID == 2 {
			t.Fatal("running inherited intent leaked into graph snapshot")
		}
		if !node.Inherited || node.SourceTaskID != 77 {
			t.Fatalf("source provenance missing: %+v", node)
		}
	}
	if len(gotEdges) != 2 {
		t.Fatalf("edge referencing hidden live intent was not removed: %+v", gotEdges)
	}
	for _, edge := range gotEdges {
		if edge.From == 2 || edge.To == 2 {
			t.Fatalf("live intent id leaked through edge: %+v", edge)
		}
	}
}

func TestFindingProvenanceInTask(t *testing.T) {
	task := &Task{ID: "10", SourceTaskIDs: []int64{7, 8}}

	local := int64(10)
	if source, inherited, allowed := findingProvenanceInTask(task, &local); !allowed || inherited || source != "" {
		t.Fatalf("local finding: source=%q inherited=%v allowed=%v", source, inherited, allowed)
	}
	sourceID := int64(7)
	if source, inherited, allowed := findingProvenanceInTask(task, &sourceID); !allowed || !inherited || source != "7" {
		t.Fatalf("direct source finding: source=%q inherited=%v allowed=%v", source, inherited, allowed)
	}
	unrelated := int64(6)
	if _, _, allowed := findingProvenanceInTask(task, &unrelated); allowed {
		t.Fatal("unrelated finding must not be readable through task context")
	}
	if _, _, allowed := findingProvenanceInTask(task, nil); allowed {
		t.Fatal("detached finding must not be readable through task context")
	}
}
