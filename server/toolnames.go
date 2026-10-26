package server

import (
	"strings"

	"finduai/db"
)

// builtinToolCN 内置平台工具 key → 中文名。findu 迁移工具的原 description 多为占位
// "findu 内置工具（统一目录迁移）"，无实义，故用本表给出可读中文名。
var builtinToolCN = map[string]string{
	"add_company_scope":            "添加公司范围",
	"add_hint":                     "添加战略提示",
	"add_intent":                   "添加探索意图",
	"add_task_hint":                "添加任务提示",
	"add_task_scope":               "添加任务范围",
	"analyze_image":                "图像分析",
	"android_analyze":              "安卓样本分析",
	"android_extract_apis":         "提取安卓 API",
	"android_findrefs":             "安卓交叉引用检索",
	"android_hardening":            "安卓加固识别",
	"android_manifest":             "安卓清单解析",
	"android_samples":              "安卓样本列表",
	"android_source":               "安卓反编译源码",
	"android_test_item_update":     "更新安卓测试项",
	"android_test_items":           "安卓测试项列表",
	"batch_task_add_task":          "批量任务添加",
	"batch_task_create":            "创建批量任务",
	"batch_task_delete":            "删除批量任务",
	"batch_task_get":               "查询批量任务",
	"batch_task_list":              "批量任务列表",
	"batch_task_pause":             "暂停批量任务",
	"batch_task_remove_task":       "批量任务移除",
	"batch_task_rerun":             "重跑批量任务",
	"batch_task_schedule_enabled":  "批量任务调度开关",
	"batch_task_start":             "启动批量任务",
	"batch_task_update_metadata":   "更新批量任务元数据",
	"batch_task_update_schedule":   "更新批量任务调度",
	"batch_task_update_task":       "更新批量任务",
	"bind_finding_traffic":         "绑定漏洞流量",
	"cancel_tool_execution":        "取消工具执行",
	"complete_asset_scan":          "完成资产扫描",
	"create_asset":                 "创建资产",
	"create_custom_tool":           "创建自定义工具",
	"create_mcp":                   "创建 MCP",
	"create_skill":                 "创建技能",
	"delete_asset":                 "删除资产",
	"delete_assets_by_host":        "按主机删除资产",
	"deprecate_project_fact":       "废弃项目事实",
	"expand_digest":                "展开摘要",
	"get_asset":                    "查询资产",
	"get_finding_retest_context":   "读取复测上下文",
	"get_finding_traffic":          "读取漏洞流量",
	"get_project_fact":             "查询项目事实",
	"get_task_graph":               "读取任务探索图",
	"get_task_node_detail":         "读取任务节点详情",
	"get_task_worker_trace":        "读取任务执行追踪",
	"get_tool_execution":           "查询工具执行",
	"get_vulnerability":            "查询漏洞",
	"get_worker_output":            "读取执行结论",
	"get_worker_trace":             "读取执行追踪",
	"goal_met":                     "完成目标",
	"graph_overview":               "探索图概览",
	"insert_assets":                "批量插入资产",
	"kill_work":                    "终止执行",
	"list_assets":                  "资产列表",
	"list_companies":               "公司列表",
	"list_facts":                   "事实列表",
	"list_findings":                "漏洞列表",
	"list_goals":                   "目标列表",
	"list_knowledge_risk_types":    "知识库风险类型",
	"list_llm_profiles":            "模型配置列表",
	"list_project_facts":           "项目事实列表",
	"list_task_findings":           "任务漏洞列表",
	"list_task_worker_traces":      "任务执行追踪列表",
	"list_tasks":                   "任务列表",
	"list_untested_assets":         "未测资产列表",
	"list_vulnerabilities":         "漏洞列表",
	"manage_webshell_add":          "添加 WebShell",
	"manage_webshell_delete":       "删除 WebShell",
	"manage_webshell_list":         "WebShell 列表",
	"manage_webshell_test":         "测试 WebShell",
	"manage_webshell_update":       "更新 WebShell",
	"miniprogram_analyze":          "小程序分析",
	"miniprogram_cloudbase":        "小程序云开发",
	"miniprogram_decrypt_userdata": "解密小程序用户数据",
	"miniprogram_devtools":         "小程序调试端点",
	"miniprogram_findrefs":         "小程序交叉引用",
	"miniprogram_jscode2session":   "小程序登录凭证",
	"miniprogram_pc_fetch":         "小程序 PC 抓取",
	"miniprogram_samples":          "小程序样本列表",
	"miniprogram_source":           "小程序源码",
	"miniprogram_structure":        "小程序结构",
	"miniprogram_test_item_update": "更新小程序测试项",
	"miniprogram_test_items":       "小程序测试项列表",
	"node_detail":                  "节点详情",
	"pause_task":                   "暂停任务",
	"prove_goal":                   "证明目标",
	"query_assets":                 "查询资产",
	"record_fact":                  "记录事实",
	"record_finding_retest_result": "记录复测结果",
	"record_vulnerability":         "登记漏洞",
	"report_finding":               "上报漏洞",
	"restore_project_fact":         "恢复项目事实",
	"search_all_worker_traces":     "搜索全部执行追踪",
	"search_knowledge_base":        "检索知识库",
	"search_project_facts":         "搜索项目事实",
	"search_task_worker_traces":    "搜索任务执行追踪",
	"search_vuln_kb":               "检索漏洞库",
	"set_constraints":              "设置约束",
	"set_goals":                    "设置目标",
	"spawn_task":                   "派发任务",
	"steer_work":                   "引导执行",
	"suggest_scope":                "建议范围",
	"traffic_blob":                 "流量数据块",
	"traffic_get":                  "读取流量",
	"traffic_search":               "检索流量",
	"update_asset":                 "更新资产",
	"update_custom_tool":           "更新自定义工具",
	"update_finding_report":        "更新漏洞报告",
	"update_mcp":                   "更新 MCP",
	"update_skill_file":            "更新技能文件",
	"upsert_project_fact":          "新建/更新项目事实",
	"wait_tool_execution":          "等待工具执行",
	"webshell_exec":                "WebShell 执行",
	"webshell_file_list":           "WebShell 文件列表",
	"webshell_file_read":           "WebShell 读取文件",
	"webshell_file_write":          "WebShell 写文件",
}

// toolDisplayName 给工具算一个可读中文名：
//   - recipe(CLI) 工具：description 本身就是简短中文名，直接用首句；
//   - 内置工具：优先取 builtinToolCN 映射，未命中再回落到 description 首句。
func toolDisplayName(t *db.Tool) string {
	if t == nil {
		return ""
	}
	if t.Runtime == "recipe" {
		if s := firstSentence(t.Description); s != "" {
			return s
		}
		return t.Key
	}
	if n, ok := builtinToolCN[t.Key]; ok {
		return n
	}
	return firstSentence(t.Description)
}

// firstSentence 取描述首句（到句号/分号/破折号/换行），作为紧凑中文名兜底。
func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	for _, sep := range []string{"。", "；", "——", "\n", "——", "—"} {
		if i := strings.Index(s, sep); i > 0 {
			return s[:i]
		}
	}
	if len(s) > 24 {
		if i := strings.IndexAny(s, "，,;"); i > 0 {
			return s[:i]
		}
	}
	return s
}
