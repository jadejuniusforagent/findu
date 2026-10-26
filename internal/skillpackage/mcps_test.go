package skillpackage

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseSkillMCPs(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{"yaml list", "---\nname: a\ndescription: d\nmcps: [nmap, sqlmap]\n---\nbody", []string{"nmap", "sqlmap"}},
		{"scalar comma", "---\nname: a\ndescription: d\nmcps: nmap, sqlmap\n---\nbody", []string{"nmap", "sqlmap"}},
		{"scalar spaces", "---\nname: a\ndescription: d\nmcps: nmap sqlmap\n---\nbody", []string{"nmap", "sqlmap"}},
		{"singular alias", "---\nname: a\ndescription: d\nmcp: [nmap]\n---\nbody", []string{"nmap"}},
		{"dedupe", "---\nname: a\ndescription: d\nmcps: [nmap, NMAP]\n---\nbody", []string{"nmap"}},
		{"absent", "---\nname: a\ndescription: d\n---\nbody", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ParseSkillMCPs([]byte(c.raw))
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("ParseSkillMCPs = %v, want %v", got, c.want)
			}
		})
	}
}

func TestParseSkillMCPsInvalid(t *testing.T) {
	if got := ParseSkillMCPs([]byte("# no frontmatter\nbody\n")); got != nil {
		t.Fatalf("expected nil for missing frontmatter, got %v", got)
	}
	if got := ParseSkillMCPs(nil); got != nil {
		t.Fatalf("expected nil for empty input, got %v", got)
	}
}

func TestParseSkillMCPsCommaStringWithBrackets(t *testing.T) {
	// 容错：有些技能包会把列表写成 "[nmap, sqlmap]" 的裸字符串。
	got := ParseSkillMCPs([]byte("---\nname: a\ndescription: d\nmcps: nmap,sqlmap;ffuf\n---\nbody"))
	want := []string{"nmap", "sqlmap", "ffuf"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseSkillMCPs = %v, want %v", got, want)
	}
}

func TestParseSkillMDToleratesScalarMCPs(t *testing.T) {
	// 标量写法不能让 manifest 解析失败（否则整个技能列表接口会挂）。
	m, _, err := ParseSkillMD([]byte("---\nname: a\ndescription: d\nmcps: nmap\n---\nbody"))
	if err != nil {
		t.Fatalf("ParseSkillMD: %v", err)
	}
	if len(m.MCPs) != 1 || m.MCPs[0] != "nmap" {
		t.Fatalf("scalar mcps: got %v", m.MCPs)
	}
}

func TestBuildSkillMDRoundTripsMCPs(t *testing.T) {
	// 关键回归：后台更新走 BuildSkillMD 重写 frontmatter，mcps 必须原样保留。
	m := &SkillManifest{Name: "x", Description: "d", MCPs: MCPList{"nmap", "sqlmap"}}
	raw, err := BuildSkillMD(m, "hello body")
	if err != nil {
		t.Fatalf("BuildSkillMD: %v", err)
	}
	back, body, err := ParseSkillMD(raw)
	if err != nil {
		t.Fatalf("ParseSkillMD: %v", err)
	}
	if !reflect.DeepEqual([]string(back.MCPs), []string{"nmap", "sqlmap"}) {
		t.Fatalf("mcps lost in round-trip: %v\nraw=%s", back.MCPs, raw)
	}
	if body != "hello body" {
		t.Fatalf("body changed: %q", body)
	}
	if got := ParseSkillMCPs(raw); !reflect.DeepEqual(got, []string{"nmap", "sqlmap"}) {
		t.Fatalf("ParseSkillMCPs on rebuilt MD = %v", got)
	}
}

// 真实数据守卫：仓库内全部技能的 SKILL.md 必须仍能被 manifest 解析（否则技能清单接口会挂）。
func TestParseSkillMDRealSkills(t *testing.T) {
	dir := filepath.Join("..", "..", "skills")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("skills dir not available: %v", err)
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name(), "SKILL.md"))
		if err != nil {
			continue
		}
		m, _, err := ParseSkillMD(raw)
		if err != nil {
			t.Errorf("%s: ParseSkillMD: %v", e.Name(), err)
			continue
		}
		if strings.TrimSpace(m.Name) == "" {
			t.Errorf("%s: empty name", e.Name())
		}
		n++
	}
	if n < 20 {
		t.Fatalf("expected >=20 real skills, got %d", n)
	}
	t.Logf("parsed %d real skills", n)
}
