package app

// miniprogram_tools —— 小程序分析:路由挂载 + Agent 原生工具注册。
// wxapkg 格式解析、解包、敏感信息扫描;输出遵循平台工具惯例:JSON 数据 + 「建议」引导字段。

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"finduai/internal/audit"
	"finduai/internal/database"
	"finduai/internal/logger"
	"finduai/internal/mcp"
	"finduai/internal/mcp/builtin"
	"finduai/internal/miniprogram"
	"finduai/internal/netproxy"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func miniprogramWorkspaceDir() string {
	if dir := strings.TrimSpace(os.Getenv("MINIPROGRAM_WORKSPACE_DIR")); dir != "" {
		return dir
	}
	return filepath.Join("data", "miniprogram_analysis")
}

// setupMiniprogramAnalysis 注册 HTTP 路由 + Agent 工具
func setupMiniprogramAnalysis(protected *gin.RouterGroup, app *App, db *database.DB, mcpServer *mcp.Server, auditSvc *audit.Service) {
	svc, err := miniprogram.NewService(db, app.logger.Logger, miniprogramWorkspaceDir())
	if err != nil {
		app.logger.Error("miniprogram analysis service init failed", zap.Error(err))
		return
	}
	app.miniprogramSvc = svc
	h := miniprogram.NewHandler(svc)
	if auditSvc != nil {
		h.SetAudit(auditSvc)
	}
	// Phase R2b：小程序分析路由交出桥（宿主运行时收口浮现同一 handler 实例）。
	app.miniprogramHandler = h
	// 路由始终注册，由中间件按 feature_modules.mobile_reverse 开关即时拦截。
	h.Register(protected, app.FeatureModuleMiddlewares()["mobile_reverse"])
	if app.config.Features.MobileReverseEffective() {
		registerMiniprogramTools(mcpServer, svc, db, app.logger)
	}
}

