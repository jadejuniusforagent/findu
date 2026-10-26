package apkanalysis

// report_scan — 自动分析报告的增强扫描:壳特征 ZIP 条目、字符串池凭据正则、
// URL/端点清单。与预设 findrefs 扫描共用同一次 DEX 遍历(每逻辑 DEX 只
// Parse 一次);凭据证据定位在持有 manager 的回调内即时完成。

import (
	"context"
	"finduai/internal/strutil"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"go.uber.org/zap"

	"finduai/internal/apkengine/dex"
	"finduai/internal/apkengine/findrefs"
	"finduai/internal/apkengine/zipprobe"
)

// ────────────────────────── 壳特征 ZIP 条目 ──────────────────────────

// packerEntrySignatures ZIP 条目名 → 壳产品(条目名小写包含即命中)。
// 来源:各加固产物 lib/ 与 assets/ 常见文件名。比 DEX 内类型特征更可靠:
// 壳运行释放真实 DEX 后类型特征仍在,但部分壳会剥离原始壳类,entry 名恒在。
var packerEntrySignatures = []struct {
	match   string
	product string
}{
	{"libjiagu", "360 加固"},
	{"libshellx", "腾讯乐固"},
	{"libshella", "腾讯乐固"},
	{"tosversion", "腾讯乐固"},
	{"libsecshell", "梆梆加固"},
	{"libdexhelper", "梆梆加固"},
	{"libexecmain", "爱加密"},
	{"ijiami", "爱加密"},
	{"libnesec", "网易易盾"},
	{"libchaosvmp", "娜迦科技"},
	{"libddog", "娜迦科技"},
	{"libbaiduprotect", "百度加固"},
	{"libmobisec", "阿里聚安全"},
	{"libkwssgmain", "通付盾"},
}

// scanPackerEntries 枚举 APK 全部 ZIP 条目,按特征表匹配壳产品。
// 返回 命中产品 → 命中条目名列表。
func scanPackerEntries(apkPath string) (map[string][]string, error) {
	r, err := zipprobe.Open(apkPath)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	entries, err := zipprobe.ParseCDEntries(r)
	if err != nil {
		return nil, err
	}
	hit := map[string][]string{}
	for _, e := range entries {
		lower := strings.ToLower(e.Name)
		for _, sig := range packerEntrySignatures {
			if strings.Contains(lower, sig.match) {
				hit[sig.product] = append(hit[sig.product], e.Name)
			}
		}
	}
	return hit, nil
}

// ────────────────────────── 字符串池扫描(凭据 + URL) ──────────────────────────

type secretRule struct {
	name     string
	re       *regexp.Regexp
	severity string
	note     string
}

// secretRules 高信号硬编码凭据特征(逐条作用于 DEX 字符串池)。
// 命中仅代表"存在该形态字符串",真伪由 Agent 反编译取证复核。
var secretRules = []secretRule{
	{"AWS AccessKey", regexp.MustCompile(`(?:AKIA|ASIA)[0-9A-Z]{16}`), "high", "AWS 云凭据形态;验证时仅做 sts 最小化确认,禁止访问业务资源"},
	{"阿里云 AccessKey", regexp.MustCompile(`LTAI[0-9A-Za-z]{12,24}`), "high", "阿里云 AK 形态;验证时仅做最小化身份确认"},
	{"GitHub Token", regexp.MustCompile(`gh[pousr]_[0-9A-Za-z]{36}`), "high", "GitHub PAT 形态"},
	{"Slack Token", regexp.MustCompile(`xox[baprs]-[0-9A-Za-z-]{10,}`), "high", "Slack token 形态"},
	{"GCP API Key", regexp.MustCompile(`AIza[0-9A-Za-z_-]{35}`), "high", "Google API Key 形态"},
	{"私钥/证书 PEM", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`), "high", "包内硬编码私钥"},
	{"OpenAI/LLM Key", regexp.MustCompile(`sk-[A-Za-z0-9_-]{20,}`), "medium", "OpenAI 形态 key;注意部分 SDK 前缀同名,需取证确认"},
	{"JWT", regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{0,30}`), "medium", "JWT 形态硬编码(可能是测试凭据/公钥 JWK)"},
	{"Google OAuth Client", regexp.MustCompile(`[0-9A-Za-z]+-[0-9A-Za-z_]{32}\.apps\.googleusercontent\.com`), "low", "Google OAuth 客户端 ID(单独存在低危,关注配套 secret)"},
	{"Firebase 数据库", regexp.MustCompile(`[a-z0-9][a-z0-9-]+\.firebase(?:io\.com|database\.app)`), "medium", "Firebase 实时数据库域名;关注是否未配置安全规则"},
}

