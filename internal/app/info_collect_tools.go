package app

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"finduai/internal/database"
	"finduai/internal/handler"
	"finduai/internal/mcp"
	"finduai/internal/mcp/builtin"

	"go.uber.org/zap"
)

// 信息收集（空间测绘）Agent 工具注册。
// 直接复用 FofaHandler 的非 HTTP 查询入口 SearchProviderCtx / SearchEngineAssets，
// 与 Web 端（/api/fofa/*）共用同一套 provider 逻辑与源状态台账回调。

func registerInfoCollectTools(server *mcp.Server, fh *handler.FofaHandler, logger *zap.Logger) {
	if server == nil || fh == nil {
		return
	}

	server.RegisterTool(mcp.Tool{
		Name: builtin.ToolSpaceSearch, ShortDescription: "空间测绘引擎查询（FOFA/ZoomEye/Quake/Shodan/Hunter）",
		Description: "在指定空间测绘搜索引擎中执行查询并返回命中资产。引擎可选 fofa、zoomeye、quake、shodan、hunter。" +
			"query 使用该引擎的原生检索语法（字段=值，多个条件用 && / || / ! 连接，必要时加括号），例如 fofa: domain=\"example.com\" && port=\"443\"。" +
			"size 默认 100，最大 10000；page 从 1 开始。返回各引擎字段不同的原始行，并给出可展示的资产摘要。",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"provider": map[string]interface{}{"type": "string", "description": "空间测绘引擎：fofa、zoomeye、quake、shodan、hunter"},
				"query":    map[string]interface{}{"type": "string", "description": "引擎原生检索语法查询语句，不能为空"},
				"size":     map[string]interface{}{"type": "integer", "description": "返回条数，默认 100，最大 10000"},
				"page":     map[string]interface{}{"type": "integer", "description": "页码，从 1 开始，默认 1"},
				"fields":   map[string]interface{}{"type": "string", "description": "逗号分隔的字段列表，默认按引擎给定"},
			},
			"required": []string{"provider", "query"},
		},
	}, func(ctx context.Context, args map[string]interface{}) (*mcp.ToolResult, error) {
		provider := strings.ToLower(strings.TrimSpace(strArg(args, "provider")))
		query := strings.TrimSpace(strArg(args, "query"))
		size := intArg(args, "size", 100)
		page := intArg(args, "page", 1)
		fields := strings.TrimSpace(strArg(args, "fields"))
		if query == "" {
			return textResult("错误: query 不能为空", true), nil
		}
		assets, rows, total, err := fh.SearchEngineAssets(ctx, provider, query, size, page)
		if err != nil {
			logger.Warn("Agent 空间测绘查询失败", zap.String("provider", provider), zap.Error(err))
			return textResult("错误: "+err.Error(), true), nil
		}
		if fields == "" {
			fields = inferFieldsFromRows(rows)
		}
		var b strings.Builder
		b.WriteString(fmt.Sprintf("%s 空间测绘查询：query=%q，命中 %d 条，本次返回 %d 条\n", displaySearchSource(provider), query, total, len(rows)))
		for _, a := range assets {
			b.WriteString(formatSearchAsset(a))
			b.WriteByte('\n')
		}
		if len(rows) == 0 {
			b.WriteString("无命中结果。请检查查询语法或更换关键词后重试。")
		} else {
			b.WriteString(fmt.Sprintf("提示：如需入库供后续扫描，可对每条资产的 host/ip/domain 调用 create_asset。"))
		}
		return textResult(b.String(), false), nil
	})

	server.RegisterTool(mcp.Tool{
		Name: builtin.ToolListSearchSources, ShortDescription: "查看空间测绘引擎配置状态",
		Description: "返回各空间测绘引擎（fofa、zoomeye、quake、shodan、hunter）是否已配置 API Key。未配置的引擎无法查询，需在系统设置→资产管理或环境变量中配置。",
		InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
	}, func(ctx context.Context, args map[string]interface{}) (*mcp.ToolResult, error) {
		var b strings.Builder
		b.WriteString("空间测绘引擎配置状态：\n")
		for _, p := range []string{"fofa", "zoomeye", "quake", "shodan", "hunter"} {
			status := "已配置"
			if !fh.SourceConfigured(p) {
				status = "未配置"
			}
			b.WriteString(fmt.Sprintf("- %s：%s\n", displaySearchSource(p), status))
		}
		return textResult(b.String(), false), nil
	})

	server.RegisterTool(mcp.Tool{
		Name: builtin.ToolSpaceSearchParse, ShortDescription: "自然语言解析为空间测绘查询语法",
		Description: "把用户的自然语言检索意图转换为指定空间测绘引擎的原生查询语法（FOFA/ZoomEye/Quake/Shodan/Hunter），" +
			"例如「找中国的 Apache 站点，标题包含登录」→ fofa 的 app=\"Apache\" && country=\"CN\" && title=\"登录\"。" +
			"常用于把口语化需求翻译成引擎语法后再调用 space_search 执行查询。" +
			"依赖 AI 模型通道，未配置 AI 时返回明确错误。",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"provider": map[string]interface{}{"type": "string", "description": "空间测绘引擎：fofa、zoomeye、quake、shodan、hunter"},
				"text":     map[string]interface{}{"type": "string", "description": "自然语言检索意图，不能为空"},
			},
			"required": []string{"provider", "text"},
		},
	}, func(ctx context.Context, args map[string]interface{}) (*mcp.ToolResult, error) {
		provider := strings.ToLower(strings.TrimSpace(strArg(args, "provider")))
		text := strings.TrimSpace(strArg(args, "text"))
		if text == "" {
			return textResult("错误: text 不能为空", true), nil
		}
		parsed, err := fh.ParseNaturalLanguageCtx(ctx, provider, text)
		if err != nil {
			logger.Warn("Agent 自然语言解析失败", zap.String("provider", provider), zap.Error(err))
			return textResult("错误: "+err.Error(), true), nil
		}
		var b strings.Builder
		b.WriteString(fmt.Sprintf("已将自然语言解析为 %s 查询语法：\n", displaySearchSource(provider)))
		if parsed.Query != "" {
			b.WriteString(fmt.Sprintf("query: %s\n", parsed.Query))
		}
		if parsed.Explanation != "" {
			b.WriteString(fmt.Sprintf("说明: %s\n", parsed.Explanation))
		}
		for _, w := range parsed.Warnings {
			b.WriteString(fmt.Sprintf("警告: %s\n", w))
		}
		if parsed.Query == "" {
			b.WriteString("未生成查询语句。请补充更明确的目标条件（如国家/端口/产品/域名/标题等）后重试。")
		} else {
			b.WriteString("提示：可直接将上方 query 作为 space_search 的 query 参数执行查询。")
		}
		return textResult(b.String(), false), nil
	})
}

