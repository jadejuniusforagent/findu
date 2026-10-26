package apkanalysis

// hardening —— 阶段0:加固识别与测试通道决策。
// 对应 docs/移动APP测试能力盘点与增强方案.md 第四节(阶段0)与第五节(五条路线):
// 在投入脱壳前先判定壳类型/跨框架栈,决定走哪条通道,避免把预算全砸在脱壳上。
//
// 快路径(默认):ZIP 条目特征(复用 packerEntrySignatures)+ 跨框架特征 +
// manifest Application 类名 + 样本统计(dex/类数),外加缓存/外部佐证:
//   - 缓存:已完成的自动分析报告里「加固」类命中(InMemoryDexClassLoader 等)
//   - 外部:apkid 在 PATH 时跑一遍指纹(未安装则降级为纯内置特征)
// 慢路径(deep=true):同步跑完整自动分析管线,补 DEX 类型特征(InMemory
// DexClassLoader / changeQuickRedirect 等),代价是一次完整 DEX 扫描。

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	"finduai/internal/apkengine/zipprobe"
)

// ────────────────────────── 报告结构 ──────────────────────────

// HardeningProduct 检出的一类加固/壳(证据聚合)。
type HardeningProduct struct {
	Product    string   `json:"product"`
	Confidence string   `json:"confidence"` // high | medium | low
	Indicators []string `json:"indicators"`
}

// FrameworkHit 检出的跨框架栈(Flutter/RN/Unity 等,决定抓包与反编译路线)。
type FrameworkHit struct {
	Framework  string   `json:"framework"`
	Indicators []string `json:"indicators"`
	Guidance   string   `json:"guidance"`
}

// HardeningStats 样本统计(统计判据:壳把业务 dex 藏起来后类数会异常少)。
type HardeningStats struct {
	DexCount   int   `json:"dex_count"`
	ClassCount int   `json:"class_count"`
	SizeBytes  int64 `json:"size_bytes"`
	// SmallClassProfile 类数异常少(壳稀释特征;未加固大厂应用通常数万类起)。
	SmallClassProfile bool `json:"small_class_profile"`
}

// DecisionRoute 一条通道建议(按 ROI 排序,映射到平台已有工具)。
type DecisionRoute struct {
	Order     int      `json:"order"`
	Key       string   `json:"key"` // dump_timing | channel | repack | api_surface | standard
	Title     string   `json:"title"`
	Rationale string   `json:"rationale"`
	Actions   []string `json:"actions"`
	Tools     []string `json:"tools"`
}

// HardeningReport 阶段0输出:壳/框架识别 + 统计判据 + 通道决策树。
type HardeningReport struct {
	SampleID   string             `json:"sample_id"`
	SampleName string             `json:"sample_name"`
	Packers    []HardeningProduct `json:"packers"`
	Frameworks []FrameworkHit     `json:"frameworks,omitempty"`
	Stats      HardeningStats     `json:"stats"`
	// APKid 外部指纹(apkid 命令输出摘要);APKidAvailable=false 表示未安装。
	APKid          *map[string][]string `json:"apkid,omitempty"`
	APKidAvailable bool                 `json:"apkid_available"`
	Decision       HardeningDecision    `json:"decision"`
	Notes          []string             `json:"notes,omitempty"`
}

// HardeningDecision 通道决策(路线顺序即推荐顺序)。
type HardeningDecision struct {
	Summary string          `json:"summary"`
	Routes  []DecisionRoute `json:"routes"`
}

// ────────────────────────── 特征表 ──────────────────────────

// frameworkSignatures 跨框架 ZIP 条目特征(条目名小写包含即计入)。
var frameworkSignatures = []struct {
	matches   []string
	framework string
	guidance  string
}{
	{
		[]string{"libflutter.so", "libapp.so"}, "Flutter",
		"Flutter 用 BoringSSL 自带证书校验,装系统 CA 也抓不到包:用 reFlutter patch libflutter.so 后重打包,或 hook ssl_verify 结果;Dart AOT 反编译用 blutter",
	},
	{
		[]string{"index.android.bundle", "libhermes.so"}, "React Native",
		"提取 assets/index.android.bundle;Hermes 字节码用 hermes-dec 反编译;加固厂商加密 bundle(如易盾 RN)需先解密",
	},
	{
		[]string{"libil2cpp.so", "global-metadata.dat", "libunity.so"}, "Unity",
		"global-metadata.dat + libil2cpp.so 用 Il2CppDumper 恢复 C# 元数据;运行时 hook 用 frida-il2cpp-bridge",
	},
}

