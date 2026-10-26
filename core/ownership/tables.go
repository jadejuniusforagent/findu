// Package ownership 把表归属（core / domain / plugin-<id>）变成代码，供 CI 校验
// 插件迁移不得越界创建或修改非本插件的表。
//
// 数据源：docs/refactor/TABLE-OWNERSHIP.md（Step 0 盘点），canonical_* 与 schema_migrations
// 是 Step 2 新增的内核表，在此显式补齐。改归属时必须同步改文档与本文件。
package ownership

// Owner 是表名到归属的映射。
var Owner = map[string]string{
	"activity":                          "core",
	"agent_prompt_vars":                 "domain",
	"agent_prompts":                     "domain",
	"agent_skill_visibility":            "domain",
	"agent_triggers":                    "domain",
	"agent_visibility":                  "domain",
	"agents":                            "domain",
	"asset_intercept_rules":             "domain",
	"assets":                            "domain",
	"attack_chain_edges":                "domain",
	"attack_chain_nodes":                "domain",
	"audit_logs":                        "core",
	"batch_task_queues":                 "domain",
	"batch_tasks":                       "domain",
	"c2_events":                         "plugin-c2",
	"c2_files":                          "plugin-c2",
	"c2_listeners":                      "plugin-c2",
	"c2_payload_artifacts":              "plugin-c2",
	"c2_profiles":                       "plugin-c2",
	"c2_sessions":                       "plugin-c2",
	"c2_tasks":                          "plugin-c2",
	"canonical_artifact":                "core",
	"canonical_message":                 "core",
	"canonical_session":                 "core",
	"canonical_tool_call":               "core",
	"canonical_turn":                    "core",
	"chat_upload_artifacts":             "domain",
	"companies":                         "domain",
	"company_scope":                     "domain",
	"conversation_activities":           "domain",
	"conversations":                     "domain",
	"exploration_anchors":               "domain",
	"exploration_edges":                 "domain",
	"exploration_nodes":                 "domain",
	"explorations":                      "domain",
	"finding_enrichment":                "domain",
	"finding_retests":                   "domain",
	"finding_traffic_bindings":          "domain",
	"findings":                          "domain",
	"fingerprint_rules":                 "plugin-intel",
	"fingerprint_sources":               "plugin-intel",
	"hitl_conversation_configs":         "plugin-hitl",
	"hitl_interrupts":                   "plugin-hitl",
	"intercept_pending":                 "plugin-hitl",
	"intercept_rules":                   "plugin-hitl",
	"knowledge_retrieval_logs":          "domain",
	"llm_profile_health":                "domain",
	"llm_profiles":                      "domain",
	"llm_records":                       "domain",
	"llm_usage":                         "domain",
	"main_sessions":                     "core",
	"mcp_servers":                       "plugin-mcp",
	"mcp_tools_cache":                   "plugin-mcp",
	"messages":                          "domain",
	"model_token_usage":                 "domain",
	"notification_channels":             "plugin-notify",
	"notification_deliveries":           "plugin-notify",
	"notification_events":               "plugin-notify",
	"notification_reads_by_user":        "plugin-notify",
	"process_details":                   "domain",
	"project_fact_edges":                "domain",
	"project_facts":                     "domain",
	"project_scope":                     "domain",
	"project_scope_suggestions":         "domain",
	"projects":                          "domain",
	"rbac_permissions":                  "core",
	"rbac_resource_assignments":         "core",
	"rbac_role_permissions":             "core",
	"rbac_roles":                        "core",
	"rbac_user_roles":                   "core",
	"rbac_users":                        "core",
	"report_settings":                   "domain",
	"report_templates":                  "domain",
	"reports":                           "domain",
	"robot_binding_codes":               "plugin-notify",
	"robot_user_bindings":               "plugin-notify",
	"robot_user_sessions":               "plugin-notify",
	"scheduler_state":                   "core",
	"schema_migrations":                 "core",
	"server_logs":                       "core",
	"settings":                          "core",
	"side_question_requests":            "domain",
	"side_question_sessions":            "domain",
	"skill_candidates":                  "domain",
	"skill_stats":                       "domain",
	"skill_usage":                       "domain",
	"sync_state":                        "core",
	"task_archives":                     "domain",
	"task_asset_links":                  "domain",
	"task_categories":                   "domain",
	"task_constraints":                  "domain",
	"task_intercept_rules":              "domain",
	"task_llm_profiles":                 "domain",
	"task_relations":                    "domain",
	"task_scope":                        "domain",
	"task_templates":                    "domain",
	"tasks":                             "domain",
	"tool_executions":                   "domain",
	"tool_stats":                        "domain",
	"tool_usage":                        "domain",
	"tools":                             "domain",
	"traffic_evidence_snapshots":        "plugin-traffic",
	"vuln_intel":                        "plugin-intel",
	"vuln_kb":                           "plugin-intel",
	"vulnerabilities":                   "domain",
	"vulnerability_alert_deliveries":    "plugin-notify",
	"vulnerability_alert_subscriptions": "plugin-notify",
	"webshell_connection_states":        "plugin-webshell",
	"webshell_connections":              "plugin-webshell",
	"workflow_definitions":              "domain",
	"workflow_node_runs":                "domain",
	"workflow_package_imports":          "domain",
	"workflow_package_inspections":      "domain",
	"workflow_runs":                     "domain",
}

// Core 返回该表归属；ok 为 false 表示未登记（新表必须补登记）。
func TableOwner(table string) (owner string, ok bool) {
	o, ok := Owner[table]
	return o, ok
}

// PluginTables 返回某插件名下的表。
func PluginTables(plugin string) []string {
	var out []string
	for t, o := range Owner {
		if o == "plugin-"+plugin {
			out = append(out, t)
		}
	}
	return out
}

// ValidatePluginTables 校验插件声明的 owns_tables 是否与其归属一致。
// 任何越界（声明了 core/domain/别的插件的表）都返回错误，CI 直接失败。
func ValidatePluginTables(plugin string, tables []string) error {
	for _, t := range tables {
		o, ok := Owner[t]
		if !ok {
			return &UnownedError{Table: t}
		}
		want := "plugin-" + plugin
		if o != want {
			return &OwnershipMismatchError{Table: t, Want: want, Got: o}
		}
	}
	return nil
}

// UnownedError 表示表没有在归属表里登记。
type UnownedError struct{ Table string }

func (e *UnownedError) Error() string {
	return "表未登记归属: " + e.Table + "（请在 docs/refactor/TABLE-OWNERSHIP.md 与 core/ownership 中补齐）"
}

// OwnershipMismatchError 表示插件声明了不属于它的表。
type OwnershipMismatchError struct{ Table, Want, Got string }

func (e *OwnershipMismatchError) Error() string {
	return "表 " + e.Table + " 归属为 " + e.Got + "，与插件声明 " + e.Want + " 不一致"
}
