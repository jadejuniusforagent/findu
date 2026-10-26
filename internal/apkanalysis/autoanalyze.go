package apkanalysis

// autoanalyze — 上传后的自动解包分析:manifest 画像 + 引擎预设扫描(加固特征/
// 敏感串/加密/网络框架/动态加载/WebView JS 桥)。单遍 DEX 多查询(每 DEX 只
// Parse 一次,全部预设一起跑),结果落 apk_scan_tasks(action=auto_analyze),
// 前端轮询取报告,再决定是否把"解包分析结果"交 Agent 深挖。

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"finduai/internal/apkengine/findrefs"
	"finduai/internal/database"
)

const (
	// ActionAutoAnalyze 自动分析在 apk_scan_tasks 里的 action 值。
	ActionAutoAnalyze = "auto_analyze"
	// autoAnalyzeTimeout 后台分析硬上限。
	autoAnalyzeTimeout = 10 * time.Minute
	// maxFindingRows 每个预设项保留的定位行数(证据样例,非全量)。
	maxFindingRows = 5
)

// AutoFinding 一项预设扫描的命中结论(Hits=0 表示未命中,保留作排除证据)。
type AutoFinding struct {
	Category string            `json:"category"`
	Name     string            `json:"name"`
	Severity string            `json:"severity"` // high | medium | low | info
	FindType string            `json:"find_type"`
	Value    string            `json:"value"`
	Hits     int               `json:"hits"`
	Rows     []findrefs.RefRow `json:"rows,omitempty"`
	Note     string            `json:"note,omitempty"`
}

// AutoReport 自动解包分析报告(manifest 画像 + 攻击面 + 预设扫描发现)。
type AutoReport struct {
	SampleID        string              `json:"sample_id"`
	SampleName      string              `json:"sample_name"`
	SHA256          string              `json:"sha256"`
	SampleSize      int64               `json:"sample_size"`
	PackageName     string              `json:"package_name,omitempty"`
	VersionName     string              `json:"version_name,omitempty"`
	DexCount        int                 `json:"dex_count"`
	ClassCount      int                 `json:"class_count"`
	Permissions     []string            `json:"permissions"`
	RiskPermissions []string            `json:"risk_permissions"`
	RiskReasons     map[string]string   `json:"risk_reasons,omitempty"`
	Components      map[string][]string `json:"components"`
	// ManifestSecurity 攻击面摘要:explicit_exported/implicitly_exported
	// (kind → 组件名)/deeplink_schemes/app_flags。
	ManifestSecurity map[string]any `json:"manifest_security,omitempty"`
	// PackerEntries 壳特征 ZIP 条目命中(产品名 → 条目名列表)。
	PackerEntries map[string][]string `json:"packer_entries,omitempty"`
	Packer        string              `json:"packer,omitempty"` // 命中的加固/壳特征名(DEX 类型 + ZIP 条目合并)
	// Endpoints 字符串池提取的 HTTP(S) 端点清单(Top 200)与域名聚合(Top 60)。
	Endpoints     []EndpointSummary `json:"endpoints,omitempty"`
	EndpointTotal int               `json:"endpoint_total"`
	EndpointHosts []HostSummary     `json:"endpoint_hosts,omitempty"`
	Findings      []AutoFinding     `json:"findings"`
	DurationMS    int64             `json:"duration_ms"`
	GeneratedAt   time.Time         `json:"generated_at"`
}

// AutoReportStatus 前端轮询返回体:running/done/error/none + 报告 + 进度。
type AutoReportStatus struct {
	TaskID    string         `json:"task_id"`
	Status    string         `json:"status"`
	Error     string         `json:"error,omitempty"`
	Progress  map[string]any `json:"progress,omitempty"` // 运行中:{"stage","index","total","detail"}
	Report    *AutoReport    `json:"report,omitempty"`
	CreatedAt time.Time      `json:"created_at,omitempty"`
}