func displaySearchSource(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "fofa":
		return "FOFA"
	case "zoomeye":
		return "ZoomEye"
	case "quake":
		return "Quake"
	case "shodan":
		return "Shodan"
	case "hunter":
		return "Hunter"
	default:
		return provider
	}
}

// inferFieldsFromRows 从未命名的原始行推断字段顺序（FofaHandler 已投影后通常是 map）。
func inferFieldsFromRows(rows []map[string]interface{}) string {
	if len(rows) == 0 {
		return ""
	}
	seen := map[string]bool{}
	for _, row := range rows {
		for k := range row {
			seen[k] = true
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

func formatSearchAsset(a *database.Asset) string {
	if a == nil {
		return ""
	}
	target := a.Domain
	if target == "" {
		target = a.IP
	}
	if target == "" {
		target = a.Host
	}
	if a.Port > 0 {
		target = fmt.Sprintf("%s:%d", target, a.Port)
	}
	parts := []string{target}
	if a.Title != "" {
		parts = append(parts, a.Title)
	}
	if a.Protocol != "" {
		parts = append(parts, a.Protocol)
	}
	if a.Country != "" || a.City != "" {
		parts = append(parts, strings.TrimSpace(a.Country+" "+a.City))
	}
	return "- " + strings.Join(parts, " | ")
}