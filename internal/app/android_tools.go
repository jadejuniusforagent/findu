package app

// android_tools —— 安卓分析:路由挂载 + Agent 原生工具注册。
// 引擎为进程内 Go 包(internal/apkengine,ASC 移植版),不经子进程;
// 工具语义:android_analyze(归档+画像)/ android_findrefs / android_source /
// android_samples。输出遵循平台工具惯例:JSON 数据 + 「建议」引导字段。

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"finduai/internal/apkanalysis"
	"finduai/internal/database"
	"finduai/internal/logger"
	"finduai/internal/mcp"
	"finduai/internal/mcp/builtin"
	"finduai/internal/security"
	"go.uber.org/zap"
)

// 工具名常量已收编至 internal/mcp/builtin（单一真源）；本地别名零成本保留，引用点不动。
const (
	ToolAndroidAnalyze     = builtin.ToolAndroidAnalyze
	ToolAndroidFindRefs    = builtin.ToolAndroidFindRefs
	ToolAndroidSource      = builtin.ToolAndroidSource
	ToolAndroidSamples     = builtin.ToolAndroidSamples
	ToolAndroidManifest    = builtin.ToolAndroidManifest
	ToolAndroidExtractAPIs = builtin.ToolAndroidExtractAPIs
	ToolAndroidHardening   = builtin.ToolAndroidHardening
	ToolAndroidRepack      = builtin.ToolAndroidRepack
)

func androidWorkspaceDir() string {
	if dir := strings.TrimSpace(os.Getenv("ANDROID_WORKSPACE_DIR")); dir != "" {
		return dir
	}
	return filepath.Join("data", "apk_analysis")
}

// setupAndroidAnalysis 在 setupRoutes 内调用:注册 HTTP 路由 + Agent 工具。
// tokenPicker 复用平台 GitHub Token 池选号函数(与 fingerprint/updatewatch 等消费方一致)。
// 返回安卓分析服务供真机接入模块复用(拉取的 APK 收编进同一工作区)。
func setupAndroidAnalysis(protected *gin.RouterGroup, app *App, db *database.DB, mcpServer *mcp.Server, tokenPicker func(ctx context.Context) string) *apkanalysis.Service {
	svc, err := apkanalysis.NewService(db, app.logger.Logger, androidWorkspaceDir())
	if err != nil {
		app.logger.Error("android analysis service init failed", zap.Error(err))
		return nil
	}
	app.androidSvc = svc // ApplyConfig 重注册 android_* 工具用(2026-09-30 评审 R1)
	// 启动清扫:重启遗留的 running 任务置为 error(否则 forever-running 且无法重新分析)
	svc.SweepStaleRunningTasks()
	// 加载 DB 中已注册的反编译后端(jadx 上传的持久化记录)
	svc.LoadRegisteredBackends(context.Background())
	h := apkanalysis.NewHandler(svc)
	// Phase R2b：安卓分析路由交出桥（同一 handler 实例给宿主运行桥复挂）。
	app.androidHandler = h
	// Agent 工具受 feature_modules.mobile_reverse 开关控制。
	if app.config.Features.MobileReverseEffective() {
		registerAndroidTools(mcpServer, svc, db, app.logger)
	}

	// 注：ASC（Python）自动更新已下线（宿主侧 Python 依赖移除）。反编译默认走
	// 内置 Go 引擎（go-dad → smali 兜底）；如需 DAD，可自行提供 ANDROID_ASC_PATH
	// + ANDROID_ASC_PYTHON（不做自动安装/自动更新）。
	_ = tokenPicker
	// 路由始终注册，由中间件按 feature_modules.mobile_reverse 开关即时拦截。
	h.Register(protected, app.FeatureModuleMiddlewares()["mobile_reverse"])
	return svc
}