// riskPermissions 高危权限速查(权限 → 风险说明)。
var riskPermissions = map[string]string{
	"android.permission.SEND_SMS":                             "外发短信(吸费/验证码窃取)",
	"android.permission.READ_SMS":                             "读取短信(验证码窃取)",
	"android.permission.RECEIVE_SMS":                          "拦截短信",
	"android.permission.READ_CONTACTS":                        "读取联系人",
	"android.permission.WRITE_CONTACTS":                       "篡改联系人",
	"android.permission.CAMERA":                               "拍照",
	"android.permission.RECORD_AUDIO":                         "录音窃听",
	"android.permission.READ_CALL_LOG":                        "通话记录",
	"android.permission.CALL_PHONE":                           "拨打电话",
	"android.permission.REQUEST_INSTALL_PACKAGES":             "安装其他应用(捆绑下载)",
	"android.permission.SYSTEM_ALERT_WINDOW":                  "悬浮窗(钓鱼/界面劫持)",
	"android.permission.BIND_DEVICE_ADMIN":                    "设备管理器(防卸载)",
	"android.permission.BIND_ACCESSIBILITY_SERVICE":           "无障碍服务(自动化操控/屏幕窃取)",
	"android.permission.READ_PHONE_STATE":                     "设备标识(IMEI)",
	"android.permission.ACCESS_FINE_LOCATION":                 "精确定位",
	"android.permission.ACCESS_BACKGROUND_LOCATION":           "后台持续定位",
	"android.permission.MANAGE_EXTERNAL_STORAGE":              "全盘存储访问",
	"android.permission.QUERY_ALL_PACKAGES":                   "枚举全部应用",
	"android.permission.WRITE_SETTINGS":                       "修改系统设置",
	"android.permission.REQUEST_IGNORE_BATTERY_OPTIMIZATIONS": "保活",
}

type presetQuery struct {
	finding AutoFinding
	query   *findrefs.Query
}

// autoPresets 预设扫描项。findrefs 语义:string/type/成员名均为子串匹配,
// type 建议传描述符片段(如 Lokhttp3 / com.secshell)。
func autoPresets() []presetQuery {
	mk := func(cat, name, sev, findType, value, note string, q *findrefs.Query) presetQuery {
		return presetQuery{finding: AutoFinding{Category: cat, Name: name, Severity: sev, FindType: findType, Value: value, Note: note}, query: q}
	}
	return []presetQuery{
		// ── 加固/壳 ──
		mk("加固", "壳 Hook 特征 changeQuickRedirect", "high", "string", "changeQuickRedirect",
			"热修框架/加固壳常见挂点;命中说明存在 Hook 机制,需先脱壳再做静态分析",
			&findrefs.Query{String: "changeQuickRedirect"}),
		mk("加固", "梆梆 SecNeo", "high", "type", "com.secneo.apkwrapper",
			"梆梆(SecNeo)加固壳类", &findrefs.Query{Type: "com.secneo.apkwrapper"}),
		mk("加固", "梆梆 SecShell", "high", "type", "com.secshell",
			"梆梆(SecShell)加固壳类", &findrefs.Query{Type: "com.secshell"}),
		mk("加固", "360 加固 StubApp", "high", "type", "com.stub.StubApp",
			"360 加固壳入口类", &findrefs.Query{Type: "com.stub.StubApp"}),
		mk("加固", "腾讯乐固 StubShell", "high", "type", "com.tencent.StubShell",
			"腾讯乐固壳类", &findrefs.Query{Type: "com.tencent.StubShell"}),
		mk("加固", "网易易盾 nis wrapper", "high", "type", "com.netease.nis",
			"网易易盾加固壳类(wrapper 释放真实 DEX)", &findrefs.Query{Type: "com.netease.nis"}),
		mk("加固", "InMemoryDexClassLoader 内存加载", "high", "type", "Ldalvik/system/InMemoryDexClassLoader",
			"Android 8+ 内存 DEX 加载:加固壳按需解密单类的典型形态,明文 dex 不常驻——先换 dump 时机(hook 构造点),不要全量扫内存",
			&findrefs.Query{Type: "Ldalvik/system/InMemoryDexClassLoader"}),
		// ── 敏感串 ──
		mk("敏感串", "token", "low", "string", "token",
			"命中数仅指示使用面;取证据行看是否有硬编码凭据", &findrefs.Query{String: "token"}),
		mk("敏感串", "password", "low", "string", "password",
			"命中数仅指示使用面;取证据行看是否有硬编码口令", &findrefs.Query{String: "password"}),
		mk("敏感串", "secret", "low", "string", "secret",
			"命中数仅指示使用面;取证据行看是否有硬编码密钥", &findrefs.Query{String: "secret"}),
		mk("敏感串", "api_key", "low", "string", "api_key",
			"命中数仅指示使用面;取证据行看是否有硬编码 API Key", &findrefs.Query{String: "api_key"}),
		mk("敏感串", "内置证书/私钥", "medium", "string", "-----BEGIN",
			"PEM 头特征,命中说明包内硬编码证书或私钥", &findrefs.Query{String: "-----BEGIN"}),
		// ── 加密 ──
		mk("加密", "JCE 加密 API(javax.crypto)", "info", "type", "Ljavax/crypto",
			"使用 Java 加密扩展;结合证据行评估算法选择与密钥管理", &findrefs.Query{Type: "Ljavax/crypto"}),
		// ── 网络框架 ──
		mk("网络", "OkHttp", "info", "type", "Lokhttp3",
			"OkHttp 网络栈", &findrefs.Query{Type: "Lokhttp3"}),
		mk("网络", "Retrofit", "info", "type", "Lretrofit2",
			"Retrofit 声明式 HTTP", &findrefs.Query{Type: "Lretrofit2"}),
		// ── 动态加载 ──
		mk("动态加载", "DexClassLoader", "high", "type", "Ldalvik/system/DexClassLoader",
			"运行时动态加载 DEX;热修/插件化常见,也可能用于隐藏恶意载荷", &findrefs.Query{Type: "Ldalvik/system/DexClassLoader"}),
		// ── WebView ──
		mk("WebView", "addJavascriptInterface", "medium", "method", "addJavascriptInterface",
			"注册 JS 桥;老系统(<4.2)可致 RCE,新系统审计桥暴露的敏感方法", &findrefs.Query{Method: &findrefs.MemberFind{Name: "addJavascriptInterface"}}),
	}
}

