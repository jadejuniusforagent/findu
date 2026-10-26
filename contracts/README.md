# 契约目录（contracts）

本目录是插件化重构的唯一契约来源。任何实现必须先对齐这里，再写代码。
Step 0 盘点见 docs/refactor/；本目录从 Step 1 开始填充。

## 一、十份契约与状态

| 编号 | 文件 | 作用 | 状态 |
|---|---|---|---|
| 1 | plugin-manifest.schema.json | 插件清单：id/版本/类型/依赖/提供物/权限/迁移 | 已起草 |
| 2 | tool-contract.schema.json | 工具输入输出 schema、副作用、超时、幂等 | 已起草（2026-10-19 增补 targets 可选字段：目标提取声明，门禁逐目标校验依据） |
| 3 | skill-contract.schema.json | 技能 frontmatter 扩展：工具/二进制/MCP 依赖 | 已起草 |
| 4 | role-contract.schema.json | agent 角色：prompt/工具白名单/技能绑定/模型参数 | 已起草 |
| 5 | storage-contract.md | repository 接口 + 表归属 + 迁移归属规则 | 已起草 |
| 6 | event-contract.schema.json | 事件名/payload/投递语义 | 已实现（Go：`core/eventbus`，含 provider 桥接与契约枚举比对测试） |
| 7 | ui-slot-contract.schema.json | 前端插槽：导航/页面/Tab/卡片 + 权限 | 已起草 |
| 8 | runtime-spi.md | agent 运行时接口（8 个方法） | 已实现 Go 接口 + mock（`core/runtime`；真实 adapter 待接） |
| 9 | ui-bff-contract.md | 前端唯一可用端点集合 | 已起草 |
| 10 | capability-providers.md | C2 / WebShell / 流量 三组可替换接口 | 已冻结为外部接入槽位（核心不实现 C2/流量，见 `docs/refactor/EXTERNAL-PROVIDER-SCOPE.md`） |

## 二、版本规则

api_version 采用 主.次 两段：
    1.0  初版冻结
    1.x  只允许新增可选字段与新增可选方法；实现方必须忽略未知字段
    2.0  允许删除字段、改语义、改必选性；必须提供迁移说明

插件 manifest 里声明 requires_api: 1.x；宿主按 主 版本匹配，次版本向下兼容。

