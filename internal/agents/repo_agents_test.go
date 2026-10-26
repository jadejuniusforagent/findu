package agents

import (
	"path/filepath"
	"testing"
)

// TestRepoSupervisorTaxonomy 校验仓库内的 supervisor 子代理分类不会在后续改动中悄悄丢失。
func TestRepoSupervisorTaxonomy(t *testing.T) {
	dir := filepath.Join("..", "..", "agents")
	load, err := LoadMarkdownAgentsDir(dir)
	if err != nil {
		t.Fatalf("load repo agents: %v", err)
	}
	if load.OrchestratorSupervisor == nil {
		t.Fatal("missing orchestrator-supervisor.md")
	}
	if load.OrchestratorSupervisor.EinoName != "finduai-supervisor" {
		t.Fatalf("supervisor eino name = %q, want finduai-supervisor", load.OrchestratorSupervisor.EinoName)
	}

	want := map[string]string{
		"recon":   "渗透测试",
		"web":     "渗透测试",
		"exploit": "渗透测试",
		"audit":   "审计取证",
		"report":  "报告撰写",
		"retest":  "漏洞复测",
		"general": "默认",
	}
	if len(load.SubAgents) != len(want) {
		t.Fatalf("sub agents = %d, want %d", len(load.SubAgents), len(want))
	}
	seen := make(map[string]bool, len(load.SubAgents))
	for _, sub := range load.SubAgents {
		if seen[sub.ID] {
			t.Fatalf("duplicate sub agent id %q", sub.ID)
		}
		seen[sub.ID] = true
		if sub.Name == "" || sub.Description == "" || sub.Instruction == "" {
			t.Fatalf("sub agent %q has empty name/description/instruction", sub.ID)
		}
		role, ok := want[sub.ID]
		if !ok {
			t.Fatalf("unexpected sub agent id %q", sub.ID)
		}
		if sub.BindRole != role {
			t.Fatalf("sub agent %q bind_role = %q, want %q", sub.ID, sub.BindRole, role)
		}
		if sub.ID != "general" && len(sub.RoleTools) == 0 {
			t.Fatalf("sub agent %q has empty tools allowlist", sub.ID)
		}
	}

	hasTool := func(id, tool string) bool {
		for _, sub := range load.SubAgents {
			if sub.ID != id {
				continue
			}
			for _, got := range sub.RoleTools {
				if got == tool {
					return true
				}
			}
		}
		return false
	}
	if !hasTool("recon", "nmap") || hasTool("recon", "sqlmap") {
		t.Fatal("recon tools allowlist should include nmap and exclude sqlmap")
	}
	if !hasTool("audit", "checkov") || hasTool("audit", "metasploit") || hasTool("audit", "webshell_exec") {
		t.Fatal("audit tools allowlist should be read-only oriented")
	}
	if hasTool("report", "exec") || hasTool("report", "record_vulnerability") {
		t.Fatal("report tools allowlist should not include exec or record_vulnerability")
	}
	for id := range want {
		if !seen[id] {
			t.Fatalf("missing sub agent %q", id)
		}
	}
}