func registerMiniprogramTools(server *mcp.Server, svc *miniprogram.Service, db *database.DB, log *logger.Logger) {
	if server == nil {
		return
	}

	// ── miniprogram_analyze: 上传 + 解包 + 敏感扫描 ──
	server.RegisterTool(mcp.Tool{
		Name:             builtin.ToolMiniprogramAnalyze,
		ShortDescription: "小程序 wxapkg 样本上传解包与敏感信息扫描",
		Description:      "把 wxapkg 收编进小程序分析工作区:解析文件格式、解压源码、扫描敏感信息(API接口/硬编码密钥/appid/云开发env)。file 传对话上传附件的 server_path(chat_uploads 下),sample 传已归档样本 id;二者给一个即可。PC 微信 4.x 的包是 V1MMWX 加密格式,需带 appid 参数解密。project_id 可绑定项目。",
		InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
			"file":       map[string]interface{}{"type": "string", "description": "对话上传附件的 server_path(chat_uploads 下)"},
			"sample":     map[string]interface{}{"type": "string", "description": "已归档样本 id 或文件名"},
			"appid":      map[string]interface{}{"type": "string", "description": "可选,小程序 AppID;PC 微信 4.x 的 V1MMWX 加密包必须提供才能解密(就是包所在文件夹名)"},
			"project_id": map[string]interface{}{"type": "string", "description": "可选,绑定项目 id"},
		}},
	}, func(ctx context.Context, args map[string]interface{}) (*mcp.ToolResult, error) {
		toolCtx, unboundErr := newMiniprogramToolContext(svc, db, ctx)
		if unboundErr != "" {
			return textResult(unboundErr, true), nil
		}
		file := strings.TrimSpace(strArg(args, "file"))
		sampleArg := strings.TrimSpace(strArg(args, "sample"))
		projectID := strings.TrimSpace(strArg(args, "project_id"))
		start := time.Now()

		// 会话绑定项目时归档强制落到本项目
		if toolCtx.scoped {
			if projectID != "" && projectID != toolCtx.projectID {
				return textResult("错误: 会话已绑定项目,样本只能归档到该项目", true), nil
			}
			projectID = toolCtx.projectID
		}

		var sample *miniprogram.Sample
		var err error
		switch {
		case file != "":
			if !isSafeUploadPath(db.ChatUploadsRoot(), file) {
				return textResult("错误: file 必须是对话上传附件的 server_path", true), nil
			}
			if !filepathIsFile(file) {
				return textResult("错误: file 不存在或不可读: "+file, true), nil
			}
			sample, err = svc.RegisterFromFiles(
				[]miniprogram.UploadPart{{Path: file, Name: filepathBase(file)}},
				projectID, "", "chat", strings.TrimSpace(strArg(args, "appid")))
		case sampleArg != "":
			sample, err = toolCtx.resolveSample(sampleArg)
		default:
			return textResult("错误: file(对话上传的 server_path)与 sample(已归档样本)至少给一个", true), nil
		}
		if err != nil {
			return textResult("错误: "+err.Error(), true), nil
		}

		// 获取发现
		findings, _ := svc.ListFindings(sample.ID, "")
		duration := time.Since(start).Milliseconds()

		return assetJSONResult(map[string]interface{}{
			"sample":         sample,
			"app_id":         sample.AppID,
			"file_count":     sample.FileCount,
			"findings":       findings,
			"findings_count": len(findings),
			"duration_ms":    duration,
			"建议":             "查看 findings 中的敏感信息(API接口/密钥/appid/云环境);用 miniprogram_source 读取关键文件;用 miniprogram_findrefs 搜索特定关键词",
		})
	})

	// ── miniprogram_source: 读取指定源码文件 ──
	server.RegisterTool(mcp.Tool{
		Name:             builtin.ToolMiniprogramSource,
		ShortDescription: "读取小程序样本中的指定源码文件",
		Description:      "读取已归档样本中指定路径的源码文件内容(JS/WXML/WXSS/JSON)。先用 miniprogram_analyze 或 miniprogram_samples 获取样本 id,再用 file_path 指定要读取的文件(相对路径,如 pages/index/index.js)。",
		InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
			"sample":    map[string]interface{}{"type": "string", "description": "样本 id 或文件名"},
			"file_path": map[string]interface{}{"type": "string", "description": "要读取的文件路径(相对路径)"},
		}, "required": []string{"sample", "file_path"}},
	}, func(ctx context.Context, args map[string]interface{}) (*mcp.ToolResult, error) {
		toolCtx, unboundErr := newMiniprogramToolContext(svc, db, ctx)
		if unboundErr != "" {
			return textResult(unboundErr, true), nil
		}
		sampleArg := strings.TrimSpace(strArg(args, "sample"))
		filePath := strings.TrimSpace(strArg(args, "file_path"))
		if _, serr := toolCtx.resolveSample(sampleArg); serr != nil {
			return textResult("错误: "+serr.Error(), true), nil
		}
		data, err := svc.ReadSourceFile(sampleArg, filePath)
		if err != nil {
			return textResult("错误: "+err.Error(), true), nil
		}
		content := string(data)
		if len(content) > 50*1024 {
			content = content[:50*1024] + "\n...[截断]"
		}
		return assetJSONResult(map[string]interface{}{"file": filePath, "content": content})
	})

	// ── miniprogram_samples: 列出已归档样本 ──
	server.RegisterTool(mcp.Tool{
		Name:             builtin.ToolMiniprogramSamples,
		ShortDescription: "列出已归档的小程序分析样本",
		Description:      "列出小程序分析工作区已归档样本(名称/appid/sha256/文件数/绑定项目/来源)。project_id 过滤绑定某项目的样本。",
		InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
			"project_id": map[string]interface{}{"type": "string", "description": "可选,按项目过滤"},
		}},
	}, func(ctx context.Context, args map[string]interface{}) (*mcp.ToolResult, error) {
		toolCtx, unboundErr := newMiniprogramToolContext(svc, db, ctx)
		if unboundErr != "" {
			return textResult(unboundErr, true), nil
		}
		projectFilter := strings.TrimSpace(strArg(args, "project_id"))
		if toolCtx.scoped {
			if projectFilter != "" && projectFilter != toolCtx.projectID {
				return textResult("错误: 会话已绑定项目,样本查询限定该项目", true), nil
			}
			projectFilter = toolCtx.projectID
		}
		samples, err := svc.ListSamples(projectFilter)
		if err != nil {
			return textResult("错误: "+err.Error(), true), nil
		}
		return assetJSONResult(map[string]interface{}{
			"samples": samples,
			"建议":      "把样本 id 作为 miniprogram_source/miniprogram_findrefs 的 sample 参数",
		})
	})

	// ── miniprogram_findrefs: 跨文件搜索 ──
	server.RegisterTool(mcp.Tool{
		Name:             builtin.ToolMiniprogramFindrefs,
		ShortDescription: "小程序样本内跨文件关键词/正则搜索",
		Description:      "对已归档样本执行跨文件搜索(keyword 为关键词子串,regex 为正则表达式),返回命中行列表。支持 JS/WXML/WXSS/JSON 等文本文件。",
		InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
			"sample":  map[string]interface{}{"type": "string", "description": "样本 id 或文件名"},
			"keyword": map[string]interface{}{"type": "string", "description": "关键词(子串匹配)"},
			"regex":   map[string]interface{}{"type": "string", "description": "正则表达式(keyword 和 regex 给一个即可)"},
			"limit":   map[string]interface{}{"type": "integer", "description": "返回行数上限,默认 100,最大 500"},
		}, "required": []string{"sample"}},
	}, func(ctx context.Context, args map[string]interface{}) (*mcp.ToolResult, error) {
		toolCtx, unboundErr := newMiniprogramToolContext(svc, db, ctx)
		if unboundErr != "" {
			return textResult(unboundErr, true), nil
		}
		sampleArg := strings.TrimSpace(strArg(args, "sample"))
		sample, serr := toolCtx.resolveSample(sampleArg)
		if serr != nil {
			return textResult("错误: "+serr.Error(), true), nil
		}
		keyword := strings.TrimSpace(strArg(args, "keyword"))
		regexPattern := strings.TrimSpace(strArg(args, "regex"))
		if keyword == "" && regexPattern == "" {
			return textResult("错误: keyword 与 regex 至少给一个", true), nil
		}
		limit := intArg(args, "limit", 100)
		if limit <= 0 || limit > 500 {
			limit = 100
		}

		// 编译正则（提供时）
		var re *regexp.Regexp
		if regexPattern != "" {
			var cerr error
			re, cerr = regexp.Compile(regexPattern)
			if cerr != nil {
				return textResult("错误: 正则表达式无效: "+cerr.Error(), true), nil
			}
		}

		// 在工作目录中搜索（与交接提示词共用同一实现）；Agent 对话内的检索同样落分析记录
		start := time.Now()
		hits, werr := miniprogram.SearchWorkspaceWithRegex(sample.Workspace, keyword, re, limit)
		duration := time.Since(start).Milliseconds()
		req := map[string]any{"sample": sampleArg, "keyword": keyword, "regex": regexPattern, "limit": limit}
		if werr != nil {
			svc.RecordTask(sample, "findrefs", req, nil, "error", werr.Error(), duration)
			return textResult("错误: "+werr.Error(), true), nil
		}
		svc.RecordTask(sample, "findrefs", req, map[string]any{"total_hits": len(hits)}, "done", "", duration)

		return assetJSONResult(map[string]interface{}{
			"sample":     sample.Name,
			"keyword":    keyword,
			"regex":      regexPattern,
			"total_hits": len(hits),
			"hits":       hits,
			"建议":         "对命中文件调用 miniprogram_source 查看完整上下文",
		})
	})

	// ── miniprogram_test_items:测试项清单(Agent 的委派任务队列)──
	server.RegisterTool(mcp.Tool{
		Name:             builtin.ToolMiniprogramTestItems,
		ShortDescription: "拉取样本的测试项清单(扫描发现生成的待验证任务)",
		Description:      "列出敏感信息扫描为样本生成的测试项(API接口/密钥/AppID/云环境/敏感URL,各带验证指令与状态)。status 过滤:pending(默认建议)/ in_progress / confirmed / clean / na。配合 miniprogram_test_item_update 使用:逐项验证后回写状态与结论。",
		InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
			"sample": map[string]interface{}{"type": "string", "description": "样本 id 或文件名"},
			"status": map[string]interface{}{"type": "string", "description": "可选,按状态过滤"},
		}, "required": []string{"sample"}},
	}, func(ctx context.Context, args map[string]interface{}) (*mcp.ToolResult, error) {
		toolCtx, unboundErr := newMiniprogramToolContext(svc, db, ctx)
		if unboundErr != "" {
			return textResult(unboundErr, true), nil
		}
		sample, err := toolCtx.resolveSample(strings.TrimSpace(strArg(args, "sample")))
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
			"建议":     "逐项按 instruction 验证(结合 miniprogram_source/miniprogram_findrefs 定位证据);每完成一项立即用 miniprogram_test_item_update 回写 status(confirmed=风险确认/clean=排除/na=不适用)与 conclusion、evidence",
		})
	})

	// ── miniprogram_test_item_update:测试项回写 ──
	server.RegisterTool(mcp.Tool{
		Name:             builtin.ToolMiniprogramTestItemUpdate,
		ShortDescription: "回写测试项状态与结论(测试闭环)",
		Description:      "更新一个小程序样本测试项的执行结果。status: in_progress/confirmed(风险确认)/clean(已排除)/na(不适用);conclusion 为一句话结论;evidence 为证据摘要(文件/行/命中值形态)。回写后概览页测试状态即时可见。",
		InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
			"item_id":         map[string]interface{}{"type": "string", "description": "测试项 id(miniprogram_test_items 返回)"},
			"status":          map[string]interface{}{"type": "string", "enum": []string{"in_progress", "confirmed", "clean", "na"}, "description": "新状态"},
			"conclusion":      map[string]interface{}{"type": "string", "description": "结论(一句话)"},
			"evidence":        map[string]interface{}{"type": "string", "description": "证据摘要"},
			"conversation_id": map[string]interface{}{"type": "string", "description": "可选,当前会话 id(溯源)"},
		}, "required": []string{"item_id", "status"}},
	}, func(ctx context.Context, args map[string]interface{}) (*mcp.ToolResult, error) {
		toolCtx, unboundErr := newMiniprogramToolContext(svc, db, ctx)
		if unboundErr != "" {
			return textResult(unboundErr, true), nil
		}
		itemID := strings.TrimSpace(strArg(args, "item_id"))
		// 会话绑定项目时,测试项所属样本必须在本项目内(跨项目回写被拒绝)
		if toolCtx.scoped {
			var sampleID string
			serr := db.QueryRow(`SELECT sample_id FROM miniprogram_test_items WHERE id = $1`, itemID).Scan(&sampleID)
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

	// ── miniprogram_jscode2session:登录态链第一步(js_code 换 openid/session_key)──
	server.RegisterTool(mcp.Tool{
		Name:             builtin.ToolMiniprogramJsCode2Session,
		ShortDescription: "jscode2session:用 AppID/Secret+js_code 换 openid/session_key(登录态链第一步)",
		Description:      "调微信 sns/jscode2session,用 app_id+secret+js_code 换取 openid/session_key/unionid。实测该接口不受 cgi-bin 的 IP 白名单约束——40164 被拦的凭据在此仍能校验,因此 code_invalid 结论(凭据已验真,仅 js_code 无效)等价于凭据旁路验证通过。js_code 来自小程序端 wx.login(抓包获取,一次性,约 5 分钟有效)。credential_id 可传已入池凭证(miniapp_key_list 取得)替代明文,与 app_id/secret 二选一。边界:换取动作仅用于授权测试;伪造 openid 建会话、批量拉数据等利用必须显式走 http_session 并遵守 ROE。",
		InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
			"credential_id": map[string]interface{}{"type": "string", "description": "可选,已入池凭证 ID(miniapp_key_list 取得);与 app_id/secret 二选一"},
			"app_id":        map[string]interface{}{"type": "string", "description": "小程序 AppID(明文凭据时必填)"},
			"secret":        map[string]interface{}{"type": "string", "description": "AppSecret(明文凭据时必填)"},
			"js_code":       map[string]interface{}{"type": "string", "description": "wx.login 返回的一次性登录凭据(抓包或用户侧获取)"},
			"proxy":         map[string]interface{}{"type": "string", "enum": []string{"proxy", "direct"}, "description": "可选,出口方式:proxy=平台出口代理(默认)/direct=直连;留空按池中保存值"},
		}, "required": []string{"js_code"}},
	}, func(ctx context.Context, args map[string]interface{}) (*mcp.ToolResult, error) {
		appID := strings.TrimSpace(strArg(args, "app_id"))
		secret := strings.TrimSpace(strArg(args, "secret"))
		proxyMode := strings.TrimSpace(strArg(args, "proxy"))
		if credID := strings.TrimSpace(strArg(args, "credential_id")); credID != "" {
			cred, cerr := svc.GetCredential(ctx, credID)
			if cerr != nil {
				return textResult("错误: 读取凭证池失败: "+cerr.Error(), true), nil
			}
			appID, secret = cred.AppID, cred.Secret
			if proxyMode == "" {
				proxyMode = cred.ProxyMode
			}
		}
		if appID == "" || secret == "" {
			return textResult("错误: app_id 与 secret 必填(或传 credential_id 复用已入池凭证)", true), nil
		}
		jsCode := strings.TrimSpace(strArg(args, "js_code"))
		if jsCode == "" {
			return textResult("错误: js_code 必填(小程序端 wx.login 返回,一次性;可从抓包流量中提取)", true), nil
		}
		result := miniprogram.JsCode2Session(ctx, netproxy.VerificationClient(proxyMode, 20*time.Second), appID, secret, jsCode)
		verdict := credentialVerdictText(result.Valid, "", result.Message)
		if result.Status == miniprogram.JsStatusCodeInvalid {
			verdict = "凭据已通过微信校验(旁路确认有效);本次 js_code 无效/已使用,换新 code 重试即可换取登录态"
		} else if result.Valid {
			verdict = "登录态换取成功: openid=" + result.OpenID
		}
		return assetJSONResult(map[string]interface{}{
			"result": result,
			"结论":     verdict,
			"建议":     "session_key 传给 miniprogram_decrypt_userdata 解密 encryptedData(手机号/unionId);报告中「接口可用性」与「是否伪造他人身份」分开表述,session_key/openid 不得整串粘贴进报告",
		})
	})

	// ── miniprogram_decrypt_userdata:encryptedData 解密(AES-128-CBC,本地计算)──
	server.RegisterTool(mcp.Tool{
		Name:             builtin.ToolMiniprogramDecryptData,
		ShortDescription: "解密小程序 sensitiveData:encryptedData+iv+session_key → 手机号/unionId 明文",
		Description:      "AES-128-CBC 解密微信小程序 sensitiveData(wx.getUserInfo/getPhoneNumber 的 encryptedData 字段):session_key/encrypted_data/iv 均为标准 base64,明文为 JSON(含 watermark.appid 归属水印)。app_id 传入时校验水印一致性——不一致说明 session_key 与数据跨小程序混用。纯本地计算,不出网、无副作用。边界:仅限授权测试范围内使用解密结果,报告不整串粘贴明文敏感字段。",
		InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
			"session_key":    map[string]interface{}{"type": "string", "description": "jscode2session 返回的 session_key(base64)"},
			"encrypted_data": map[string]interface{}{"type": "string", "description": "小程序端返回的 encryptedData(base64)"},
			"iv":             map[string]interface{}{"type": "string", "description": "加密向量 iv(base64)"},
			"app_id":         map[string]interface{}{"type": "string", "description": "可选,校验明文水印归属;不一致时报错"},
		}, "required": []string{"session_key", "encrypted_data", "iv"}},
	}, func(ctx context.Context, args map[string]interface{}) (*mcp.ToolResult, error) {
		res, err := miniprogram.DecryptUserData(
			strArg(args, "session_key"), strArg(args, "encrypted_data"), strArg(args, "iv"),
			strings.TrimSpace(strArg(args, "app_id")))
		if err != nil {
			return textResult("错误: "+err.Error(), true), nil
		}
		return assetJSONResult(map[string]interface{}{
			"watermark_appid": res.WatermarkAppID,
			"data":            res.Data,
			"建议":              "水印 appid 与目标小程序一致才可作为证据;手机号/unionId 属个人敏感信息,报告只写形态(如 138****0000)不落明文",
		})
	})

	// ── miniprogram_pc_fetch:PC 微信目录直取(免 root 取包)──
	server.RegisterTool(mcp.Tool{
		Name:             builtin.ToolMiniprogramPCFetch,
		ShortDescription: "PC 微信缓存目录直取 wxapkg 进小程序分析(WSL/Windows 免 root)",
		Description:      "扫描 PC 微信的小程序包缓存(微信 4.x radium:AppData/Roaming/Tencent/xwechat/radium/users/*/applet/packages;微信 3.x:Documents/WeChat Files/Applet),action=scan 只列出(appid+包+是否加密);action=harvest 把目标 appid 的包注册进「小程序分析」——AppID 取自目录名,加密包自动解密,后续解包/敏感扫描/测试项账本全部可用。前提:PC 微信里打开过目标小程序。扫描根默认枚举 /mnt/<盘>/Users/*(WSL),可用 root 参数或 MINIPROGRAM_PC_SCAN_ROOTS 环境变量覆盖。",
		InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
			"action": map[string]interface{}{"type": "string", "enum": []string{"scan", "harvest"}, "description": "scan=只列出 / harvest=拉取注册"},
			"appid":  map[string]interface{}{"type": "string", "description": "可选,只处理该 appid(harvest 推荐指定;省略则拉取全部,有数量上限)"},
			"root":   map[string]interface{}{"type": "string", "description": "可选,扫描根目录(默认 /mnt/<盘>/Users/* + MINIPROGRAM_PC_SCAN_ROOTS)"},
		}, "required": []string{"action"}},
	}, func(ctx context.Context, args map[string]interface{}) (*mcp.ToolResult, error) {
		roots := []string{}
		if root := strings.TrimSpace(strArg(args, "root")); root != "" {
			roots = append(roots, root)
		} else {
			roots = miniprogram.PCDefaultScanRoots()
		}
		groups := miniprogram.ScanPCPackages(roots)
		if wantAppID := strings.TrimSpace(strArg(args, "appid")); wantAppID != "" {
			filtered := groups[:0:0]
			for _, g := range groups {
				if strings.EqualFold(g.AppID, wantAppID) {
					filtered = append(filtered, g)
				}
			}
			groups = filtered
		}
		if strings.TrimSpace(strArg(args, "action")) != "harvest" {
			return assetJSONResult(map[string]interface{}{
				"apps": groups, "total": len(groups),
				"建议": "确认目标 appid 后调 harvest 拉取注册;缓存为空时先在 PC 微信里打开目标小程序(加密包解密用目录名 appid,自动完成)",
			})
		}
		if len(groups) == 0 {
			return textResult("没有可拉取的包:先在 PC 微信里打开目标小程序,或检查 root/缓存路径(Windows 宿主走 /mnt/<盘>/Users/<用户>/...)", false), nil
		}
		toolCtx, unboundErr := newMiniprogramToolContext(svc, db, ctx)
		if unboundErr != "" {
			return textResult(unboundErr, true), nil
		}
		projectID := toolCtx.projectID // 会话绑定项目时样本归该项目
		tmpDir, err := os.MkdirTemp("", "wxapkg_pc_*")
		if err != nil {
			return textResult("错误: "+err.Error(), true), nil
		}
		defer func() { _ = os.RemoveAll(tmpDir) }()
		samples := make([]map[string]interface{}, 0, len(groups))
		pulled := 0
		for _, g := range groups {
			parts := make([]miniprogram.UploadPart, 0, len(g.Files))
			for _, f := range g.Files {
				if pulled >= miniprogram.PCFetchMaxFiles {
					break
				}
				data, rerr := os.ReadFile(f.Path)
				if rerr != nil {
					continue
				}
				local := filepath.Join(tmpDir, fmt.Sprintf("%s_%d.wxapkg", g.AppID, len(parts)))
				if werr := os.WriteFile(local, data, 0o600); werr != nil {
					continue
				}
				parts = append(parts, miniprogram.UploadPart{
					Path: local,
					Name: fmt.Sprintf("%s_%s", filepath.Base(filepath.Dir(f.Path)), f.Name),
				})
				pulled++
			}
			if len(parts) == 0 {
				continue
			}
			sample, rerr := svc.RegisterFromFiles(parts, projectID, "", "pc_harvest", g.AppID)
			if rerr != nil {
				samples = append(samples, map[string]interface{}{"appid": g.AppID, "error": rerr.Error()})
				continue
			}
			samples = append(samples, map[string]interface{}{"id": sample.ID, "name": sample.Name, "appid": sample.AppID, "file_count": sample.FileCount})
		}
		return assetJSONResult(map[string]interface{}{
			"pulled": pulled, "samples": samples,
			"建议": "样本已在「小程序分析」页并完成解包+敏感扫描;用 miniprogram_test_items 拉测试项账本,继续源码审计与登录态链(miniprogram_jscode2session)",
		})
	})

	// ── miniprogram_structure:包结构报告(路由/分包/插件/worker/WASM/web-view)──
	server.RegisterTool(mcp.Tool{
		Name:             builtin.ToolMiniprogramStructure,
		ShortDescription: "小程序包结构报告:路由/分包/插件/Worker/WASM/开放数据域/web-view 域名",
		Description:      "读取已归档样本的声明文件与产物形态,产出结构报告:app.json 路由与分包、插件声明(含分包内 plugins)、Worker 脚本目录、WASM 产物、开放数据域声明、后台能力声明、web-view 静态落地域名与动态绑定页面(内嵌 H5 → Web 角色交接素材)、文本外资产统计。附覆盖边界提示(插件源码不在样本内/WASM 检索不到等)。用于审计开局建立攻击面地图,配合 miniprogram_source/miniprogram_findrefs 深挖。",
		InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
			"sample": map[string]interface{}{"type": "string", "description": "样本 id 或文件名"},
		}, "required": []string{"sample"}},
	}, func(ctx context.Context, args map[string]interface{}) (*mcp.ToolResult, error) {
		toolCtx, unboundErr := newMiniprogramToolContext(svc, db, ctx)
		if unboundErr != "" {
			return textResult(unboundErr, true), nil
		}
		sample, serr := toolCtx.resolveSample(strings.TrimSpace(strArg(args, "sample")))
		if serr != nil {
			return textResult("错误: "+serr.Error(), true), nil
		}
		report := miniprogram.AnalyzeStructure(sample.Workspace)
		return assetJSONResult(map[string]interface{}{
			"sample": sample.Name,
			"report": report,
			"建议":     "插件/WASM 在覆盖边界提示里看到就单独评估;webview_hosts 是内嵌 H5 交接 Web 角色的素材;workers 目录与 wasm 之外的 JS 都可用 miniprogram_findrefs 检索",
		})
	})

	// ── miniprogram_cloudbase:云开发资产盘点 + 测试项生成 ──
	server.RegisterTool(mcp.Tool{
		Name:             builtin.ToolMiniprogramCloudBase,
		ShortDescription: "云开发(CloudBase)资产盘点:env/云函数/集合/云存储 fileID + 逐资产测试项",
		Description:      "扫描样本内云开发调用痕迹(wx.cloud.init 的 env、callFunction 函数名、.collection 集合名、cloud:// fileID、匿名登录痕迹),返回资产清单;每个云函数/集合自动生成测试项(未授权直调/集合权限矩阵)进测试项账本,与扫描发现同一账本闭环。实际调用验证走 http_probe(HTTP 触发路径)与 cloud_key_verify(云凭据);小程序上下文外不可达的函数在账本结论标 na。",
		InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
			"sample": map[string]interface{}{"type": "string", "description": "样本 id 或文件名"},
		}, "required": []string{"sample"}},
	}, func(ctx context.Context, args map[string]interface{}) (*mcp.ToolResult, error) {
		toolCtx, unboundErr := newMiniprogramToolContext(svc, db, ctx)
		if unboundErr != "" {
			return textResult(unboundErr, true), nil
		}
		sample, serr := toolCtx.resolveSample(strings.TrimSpace(strArg(args, "sample")))
		if serr != nil {
			return textResult("错误: "+serr.Error(), true), nil
		}
		added, inv, err := svc.EnsureCloudBaseTestItems(sample.ID)
		if err != nil {
			return textResult("错误: "+err.Error(), true), nil
		}
		hint := "样本内没有云开发调用痕迹;若业务侧确认用了云开发,检查分包/插件包是否未合并"
		if added > 0 {
			hint = "已生成 " + fmt.Sprint(added) + " 个云开发测试项:用 miniprogram_test_items 拉取(status=pending)逐项验证;云函数先看传参是否带身份,集合用 A/B 双身份验权限矩阵"
		}
		return assetJSONResult(map[string]interface{}{
			"sample":           sample.Name,
			"inventory":        inv,
			"functions":        len(inv.Functions),
			"collections":      len(inv.Collections),
			"test_items_added": added,
			"建议":               hint,
		})
	})

	// ── miniprogram_devtools:DevTools 协议通道(目标枚举/求值/hook)──
	server.RegisterTool(mcp.Tool{
		Name:             builtin.ToolMiniprogramDevTools,
		ShortDescription: "DevTools 协议通道:连开发者工具/PC 微信调试端口,运行时 hook wx.request/改 JS/绕前端校验",
		Description:      "走标准 CDP 连已开启服务端口的微信开发者工具或 PC 微信小程序模式调试端点(http://127.0.0.1:<port>,默认 9420;WSL 连 Windows 宿主用宿主 IP)。action=targets 枚举可调试目标;action=hook 安装预设(wx_request=拦截 wx.request 录制请求入参出参/storage=全量导出本地缓存/page_stack=页面栈快照);action=read 取 wx_request 录制数据(clear=true 清空续录);action=eval 执行任意 JS(Runtime.evaluate,支持 await)——前端校验绕过、签名入参观察、限购逻辑篡改都在运行时做。仅限已授权测试场景。",
		InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
			"action":      map[string]interface{}{"type": "string", "enum": []string{"targets", "hook", "read", "eval"}, "description": "targets=枚举目标 / hook=装预设 / read=取录制 / eval=执行 JS"},
			"base_url":    map[string]interface{}{"type": "string", "description": "调试端点,默认 http://127.0.0.1:9420;WSL 连 Windows 宿主用宿主 IP"},
			"target":      map[string]interface{}{"type": "string", "description": "hook/read/eval 时必填:目标 id/title/url 子串(多命中会报错列出候选)"},
			"preset":      map[string]interface{}{"type": "string", "enum": []string{"wx_request", "storage", "page_stack"}, "description": "hook 时必填:预设名"},
			"expression":  map[string]interface{}{"type": "string", "description": "eval 时必填:要执行的 JS 表达式(支持 await)"},
			"clear":       map[string]interface{}{"type": "boolean", "description": "read 时可选,取完清空录制缓冲"},
			"timeout_sec": map[string]interface{}{"type": "integer", "description": "可选,求值超时秒数,默认 10,上限 120"},
		}, "required": []string{"action"}},
	}, func(ctx context.Context, args map[string]interface{}) (*mcp.ToolResult, error) {
		baseURL := strings.TrimSpace(strArg(args, "base_url"))
		if baseURL == "" {
			baseURL = "http://127.0.0.1:9420"
		}
		client := &http.Client{Timeout: 15 * time.Second}
		targets, err := miniprogram.ListDevToolsTargets(ctx, client, baseURL)
		action := strings.TrimSpace(strArg(args, "action"))
		if action != "targets" {
			if err != nil {
				return textResult("错误: "+err.Error(), true), nil
			}
			wsURL, rerr := miniprogram.ResolveTargetURL(targets, strArg(args, "target"))
			if rerr != nil {
				return textResult("错误: "+rerr.Error(), true), nil
			}
			timeoutSec := intArg(args, "timeout_sec", 10)
			switch action {
			case "hook":
				preset, ok := miniprogram.HookPresetByName(strArg(args, "preset"))
				if !ok {
					return textResult("错误: preset 必填(wx_request/storage/page_stack)", true), nil
				}
				got, eerr := miniprogram.EvaluateOnTarget(ctx, wsURL, preset.Install, timeoutSec)
				if eerr != nil {
					return textResult("错误: 安装失败: "+eerr.Error(), true), nil
				}
				return assetJSONResult(map[string]interface{}{
					"preset": preset.Name, "install_result": got, "description": preset.Description,
					"建议": "wx_request 装好后回到小程序里操作触发请求,再 action=read 取录制;storage/page_stack 的安装表达式直接返回数据",
				})
			case "read":
				got, eerr := miniprogram.EvaluateOnTarget(ctx, wsURL, miniprogram.DevToolsReadWxRequestLog(boolArg(args, "clear")), timeoutSec)
				if eerr != nil {
					return textResult("错误: 读取失败: "+eerr.Error(), true), nil
				}
				return assetJSONResult(map[string]interface{}{"requests_json": got,
					"建议": "请求清单结合 miniprogram_findrefs 定位调用点;对高价值接口用 http_session 保登录态重放做越权验证"})
			case "eval":
				expression := strings.TrimSpace(strArg(args, "expression"))
				if expression == "" {
					return textResult("错误: eval 需要 expression(要执行的 JS)", true), nil
				}
				got, eerr := miniprogram.EvaluateOnTarget(ctx, wsURL, expression, timeoutSec)
				if eerr != nil {
					return textResult("错误: "+eerr.Error(), true), nil
				}
				return assetJSONResult(map[string]interface{}{"result": got,
					"建议": "运行时观察到的签名/加密入参配合 node 复现算法;绕过的前端校验在报告中标注触发方式与影响面"})
			default:
				return textResult("错误: action 必须是 targets/hook/read/eval", true), nil
			}
		}
		if err != nil {
			return textResult("错误: "+err.Error()+"(确认开发者工具已开启「设置→安全→服务端口」;WSL 连 Windows 宿主需用宿主 IP)", true), nil
		}
		return assetJSONResult(map[string]interface{}{
			"base_url": baseURL, "targets": targets, "total": len(targets),
			"建议": "service/content 上下文是小程序逻辑所在;确认目标后 action=hook 装 wx_request 预设,或 action=eval 直接求值",
		})
	})

	log.Info("miniprogram analysis tools registered",
		zap.String("tools", strings.Join([]string{builtin.ToolMiniprogramAnalyze, builtin.ToolMiniprogramSource, builtin.ToolMiniprogramSamples, builtin.ToolMiniprogramFindrefs, builtin.ToolMiniprogramTestItems, builtin.ToolMiniprogramTestItemUpdate, builtin.ToolMiniprogramJsCode2Session, builtin.ToolMiniprogramDecryptData, builtin.ToolMiniprogramPCFetch, builtin.ToolMiniprogramStructure, builtin.ToolMiniprogramCloudBase, builtin.ToolMiniprogramDevTools}, ",")))
}

