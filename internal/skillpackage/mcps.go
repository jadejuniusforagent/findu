package skillpackage

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// MCPList 是 SKILL.md frontmatter 中 mcps 字段的容错字符串列表。
// 同时接受 YAML 序列（mcps: [nmap, sqlmap]）与逗号/空白分隔的标量（mcps: nmap, sqlmap），
// 使手写的技能包永不会因该字段类型不符而拖垮 manifest 解析。
type MCPList []string

// UnmarshalYAML 实现 yaml.Unmarshaler，容忍序列与标量两种写法。
func (m *MCPList) UnmarshalYAML(value *yaml.Node) error {
	var raw any
	if err := value.Decode(&raw); err != nil {
		return err
	}
	*m = MCPList(normalizeMCPList(raw))
	return nil
}

// ParseSkillMCPs 从 SKILL.md frontmatter 中提取 mcps / mcp 声明（技能所需的工具或 MCP 服务名）。
//
// 与 SkillManifest.MCPs 的关系：manifest 字段用于 HTTP CRUD 的往返保真（BuildSkillMD 会写回），
// 本函数额外兼容 FINDU/norma 约定里的单数别名 mcp:，供运行时解锁逻辑只读消费。
// 无法解析或未声明时返回 nil。
func ParseSkillMCPs(raw []byte) []string {
	fmYAML, _, err := ExtractSkillMDFrontMatterYAML(raw)
	if err != nil {
		return nil
	}
	var probe struct {
		MCPs MCPList `yaml:"mcps"`
		MCP  MCPList `yaml:"mcp"`
	}
	if err := yaml.Unmarshal([]byte(fmYAML), &probe); err != nil {
		return nil
	}
	if len(probe.MCPs) > 0 {
		return probe.MCPs
	}
	return probe.MCP
}

func normalizeMCPList(v any) []string {
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		return splitMCPScalar(t)
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			switch s := item.(type) {
			case nil:
				continue
			case string:
				out = append(out, splitMCPScalar(s)...)
			default:
				out = append(out, splitMCPScalar(toScalarString(s))...)
			}
		}
		return dedupeTrimMCPs(out)
	case []string:
		return dedupeTrimMCPs(t)
	default:
		return splitMCPScalar(toScalarString(t))
	}
}

func toScalarString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, err := yaml.Marshal(v)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// splitMCPScalar 把形如 "nmap, sqlmap" 或 "nmap sqlmap" 的标量拆成条目。
func splitMCPScalar(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		switch r {
		case ',', ';', '|', ' ', '\t', '\n', '\r':
			return true
		}
		return false
	})
	return dedupeTrimMCPs(fields)
}

func dedupeTrimMCPs(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		n := strings.TrimSpace(s)
		if n == "" {
			continue
		}
		k := strings.ToLower(n)
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, n)
	}
	return out
}
