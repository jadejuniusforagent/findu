package apkanalysis

// report_md — AutoReport 的 Markdown 导出(报告页"导出"与 Agent 交接共用渲染)。

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// ReportMarkdown 把报告渲染为 Markdown。
func ReportMarkdown(r *AutoReport) string {
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }

	w("# 安卓自动解包分析 · %s", r.SampleName)
	w("")
	w("> 生成于 %s · 引擎耗时 %dms", r.GeneratedAt.Format(time.DateTime), r.DurationMS)
	w("")
	w("| 项 | 值 |")
	w("|---|---|")
	w("| 包名 | %s |", orDash(r.PackageName))
	w("| 版本 | %s |", orDash(r.VersionName))
	w("| SHA256 | `%s` |", r.SHA256)
	w("| 体积 | %s |", humanSize(r.SampleSize))
	w("| DEX / 类 | %d / %d |", r.DexCount, r.ClassCount)
	w("| 加固/壳 | %s |", orDash(r.Packer))
	w("")

	// 攻击面
	if sec := r.ManifestSecurity; sec != nil {
		w("## 攻击面")
		w("")
		explicit, _ := sec["explicit_exported"].(map[string]any)
		implied, _ := sec["implicitly_exported"].(map[string]any)
		for _, kind := range []string{"activity", "activity-alias", "service", "receiver", "provider"} {
			if names, ok := explicit[kind].([]any); ok && len(names) > 0 {
				w("- 显式导出 **%s**: %s", kind, joinAny(names))
			}
		}
		for _, kind := range []string{"activity", "activity-alias", "service", "receiver"} {
			if names, ok := implied[kind].([]any); ok && len(names) > 0 {
				w("- 隐式导出 **%s**(targetSdk<31 默认导出): %s", kind, joinAny(names))
			}
		}
		if schemes, ok := sec["deeplink_schemes"].([]any); ok && len(schemes) > 0 {
			w("- deeplink scheme: %s", joinAny(schemes))
		}
		if flags, ok := sec["app_flags"].(map[string]any); ok && len(flags) > 0 {
			keys := make([]string, 0, len(flags))
			for k := range flags {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			w("- 危险标志: %s", strings.Join(keys, ", "))
		}
		w("")
	}

	// 权限
	if len(r.Permissions) > 0 {
		w("## 权限(%d 项,高危 %d 项)", len(r.Permissions), len(r.RiskPermissions))
		w("")
		if len(r.RiskPermissions) > 0 {
			w("| 高危权限 | 风险 |")
			w("|---|---|")
			for _, p := range r.RiskPermissions {
				w("| %s | %s |", strings.TrimPrefix(p, "android.permission."), r.RiskReasons[p])
			}
			w("")
		}
		w("<details><summary>全部权限</summary>")
		w("")
		for _, p := range r.Permissions {
			w("- `%s`", p)
		}
		w("")
		w("</details>")
		w("")
	}

	// 组件
	if len(r.Components) > 0 {
		w("## 组件")
		w("")
		for _, kind := range []string{"activity", "service", "receiver", "provider"} {
			names := r.Components[kind]
			w("- %s: %d", strings.Title(kind), len(names))
		}
		w("")
	}

	// 端点
	if len(r.Endpoints) > 0 {
		w("## HTTP(S) 端点(去重 %d,展示 %d)", r.EndpointTotal, len(r.Endpoints))
		w("")
		w("| # | URL | 域名 | 次数 |")
		w("|---|-----|------|------|")
		for i, ep := range r.Endpoints {
			w("| %d | %s | %s | %d |", i+1, ep.URL, ep.Host, ep.Hits)
		}
		w("")
	}

	// 发现
	w("## 扫描发现")
	w("")
	if len(r.Findings) == 0 {
		w("无。")
	} else {
		w("| 级别 | 类别 | 检测项 | 命中 | 说明 |")
		w("|---|---|---|---|---|")
		for _, f := range r.Findings {
			note := strings.ReplaceAll(f.Note, "|", "\\|")
			w("| %s | %s | %s | %d | %s |", sevCN(f.Severity), f.Category, f.Name, f.Hits, note)
		}
		w("")
		w("证据定位(每项至多 %d 行):", maxFindingRows)
		w("")
		for _, f := range r.Findings {
			if len(f.Rows) == 0 {
				continue
			}
			w("- **%s**", f.Name)
			for _, row := range f.Rows {
				w("  - `%s` → %s(%s)", dotClassName(row.Class), row.Method, row.DEX)
			}
		}
	}
	return b.String()
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