func registerAndroidTools(server *mcp.Server, svc *apkanalysis.Service, db *database.DB, log *logger.Logger) {
	if server == nil {
		return
	}

	// ── android_analyze:样本收编 + 基础画像(阶段2补 manifest/反编译画像)──
	server.RegisterTool(mcp.Tool{
		Name:             ToolAndroidAnalyze,
		ShortDescription: "安卓 APK 样本入库与画像(sha256 去重归档)",
		Description:      "把 APK 收编进安卓分析工作区并生成基础画像(dex 数/类数/包信息),随后在「安卓分析」页可见。file 传要收编的包:对话上传附件的 server_path(chat_uploads 下绝对路径),或本会话工作区里的路径——自己 curl/wget 下载、解包合并 split、重打包签名的产物直接传工作区路径即可(相对路径按会话工作区解析,如 download/app.apk),也可传本会话产物目录下的路径;跨项目工作区路径会被拒绝。apk 传已归档样本 id 或文件名;file 与 apk 给一个即可。project_id 可绑定项目(会话绑定项目时强制归到该项目)。",
		InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
			"file":       map[string]interface{}{"type": "string", "description": "对话附件 server_path,或本会话工作区/会话产物下的路径(绝对或相对工作区)"},
			"apk":        map[string]interface{}{"type": "string", "description": "已归档样本 id 或文件名"},
			"project_id": map[string]interface{}{"type": "string", "description": "可选,绑定项目 id"},
		}},
	}, func(ctx context.Context, args map[string]interface{}) (*mcp.ToolResult, error) {
		toolCtx, unboundErr := newAndroidToolContext(svc, db, ctx)
		if unboundErr != "" {
			return textResult(unboundErr, true), nil
		}
		file := strings.TrimSpace(strArg(args, "file"))
		apk := strings.TrimSpace(strArg(args, "apk"))
		projectID := strings.TrimSpace(strArg(args, "project_id"))
		start := time.Now()

		// 会话绑定项目时归档强制落到本项目:样本项目与资产库同边界,
		// 不能通过显式 project_id 把样本录入其他项目。
		if toolCtx.scoped {
			if projectID != "" && projectID != toolCtx.projectID {
				return textResult("错误: 会话已绑定项目,样本只能归档到该项目(跨项目写入被拒绝)", true), nil
			}
			projectID = toolCtx.projectID
		}

		var sample *apkanalysis.Sample
		var err error
		switch {
		case file != "":
			abs, source, rerr := resolveAndroidIngestPath(toolCtx, ctx, file)
			if rerr != nil {
				return textResult("错误: "+rerr.Error(), true), nil
			}
			// 脱壳产物(dex 集合 zip / .apks / .xapk):规范化后走同一收编管道
			switch {
			case strings.HasSuffix(strings.ToLower(abs), ".zip"),
				strings.HasSuffix(strings.ToLower(abs), ".apks"),
				strings.HasSuffix(strings.ToLower(abs), ".xapk"):
				sample, err = svc.RegisterDexSet(ctx, abs, strings.TrimSpace(strArg(args, "name")), projectID, source+"_dexset")
			default:
				sample, err = svc.RegisterFromFile(ctx, abs, strings.TrimSpace(strArg(args, "name")), projectID, source)
			}
		case apk != "":
			sample, err = toolCtx.resolveSample(apk)
		default:
			return textResult("错误: file(对话附件/工作区/会话产物下的包路径)与 apk(已归档样本)至少给一个", true), nil
		}
		if err != nil {
			return textResult("错误: "+err.Error(), true), nil
		}
		profile := map[string]interface{}{}
		if manifestErr := ctx.Err(); manifestErr == nil {
			if p, err := svc.ManifestProfile(ctx, sample.ID); err == nil {
				profile = p
			}
		}
		// 安全摘要:导出组件/deeplink scheme/危险标志 —— 免 Agent 手写 AXML 解析
		security := map[string]interface{}{}
		if ctx.Err() == nil {
			if sec, err := svc.ManifestSecurityProfile(ctx, sample.ID); err == nil {
				security = sec
			}
		}
		// 归档/画像调用落任务历史(Agent 会话里的调用与前端动作同源可见)
		svc.RecordTask(sample, "archive", map[string]any{"file": file, "apk": apk, "project_id": projectID},
			map[string]any{"sample_id": sample.ID, "dex_count": sample.DexCount, "class_count": sample.ClassCount},
			"done", "", time.Since(start).Milliseconds())
		return assetJSONResult(map[string]interface{}{
			"sample":            sample,
			"dex_count":         sample.DexCount,
			"class_count":       sample.ClassCount,
			"manifest":          profile,
			"manifest_security": security,
			"建议":                "看 manifest.permissions 与 manifest_security(导出组件/deeplink/危险标志);下一步用 android_findrefs 搜敏感字符串(如 token/upload/exec)或壳特征(如 changeQuickRedirect);可疑类用 android_source 反编译深挖;需要完整 manifest 细节(intent-filter/权限声明)时调 android_manifest",
		})
	})

	// ── android_findrefs:四类交叉引用检索 ──
	server.RegisterTool(mcp.Tool{
		Name:             ToolAndroidFindRefs,
		ShortDescription: "APK 跨 DEX 交叉引用检索(string/type/method/field)",
		Description:      "对已归档样本执行 ASC 引擎毫秒级交叉引用检索。find_type: string(字符串,如 token/upload)/ type(类型描述符或子串)/ method(方法名)/ field(字段名)。method/field 可用 class 过滤(精确传 dalvik 形态如 Lcom/foo/Bar;或开 fuzzy 用子串如 com.foo)。输出为 命中DEX/类/方法/命中项 结构化行;total_hits 超过 rows 数说明被 limit 截断,可换更精确的 value 缩小范围。",
		InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
			"apk":       map[string]interface{}{"type": "string", "description": "样本 id 或文件名"},
			"find_type": map[string]interface{}{"type": "string", "enum": []string{"string", "type", "method", "field"}, "description": "检索维度"},
			"value":     map[string]interface{}{"type": "string", "description": "查询值"},
			"class":     map[string]interface{}{"type": "string", "description": "可选,method/field 的类名过滤"},
			"fuzzy":     map[string]interface{}{"type": "boolean", "description": "类名模糊匹配(默认 false)"},
			"limit":     map[string]interface{}{"type": "integer", "description": "返回行数上限,默认 1000,最大 5000"},
		}, "required": []string{"apk", "find_type", "value"}},
	}, func(ctx context.Context, args map[string]interface{}) (*mcp.ToolResult, error) {
		toolCtx, unboundErr := newAndroidToolContext(svc, db, ctx)
		if unboundErr != "" {
			return textResult(unboundErr, true), nil
		}
		apk := strings.TrimSpace(strArg(args, "apk"))
		if _, serr := toolCtx.resolveSample(apk); serr != nil {
			return textResult("错误: "+serr.Error(), true), nil
		}
		req := apkanalysis.FindRefsRequest{
			Sample:   apk,
			FindType: strings.TrimSpace(strArg(args, "find_type")),
			Value:    strings.TrimSpace(strArg(args, "value")),
			Class:    strings.TrimSpace(strArg(args, "class")),
			Fuzzy:    boolArg(args, "fuzzy"),
			Limit:    intArg(args, "limit", 200),
		}
		if req.Limit <= 0 || req.Limit > 500 {
			req.Limit = 200 // Agent 消费面限 200 行,防上下文溢出
		}
		result, err := svc.FindRefs(ctx, req)
		if err != nil {
			return textResult("错误: "+err.Error(), true), nil
		}
		return assetJSONResult(map[string]interface{}{
			"sample":     result.SampleName,
			"find_type":  result.FindType,
			"value":      result.Value,
			"total_hits": result.TotalHits,
			"limited":    result.Limited,
			"rows":       result.Rows,
			"建议":         "rows 是 命中DEX/类/方法/命中项 定位信息(无上下文);对可疑类调 android_source 拉反编译源码(阶段2);total_hits > len(rows) 时收紧 value",
		})
	})

	// ── android_source:反编译单类(阶段2)──
	server.RegisterTool(mcp.Tool{
		Name:             ToolAndroidSource,
		ShortDescription: "反编译 APK 内指定类为 Java 源码",
		Description:      "对已归档样本反编译单个类(按降级链选后端:go-dad 内置默认/jadx/DAD/smali 兜底)。返回 dex(来源 DEX 名)与 source(Java 形态源码;异常时回落到 smali 字节码)。⚠ go-dad 为 Basic 级还原,复杂结构可能带注释占位——关键逻辑请结合 smali 视图交叉验证。",
		InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
			"apk":   map[string]interface{}{"type": "string", "description": "样本 id 或文件名"},
			"class": map[string]interface{}{"type": "string", "description": "类名,Lcom/foo/Bar; 或 com.foo.Bar"},
		}, "required": []string{"apk", "class"}},
	}, func(ctx context.Context, args map[string]interface{}) (*mcp.ToolResult, error) {
		toolCtx, unboundErr := newAndroidToolContext(svc, db, ctx)
		if unboundErr != "" {
			return textResult(unboundErr, true), nil
		}
		apk := strings.TrimSpace(strArg(args, "apk"))
		if _, serr := toolCtx.resolveSample(apk); serr != nil {
			return textResult("错误: "+serr.Error(), true), nil
		}
		dexName, source, err := svc.GetSource(ctx,
			apk,
			strings.TrimSpace(strArg(args, "class")))
		if errors.Is(err, apkanalysis.ErrDecompilerUnavailable) {
			return textResult("反编译后端不可用:go-dad 未注册(jadx 需在后端管理上传 jar;DAD 需 ANDROID_ASC_PATH 指向 ASC fork)。当前可用兜底为 Smali 视图(请改用类浏览的 Smali 视图)", true), nil
		}
		if err != nil {
			return textResult("错误: "+err.Error(), true), nil
		}
		if len(source) > 50*1024 {
			source = source[:50*1024] + "\n...[截断,建议按方法分段查看]"
		}
		return assetJSONResult(map[string]interface{}{"dex": dexName, "source": source})
	})

	// ── android_manifest:完整 AndroidManifest 解码 XML ──
	server.RegisterTool(mcp.Tool{
		Name:             ToolAndroidManifest,
		ShortDescription: "APK AndroidManifest 完整解码 XML(原生 AXML 解码,免手写解析)",
		Description:      "返回已归档样本的 AndroidManifest.xml 解码后完整 XML,含全部组件 exported 标志/intent-filter/deeplink/自定义权限等细节。仅需导出组件与 deeplink 摘要时优先用 android_analyze 的 manifest_security 字段;本工具用于需要逐条核对原始声明的场景。超长时截断。",
		InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
			"apk": map[string]interface{}{"type": "string", "description": "样本 id 或文件名"},
		}, "required": []string{"apk"}},
	}, func(ctx context.Context, args map[string]interface{}) (*mcp.ToolResult, error) {
		toolCtx, unboundErr := newAndroidToolContext(svc, db, ctx)
		if unboundErr != "" {
			return textResult(unboundErr, true), nil
		}
		apk := strings.TrimSpace(strArg(args, "apk"))
		if _, serr := toolCtx.resolveSample(apk); serr != nil {
			return textResult("错误: "+serr.Error(), true), nil
		}
		start := time.Now()
		xml, err := svc.Manifest(ctx, apk)
		if err != nil {
			return textResult("错误: "+err.Error(), true), nil
		}
		// manifest 解码调用落任务历史
		if sample, serr := svc.GetSample(apk); serr == nil {
			svc.RecordTask(sample, "manifest", map[string]any{"apk": apk},
				map[string]any{"bytes": len(xml)}, "done", "", time.Since(start).Milliseconds())
		}
		if len(xml) > 100*1024 {
			xml = xml[:100*1024] + "\n...[截断,超大 manifest;请改用 android_analyze 的 manifest_security 摘要]"
		}
		return assetJSONResult(map[string]interface{}{"manifest": xml})
	})

	// ── android_extract_apis:API 提取专项 ──
	server.RegisterTool(mcp.Tool{
		Name:             ToolAndroidExtractAPIs,
		ShortDescription: "提取样本内的 API 端点/域名/deeplink(接口测试目标清单)",
		Description:      "扫描样本 dex/assets/resources 的 URL 模式,分类输出:带路径的 API 端点(可直接喂 http_probe/http_session)、域名资产、deeplink scheme。噪声域(w3.org/apache 等)已过滤。加固样本的明文字符串会被壳吸收:先脱壳(FRIDA-DEXDump via android_device_frida)把产物收编为新样本再调用本工具;未加固样本直接可用。",
		InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
			"apk":   map[string]interface{}{"type": "string", "description": "样本 id 或文件名"},
			"limit": map[string]interface{}{"type": "integer", "description": "可选,每类输出上限,默认 500"},
		}, "required": []string{"apk"}},
	}, func(ctx context.Context, args map[string]interface{}) (*mcp.ToolResult, error) {
		toolCtx, unboundErr := newAndroidToolContext(svc, db, ctx)
		if unboundErr != "" {
			return textResult(unboundErr, true), nil
		}
		sample, err := toolCtx.resolveSample(strings.TrimSpace(strArg(args, "apk")))
		if err != nil {
			return textResult("错误: "+err.Error(), true), nil
		}
		res, err := svc.ExtractAPIs(ctx, sample.ID, intArg(args, "limit", 500))
		if err != nil {
			return textResult("错误: "+err.Error(), true), nil
		}
		return assetJSONResult(map[string]interface{}{
			"sample":    res.SampleName,
			"endpoints": res.Endpoints, "hosts": res.Hosts, "deeplinks": res.Deeplinks,
			"total_urls": res.TotalURLs, "truncated": res.Truncated,
			"建议": "端点清单可交给 http_probe/http_session 做接口测试(带登录态 Cookie/Token 即已认证越权);内网/测试域名(如 *.test.*.local)重点核查;deeplink 用 android_device_deeplink 在真机实测",
		})
	})

	// ── android_samples:样本清单 ──
	server.RegisterTool(mcp.Tool{
		Name:             ToolAndroidSamples,
		ShortDescription: "列出已归档的安卓分析样本",
		Description:      "列出安卓分析工作区已归档样本(名称/sha256/dex 与类数/绑定项目/来源)。project_id 过滤绑定某项目的样本。",
		InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
			"project_id": map[string]interface{}{"type": "string", "description": "可选,按项目过滤"},
		}},
	}, func(ctx context.Context, args map[string]interface{}) (*mcp.ToolResult, error) {
		toolCtx, unboundErr := newAndroidToolContext(svc, db, ctx)
		if unboundErr != "" {
			return textResult(unboundErr, true), nil
		}
		projectFilter := strings.TrimSpace(strArg(args, "project_id"))
		// 会话绑定项目时样本清单限定该项目;显式传了不同项目 → 拒绝
		if toolCtx.scoped {
			if projectFilter != "" && projectFilter != toolCtx.projectID {
				return textResult("错误: 会话已绑定项目,样本查询限定该项目(跨项目查询被拒绝)", true), nil
			}
			projectFilter = toolCtx.projectID
		}
		samples, err := svc.ListSamples(projectFilter)
		if err != nil {
			return textResult("错误: "+err.Error(), true), nil
		}
		return assetJSONResult(map[string]interface{}{
			"samples": samples,
			"建议":      "把样本名作为 android_findrefs/android_source 的 apk 参数",
		})
	})

	// ── android_test_items:测试项清单(Agent 的委派任务队列)──
	server.RegisterTool(mcp.Tool{
		Name:             builtin.ToolAndroidTestItems,
		ShortDescription: "拉取样本的测试项清单(扫描结果生成的待验证任务)",
		Description:      "列出自动解包分析为样本生成的测试项(发现/导出组件/deeplink/危险标志,各带验证指令与状态)。status 过滤:pending(默认建议)/ in_progress / confirmed / clean / na。配合 android_test_item_update 使用:逐项验证后回写状态与结论。",
		InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
			"apk":    map[string]interface{}{"type": "string", "description": "样本 id 或文件名"},
			"status": map[string]interface{}{"type": "string", "description": "可选,按状态过滤"},
		}, "required": []string{"apk"}},
	}, func(ctx context.Context, args map[string]interface{}) (*mcp.ToolResult, error) {
		toolCtx, unboundErr := newAndroidToolContext(svc, db, ctx)
		if unboundErr != "" {
			return textResult(unboundErr, true), nil
		}
		sample, err := toolCtx.resolveSample(strings.TrimSpace(strArg(args, "apk")))
		if err != nil {
			return textResult("错误: "+err.Error(), true), nil
		}
		items, err := svc.ListTestItems(sample.ID, strings.TrimSpace(strArg(args, "status")))
		if err != nil {
			return textResult("错误: "+err.Error(), true), nil
		}
		return assetJSONResult(map[string]interface{}{
			"sample": sample.Name,
			"total":  len(items),
			"items":  items,
			"建议":     "逐项按 instruction 验证(结合 android_source/android_findrefs/android_manifest);每完成一项立即用 android_test_item_update 回写 status(confirmed=风险确认/clean=排除/na=不适用)与 conclusion、evidence",
		})
	})

	// ── android_test_item_update:测试项回写 ──
	server.RegisterTool(mcp.Tool{
		Name:             builtin.ToolAndroidTestItemUpdate,
		ShortDescription: "回写测试项状态与结论(测试闭环)",
		Description:      "更新一个测试项的执行结果。status: in_progress/confirmed(风险确认)/clean(已排除)/na(不适用);conclusion 为一句话结论;evidence 为证据摘要(类/方法/行为)。回写后概览页测试状态列即时可见。",
		InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
			"item_id":         map[string]interface{}{"type": "string", "description": "测试项 id(android_test_items 返回)"},
			"status":          map[string]interface{}{"type": "string", "enum": []string{"in_progress", "confirmed", "clean", "na"}, "description": "新状态"},
			"conclusion":      map[string]interface{}{"type": "string", "description": "结论(一句话)"},
			"evidence":        map[string]interface{}{"type": "string", "description": "证据摘要"},
			"conversation_id": map[string]interface{}{"type": "string", "description": "可选,当前会话 id(溯源)"},
		}, "required": []string{"item_id", "status"}},
	}, func(ctx context.Context, args map[string]interface{}) (*mcp.ToolResult, error) {
		toolCtx, unboundErr := newAndroidToolContext(svc, db, ctx)
		if unboundErr != "" {
			return textResult(unboundErr, true), nil
		}
		itemID := strings.TrimSpace(strArg(args, "item_id"))
		// 会话绑定项目时,测试项所属样本必须在本项目内(跨项目回写被拒绝)
		if toolCtx.scoped {
			var sampleID string
			serr := db.QueryRow(`SELECT sample_id FROM apk_test_items WHERE id = $1`, itemID).Scan(&sampleID)
			if serr != nil {
				return textResult("错误: 测试项不存在: "+serr.Error(), true), nil
			}
			if sample, gerr := svc.GetSample(sampleID); gerr == nil {
				if strings.TrimSpace(sample.ProjectID) != "" && sample.ProjectID != toolCtx.projectID {
					return textResult("错误: 测试项所属样本不属于当前会话绑定的项目,跨项目回写被拒绝", true), nil
				}
			}
		}
		item, err := svc.UpdateTestItem(
			itemID,
			strings.TrimSpace(strArg(args, "status")),
			strings.TrimSpace(strArg(args, "conclusion")),
			strings.TrimSpace(strArg(args, "evidence")),
			strings.TrimSpace(strArg(args, "conversation_id")))
		if err != nil {
			return textResult("错误: "+err.Error(), true), nil
		}
		return assetJSONResult(map[string]interface{}{
			"item": item,
			"建议":   "继续下一项;全部完成后在结论里汇总 confirmed 项与整体风险",
		})
	})

	// ── android_hardening:阶段0 加固识别与通道决策(壳/跨框架/apkid)──
	server.RegisterTool(mcp.Tool{
		Name:             ToolAndroidHardening,
		ShortDescription: "加固识别与测试通道决策(阶段0):壳类型/跨框架栈/apkid 指纹 → 路线建议",
		Description:      "在投入脱壳前先判定壳类型,决定走哪条通道,避免把预算全砸在脱壳上。检测面:ZIP 条目特征(高置信,不受壳剥离影响)、manifest Application 壳类名、样本统计(类数异常少=壳稀释)、缓存自动分析的 DEX 特征(InMemoryDexClassLoader 等)、跨框架栈(Flutter/RN/Unity,决定抓包与反编译路线)、apkid 外部指纹(已安装时)。输出按 ROI 排序的通道决策树:换 dump 时机(hook InMemoryDex 构造)→ 换注入通道(patched server/gadget/Zygisk)→ 重打包注入 → 接口面(始终建议并行,加固只保护客户端,业务漏洞在服务端)。deep=true 同步跑完整 DEX 扫描补类型特征(大样本耗时,先跑快路径)。壳产物收编与「加固识别」扫描在自动分析里也有,但本工具给出的是可行动的路线而非仅特征清单。",
		InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
			"apk":  map[string]interface{}{"type": "string", "description": "样本 id 或文件名"},
			"deep": map[string]interface{}{"type": "boolean", "description": "可选,true 时同步跑完整 DEX 扫描补类型特征(慢,默认 false 用快路径+缓存)"},
		}, "required": []string{"apk"}},
	}, func(ctx context.Context, args map[string]interface{}) (*mcp.ToolResult, error) {
		toolCtx, unboundErr := newAndroidToolContext(svc, db, ctx)
		if unboundErr != "" {
			return textResult(unboundErr, true), nil
		}
		sample, err := toolCtx.resolveSample(strings.TrimSpace(strArg(args, "apk")))
		if err != nil {
			return textResult("错误: "+err.Error(), true), nil
		}
		start := time.Now()
		report, err := svc.BuildHardeningReport(ctx, sample, boolArg(args, "deep"))
		if err != nil {
			return textResult("错误: "+err.Error(), true), nil
		}
		svc.RecordTask(sample, "hardening", map[string]any{"apk": sample.Name, "deep": boolArg(args, "deep")},
			map[string]any{"packers": len(report.Packers), "frameworks": len(report.Frameworks)},
			"done", "", time.Since(start).Milliseconds())
		return assetJSONResult(map[string]interface{}{
			"report": report,
			"建议":     "按 decision.routes 顺序行动:route.key=dump_timing 用 android_device_frida action=builtin;repack 路线先 action=check;api_surface 与脱壳并行推进不要串行等待;跨框架栈命中时先看 frameworks[].guidance(Flutter 抓包需 reFlutter)",
		})
	})

	log.Info("android analysis tools registered",
		zap.String("tools", strings.Join([]string{ToolAndroidAnalyze, ToolAndroidFindRefs, ToolAndroidSource, ToolAndroidSamples, ToolAndroidManifest, ToolAndroidExtractAPIs, ToolAndroidHardening, "android_test_items", "android_test_item_update"}, ",")))
}

