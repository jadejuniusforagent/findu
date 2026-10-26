package apkanalysis

// compare — 同包名不同版本(或任意两个)样本的自动分析报告对比。
// 报告历史天然存在于 apk_scan_tasks(每次 auto_analyze 各存一份 result_json),
// 这里取两个样本各自最新 done 报告做结构化 diff:权限/攻击面/发现/端点/壳。

import (
	"fmt"
	"sort"
)

// DiffEntry 通用差分行。
type DiffEntry struct {
	Key    string `json:"key"` // 权限名/组件名/scheme/域名/发现名
	Base   int    `json:"base"`
	Target int    `json:"target"`
}

// CompareResult 报告对比结果。
type CompareResult struct {
	Base   *Sample `json:"base"`
	Target *Sample `json:"target"`

	// 报告级
	BasePackage   string `json:"base_package,omitempty"`
	TargetPackage string `json:"target_package,omitempty"`
	BaseVersion   string `json:"base_version,omitempty"`
	TargetVersion string `json:"target_version,omitempty"`
	PackerChanged bool   `json:"packer_changed"`
	BasePacker    string `json:"base_packer,omitempty"`
	TargetPacker  string `json:"target_packer,omitempty"`

	PermissionsAdded   []string `json:"permissions_added"`
	PermissionsRemoved []string `json:"permissions_removed"`

	// 导出组件(显式+隐式合并,key=kind:name)
	ComponentsAdded   []string `json:"components_added"`
	ComponentsRemoved []string `json:"components_removed"`

	DeeplinksAdded   []string `json:"deeplinks_added"`
	DeeplinksRemoved []string `json:"deeplinks_removed"`

	FlagsAdded   []string `json:"flags_added"`
	FlagsRemoved []string `json:"flags_removed"`

	// 发现:按 (category+name) 对齐,命中数变化
	FindingsChanged []FindingDiff `json:"findings_changed"`

	// 端点域名差
	HostsAdded   []string `json:"hosts_added"`
	HostsRemoved []string `json:"hosts_removed"`
}

// FindingDiff 单项发现的两侧对比。
type FindingDiff struct {
	Category   string `json:"category"`
	Name       string `json:"name"`
	BaseHits   int    `json:"base_hits"`
	TargetHits int    `json:"target_hits"`
	// Severity 变化(极少,保留字段)
	BaseSeverity   string `json:"base_severity,omitempty"`
	TargetSeverity string `json:"target_severity,omitempty"`
}

