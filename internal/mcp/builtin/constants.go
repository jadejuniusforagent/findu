package builtin

// 内置工具名称常量
// 所有代码中使用内置工具名称的地方都应该使用这些常量，而不是硬编码字符串
const (
	// 漏洞管理工具
	ToolRecordVulnerability = "record_vulnerability"
	ToolListVulnerabilities = "list_vulnerabilities"
	ToolGetVulnerability    = "get_vulnerability"

	// 资产管理工具
	ToolCreateAsset       = "create_asset"
	ToolGetAsset          = "get_asset"
	ToolQueryAssets       = "query_assets"
	ToolUpdateAsset       = "update_asset"
	ToolDeleteAsset       = "delete_asset"
	ToolCompleteAssetScan = "complete_asset_scan"

	// 信息收集（空间测绘）工具
	ToolSpaceSearch       = "space_search"
	ToolSpaceSearchParse  = "space_search_parse"
	ToolListSearchSources = "list_search_sources"

	// 项目黑板（事实）工具
	ToolUpsertProjectFact    = "upsert_project_fact"
	ToolGetProjectFact       = "get_project_fact"
	ToolListProjectFacts     = "list_project_facts"
	ToolSearchProjectFacts   = "search_project_facts"
	ToolDeprecateProjectFact = "deprecate_project_fact"
	ToolRestoreProjectFact   = "restore_project_fact"

	// 技能自扩展工具（P2）：agent 直接沉淀/修订技能包
	ToolCreateSkill     = "create_skill"
	ToolUpdateSkillFile = "update_skill_file"

	// 知识库工具
	ToolListKnowledgeRiskTypes = "list_knowledge_risk_types"
	ToolSearchKnowledgeBase    = "search_knowledge_base"

	// 视觉分析（本地图片 → VL 模型 → 文本摘要）
	ToolAnalyzeImage = "analyze_image"

	// 长耗时工具执行控制（后台 execution 查询/等待/取消）
	ToolGetToolExecution    = "get_tool_execution"
	ToolWaitToolExecution   = "wait_tool_execution"
	ToolCancelToolExecution = "cancel_tool_execution"

	// WebShell 助手工具（AI 在 WebShell 管理 - AI 助手 中使用）
	ToolWebshellExec      = "webshell_exec"
	ToolWebshellFileList  = "webshell_file_list"
	ToolWebshellFileRead  = "webshell_file_read"
	ToolWebshellFileWrite = "webshell_file_write"

	// WebShell 连接管理工具（用于通过 MCP 管理 webshell 连接）
	ToolManageWebshellList   = "manage_webshell_list"
	ToolManageWebshellAdd    = "manage_webshell_add"
	ToolManageWebshellUpdate = "manage_webshell_update"
	ToolManageWebshellDelete = "manage_webshell_delete"
	ToolManageWebshellTest   = "manage_webshell_test"

	// 批量任务队列（与 Web 端批量任务一致，供模型创建/启停/查询队列）
	ToolBatchTaskList            = "batch_task_list"
	ToolBatchTaskGet             = "batch_task_get"
	ToolBatchTaskCreate          = "batch_task_create"
	ToolBatchTaskStart           = "batch_task_start"
	ToolBatchTaskRerun           = "batch_task_rerun"
	ToolBatchTaskPause           = "batch_task_pause"
	ToolBatchTaskDelete          = "batch_task_delete"
	ToolBatchTaskUpdateMetadata  = "batch_task_update_metadata"
	ToolBatchTaskUpdateSchedule  = "batch_task_update_schedule"
	ToolBatchTaskScheduleEnabled = "batch_task_schedule_enabled"
	ToolBatchTaskAdd             = "batch_task_add_task"
	ToolBatchTaskUpdate          = "batch_task_update_task"
	ToolBatchTaskRemove          = "batch_task_remove_task"

	// C2 工具集（合并同类项，8 个统一工具）
	ToolC2Listener   = "c2_listener"    // 监听器管理（create/start/stop/list/get/update/delete）
	ToolC2Session    = "c2_session"     // 会话管理（list/get/set_sleep/kill/delete）
	ToolC2Task       = "c2_task"        // 任务下发（统一 task_type 参数）
	ToolC2TaskManage = "c2_task_manage" // 任务管理（get_result/wait/list/cancel）
	ToolC2Payload    = "c2_payload"     // Payload 生成（oneliner/build）
	ToolC2Event      = "c2_event"       // 事件查询
	ToolC2Profile    = "c2_profile"     // Malleable Profile 管理（list/get/create/update/delete）
	ToolC2File       = "c2_file"        // 文件管理（list/get_result）

	// 小程序分析工具（见 internal/miniprogram）
	ToolMiniprogramAnalyze        = "miniprogram_analyze"          // wxapkg 上传+解包+敏感扫描
	ToolMiniprogramSource         = "miniprogram_source"           // 读取指定源码文件
	ToolMiniprogramSamples        = "miniprogram_samples"          // 列出已归档样本
	ToolMiniprogramFindrefs       = "miniprogram_findrefs"         // 跨文件搜索
	ToolMiniprogramTestItems      = "miniprogram_test_items"       // 测试项清单(Agent 委派任务队列)
	ToolMiniprogramTestItemUpdate = "miniprogram_test_item_update" // 测试项状态与结论回写
	ToolMiniprogramJsCode2Session = "miniprogram_jscode2session"   // js_code 换 openid+session_key
	ToolMiniprogramDecryptData    = "miniprogram_decrypt_userdata" // encryptedData+iv+session_key 解密
	ToolMiniprogramPCFetch        = "miniprogram_pc_fetch"         // 扫描/harvest PC 微信缓存 wxapkg
	ToolMiniprogramStructure      = "miniprogram_structure"        // 包结构报告:路由/分包/插件/WASM/域
	ToolMiniprogramCloudBase      = "miniprogram_cloudbase"        // 云开发资产盘点+测试项生成
	ToolMiniprogramDevTools       = "miniprogram_devtools"         // DevTools 协议通道:枚举/求值/hook

	// Android APK 分析工具（见 internal/apkanalysis）
	ToolAndroidAnalyze        = "android_analyze"          // APK 上传+解析+敏感扫描
	ToolAndroidFindRefs       = "android_findrefs"         // 跨文件字符串/方法引用检索
	ToolAndroidSource         = "android_source"           // 反编译单类源码(jadx/dad/smali 降级链)
	ToolAndroidSamples        = "android_samples"          // 样本列表
	ToolAndroidManifest       = "android_manifest"         // AndroidManifest 解析视图
	ToolAndroidExtractAPIs    = "android_extract_apis"     // 提取 API 调用
	ToolAndroidHardening      = "android_hardening"        // 加固检测
	ToolAndroidRepack         = "android_repack"           // 重打包
	ToolAndroidTestItems      = "android_test_items"       // 测试项清单
	ToolAndroidTestItemUpdate = "android_test_item_update" // 测试项回写

	// Android 真机接入工具（adb/frida/投屏）
	ToolAndroidDevice            = "android_device"
	ToolAndroidDeviceApps        = "android_device_apps"
	ToolAndroidDevicePull        = "android_device_pull"
	ToolAndroidDeviceShell       = "android_device_shell"
	ToolAndroidDeviceLogcat      = "android_device_logcat"
	ToolAndroidDeviceScreenshot  = "android_device_screenshot"
	ToolAndroidDeviceCapture     = "android_device_capture"
	ToolAndroidDeviceDeeplink    = "android_device_deeplink"
	ToolAndroidDeviceConnect     = "android_device_connect"
	ToolAndroidDeviceDumpDex     = "android_device_dumpdex"
	ToolAndroidDeviceData        = "android_device_data"
	ToolAndroidDeviceFrida       = "android_device_frida"
	ToolAndroidDeviceMiniprogram = "android_device_miniprogram"
	ToolAndroidDeviceMirror      = "android_device_mirror"
	ToolAndroidDeviceInject      = "android_device_inject"
	ToolAndroidDeviceDynamic     = "android_device_dynamic"
)