// secretCombined 单遍加速:合并 alternation 先命中,再逐规则归因。
var secretCombined = func() *regexp.Regexp {
	parts := make([]string, 0, len(secretRules))
	for _, r := range secretRules {
		parts = append(parts, r.re.String())
	}
	return regexp.MustCompile(strings.Join(parts, "|"))
}()

const (
	// maxEndpointURLs 报告保留的端点条数(按命中次数取 Top)。
	maxEndpointURLs = 200
	// maxEndpointHosts 报告保留的域名数。
	maxEndpointHosts = 60
	// maxSecretSamples 每条凭据规则送 findrefs 定位证据的样本值上限。
	maxSecretSamples = 3
	// maxSecretValues 报告展示的每规则去重命中值上限。
	maxSecretValues = 8
)

// urlRegex URL 提取(字符串池内容不可信,长度截断)。
var urlRegex = regexp.MustCompile(`https?://[^\s"'<>\\){}\]\x00-\x1f]{4,256}`)

// EndpointSummary 端点清单条目(按字符串池出现次数聚合)。
type EndpointSummary struct {
	URL  string `json:"url"`
	Host string `json:"host"`
	Hits int    `json:"hits"`
}

// HostSummary 域名聚合。
type HostSummary struct {
	Host string `json:"host"`
	Hits int    `json:"hits"`
}

// dexScanResult 单次 DEX 遍历的完整产出(预设 + 凭据 + 端点)。
type dexScanResult struct {
	findings  []AutoFinding
	endpoints []EndpointSummary
	hosts     []HostSummary
	urlTotal  int
}

// secretState 单条凭据规则的扫描态:去重命中值 + 证据行(即时定位)。
type secretState struct {
	values  map[string]int
	sampled map[string]bool
	rows    []findrefs.RefRow
}

// matchSecretRule 对 combined 正则的命中片段做规则归因(逐规则复测)。
func matchSecretRule(m string) (int, bool) {
	for ri, rule := range secretRules {
		if rule.re.MatchString(m) {
			return ri, true
		}
	}
	return 0, false
}

// scanDexContent 单遍 DEX 多用途扫描:每个逻辑 DEX 只 Parse 一次,同时跑
// 引擎预设查询、字符串池凭据正则与 URL 提取。progress 按 (已处理条目数,
// 条目总数) 回调(进度粒度 = ZIP 条目)。
func scanDexContent(ctx context.Context, log *zap.Logger, apkPath string, progress func(done, total int)) (dexScanResult, error) {
	out := dexScanResult{}
	presets := autoPresets()
	out.findings = make([]AutoFinding, len(presets))
	for i, p := range presets {
		out.findings[i] = p.finding
	}
	states := make([]secretState, len(secretRules))
	for i := range states {
		states[i] = secretState{values: map[string]int{}, sampled: map[string]bool{}}
	}
	urlCounts := map[string]int{}
	hostCounts := map[string]int{}

	err := forEachDexProgress(ctx, log, apkPath, nil, progress, func(mgr *findrefs.Manager) {
		// 预设查询(沿用既有语义)
		for i, p := range presets {
			res := mgr.FindRef(p.query)
			for _, row := range mgr.AggregateRows(res) {
				out.findings[i].Hits += len(row.Matched)
				if len(out.findings[i].Rows) < maxFindingRows {
					out.findings[i].Rows = append(out.findings[i].Rows, row)
				}
			}
		}
		// 字符串池:URL 提取 + 凭据正则
		d := mgr.Dex()
		n := int(d.NumStrings())
		for i := 0; i < n; i++ {
			str := d.GetString(uint32(i))
			if str == "" {
				continue
			}
			for _, m := range urlRegex.FindAllString(str, 4) {
				urlCounts[m]++
				if h := hostOf(m); h != "" {
					hostCounts[h]++
				}
			}
			for _, m := range secretCombined.FindAllString(str, 4) {
				if ri, ok := matchSecretRule(m); ok {
					st := &states[ri]
					st.values[m]++
					// 证据即时定位:每规则前 maxSecretSamples 个去重值,
					// 在当前 manager 上反查引用行
					if len(st.sampled) < maxSecretSamples && !st.sampled[m] && len(st.rows) < maxFindingRows {
						st.sampled[m] = true
						for _, row := range mgr.AggregateRows(mgr.FindRef(&findrefs.Query{String: m})) {
							if len(st.rows) >= maxFindingRows {
								break
							}
							st.rows = append(st.rows, row)
						}
					}
				}
			}
		}
	})
	if err != nil {
		return out, err
	}

	// 凭据 findings:仅命中规则进入报告
	for ri, rule := range secretRules {
		st := &states[ri]
		if len(st.values) == 0 {
			continue
		}
		values := sortedValues(st.values)
		if len(values) > maxSecretValues {
			values = values[:maxSecretValues]
		}
		sampleList := values
		if len(sampleList) > maxSecretSamples {
			sampleList = sampleList[:maxSecretSamples]
		}
		out.findings = append(out.findings, AutoFinding{
			Category: "硬编码凭据",
			Name:     rule.name,
			Severity: rule.severity,
			FindType: "regex",
			Value:    truncate(rule.re.String(), 80),
			Hits:     len(st.values),
			Note: rule.note +
				";去重命中 " + strconv.Itoa(len(st.values)) + " 个值" +
				",样本: " + strings.Join(quoteValues(sampleList), ", "),
			Rows: st.rows,
		})
	}

	out.urlTotal = len(urlCounts)
	out.endpoints = topEndpoints(urlCounts, maxEndpointURLs)
	out.hosts = topHosts(hostCounts, maxEndpointHosts)
	return out, nil
}