// shellApplicationSignatures manifest application 类名 → 壳产品(小写包含)。
var shellApplicationSignatures = []struct {
	match   string
	product string
}{
	{"com.stub.stubapp", "360 加固"},
	{"com.qihoo.util", "360 加固"},
	{"com.secneo.apkwrapper", "梆梆加固"},
	{"com.secshell", "梆梆加固"},
	{"s.h.e.l.l", "梆梆加固"},
	{"com.tencent.stubshell", "腾讯乐固"},
	{"com.shell.superapplication", "爱加密"},
	{"com.netease.nis", "网易易盾"},
	{"com.netease.mobsec", "网易易盾"},
	{"com.baidu.protect", "百度加固"},
	{"com.alibaba.wirelesssecurity", "阿里聚安全"},
}

// smallClassThreshold 类数低于该阈值视为「类数异常少」(壳稀释)。
const smallClassThreshold = 3000

// manifestApplicationRe 从解码后的 manifest XML 里抓 application android:name。
var manifestApplicationRe = regexp.MustCompile(`<application[^>]*android:name="([^"]+)"`)

// ────────────────────────── 服务方法 ──────────────────────────

// BuildHardeningReport 构建阶段0报告。deep=true 时同步跑完整自动分析
// (DEX 类型特征;大样本耗时,调用方用 ctx 控制预算)。
func (s *Service) BuildHardeningReport(ctx context.Context, sample *Sample, deep bool) (*HardeningReport, error) {
	report := &HardeningReport{
		SampleID:   sample.ID,
		SampleName: sample.Name,
		Stats: HardeningStats{
			DexCount:   sample.DexCount,
			ClassCount: sample.ClassCount,
			SizeBytes:  sample.Size,
		},
		Packers: []HardeningProduct{},
	}
	report.Stats.SmallClassProfile = sample.ClassCount > 0 && sample.ClassCount < smallClassThreshold
	products := map[string]*HardeningProduct{}
	addIndicator := func(product, confidence, indicator string) {
		p, ok := products[product]
		if !ok {
			p = &HardeningProduct{Product: product, Confidence: "low"}
			products[product] = p
		}
		if confRank(confidence) > confRank(p.Confidence) {
			p.Confidence = confidence
		}
		p.Indicators = append(p.Indicators, indicator)
	}

	// 1) ZIP 条目特征(高置信:条目名恒在,不受壳剥离影响)
	entries, err := scanPackerEntries(sample.Workspace)
	if err != nil {
		return nil, fmt.Errorf("扫描 ZIP 条目失败: %w", err)
	}
	for product, names := range entries {
		addIndicator(product, "high", "ZIP 条目: "+strings.Join(names, ", "))
	}

	// 2) 跨框架栈识别
	frameworks := detectFrameworks(sample.Workspace)
	report.Frameworks = frameworks

	// 3) manifest Application 类名(中置信)
	if xml, merr := s.Manifest(ctx, sample.ID); merr == nil {
		if m := manifestApplicationRe.FindStringSubmatch(xml); m != nil {
			cls := strings.TrimSpace(m[1])
			if cls != "" && cls != "android.app.Application" {
				lower := strings.ToLower(cls)
				for _, sig := range shellApplicationSignatures {
					if strings.Contains(lower, sig.match) {
						addIndicator(sig.product, "medium", "Application 类: "+cls)
						break
					}
				}
			}
		}
	}

	// 4) 缓存的自动分析报告「加固」命中(低置信佐证;含 InMemoryDexClassLoader)
	if st, gerr := s.GetAutoReport(sample.ID); gerr == nil && st.Report != nil {
		for _, f := range st.Report.Findings {
			if f.Category == "加固" && f.Hits > 0 {
				addIndicator(strings.TrimSuffix(f.Name, "(条目特征)"), "low",
					fmt.Sprintf("DEX 扫描特征 %s: %d 命中(%s)", f.FindType, f.Hits, f.Value))
			}
		}
	}

	// 5) deep:同步完整扫描补 DEX 类型特征
	if deep {
		if _, aerr := s.BuildAutoReport(ctx, sample); aerr == nil {
			if st, gerr := s.GetAutoReport(sample.ID); gerr == nil && st.Report != nil {
				for _, f := range st.Report.Findings {
					if f.Category == "加固" && f.Hits > 0 && f.FindType != "zip_entry" {
						addIndicator(strings.TrimSuffix(f.Name, "(条目特征)"), "low",
							fmt.Sprintf("deep 扫描特征 %s: %d 命中", f.FindType, f.Hits))
					}
				}
			}
		} else {
			report.Notes = append(report.Notes, "deep 扫描失败(仍可按快路径结论行动): "+aerr.Error())
		}
	}

	for _, p := range products {
		report.Packers = append(report.Packers, *p)
	}
	sort.Slice(report.Packers, func(i, j int) bool {
		if confRank(report.Packers[i].Confidence) != confRank(report.Packers[j].Confidence) {
			return confRank(report.Packers[i].Confidence) > confRank(report.Packers[j].Confidence)
		}
		return report.Packers[i].Product < report.Packers[j].Product
	})

	// 6) apkid 外部指纹(存在才跑,超时 90s)
	if path, lookErr := exec.LookPath("apkid"); lookErr == nil {
		report.APKidAvailable = true
		if hits, aerr := runAPKid(ctx, path, sample.Workspace); aerr != nil {
			report.Notes = append(report.Notes, "apkid 运行失败: "+aerr.Error())
		} else if len(hits) > 0 {
			report.APKid = &hits
		}
	}

	report.Decision = buildDecision(report)
	return report, nil
}

