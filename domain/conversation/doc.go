// Package conversation 会话领域：会话、回合、消息，以及会话链路上的审批拦截与旁路问答。
//
// step 3 批 3 第五刀迁入：intercept（intercept_rules / intercept_pending 的规则与
// 审批流水）、interceptdetail（audit JSONB 与决策/执行状态机）、sidequestions
// （side_question_sessions / side_question_requests）。
// step 3 批 3 第六刀迁入：repo（conversations 会话线程 + conversation_activities
// 话步）。CreateConversation / UpdateConversationProfile 的 LLM profile 加锁协议、
// chat_mentions 跨 catalog 搜索留在 db 装配层；Activity 上提 core/model。
//
// 迁移边界：GetInterceptExecution（审批执行导航）留在 db 装配层——它读 activity
// （探索域）、conversation_activities、tasks（任务域）三个外部域的表，一个方法
// 跨多域不整段搬（与 ToolUsageCounts / NotificationAssetNames 同例）。
//
// 注意：intercept_rules / intercept_pending 在 ownership 元数据里标为 plugin-hitl，
// Step 4 HITL 插件化时数据面归插件；迁移期 SQL 收在本包，插件侧直接复用。
//
// 分层约束：属于 domain，可 import finduai/core/* 与叶子包（.finduai/sidequestion），
// 禁止 import finduai/plugins/* 与 finduai/db。校验：cmd/layercheck。
package conversation