// ────────────────────────── 遍历器(带进度) ──────────────────────────

// forEachDexProgress 在 forEachDex 基础上提供 (done,total) 进度回调。
// total = ZIP 条目数(进度粒度=条目,单个条目内多逻辑 DEX 不再细分)。
func forEachDexProgress(ctx context.Context, log *zap.Logger, apkPath string, warnings *[]string, progress func(done, total int), fn func(mgr *findrefs.Manager)) error {
	r, err := zipprobe.Open(apkPath)
	if err != nil {
		return err
	}
	defer r.Close()
	entries, err := zipprobe.ParseCDDexEntries(r)
	if err != nil {
		return err
	}
	zipprobe.SortByCompSize(entries)
	total := len(entries)
	done := 0
	tick := func() {
		done++
		if progress != nil {
			progress(done, total)
		}
	}
	for _, e := range entries {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		data, err := zipprobe.InflateDex(r, e)
		if err != nil {
			warnDex(log, warnings, e.Name+": "+err.Error())
			tick()
			continue
		}
		for _, ld := range zipprobe.LogicalDexBuffers(e.Name, data) {
			d, err := dex.Parse(ld.Buf, ld.Name)
			if err != nil {
				warnDex(log, warnings, ld.Name+": "+err.Error())
				continue
			}
			fn(findrefs.NewManager(d))
		}
		tick()
	}
	return nil
}

// ────────────────────────── 小工具 ──────────────────────────

func sortedValues(m map[string]int) []string {
	type kv struct {
		k string
		v int
	}
	list := make([]kv, 0, len(m))
	for k, v := range m {
		list = append(list, kv{k, v})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].v != list[j].v {
			return list[i].v > list[j].v
		}
		return list[i].k < list[j].k
	})
	out := make([]string, 0, len(list))
	for _, x := range list {
		out = append(out, x.k)
	}
	return out
}

func topEndpoints(m map[string]int, limit int) []EndpointSummary {
	values := sortedValues(m)
	out := make([]EndpointSummary, 0, len(values))
	for _, u := range values {
		out = append(out, EndpointSummary{URL: u, Host: hostOf(u), Hits: m[u]})
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func topHosts(m map[string]int, limit int) []HostSummary {
	values := sortedValues(m)
	out := make([]HostSummary, 0, len(values))
	for _, h := range values {
		out = append(out, HostSummary{Host: h, Hits: m[h]})
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func hostOf(raw string) string {
	// 重复清单收敛：委托 strutil.HostOf（空值语义一致，调用点已判空）
	return strutil.HostOf(raw)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func quoteValues(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		s := truncate(v, 48)
		if len(v) > 48 {
			s += "…"
		}
		out = append(out, `"`+s+`"`)
	}
	return out
}