// detectFrameworks ZIP 条目 → 跨框架栈识别。
func detectFrameworks(apkPath string) []FrameworkHit {
	r, err := zipprobe.Open(apkPath)
	if err != nil {
		return nil
	}
	defer func() { _ = r.Close() }()
	entries, err := zipprobe.ParseCDEntries(r)
	if err != nil {
		return nil
	}
	hits := map[string]*FrameworkHit{}
	for _, e := range entries {
		lower := strings.ToLower(e.Name)
		for _, sig := range frameworkSignatures {
			for _, m := range sig.matches {
				if strings.Contains(lower, m) {
					fw, ok := hits[sig.framework]
					if !ok {
						fw = &FrameworkHit{Framework: sig.framework, Guidance: sig.guidance}
						hits[sig.framework] = fw
					}
					fw.Indicators = append(fw.Indicators, e.Name)
				}
			}
		}
	}
	out := make([]FrameworkHit, 0, len(hits))
	for _, fw := range hits {
		out = append(out, *fw)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Framework < out[j].Framework })
	return out
}

// runAPKid 执行 apkid --json,返回 规则 → 命中文件/条目 摘要。
func runAPKid(ctx context.Context, bin, apkPath string) (map[string][]string, error) {
	cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, bin, "--json", apkPath).Output()
	if err != nil {
		return nil, fmt.Errorf("apkid: %w", err)
	}
	// apkid 输出: [{"filename": "...", "results": {"libjiagu.so": {"apkid": {"anti_disassembly": [...], ...}}}}]
	var parsed []struct {
		Filename string `json:"filename"`
		Results  map[string]struct {
			APKid map[string][]string `json:"apkid"`
		} `json:"results"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, fmt.Errorf("解析 apkid 输出: %w", err)
	}
	hits := map[string][]string{}
	for _, item := range parsed {
		for _, fileResult := range item.Results {
			for rule, matches := range fileResult.APKid {
				hits[rule] = append(hits[rule], strings.Join(matches, ", "))
			}
		}
	}
	return hits, nil
}

// buildDecision 通道决策树(五条路线按 ROI 排序;无壳时给常规通道)。
func buildDecision(r *HardeningReport) HardeningDecision {
	hardened := len(r.Packers) > 0
	productNames := make([]string, 0, len(r.Packers))
	for _, p := range r.Packers {
		productNames = append(productNames, p.Product)
	}
	decision := HardeningDecision{Routes: []DecisionRoute{}}
	if !hardened {
		decision.Summary = "未检出加固特征(静态代码面可用;若体感脱壳困难,可 deep=true 复扫并核对 apkid)"
		decision.Routes = append(decision.Routes,
			DecisionRoute{
				Order: 1, Key: "standard", Title: "常规注入通道",
				Rationale: "无壳场景 frida 常规注入即可,不必上对抗手段",
				Actions: []string{
					"android_device_frida action=push/start(ABI 与主机 frida 版本一致)",
					"execute-python-script 跑 hook/脱壳脚本;组件/deeplink 实测走 android_device_shell/android_device_deeplink",
				},
				Tools: []string{"android_device_frida", "android_device_shell", "android_device_deeplink"},
			},
			apiSurfaceRoute(false))
		return decision
	}

	decision.Summary = "检出加固: " + strings.Join(productNames, ", ") +
		";静态明文代码面受限,按下列通道顺序推进(先换 dump 时机,再换注入通道,同时并行打接口面)"
	decision.Routes = append(decision.Routes,
		DecisionRoute{
			Order: 1, Key: "dump_timing", Title: "换 dump 时机:hook 内存 DEX 加载点(首选)",
			Rationale: "按需解密壳的明文 dex 只在类加载窗口存在,等落盘或全量扫内存都拿不到;壳自己会在 InMemoryDexClassLoader/DexFile.openInMemoryDexFile 处把明文 dex 送上门",
			Actions: []string{
				"android_device_frida action=push_stealth(start 前置;反注入目标必选 stealth)",
				"android_device_frida action=builtin script=inmemory_dex_dump(Java 层 hook + FART 式主动调用逼壳解密全部类)",
				"命中不稳时换 script=native_dex_dump(libart.so 符号级兜底);产物 dexset_zip 直接传 android_analyze 收编",
			},
			Tools: []string{"android_device_frida", "android_analyze"},
		},
		DecisionRoute{
			Order: 2, Key: "channel", Title: "换注入通道:patched server / gadget / Zygisk / LSPosed",
			Rationale: "反注入分两层:server 进程层(改名+补丁可过)与注入 agent 内存层(重型脚本被拦);换通道让第一层直接失效",
			Actions: []string{
				"先 push_stealth(Florida/strongR patched 构建,scripts/setup-mobile-tools.sh 预置多 ABI)",
				"仍被拦:frida-gadget 重打包注入(走 repack 通道)或 Magisk Zygisk+Shamiko / LSPosed 模块(免 frida 特征)",
			},
			Tools: []string{"android_device_frida"},
		},
		DecisionRoute{
			Order: 3, Key: "repack", Title: "重打包/签名链:改壳入口注入 gadget",
			Rationale: "apktool 解出的 smali 是壳的,业务代码不存在——只能改壳的入口/检测逻辑;注入 gadget 后无 frida-server 进程,配合轻量 hook 第二层检测命中概率大幅下降",
			Actions: []string{
				"android_repack action=check 确认 apktool/签名工具链",
				"android_repack action=inject_gadget(注入 libgadget.so 加载点)→ resign 重签 → install 实测",
			},
			Tools: []string{"android_repack", "android_device_shell"},
		},
		apiSurfaceRoute(true))
	return decision
}

// apiSurfaceRoute 接口/业务面路线(始终给出:加固只保护客户端,业务漏洞在服务端)。
func apiSurfaceRoute(hardened bool) DecisionRoute {
	r := DecisionRoute{
		Order: 4, Key: "api_surface", Title: "不脱壳,直接打接口面(ROI 最高,建议并行推进)",
		Rationale: "加固保护的是客户端逻辑,业务漏洞在服务端;抓包链路已能拿到全部端点与已登录态,内网端点+已登录态往往比脱壳后的源码更容易出漏洞",
		Actions: []string{
			"android_device_capture action=auto 一键抓包(root 装系统 CA + USB 代理)",
			"android_extract_apis 提取端点清单;http_session 带登录态做越权/参数篡改/批量/支付逻辑",
		},
		Tools: []string{"android_device_capture", "android_extract_apis", "http_session"},
	}
	if hardened {
		r.Actions = append(r.Actions, "客户端硬啃不下时,这条通道是产出可提报漏洞的主路径,不必死磕脱壳")
	}
	return r
}

func confRank(c string) int {
	switch c {
	case "high":
		return 3
	case "medium":
		return 2
	default:
		return 1
	}
}