// IsBuiltinTool 检查工具名称是否是内置工具
func IsBuiltinTool(toolName string) bool {
	switch toolName {
	case ToolRecordVulnerability,
		ToolListVulnerabilities,
		ToolGetVulnerability,
		ToolCreateAsset,
		ToolGetAsset,
		ToolQueryAssets,
		ToolUpdateAsset,
		ToolDeleteAsset,
		ToolCompleteAssetScan,
		ToolUpsertProjectFact,
		ToolGetProjectFact,
		ToolListProjectFacts,
		ToolSearchProjectFacts,
		ToolDeprecateProjectFact,
		ToolRestoreProjectFact,
		ToolListKnowledgeRiskTypes,
		ToolSearchKnowledgeBase,
		ToolAnalyzeImage,
		ToolGetToolExecution,
		ToolWaitToolExecution,
		ToolCancelToolExecution,
		ToolWebshellExec,
		ToolWebshellFileList,
		ToolWebshellFileRead,
		ToolWebshellFileWrite,
		ToolManageWebshellList,
		ToolManageWebshellAdd,
		ToolManageWebshellUpdate,
		ToolManageWebshellDelete,
		ToolManageWebshellTest,
		ToolBatchTaskList,
		ToolBatchTaskGet,
		ToolBatchTaskCreate,
		ToolBatchTaskStart,
		ToolBatchTaskRerun,
		ToolBatchTaskPause,
		ToolBatchTaskDelete,
		ToolBatchTaskUpdateMetadata,
		ToolBatchTaskUpdateSchedule,
		ToolBatchTaskScheduleEnabled,
		ToolBatchTaskAdd,
		ToolBatchTaskUpdate,
		ToolBatchTaskRemove,
		// C2 工具
		ToolC2Listener,
		ToolC2Session,
		ToolC2Task,
		ToolC2TaskManage,
		ToolC2Payload,
		ToolC2Event,
		ToolC2Profile,
		ToolC2File,
		// 小程序分析工具
		ToolMiniprogramAnalyze,
		ToolMiniprogramSource,
		ToolMiniprogramSamples,
		ToolMiniprogramFindrefs,
		ToolMiniprogramTestItems,
		ToolMiniprogramTestItemUpdate,
		ToolMiniprogramJsCode2Session,
		ToolMiniprogramDecryptData,
		ToolMiniprogramPCFetch,
		ToolMiniprogramStructure,
		ToolMiniprogramCloudBase,
		ToolMiniprogramDevTools,
		// Android APK 分析 + 真机工具
		ToolAndroidAnalyze,
		ToolAndroidFindRefs,
		ToolAndroidSource,
		ToolAndroidSamples,
		ToolAndroidManifest,
		ToolAndroidExtractAPIs,
		ToolAndroidHardening,
		ToolAndroidRepack,
		ToolAndroidTestItems,
		ToolAndroidTestItemUpdate,
		ToolAndroidDevice,
		ToolAndroidDeviceApps,
		ToolAndroidDevicePull,
		ToolAndroidDeviceShell,
		ToolAndroidDeviceLogcat,
		ToolAndroidDeviceScreenshot,
		ToolAndroidDeviceCapture,
		ToolAndroidDeviceDeeplink,
		ToolAndroidDeviceConnect,
		ToolAndroidDeviceDumpDex,
		ToolAndroidDeviceData,
		ToolAndroidDeviceFrida,
		ToolAndroidDeviceMiniprogram,
		ToolAndroidDeviceMirror,
		ToolAndroidDeviceInject,
		ToolAndroidDeviceDynamic,
		// 信息收集（空间测绘）工具
		ToolSpaceSearch,
		ToolSpaceSearchParse,
		ToolListSearchSources:
		return true
	default:
		return false
	}
}