// androidConversationProjectScope 当前会话隐含的项目硬边界(与资产库
// agentAssetProjectScope 同语义):会话绑定项目时,安卓工具的样本查询、
// 归档、检索与反编译都只能在本项目内活动。未绑定项目的会话(或无会话
// 上下文的调用)保持既有全量可见行为。
func androidConversationProjectScope(db *database.DB, ctx context.Context) (projectID string, scoped bool, err error) {
	conversationID := conversationIDFromToolCtx(ctx)
	if conversationID == "" {
		return "", false, nil
	}
	projectID, err = db.GetConversationProjectID(conversationID)
	if err != nil {
		return "", false, fmt.Errorf("无法确定当前对话的项目范围")
	}
	projectID = strings.TrimSpace(projectID)
	return projectID, projectID != "", nil
}

// androidResolveSample 取样本并应用会话项目边界:会话绑定项目时,样本必须
// 属于该项目(未绑项目的旧样本视为边界内放行),否则返回错误。
func (r *androidToolContext) resolveSample(apk string) (*apkanalysis.Sample, error) {
	sample, err := r.svc.GetSample(apk)
	if err != nil {
		return nil, err
	}
	if r.scoped && strings.TrimSpace(sample.ProjectID) != "" && sample.ProjectID != r.projectID {
		return nil, fmt.Errorf("样本「%s」不属于当前会话绑定的项目,跨项目访问被拒绝(请用 android_samples 按项目筛选) 或者在新项目的对话里归档", sample.Name)
	}
	return sample, nil
}