// CompareSamples 对比两个样本的最新自动分析报告。
func (s *Service) CompareSamples(baseID, targetID string) (*CompareResult, error) {
	if baseID == targetID {
		return nil, fmt.Errorf("base 与 target 不能是同一样本")
	}
	baseSample, err := s.GetSample(baseID)
	if err != nil {
		return nil, fmt.Errorf("base: %w", err)
	}
	targetSample, err := s.GetSample(targetID)
	if err != nil {
		return nil, fmt.Errorf("target: %w", err)
	}
	baseSt, err := s.GetAutoReport(baseSample.ID)
	if err != nil {
		return nil, err
	}
	targetSt, err := s.GetAutoReport(targetSample.ID)
	if err != nil {
		return nil, err
	}
	if baseSt.Report == nil {
		return nil, fmt.Errorf("base 样本尚无分析报告: %s", baseSample.Name)
	}
	if targetSt.Report == nil {
		return nil, fmt.Errorf("target 样本尚无分析报告: %s", targetSample.Name)
	}
	base, target := baseSt.Report, targetSt.Report

	res := &CompareResult{
		Base:          baseSample,
		Target:        targetSample,
		BasePackage:   base.PackageName,
		TargetPackage: target.PackageName,
		BaseVersion:   base.VersionName,
		TargetVersion: target.VersionName,
		BasePacker:    base.Packer,
		TargetPacker:  target.Packer,
		PackerChanged: base.Packer != target.Packer,
	}

	// 权限
	basePerms := toSet(base.Permissions)
	targetPerms := toSet(target.Permissions)
	res.PermissionsAdded = sortedSub(targetPerms, basePerms)
	res.PermissionsRemoved = sortedSub(basePerms, targetPerms)

	// 导出组件(显式+隐式合并)
	res.ComponentsAdded, res.ComponentsRemoved = diffSets(
		exportedComponentKeys(base.ManifestSecurity),
		exportedComponentKeys(target.ManifestSecurity))

	// deeplink
	baseSchemes := anySliceToSet(base.ManifestSecurity, "deeplink_schemes")
	targetSchemes := anySliceToSet(target.ManifestSecurity, "deeplink_schemes")
	res.DeeplinksAdded = sortedSub(targetSchemes, baseSchemes)
	res.DeeplinksRemoved = sortedSub(baseSchemes, targetSchemes)

	// 危险标志
	baseFlags := flagKeys(base.ManifestSecurity)
	targetFlags := flagKeys(target.ManifestSecurity)
	res.FlagsAdded = sortedSub(targetFlags, baseFlags)
	res.FlagsRemoved = sortedSub(baseFlags, targetFlags)

	// 发现(按 category:name 对齐)
	baseF := map[string]AutoFinding{}
	for _, f := range base.Findings {
		baseF[f.Category+"/"+f.Name] = f
	}
	targetF := map[string]AutoFinding{}
	for _, f := range target.Findings {
		targetF[f.Category+"/"+f.Name] = f
	}
	keys := make([]string, 0, len(baseF)+len(targetF))
	for k := range baseF {
		keys = append(keys, k)
	}
	for k := range targetF {
		if _, ok := baseF[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		b, inBase := baseF[k]
		t, inTarget := targetF[k]
		if !inBase {
			b = AutoFinding{Severity: t.Severity}
		}
		if !inTarget {
			t = AutoFinding{Severity: b.Severity}
		}
		if !inBase || !inTarget || b.Hits != t.Hits || b.Severity != t.Severity {
			res.FindingsChanged = append(res.FindingsChanged, FindingDiff{
				Category: b.Category, Name: b.Name,
				BaseHits: b.Hits, TargetHits: t.Hits,
				BaseSeverity: b.Severity, TargetSeverity: t.Severity,
			})
		}
	}

	// 端点域名
	baseHosts := map[string]int{}
	for _, h := range base.EndpointHosts {
		baseHosts[h.Host] = h.Hits
	}
	targetHosts := map[string]int{}
	for _, h := range target.EndpointHosts {
		targetHosts[h.Host] = h.Hits
	}
	res.HostsAdded = sortedSub(targetHosts, baseHosts)
	res.HostsRemoved = sortedSub(baseHosts, targetHosts)

	return res, nil
}

// SameVersionCandidates 同包名其他样本(概览页"与旧版本对比"的候选)。
func (s *Service) SamePackageSamples(sampleID string) ([]*Sample, error) {
	sample, err := s.GetSample(sampleID)
	if err != nil {
		return nil, err
	}
	if sample.PackageName == "" {
		return []*Sample{}, nil
	}
	rows, err := s.db.Query(`SELECT id, name, sha256, size, workspace_path,
		COALESCE(package_name,''), COALESCE(version_name,''), dex_count, class_count,
		COALESCE(project_id,''), source, created_at
		FROM apk_samples
		WHERE package_name = $1 AND id <> $2
		ORDER BY created_at DESC LIMIT 20`, sample.PackageName, sample.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Sample{}
	for rows.Next() {
		smp := &Sample{}
		if err := rows.Scan(&smp.ID, &smp.Name, &smp.SHA256, &smp.Size, &smp.Workspace,
			&smp.PackageName, &smp.VersionName, &smp.DexCount, &smp.ClassCount,
			&smp.ProjectID, &smp.Source, &smp.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, smp)
	}
	return out, rows.Err()
}

// ── helpers ──

// exportedComponentKeys 显式+隐式导出组件合并为 "kind:name" 键集合。
func exportedComponentKeys(sec map[string]any) map[string]bool {
	out := map[string]bool{}
	if sec == nil {
		return out
	}
	for _, section := range []string{"explicit_exported", "implicitly_exported"} {
		m, ok := sec[section].(map[string]any)
		if !ok {
			continue
		}
		for kind, listAny := range m {
			list, ok := listAny.([]any)
			if !ok {
				continue
			}
			for _, n := range list {
				out[kind+":"+fmt.Sprintf("%v", n)] = true
			}
		}
	}
	return out
}

// anySliceToSet sec[section] ([]any) → 字符串集合。
func anySliceToSet(sec map[string]any, section string) map[string]bool {
	out := map[string]bool{}
	if sec == nil {
		return out
	}
	if list, ok := sec[section].([]any); ok {
		for _, v := range list {
			out[fmt.Sprintf("%v", v)] = true
		}
	}
	return out
}

// flagKeys app_flags 的键集合。
func flagKeys(sec map[string]any) map[string]bool {
	out := map[string]bool{}
	if sec == nil {
		return out
	}
	if m, ok := sec["app_flags"].(map[string]any); ok {
		for k := range m {
			out[k] = true
		}
	}
	return out
}

// sortedSub super - sub 的键差(值类型任意,仅取键集)。
func sortedSub[V any](super, sub map[string]V) []string {
	out := []string{}
	for k := range super {
		if _, ok := sub[k]; !ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func diffSets(base, target map[string]bool) (added, removed []string) {
	return sortedSub(target, base), sortedSub(base, target)
}