var severityOrder = map[string]int{"high": 0, "medium": 1, "low": 2, "info": 3}

// StartAutoAnalyze 为样本启动后台自动解包分析。已存在 running 任务时幂等返回
// 该任务(started=false)。任务落库后立即返回,分析在 goroutine 中执行。
// 检查+插入放在事务里并取样本级 advisory lock,消除并发双击的 TOCTOU 竞态。
func (s *Service) StartAutoAnalyze(sampleID string) (taskID string, started bool, err error) {
	sample, err := s.GetSample(sampleID)
	if err != nil {
		return "", false, err
	}
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return "", false, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()
	// 锁粒度=样本(哈希到 int64),同样本请求串行化,不同样本互不阻塞
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext($1))`, "apk_auto_analyze:"+sample.ID); err != nil {
		return "", false, fmt.Errorf("advisory lock: %w", err)
	}
	var existing string
	err = tx.QueryRow(`SELECT id FROM apk_scan_tasks
		WHERE sample_id = $1 AND action = $2 AND status = 'running'
		ORDER BY created_at DESC LIMIT 1`, sample.ID, ActionAutoAnalyze).Scan(&existing)
	if err == nil && existing != "" {
		return existing, false, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", false, fmt.Errorf("query running task: %w", err)
	}
	taskID = uuid.NewString()
	if _, err := tx.Exec(`INSERT INTO apk_scan_tasks
		(id, sample_id, project_id, action, query_json, status, result_json, error, duration_ms, created_at)
		VALUES ($1,$2,NULLIF($3,'')::text,$4,'{}','running','','',0,$5)`,
		taskID, sample.ID, sample.ProjectID, ActionAutoAnalyze, time.Now()); err != nil {
		return "", false, fmt.Errorf("insert task: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", false, fmt.Errorf("commit: %w", err)
	}
	go s.runAutoAnalyze(taskID, sample)
	return taskID, true, nil
}

// SweepStaleRunningTasks 启动清扫:服务重启后 running 任务已无执行体,
// 全部置为 error,否则 GetAutoReport 永远显示 running 且 StartAutoAnalyze
// 的 running 幂等检查会拒绝重新分析。
func (s *Service) SweepStaleRunningTasks() {
	res, err := s.db.Exec(`UPDATE apk_scan_tasks
		SET status = 'error', error = 'interrupted by server restart'
		WHERE status = 'running'`)
	if err != nil {
		s.log.Warn("sweep stale running tasks failed", zap.Error(err))
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		s.log.Info("swept stale running android tasks", zap.Int64("count", n))
	}
}

// runAutoAnalyze 后台执行并回写任务状态(超时硬上限 10 分钟)。
// recover 兜底:分析代码路径的 panic 只影响本任务,不能带崩整个服务进程。
func (s *Service) runAutoAnalyze(taskID string, sample *Sample) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("auto-analyze panicked", zap.String("task", taskID), zap.Any("panic", r))
			if _, err := s.db.Exec(`UPDATE apk_scan_tasks
				SET status='error', error=NULLIF($2,'')::text WHERE id=$1`,
				taskID, fmt.Sprintf("internal panic: %v", r)); err != nil {
				s.log.Warn("panic 兜底任务状态回写失败(等待 sweep 兜底)", zap.String("task", taskID), zap.Error(err))
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), autoAnalyzeTimeout)
	defer cancel()
	start := time.Now()
	progress := func(stage string, detail map[string]any) {
		s.updateTaskProgress(taskID, stage, detail)
	}
	report, err := s.BuildAutoReportProgress(ctx, sample, progress)
	status, errText := "done", ""
	if err != nil {
		status, errText = "error", err.Error()
	}
	resultJSON := ""
	if report != nil {
		if b, merr := json.Marshal(report); merr == nil {
			resultJSON = string(b)
		}
	}
	dur := time.Since(start).Milliseconds()
	if _, uerr := s.db.Exec(`UPDATE apk_scan_tasks
		SET status=$2, result_json=$3, error=NULLIF($4,'')::text, duration_ms=$5, progress=NULL WHERE id=$1`,
		taskID, status, resultJSON, errText, dur); uerr != nil {
		s.log.Warn("auto-analyze task update failed", zap.String("task", taskID), zap.Error(uerr))
	}
	s.log.Info("android auto-analyze finished",
		zap.String("task", taskID), zap.String("sample", sample.Name),
		zap.String("status", status), zap.Int64("ms", dur), zap.Error(err))

	// 报告完成后重建测试项(扫描 → Agent 测试闭环的靶子)
	if status == "done" {
		if _, terr := s.RegenerateTestItems(sample.ID); terr != nil {
			s.log.Warn("regenerate test items failed", zap.String("sample", sample.Name), zap.Error(terr))
		}
		// 逆向发现的域名/IP 自动沉淀到资产库(绑定样本项目;无项目不落,避免游离资产)
		if report != nil && strings.TrimSpace(sample.ProjectID) != "" {
			s.importEndpointAssets(sample, report)
		}
	}
}

// importEndpointAssets 把自动分析提取的 HTTP(S) 端点(域名/IP)导入资产库。
// 仅处理绑定了项目的样本,资产绑定该项目;source=android-apk 便于资产库按来源筛选。
// 幂等:UpsertAssets 按 host|port|protocol 去重,重复分析不会重复入账。
func (s *Service) importEndpointAssets(sample *Sample, report *AutoReport) {
	assets := endpointAssetsFromReport(sample.ProjectID, report)
	if len(assets) == 0 {
		return
	}
	// 后台分析无会话主事人:owner 留空,资产仅按项目归属(不写 rbac 资源分配)
	result, err := s.db.UpsertAssetsNoRescore(assets, "")
	if err != nil {
		s.log.Warn("import android endpoints to asset library failed",
			zap.String("sample", sample.Name), zap.String("project", sample.ProjectID), zap.Error(err))
		return
	}
	s.log.Info("android endpoints imported to asset library",
		zap.String("sample", sample.Name), zap.String("project", sample.ProjectID),
		zap.Int("created", result.Created), zap.Int("updated", result.Updated), zap.Int("skipped", result.Skipped))
}

// endpointAssetsFromReport 报告端点 → 资产条目(纯函数,便于单测)。
// 以域名聚合为准(hosts),端点清单补漏;host 去重;非法/占位 host 跳过。
func endpointAssetsFromReport(projectID string, report *AutoReport) []*database.Asset {
	if report == nil {
		return nil
	}
	seen := map[string]bool{}
	mkAsset := func(host string) {
		host = strings.TrimSpace(host)
		host = strings.TrimPrefix(strings.TrimPrefix(host, "https://"), "http://")
		if idx := strings.IndexAny(host, "/:?#"); idx >= 0 {
			host = host[:idx]
		}
		host = strings.Trim(host, "[]")
		if host == "" || seen[host] {
			return
		}
		asset := &database.Asset{
			ProjectID: projectID,
			Host:      host,
			Source:    "android-apk",
			Status:    "active",
			Tags:      []string{"android-apk"},
		}
		if ip := net.ParseIP(host); ip != nil {
			asset.IP = ip.String()
		} else {
			asset.Domain = strings.ToLower(host)
		}
		if database.ValidateAssetForImport(asset) == nil {
			seen[host] = true
		}
	}
	for _, h := range report.EndpointHosts {
		mkAsset(h.Host)
	}
	for _, ep := range report.Endpoints {
		mkAsset(ep.Host)
	}
	out := make([]*database.Asset, 0, len(seen))
	for host := range seen {
		asset := &database.Asset{ProjectID: projectID, Host: host, Source: "android-apk", Status: "active", Tags: []string{"android-apk"}}
		if ip := net.ParseIP(host); ip != nil {
			asset.IP = ip.String()
		} else {
			asset.Domain = strings.ToLower(host)
		}
		out = append(out, asset)
	}
	return out
}

// updateTaskProgress 任务进度落库(stage + 附加字段;失败仅记日志不阻断分析)。
func (s *Service) updateTaskProgress(taskID, stage string, detail map[string]any) {
	obj := map[string]any{"stage": stage}
	for k, v := range detail {
		obj[k] = v
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return
	}
	_, err = s.db.Exec(`UPDATE apk_scan_tasks SET progress = $2 WHERE id = $1`, taskID, string(b))
	if err != nil {
		s.log.Warn("update task progress failed", zap.String("task", taskID), zap.Error(err))
	}
}

// progressFn 阶段进度回调:stage ∈ manifest | packer | dex_scan | done。
type progressFn func(stage string, detail map[string]any)

// BuildAutoReport 执行画像 + 增强扫描(同步,供后台任务与测试直调)。
func (s *Service) BuildAutoReport(ctx context.Context, sample *Sample) (*AutoReport, error) {
	return s.BuildAutoReportProgress(ctx, sample, nil)
}

// BuildAutoReportProgress 同 BuildAutoReport,额外回报分阶段进度。
// 流程:manifest 画像与攻击面 → 壳特征 ZIP 条目 → 单遍 DEX 扫描(预设 +
// 凭据正则 + URL 提取)→ 汇总排序。manifest 失败不阻断,DEX 全局损坏才报错。
func (s *Service) BuildAutoReportProgress(ctx context.Context, sample *Sample, progress progressFn) (*AutoReport, error) {
	start := time.Now()
	step := func(stage string, detail map[string]any) {
		if progress != nil {
			progress(stage, detail)
		}
	}
	report := &AutoReport{
		SampleID:    sample.ID,
		SampleName:  sample.Name,
		SHA256:      sample.SHA256,
		SampleSize:  sample.Size,
		DexCount:    sample.DexCount,
		ClassCount:  sample.ClassCount,
		Permissions: []string{},
		Components:  map[string][]string{},
		GeneratedAt: time.Now(),
	}

	// 1) manifest 画像 + 攻击面摘要(失败不阻断:坏 manifest 不影响 DEX 扫描)
	step("manifest", nil)
	if profile, err := s.ManifestProfile(ctx, sample.ID); err == nil {
		if v, ok := profile["package_name"].(string); ok {
			report.PackageName = v
		}
		if v, ok := profile["version_name"].(string); ok {
			report.VersionName = v
		}
		if perms, ok := profile["permissions"].([]string); ok {
			report.Permissions = perms
		}
		for _, kind := range []string{"activity", "service", "receiver", "provider"} {
			if names, ok := profile[kind+"s"].([]string); ok {
				report.Components[kind] = names
			} else {
				report.Components[kind] = []string{}
			}
		}
	}
	report.RiskPermissions, report.RiskReasons = pickRiskPermissions(report.Permissions)
	if sec, err := s.ManifestSecurityProfile(ctx, sample.ID); err == nil {
		report.ManifestSecurity = sec
	}

	// 2) 壳特征 ZIP 条目扫描(命中并入 Packer 汇总与 findings,证据=条目名)
	step("packer", nil)
	if products, err := scanPackerEntries(sample.Workspace); err == nil && len(products) > 0 {
		report.PackerEntries = products
		names := make([]string, 0, len(products))
		for product, entryNames := range products {
			names = append(names, product)
			f := AutoFinding{
				Category: "加固",
				Name:     product + "(条目特征)",
				Severity: "high",
				FindType: "zip_entry",
				Value:    strings.Join(entryNames, ", "),
				Hits:     len(entryNames),
				Note:     "按 ZIP 条目名识别(比 DEX 类型特征可靠,不受壳剥离影响)",
			}
			for _, en := range entryNames {
				if len(f.Rows) >= maxFindingRows {
					break
				}
				f.Rows = append(f.Rows, findrefs.RefRow{DEX: "-", Class: "(zip entry)", Method: "-", Matched: []string{en}})
			}
			report.Findings = append(report.Findings, f)
		}
		sort.Strings(names)
		if report.Packer == "" {
			report.Packer = strings.Join(names, ", ")
		}
	}

	// 3) 单遍 DEX 扫描:预设 + 凭据 + 端点
	step("dex_scan", nil)
	scan, err := scanDexContent(ctx, s.log, sample.Workspace, func(done, total int) {
		step("dex_scan", map[string]any{"index": done, "total": total})
	})
	if err != nil {
		return report, err
	}
	report.Findings = append(report.Findings, scan.findings...)
	report.Endpoints = scan.endpoints
	report.EndpointTotal = scan.urlTotal
	report.EndpointHosts = scan.hosts

	// Packer 汇总:DEX 类型特征命中(ZIP 条目之外)并入
	if dexPacker := packerOf(scan.findings); dexPacker != "" {
		if report.Packer == "" {
			report.Packer = dexPacker
		} else {
			report.Packer += ", " + dexPacker
		}
	}

	sortFindings(report.Findings)
	report.DurationMS = time.Since(start).Milliseconds()
	report.GeneratedAt = time.Now()
	step("done", nil)
	return report, nil
}

// sortFindings 稳定排序:级别 → 命中数 → 名称。
func sortFindings(findings []AutoFinding) {
	sort.SliceStable(findings, func(i, j int) bool {
		si, sj := severityOrder[findings[i].Severity], severityOrder[findings[j].Severity]
		if si != sj {
			return si < sj
		}
		if findings[i].Hits != findings[j].Hits {
			return findings[i].Hits > findings[j].Hits
		}
		return findings[i].Name < findings[j].Name
	})
}

// scanPresets 仅跑引擎预设扫描(兼容入口,完整扫描走 BuildAutoReportProgress
// → scanDexContent)。返回恰好 len(autoPresets()) 项,按级别/命中排序。
func scanPresets(ctx context.Context, log *zap.Logger, apkPath string) ([]AutoFinding, error) {
	res, err := scanDexContent(ctx, log, apkPath, nil)
	if err != nil {
		return nil, err
	}
	findings := res.findings[:len(autoPresets()):len(autoPresets())]
	sortFindings(findings)
	return findings, nil
}

func packerOf(findings []AutoFinding) string {
	packers := []string{}
	for _, f := range findings {
		if f.Category == "加固" && f.Hits > 0 {
			packers = append(packers, f.Name)
		}
	}
	return strings.Join(packers, ", ")
}

func pickRiskPermissions(perms []string) (risk []string, reasons map[string]string) {
	reasons = map[string]string{}
	for _, p := range perms {
		if why, ok := riskPermissions[p]; ok {
			risk = append(risk, p)
			reasons[p] = why
		}
	}
	return risk, reasons
}

// GetAutoReport 返回样本最近一次自动分析状态与报告(none=从未分析)。
func (s *Service) GetAutoReport(sampleID string) (*AutoReportStatus, error) {
	sample, err := s.GetSample(sampleID)
	if err != nil {
		return nil, err
	}
	st := &AutoReportStatus{Status: "none"}
	var resultJSON, errText, progressJSON string
	err = s.db.QueryRow(`SELECT id, status, COALESCE(result_json,''), COALESCE(error,''), COALESCE(progress,''), created_at
		FROM apk_scan_tasks WHERE sample_id = $1 AND action = $2
		ORDER BY created_at DESC LIMIT 1`, sample.ID, ActionAutoAnalyze).
		Scan(&st.TaskID, &st.Status, &resultJSON, &errText, &progressJSON, &st.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	st.Error = errText
	if progressJSON != "" {
		p := map[string]any{}
		if json.Unmarshal([]byte(progressJSON), &p) == nil {
			st.Progress = p
		}
	}
	if st.Status == "done" && resultJSON != "" {
		report := &AutoReport{}
		if err := json.Unmarshal([]byte(resultJSON), report); err != nil {
			return nil, fmt.Errorf("decode report: %w", err)
		}
		st.Report = report
	}
	return st, nil
}