// androidToolContext 汇聚工具 handler 内项目边界的解析结果,避免每个工具重复解析。
type androidToolContext struct {
	svc       *apkanalysis.Service
	db        *database.DB
	projectID string // 会话绑定项目;"" = 未绑定
	scoped    bool
}

func newAndroidToolContext(svc *apkanalysis.Service, db *database.DB, ctx context.Context) (*androidToolContext, string) {
	projectID, scoped, serr := androidConversationProjectScope(db, ctx)
	if serr != nil {
		return nil, "错误: " + serr.Error()
	}
	return &androidToolContext{svc: svc, db: db, projectID: projectID, scoped: scoped}, ""
}

// ────────────────────────── 样本收编路径 ──────────────────────────

// androidIngestRoots 当前会话允许收编样本的根目录（按项目/会话硬隔离）：
//   - 对话附件 chat_uploads（附件归属另有 DB 校验，此处只管目录边界）
//   - 本会话工作区 projects/<本项目> 或 conversations/<本会话>——Agent exec 的 cwd
//   - 本会话产物 data/conversation_artifacts/<本会话>（产物按会话分目录）
//
// 与 exec / 文件系统工具共用同一套根目录解析（security 包），保证「Agent 能写到的
// 地方就能收编」，不出现「自己下的包归档不进来」。
func androidIngestRoots(projectID, conversationID string) []apkanalysis.IngestRoot {
	roots := make([]apkanalysis.IngestRoot, 0, 3)
	if dir, err := security.ChatUploadsRootDir(); err == nil {
		roots = append(roots, apkanalysis.IngestRoot{Dir: dir, Source: "chat", Label: "对话附件 " + dir})
	}
	if ws := security.ConversationWorkspaceDir("", projectID, conversationID); ws != "" {
		if abs, aerr := filepath.Abs(ws); aerr == nil {
			roots = append(roots, apkanalysis.IngestRoot{Dir: abs, Source: "workspace", Label: "本会话工作区 " + abs})
		}
	}
	// 无会话上下文时不给产物根：产物按会话分目录，缺会话定位不到"本会话"那棵子树。
	if dir, err := security.ConversationArtifactsDir(conversationID); err == nil {
		roots = append(roots, apkanalysis.IngestRoot{Dir: dir, Source: "conversation_artifact", Label: "本会话产物 " + dir})
	}
	return roots
}

