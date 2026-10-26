package apkanalysis

// handoff — 扫描结果 → Agent 测试的交接契约。
// 提示词组装统一在服务端(与工具能力/预设定义同处维护),前端只声明
// scope + filter,不再各自拼模板。返回自包含消息,由前端经现有
// 建会话 + composer 自动发送链路投递(所有权/权限复用现有 API 语义)。

import (
	"finduai/internal/strutil"
	"fmt"
	"sort"
	"strings"
)

// lineList 提示词行缓冲(append 语义的简写)。
type lineList []string

func (l *lineList) push(s string) { *l = append(*l, s) }

// HandoffScope 支持的交接范围。
const (
	ScopeReport        = "report"         // 整份报告(画像+攻击面+全部发现)
	ScopeFindings      = "findings"       // 发现清单(可按级别/类别过滤)
	ScopeAttackSurface = "attack_surface" // 导出组件/deeplink/危险标志 → 组件测试计划
	ScopeEndpoints     = "endpoints"      // 端点清单 → API 测试输入
	ScopeClass         = "class"          // 单个类深挖(target.class)
	ScopeFindRefs      = "findrefs"       // 单条检索命中上下文(兼容旧行为)
)

// HandoffRole 交接会话落地的默认角色名。
// 安卓分析交接默认落到「二进制分析」：该角色 tools 内置 android_analyze /
// android_findrefs / android_source / android_samples / android_manifest 等
// 平台原生 APK 分析工具链。避免新建会话默认落在「默认」角色（tools 为空），
// 导致接收 Agent 找不到反编译/检索工具。角色名与 roles/二进制分析.yaml 的 name 一致。
// 前端交接弹窗可选角色覆盖此默认(HandoffRequest.Role)；未知角色由发送链
// resolveRoleToPersist 兜底。
const HandoffRole = "二进制分析"

// HandoffFilter 发现过滤(severity/category 任一为空 = 不过滤)。
type HandoffFilter struct {
	Severity []string `json:"severity"` // high/medium/low/info
	Category []string `json:"category"` // 加固/硬编码凭据/敏感串/加密/网络/动态加载/WebView
	MinHits  int      `json:"min_hits"` // 命中数下限
}

// HandoffRequest 交接请求。
type HandoffRequest struct {
	Scope    string        `json:"scope"`
	SampleID string        `json:"sample_id"` // 样本定位(body 顶层;query 亦可)
	Role     string        `json:"role"`      // 可选,覆盖默认交接角色(见 HandoffRole);未知角色由发送链 resolveRoleToPersist 兜底
	Filter   HandoffFilter `json:"filter"`
	Target   struct {
		Class   string `json:"class"`   // scope=class
		Dex     string `json:"dex"`     // scope=findrefs/class
		Method  string `json:"method"`  // scope=findrefs
		Matched string `json:"matched"` // scope=findrefs
	} `json:"target"`
}

// HandoffResult 交接载荷(title → 会话标题,message → 首条消息)。
type HandoffResult struct {
	SampleID   string `json:"sample_id"`
	SampleName string `json:"sample_name"`
	Title      string `json:"title"`
	ProjectID  string `json:"project_id,omitempty"`
	Role       string `json:"role,omitempty"` // 新建会话应应用的角色(见 HandoffRole)
	Message    string `json:"message"`
	Summary    string `json:"summary"` // 人读的一行摘要(按钮旁提示)
}