// ────────────────────────── 工具上下文 ──────────────────────────

type miniprogramToolContext struct {
	svc       *miniprogram.Service
	db        *database.DB
	projectID string
	scoped    bool
}

func newMiniprogramToolContext(svc *miniprogram.Service, db *database.DB, ctx context.Context) (*miniprogramToolContext, string) {
	projectID, scoped, serr := miniprogramConversationProjectScope(db, ctx)
	if serr != nil {
		return nil, "错误: " + serr.Error()
	}
	return &miniprogramToolContext{svc: svc, db: db, projectID: projectID, scoped: scoped}, ""
}

func (r *miniprogramToolContext) resolveSample(idOrName string) (*miniprogram.Sample, error) {
	sample, err := r.svc.GetSample(idOrName)
	if err != nil {
		return nil, fmt.Errorf("样本不存在: %w", err)
	}
	if r.scoped && sample.ProjectID != "" && sample.ProjectID != r.projectID {
		return nil, fmt.Errorf("样本「%s」不属于当前会话绑定的项目", sample.Name)
	}
	return sample, nil
}

func miniprogramConversationProjectScope(db *database.DB, ctx context.Context) (string, bool, error) {
	conversationID := conversationIDFromToolCtx(ctx)
	if conversationID == "" {
		return "", false, nil
	}
	projectID, err := db.GetConversationProjectID(conversationID)
	if err != nil {
		return "", false, fmt.Errorf("无法确定当前对话的项目范围")
	}
	projectID = strings.TrimSpace(projectID)
	return projectID, projectID != "", nil
}

// isSafeUploadPath 校验上传路径安全性(只允许 chat_uploads 目录下的绝对路径,
// 与 apkanalysis.IsSafeUploadPath 同语义:相对路径与目录逃逸一律拒绝)。
// root 为 chat_uploads 根目录(来自 db.ChatUploadsRoot);空根一律拒绝。
func isSafeUploadPath(root, p string) bool {
	if strings.TrimSpace(root) == "" {
		return false
	}
	if !filepath.IsAbs(p) {
		return false
	}
	clean := filepath.Clean(p)
	rel, err := filepath.Rel(root, clean)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// filepathBase 取路径最后一段(工具参数是 server_path)
func filepathBase(p string) string {
	return filepath.Base(strings.TrimSpace(p))
}

// credentialVerdictText 凭证判定文案(与 cloudkeys/llmkeys 同款)。
func credentialVerdictText(valid bool, code, message string) string {
	if valid {
		return "凭证有效"
	}
	if code != "" {
		return "凭证无效(" + code + ")"
	}
	if message != "" {
		return "无法判定:" + message
	}
	return "凭证无效/不可用"
}