// resolveAndroidIngestPath 解析 Agent 传入的样本路径并按项目边界校验：
// 相对路径按本会话工作区解析（Agent 的 cwd），其次进程 cwd；命中的根目录决定来源标记。
// 工作区路径越出本会话工作区（含未绑定项目会话触碰 projects/*）直接拒绝。
func resolveAndroidIngestPath(tc *androidToolContext, ctx context.Context, raw string) (string, string, error) {
	projectID := strings.TrimSpace(mcp.MCPProjectIDFromContext(ctx))
	if projectID == "" {
		projectID = tc.projectID
	}
	conversationID := conversationIDFromToolCtx(ctx)
	ownWorkspace := security.ConversationWorkspaceDir("", projectID, conversationID)

	// 跨项目工作区前置拒绝：先词法解析候选路径，再判是否落在工作区根内但不属于本会话。
	if candidate := androidIngestCandidate(raw, ownWorkspace); candidate != "" {
		if wsRoot, werr := security.WorkspaceRootDir(""); werr == nil && security.PathWithin(wsRoot, candidate) {
			if ownWorkspace == "" || !security.PathWithin(ownWorkspace, candidate) {
				hint := "本会话无工作区（无项目/会话上下文）"
				if ownWorkspace != "" {
					hint = "本会话工作区: " + ownWorkspace
				}
				return "", "", fmt.Errorf("跨项目工作区路径被拒绝: %s 不落在本会话工作区内（工作区按项目硬隔离）;%s", candidate, hint)
			}
		}
	}

	abs, source, err := apkanalysis.ResolveIngestFile(raw, []string{ownWorkspace, "."}, androidIngestRoots(projectID, conversationID))
	if err != nil {
		return "", "", err
	}
	return abs, source, nil
}

// androidIngestCandidate 词法解析候选绝对路径（相对路径按本会话工作区，其次进程 cwd）。
func androidIngestCandidate(raw, ownWorkspace string) string {
	p := strings.TrimSpace(raw)
	if p == "" {
		return ""
	}
	if !filepath.IsAbs(p) && strings.TrimSpace(ownWorkspace) != "" {
		p = filepath.Join(ownWorkspace, p)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return ""
	}
	return abs
}

// ────────────────────────── 小工具 ──────────────────────────

func filepathIsFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