## 三、校验方式（Step 1 结束前接入 CI）

    1 JSON Schema 校验：contracts/*.schema.json 校验 plugins/*/plugin.yaml 与 skills/*/SKILL.md 的 frontmatter
    2 SPI 编译校验：contracts/runtime-spi 的 Go 接口必须由 adapter 实现，编译期保证方法齐全
    3 表归属校验：插件 migrations 里出现的表必须在本插件 manifest 的 owns_tables 中声明

## 四、维护规则

1 新增能力先改契约，再写实现，最后补样例
2 任何契约变更都要更新本文件的版本与状态列
3 契约文件不允许内嵌实现细节（如具体表名、具体工具名），只描述形状

## 运行时 adapter 状态（2026-10-20）

第 8 份 runtime-spi 已新增 Eino/askernel 真实 adapter：`askernel/runtimeadapter`。
CreateSession / AppendTurn / StreamEvents / InvokeModel / CallTool / Plan / Health 已落到
真实 askernel 会话；Snapshot/Restore 在批 5 已实现，Health 声明 Snapshot=true。findu 应用层 adapter 见下一节。

## findu application adapter（2026-10-20）

第 8 份 runtime-spi 的 findu 应用层 adapter 已落地：`agent/runtimeadapter`（包装 `agent.ChatAgent`）。
Eino 低层 `askernel/runtimeadapter` 与 findu 应用层 adapter 均实现 Runtime；findu 应用层
已在批 4 实现 Snapshot/Restore（Health Snapshot=true），Eino 低层在批 5 也已对齐。
共用模型调用在 `internal/spimodel`。

## Snapshot/Restore 状态（2026-10-20）

`agent/runtimeadapter` 已实现真实 Snapshot/Restore：原子替换 ChatAgent transcript + 恢复 turns/pending，
Health 声明 Snapshot=true。Eino 低层 adapter 在批 5 也已实现 Snapshot/Restore。

## 双 Snapshot 对齐（2026-10-20）

Eino 低层 adapter 已在批 5 实现 Snapshot/Restore（askernel DTO/usage + 可选 transcript），
Health Snapshot=true；native adapter 的 ChatAgent transcript 快照见批 4。两套 adapter 现在都声明 Snapshot=true。

## 生产装配工厂（2026-10-20）

`server.RuntimeAdapterForAgent` 已可从现役 ChatAgent 装配 findu adapter；server 级端到端测试通过。
`runConversationTurn` 已接入 `chatConversation` 开关，默认仍走原路径，未新增 API、未重建；线上 merge43。

## 切流开关（2026-10-20）

`server.chatConversation` 默认走原 ChatAgent.Chat；设置 `FINDU_RUNTIME_SPI=1/true/on/yes` 后走 native adapter。
ActivitySink/ResultSink 保证现有活动流与最终结果回填；默认未启用，代码已随 merge44 部署。

## merge44 部署（2026-10-20）

`p8.9-merge44` 已部署，`FINDU_RUNTIME_SPI` 未设置。21 表行数快照 +
`scripts/verify_deploy.sh verify` → VERIFY_OK；切流 canary 未成功（路由归属见下节）。

## 路由归属核查（2026-10-20）

`/api/conversations` 由 findu 兜底，`server.chatConversation` 不是线上对话入口；实际聊天运行时是
findu `internal/handler.AgentHandler`。runtime-spi switch canary 未命中，已恢复关闭；Step 5 生产切流仍需 findu 内部 adapter 或路由归属评审。

## eino-agent 执行器接管（2026-10-20）

`/api/eino-agent` 已从 findu 所有权移出，findu ChatAgent 接管单轮执行；findu conversations/messages/process_details
与 SSE UI 契约保留。已部署 merge47 并 canary 成功；CRUD/deep/附件/停止/数据迁移仍未接管。

## bridge 停止（2026-10-20）

bridge 运行中 `/api/conversations/{id}/stop` 由 findu 取消 ChatAgent，并发送 findu SSE cancelled；非 bridge 会话仍走 findu。
NULL `agent_key` 已兼容。merge48 stop canary 通过，VERIFY_OK。

## 附件与 role 映射（2026-10-20）

bridge 已消费 findu 附件 serverPath/content，并按 role 映射到 findu agent key。
merge49 基础 canary/VERIFY_OK；上传接口、完整 role 工具白名单、deep/多代理与迁移仍未接管。

## 附件/历史 canary（2026-10-20）

真实附件 Read 返回唯一内容；findu 历史 messages 回填 findu transcript 后，模型正确回答 BLUE-42。
merge49 未变，VERIFY_OK。

## 会话详情读路径（2026-10-20）

`GET /api/conversations/{id}` 已由 findu 从共享 PG 返回 messages/processDetails；list/create/delete 仍 findu。
merge50 详情 canary 通过，VERIFY_OK。

## 会话写路径（2026-10-20）

标题/置顶/项目绑定已由 findu 写共享 PG；merge51 写路径 canary 通过，VERIFY_OK。
list/create/delete、delete-turn/plan/token/results、deep/多代理与迁移仍未接管。

## delete-turn（2026-10-20）

`POST /api/conversations/{id}/delete-turn` 已由 findu 事务删除 messages/process_details 并清 last_react_*。
merge52 canary 通过，VERIFY_OK；list/create/delete、plan/token/results、deep 仍 findu。

## 会话 CRUD（2026-10-20）

GET/POST list/create、GET/PUT/PATCH/DELETE 精确会话、pinned/project/delete-turn 已由 findu 接管共享 PG。
merge53 CRUD canary 通过，VERIFY_OK；batch delete、plan/token/results、deep/多代理与迁移仍未接管。

## 会话扩展读路径（2026-10-21）

GET /{id}/token-usage 聚合共享 model_token_usage；GET /{id}/results 返回 messages + vulnerabilities；GET /{id}/plan-tasks 仅在 bridge 运行中由 findu 读取共享 plantask，其余回落 findu。merge54 extras canary 通过，VERIFY_OK。

## 批量删除（2026-10-21）

POST /api/conversations/delete/batch 由 findu 接管，事务清理 process_details/messages/conversation_activities/conversations。merge55 BATCH_DELETE_CANARY_OK，VERIFY_OK。

## bridge 绑定（2026-10-21）

/api/eino-agent* 新建会话写入 project/role/agent_mode/webshell_connection_id；WebShell AI eino_single 绑定不再丢失。merge56 canary 通过，VERIFY_OK。