// BuildHandoff 组装交接消息。报告缺失时 report/reportErr 提示先跑分析。
func (s *Service) BuildHandoff(sampleID string, req HandoffRequest) (*HandoffResult, error) {
	sample, err := s.GetSample(sampleID)
	if err != nil {
		return nil, err
	}
	scope := req.Scope
	if scope == "" {
		scope = ScopeReport
	}

	var report *AutoReport
	st, _ := s.GetAutoReport(sample.ID)
	if st != nil {
		report = st.Report
	}

	handoffRole := HandoffRole
	if strings.TrimSpace(req.Role) != "" {
		handoffRole = strings.TrimSpace(req.Role)
	}
	res := &HandoffResult{
		SampleID:   sample.ID,
		SampleName: sample.Name,
		ProjectID:  sample.ProjectID,
		Role:       handoffRole,
	}
	titlePrefix := "安卓分析:" + sample.Name

	switch scope {
	case ScopeClass:
		class := normalizeClassQuery(req.Target.Class, false)
		if class == "" {
			return nil, fmt.Errorf("scope=class 需要 target.class")
		}
		res.Title = titlePrefix + " · " + class
		res.Summary = "单类深挖:" + class
		res.Message = buildClassHandoff(sample, report, class, req.Target.Dex)
	case ScopeFindRefs:
		res.Title = titlePrefix + " · 引用核查"
		res.Summary = "单条引用上下文核查"
		res.Message = buildFindRefsHandoff(sample, req)
	case ScopeAttackSurface:
		if report == nil {
			return nil, fmt.Errorf("尚无自动分析报告,请先执行自动解包分析")
		}
		res.Title = titlePrefix + " · 组件攻击面测试"
		res.Summary = "导出组件/deeplink 逐项测试"
		res.Message = buildAttackSurfaceHandoff(sample, report)
	case ScopeEndpoints:
		if report == nil || len(report.Endpoints) == 0 {
			return nil, fmt.Errorf("尚无端点清单,请先执行自动解包分析")
		}
		res.Title = titlePrefix + " · API 端点梳理"
		res.Summary = fmt.Sprintf("%d 个端点交接梳理与测试", len(report.Endpoints))
		res.Message = buildEndpointsHandoff(sample, report)
	case ScopeFindings:
		if report == nil {
			return nil, fmt.Errorf("尚无自动分析报告,请先执行自动解包分析")
		}
		res.Title = titlePrefix + " · 发现核查"
		res.Message = buildFindingsHandoff(sample, report, req.Filter)
		res.Summary = res.firstLine(res.Message)
	default:
		// ScopeReport / 未知值兜底
		res.Title = titlePrefix
		res.Summary = "整份报告交接深挖"
		res.Message = buildReportHandoff(sample, report)
	}
	return res, nil
}

func (r *HandoffResult) firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line != "" {
			return line
		}
	}
	return ""
}

// ────────────────────────── 各 scope 提示词 ──────────────────────────

// handoffHeader 自包含开场:新会话无历史,背景必须带齐。
func handoffHeader(sample *Sample, topic string) *lineList {
	lines := lineList{}
	lines.push(fmt.Sprintf("【安卓样本分析交接】本消息来自安卓分析工作台(%s),是新会话的首条消息,背景自包含无需追问。", topic))
	lines.push("")
	lines.push("## 样本")
	lines.push(fmt.Sprintf("- 样本: %s(ID: %s,后续 android_findrefs / android_source 的 apk 参数直接用该 ID)", sample.Name, sample.ID))
	lines.push(fmt.Sprintf("- sha256: %s · 体积 %s · DEX %d / 类 %d",
		sample.SHA256, humanSize(sample.Size), sample.DexCount, sample.ClassCount))
	if sample.PackageName != "" {
		lines.push(fmt.Sprintf("- 包名: %s · 版本: %s", sample.PackageName, sample.VersionName))
	}
	return &lines
}

func buildReportHandoff(sample *Sample, report *AutoReport) string {
	lines := handoffHeader(sample, "整份自动解包分析报告")
	if report == nil {
		lines.push("")
		lines.push("## 任务")
		lines.push("该样本尚未执行自动解包分析。请先调用 android_analyze 收集画像,再按 android_findrefs → android_source 流程完成静态安全分析,输出 结论/证据/影响 结构化结论。")
		return strings.Join(*lines, "\n")
	}
	lines.push("")
	lines.push("## 自动分析报告")
	appendReportDigest(lines, report, nil)
	appendStandardTasks(lines, sample.ProjectID != "")
	return strings.Join(*lines, "\n")
}