// GetAllBuiltinTools 返回所有内置工具名称列表
func GetAllBuiltinTools() []string {
	return []string{
		ToolRecordVulnerability,
		ToolListVulnerabilities,
		ToolGetVulnerability,
		ToolCreateAsset,
		ToolGetAsset,
		ToolQueryAssets,
		ToolUpdateAsset,
		ToolDeleteAsset,
		ToolCompleteAssetScan,
		ToolUpsertProjectFact,
		ToolGetProjectFact,
		ToolListProjectFacts,
		ToolSearchProjectFacts,
		ToolDeprecateProjectFact,
		ToolRestoreProjectFact,
		ToolListKnowledgeRiskTypes,
		ToolSearchKnowledgeBase,
		ToolAnalyzeImage,
		ToolGetToolExecution,
		ToolWaitToolExecution,
		ToolCancelToolExecution,
		ToolWebshellExec,
		ToolWebshellFileList,
		ToolWebshellFileRead,
		ToolWebshellFileWrite,
		ToolManageWebshellList,
		ToolManageWebshellAdd,
		ToolManageWebshellUpdate,
		ToolManageWebshellDelete,
		ToolManageWebshellTest,
		ToolBatchTaskList,
		ToolBatchTaskGet,
		ToolBatchTaskCreate,
		ToolBatchTaskStart,
		ToolBatchTaskRerun,
		ToolBatchTaskPause,
		ToolBatchTaskDelete,
		ToolBatchTaskUpdateMetadata,
		ToolBatchTaskUpdateSchedule,
		ToolBatchTaskScheduleEnabled,
		ToolBatchTaskAdd,
		ToolBatchTaskUpdate,
		ToolBatchTaskRemove,
		// C2 工具
		ToolC2Listener,
		ToolC2Session,
		ToolC2Task,
		ToolC2TaskManage,
		ToolC2Payload,
		ToolC2Event,
		ToolC2Profile,
		ToolC2File,
		ToolMiniprogramAnalyze,
		ToolMiniprogramSource,
		ToolMiniprogramSamples,
		ToolMiniprogramFindrefs,
		ToolMiniprogramTestItems,
		ToolMiniprogramTestItemUpdate,
		ToolMiniprogramJsCode2Session,
		ToolMiniprogramDecryptData,
		ToolMiniprogramPCFetch,
		ToolMiniprogramStructure,
		ToolMiniprogramCloudBase,
		ToolMiniprogramDevTools,
		ToolAndroidAnalyze,
		ToolAndroidFindRefs,
		ToolAndroidSource,
		ToolAndroidSamples,
		ToolAndroidManifest,
		ToolAndroidExtractAPIs,
		ToolAndroidHardening,
		ToolAndroidRepack,
		ToolAndroidTestItems,
		ToolAndroidTestItemUpdate,
		ToolAndroidDevice,
		ToolAndroidDeviceApps,
		ToolAndroidDevicePull,
		ToolAndroidDeviceShell,
		ToolAndroidDeviceLogcat,
		ToolAndroidDeviceScreenshot,
		ToolAndroidDeviceCapture,
		ToolAndroidDeviceDeeplink,
		ToolAndroidDeviceConnect,
		ToolAndroidDeviceDumpDex,
		ToolAndroidDeviceData,
		ToolAndroidDeviceFrida,
		ToolAndroidDeviceMiniprogram,
		ToolAndroidDeviceMirror,
		ToolAndroidDeviceInject,
		ToolAndroidDeviceDynamic,
		// 信息收集（空间测绘）工具
		ToolSpaceSearch,
		ToolSpaceSearchParse,
		ToolListSearchSources,
	}
}