func buildFindingsHandoff(sample *Sample, report *AutoReport, filter HandoffFilter) string {
	lines := handoffHeader(sample, "扫描发现核查")
	hits := filterFindings(report.Findings, filter)
	lines.push("")
	lines.push(fmt.Sprintf("## 待核查发现(过滤后 %d 项)", len(hits)))
	if len(hits) == 0 {
		lines.push("过滤条件下无命中项。请基于报告其余信息判断是否有遗漏的分析角度。")
	} else {
		for _, f := range hits {
			ev := ""
			if len(f.Rows) > 0 {
				ev = fmt.Sprintf(",如 %s -> %s", dotClassName(f.Rows[0].Class), f.Rows[0].Method)
			}
			lines.push(fmt.Sprintf("- [%s] %s · %s:命中 %d 处%s", sevCN(f.Severity), f.Category, f.Name, f.Hits, ev))
		}
	}
	appendStandardTasks(lines, sample.ProjectID != "")
	return strings.Join(*lines, "\n")
}

func buildAttackSurfaceHandoff(sample *Sample, report *AutoReport) string {
	lines := handoffHeader(sample, "组件攻击面测试计划")
	sec := report.ManifestSecurity
	lines.push("")
	lines.push("## 攻击面(manifest_security)")
	if sec == nil {
		lines.push("manifest 安全摘要缺失,请先用 android_analyze / android_manifest 补齐组件信息再评估。")
	}
	if m, ok := sec["explicit_exported"].(map[string]any); ok {
		for _, kind := range []string{"activity", "activity-alias", "service", "receiver", "provider"} {
			if names, ok := m[kind].([]any); ok && len(names) > 0 {
				lines.push(fmt.Sprintf("- 显式导出 %s: %s", kind, joinAny(names)))
			}
		}
	}
	if m, ok := sec["implicitly_exported"].(map[string]any); ok {
		for _, kind := range []string{"activity", "activity-alias", "service", "receiver"} {
			if names, ok := m[kind].([]any); ok && len(names) > 0 {
				lines.push(fmt.Sprintf("- 隐式导出 %s(带 intent-filter 未声明 exported,targetSdk<31 默认导出): %s", kind, joinAny(names)))
			}
		}
	}
	if schemes, ok := sec["deeplink_schemes"].([]any); ok && len(schemes) > 0 {
		lines.push(fmt.Sprintf("- deeplink scheme: %s", joinAny(schemes)))
	}
	if flags, ok := sec["app_flags"].(map[string]any); ok && len(flags) > 0 {
		keys := make([]string, 0, len(flags))
		for k := range flags {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		lines.push("- 危险标志: " + strings.Join(keys, ", "))
	}
	lines.push("")
	lines.push("## 任务(逐组件给出结论,不要泛泛而谈)")
	lines.push("1) 对每个导出组件用 android_manifest 核对 intent-filter/permission 保护,判断可否被任意 App 拉起/绑定/接收广播")
	lines.push("2) 对可疑组件用 android_source 反编译入口类,审计参数处理(onCreate/onStartCommand/onReceive 是否直接信任外部 Intent)")
	lines.push("3) deeplink 逐个 scheme 给出可构造的测试 URI 与预期风险(token/支付参数注入)")
	lines.push("4) 汇总:组件/风险/复现路径/修复建议")
	appendFactHint(lines, sample.ProjectID != "")
	return strings.Join(*lines, "\n")
}

func buildEndpointsHandoff(sample *Sample, report *AutoReport) string {
	lines := handoffHeader(sample, "API 端点清单梳理")
	lines.push("")
	lines.push(fmt.Sprintf("## 端点清单(字符串池提取,去重 %d 个,展示 Top %d)", report.EndpointTotal, len(report.Endpoints)))
	domainCount := map[string]int{}
	for _, ep := range report.Endpoints {
		domainCount[ep.Host]++
	}
	hosts := make([]string, 0, len(domainCount))
	for h := range domainCount {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	lines.push(fmt.Sprintf("- 涉及域名 %d 个: %s", len(hosts), strings.Join(hosts, ", ")))
	lines.push("")
	lines.push("| # | URL | 域名 | 出现次数 |")
	lines.push("|---|-----|------|---------|")
	for i, ep := range report.Endpoints {
		if i >= 80 {
			lines.push(fmt.Sprintf("| … | 共 %d 条,更多见报告 | | |", len(report.Endpoints)))
			break
		}
		lines.push(fmt.Sprintf("| %d | %s | %s | %d |", i+1, ep.URL, ep.Host, ep.Hits))
	}
	lines.push("")
	lines.push("## 任务")
	lines.push("1) 归纳 API 结构(baseURL/版本/路径规律),标注认证线索(端点名含 login/token/auth 等)")
	lines.push("2) 标注可在 Web/API 渗透中优先验证的端点与参数形态(仅静态标注,不发真实请求)")
	lines.push("3) 输出结构化端点清单(方法不明处以 GET? 占位),供后续 API 测试消费")
	appendFactHint(lines, sample.ProjectID != "")
	return strings.Join(*lines, "\n")
}

func buildClassHandoff(sample *Sample, report *AutoReport, class, dexName string) string {
	lines := handoffHeader(sample, "单类深挖")
	if dexName != "" {
		lines.push("- 所在 DEX: " + dexName)
	}
	// 关联发现:该类是否出现在证据行里
	if report != nil {
		related := []string{}
		for _, f := range report.Findings {
			for _, row := range f.Rows {
				if dotClassName(row.Class) == class || row.Class == class {
					related = append(related, fmt.Sprintf("%s(%s)", f.Name, f.Category))
					break
				}
			}
		}
		if len(related) > 0 {
			lines.push("- 关联发现: " + strings.Join(related, ", "))
		}
	}
	lines.push("")
	lines.push("## 任务")
	lines.push(fmt.Sprintf("1) 用 android_source 反编译 %s,梳理该类职责与关键方法", class))
	lines.push("2) 结合 android_findrefs 找出调用方/被调用面,判断在业务里的位置(网络/加密/动态加载/持久化)")
	lines.push("3) 评估风险(硬编码凭据/不安全加密/可利用入口),输出 结论/证据/影响")
	return strings.Join(*lines, "\n")
}

func buildFindRefsHandoff(sample *Sample, req HandoffRequest) string {
	lines := handoffHeader(sample, "交叉引用核查")
	t := req.Target
	if t.Class != "" {
		lines.push(fmt.Sprintf("- 定位: %s (DEX: %s)", t.Class, t.Dex))
	}
	if t.Method != "" {
		lines.push("- 方法: " + t.Method)
	}
	if t.Matched != "" {
		lines.push("- 命中内容: " + t.Matched)
	}
	lines.push("")
	lines.push("## 任务")
	lines.push("1) 反编译该类,结合命中内容判断行为(网络/加密/动态加载/持久化)")
	lines.push("2) 评估是否存在风险并给出证据")
	return strings.Join(*lines, "\n")
}

// ────────────────────────── 共用片段 ──────────────────────────

func appendReportDigest(lines *lineList, report *AutoReport, filter *HandoffFilter) {
	if report.PackageName != "" {
		*lines = append(*lines, fmt.Sprintf("- 包名: %s · 版本: %s", report.PackageName, report.VersionName))
	}
	if report.Packer != "" {
		*lines = append(*lines, "- 🔴 加固/壳: "+report.Packer+"(静态结论需谨慎,优先评估脱壳必要性)")
	}
	if report.RiskPermissions != nil && len(report.RiskPermissions) > 0 {
		*lines = append(*lines, fmt.Sprintf("- 高危权限 %d 项: %s", len(report.RiskPermissions), shortPerms(report.RiskPermissions)))
	}
	if sec := report.ManifestSecurity; sec != nil {
		if m, ok := sec["explicit_exported"].(map[string]any); ok {
			n := 0
			for _, v := range m {
				if arr, ok := v.([]any); ok {
					n += len(arr)
				}
			}
			if n > 0 {
				*lines = append(*lines, fmt.Sprintf("- 显式导出组件 %d 个", n))
			}
		}
		if schemes, ok := sec["deeplink_schemes"].([]any); ok && len(schemes) > 0 {
			*lines = append(*lines, "- deeplink scheme: "+joinAny(schemes))
		}
	}
	if report.EndpointTotal > 0 {
		*lines = append(*lines, fmt.Sprintf("- HTTP(S) 端点 %d 个(Top %d 见清单)", report.EndpointTotal, len(report.Endpoints)))
	}
	hits := filterFindings(report.Findings, func() HandoffFilter {
		if filter != nil {
			return *filter
		}
		return HandoffFilter{}
	}())
	*lines = append(*lines, "")
	*lines = append(*lines, fmt.Sprintf("## 扫描发现(命中 %d 项)", countHits(hits)))
	for _, f := range hits {
		ev := ""
		if len(f.Rows) > 0 {
			ev = fmt.Sprintf(",如 %s -> %s", dotClassName(f.Rows[0].Class), f.Rows[0].Method)
		}
		*lines = append(*lines, fmt.Sprintf("- [%s] %s · %s:命中 %d 处%s", sevCN(f.Severity), f.Category, f.Name, f.Hits, ev))
	}
}

func appendStandardTasks(lines *lineList, hasProject bool) {
	*lines = append(*lines, "")
	*lines = append(*lines, "## 任务")
	*lines = append(*lines, "0) 测试闭环:先用 android_test_items 拉取本样本的测试项清单,逐项验证并在完成后立即用 android_test_item_update 回写 status 与 conclusion/evidence(测试账本,概览页可见)")
	*lines = append(*lines, "1) 优先研判高危项(加固/硬编码凭据/动态加载/JS 桥),逐项给出判断依据")
	*lines = append(*lines, "2) 对可疑类用 android_source 反编译取证(逐类查看,不要全量拉取)")
	*lines = append(*lines, "3) 输出整体风险结论与证据清单(结论/证据/影响)")
	if hasProject {
		*lines = append(*lines, "4) 有沉淀价值的结论用 upsert_project_fact 写入项目事实库")
	}
}

func appendFactHint(lines *lineList, hasProject bool) {
	if hasProject {
		*lines = append(*lines, "")
		*lines = append(*lines, "确认的结论(域名/组件风险/凭据)用 upsert_project_fact 写入项目事实库,供后续会话复用。")
	}
}

func filterFindings(findings []AutoFinding, filter HandoffFilter) []AutoFinding {
	sevSet := toSet(filter.Severity)
	catSet := toSet(filter.Category)
	out := []AutoFinding{}
	for _, f := range findings {
		if f.Hits <= 0 && (sevSet != nil || catSet != nil || filter.MinHits > 0) {
			// 明确给出过滤条件时,零命中项无核查价值
			continue
		}
		if filter.MinHits > 0 && f.Hits < filter.MinHits {
			continue
		}
		if sevSet != nil && !sevSet[f.Severity] {
			continue
		}
		if catSet != nil && !catSet[f.Category] {
			continue
		}
		out = append(out, f)
	}
	return out
}

func countHits(findings []AutoFinding) int {
	n := 0
	for _, f := range findings {
		if f.Hits > 0 {
			n++
		}
	}
	return n
}

func toSet(list []string) map[string]bool {
	if len(list) == 0 {
		return nil
	}
	set := map[string]bool{}
	for _, v := range list {
		set[strings.TrimSpace(v)] = true
	}
	return set
}

func sevCN(sev string) string {
	return map[string]string{"high": "高", "medium": "中", "low": "低", "info": "信息"}[sev]
}

func dotClassName(cls string) string {
	if strings.HasPrefix(cls, "L") && strings.HasSuffix(cls, ";") {
		return strings.ReplaceAll(cls[1:len(cls)-1], "/", ".")
	}
	return cls
}

func shortPerms(perms []string) string {
	out := make([]string, 0, len(perms))
	for _, p := range perms {
		out = append(out, strings.TrimPrefix(p, "android.permission."))
	}
	if len(out) > 10 {
		return strings.Join(out[:10], ", ") + fmt.Sprintf(" 等 %d 项", len(out))
	}
	return strings.Join(out, ", ")
}

func joinAny(list []any) string {
	parts := make([]string, 0, len(list))
	for _, v := range list {
		parts = append(parts, fmt.Sprintf("%v", v))
	}
	return strings.Join(parts, ", ")
}

func humanSize(n int64) string {
	// 重复清单收敛：委托 strutil.HumanSize
	return strutil.HumanSize(n)
}
