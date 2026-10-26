# runtime-spi（第 8 份契约）

状态：已实现 Go 接口 + 内存 mock；真实 adapter 待接（2026-10-20）。校验方式：Go 接口编译期保证 + Eino 与 findu 原生两个存量实现回填。

## 一、定位

本契约抽象的是「agent 运行时的对外 I/O 边界」，不是「agent 怎么思考」。
换运行时（吸收新 agent）时只换 adapter，core 与前端不动。

明确不抽象（留在 adapter 内，不进 SPI）：
    1 规划算法与拓扑（单 agent / planner+worker / 多 agent 协作）
    2 反思、重试、上下文压缩策略
    3 prompt 组织与模型选择策略

## 二、八个方法

    1 CreateSession(ctx, SessionSpec) (SessionID, error)
      必选。建立隔离边界；SessionSpec 含 project_id、title、adapter 私有参数透传位。

    2 AppendTurn(ctx, SessionID, Turn) (TurnID, error)
      必选。写入一个回合（用户输入或 agent 输出）；canonical 落库由 core 负责，adapter 只提交内容。

    3 StreamEvents(ctx, SessionID, from_seq) (<-chan Event, error)
      必选。事件流（token 增量、工具调用开始/结束、计划更新、错误、结束）。事件类型固定，payload 见 event-contract。

    4 InvokeModel(ctx, ModelRequest) (ModelResponse, error)
      必选。模型调用；provider 中立，tools schema 由 core 传入。adapter 不得直连厂商 SDK 之外的路径。

    5 CallTool(ctx, ToolCall) (ToolResult, error)
      必选。工具调用唯一入口；进程内直调或转发由宿主决定，adapter 不感知 transport。

    6 Plan(ctx, Goal, State) ([]Intent, error)
      必选（可为退化实现：单意图）。只要求返回意图列表，不规定算法。

    7 Snapshot(ctx, SessionID) (State, error) / Restore(ctx, SessionID, State) error
      可选。中断恢复与上下文压缩用。findu 原生实现预计缺此项，由 Health 声明为未支持。

    8 Health(ctx) (Capabilities, error)
      必选。能力声明：是否支持流式、是否支持并行 worker、是否支持 Snapshot、支持的事件类型集合。

Go 落地（`core/runtime`）：1~6 与 Health 由必选接口 `Runtime` 声明；第 7 项的
`Snapshot`/`Restore` 是可选能力，由独立接口 `Snapshotter` 声明，是否支持以
`Health().Snapshot` 为准。不支持的可选能力不得用空方法伪装，必须返回
`ErrUnsupported`。

## 三、错误与超时语义

    1 所有方法必须响应 ctx 取消，不得忽略 Done 通道
    2 可重试错误用 ErrRetryable 包装；不可重试（参数错误、权限）直接返回
    3 流式方法在 adapter 内部异常时，必须发送 Event{type=error} 后关闭通道，不得静默关闭

## 四、双存量回填状态（待 Step 1 校验后填表）

| 方法 | Eino ADK | findu 原生 | 结论 |
|---|---|---|---|
| CreateSession | 待填 | 待填 | 待定 |
| AppendTurn | 待填 | 待填 | 待定 |
| StreamEvents | 待填 | 待填 | 待定 |
| InvokeModel | 待填 | 待填 | 待定 |
| CallTool | 待填 | 待填 | 待定 |
| Plan | 待填 | 待填 | 待定 |
| Snapshot/Restore | 待填 | 待填 | 待定 |
| Health | 待填 | 待填 | 待定 |

填表规则：能对上=必选；只有一边有=可选并由 Health 声明；两边都没有=从 SPI 删除，不预留。

2026-10-20 更新：Go 接口、mock 与契约测试已落地（`core/runtime`、`core/runtime/mock`）；
本表仍待 Eino / findu adapter 接入时逐项回填，不把「接口已落」当成「adapter 已验证」。

## 五、Go 落地（Step 5 批 1）

- 必选接口：`runtime.Runtime`，包含 CreateSession / AppendTurn / StreamEvents /
  InvokeModel / CallTool / Plan / Health。
- 可选能力：`runtime.Snapshotter`，包含 Snapshot / Restore；实现时 Health 必须声明
  `Snapshot=true`。
- 事件：`runtime.Event` 是 `eventbus.Envelope` 的类型别名，event-contract 的字段、
  枚举与 seq 校验只有一份。
- 错误：`ErrNotFound` / `ErrInvalid` / `ErrConflict` / `ErrUnsupported` / `ErrRetryable`；
  `Retryable(err)` 同时保留 `ErrRetryable` 与底层错误链。
- mock：`core/runtime/mock.NewRuntime()`，同步执行并确定性重放事件；仅用于契约测试
  与本地演练，不进入生产装配。
- 契约测试：`core/runtime/contract_test.go` 覆盖会话/回合/模型/工具/规划/快照事件、
  `from_seq` 游标、缺失资源、参数负例、ctx 取消与可重试错误；合并覆盖率 **90.7%**。


## 六、Eino adapter 回填（Step 5 批 2，2026-10-20）

`askernel/runtimeadapter` 已实现 `runtime.Runtime`，编译期断言通过。Eino 侧矩阵状态：
CreateSession / AppendTurn / StreamEvents / InvokeModel / CallTool / Plan / Health 均已落到
真实 `askernel.Session` 或 `Options.Model` / `tools.Registry`；Snapshot/Restore 在批 5 已补齐
（askernel DTO/usage + 可选 transcript），`Health().Snapshot=true`。

已知边界：
1 `InvokeModel` / `CallTool` 是直调原语，不修改 askernel DTO；canonical 落库仍归 core。
2 `AppendTurn` 只把用户回合排入下一次 `StreamEvents`；assistant/system/tool 回合暂不注入
   askernel DTO，跨进程恢复仍依赖 transcript / 后续 Snapshot。
3 同一 Session 不并发 `Prompt`；并发 live stream 返回 `ErrConflict`。
4 本批未接生产装配，线上仍为 `p8.9-merge43`；findu 原生 adapter 仍待接。


## 七、findu 应用层 adapter 回填（Step 5 批 3，2026-10-20）

`agent/runtimeadapter` 已实现 `runtime.Runtime`，包装生产入口 `agent.ChatAgent`，编译期断言通过。
Eino 侧与 findu 侧现在都有真实 adapter：

- Eino 低层：`askernel/runtimeadapter`，事件源 `askernel.RunEvent`。
- findu 应用层：`agent/runtimeadapter`，事件源 `core/model.Activity`，保留 prompt 装配、
  技能/MCP 工具增强、transcript/resume 与 guard。
- 共用模型调用/消息转换：`internal/spimodel`。

Snapshot/Restore 在本批（批 3）尚未实现；批 4 已为 findu 应用层 adapter 补上真实
`Snapshotter`（见第八节），Eino 低层 adapter 仍为 `Snapshot=false`。
本批未接生产装配，线上仍为 `p8.9-merge43`。

已知边界：
1 pure-text Chat 可能只有 `turn.created(assistant)`，没有 `token.delta`（最终文本由 result
   Activity 携带，capture 会去掉重复尾段）；直接 `InvokeModel` 会记录 `token.delta`。
2 `CallTool` 默认走 `DefaultTools + agent.AugmentTools`，不含 ChatAgent 额外装配的
   WebFetch/WebSearch；需要时由 Config.Tools 注入。
3 `CallTool` 不跑 permission/approval 管线；宿主负责授权。
4 同一 Session 不并发 Chat；assistant/system/tool 的 AppendTurn 只记账，不注入 transcript。


## 八、findu Snapshot/Restore 回填（Step 5 批 4，2026-10-20）

`agent/runtimeadapter` 已实现 `runtime.Snapshotter`：

- `Snapshot`：读取 ChatAgent 的主 transcript（`transcript.Store.Load`），连同 SPI turns、
  pending 队列一起写入 `State.Data`；`State.Version=1`，`State.Seq` 为该 session 最后事件序号。
- `Restore`：校验 state 版本、session id、agent_key 后，用 `transcript.Store.Replace`
  原子替换目标 transcript，并恢复 turns/pending；下一次 Chat 的 Resume 读取恢复后的历史。
- 通过 `SessionSpec.Options["session_id"]` 可把新 adapter 重挂到既有 session；路径型 id
  （`.`、`..`、含 `/` 或 `\`）返回 `ErrInvalid`。
- `Health().Snapshot=true`；session 运行中调用 Snapshot/Restore 返回 `ErrConflict`。
- Eino 低层 adapter 仍为 `Snapshot=false`（askernel 的 DTO 恢复尚未实现）。

边界：Snapshot 捕获的是运行时历史/回合，不重放事件日志；native adapter 要求 ChatAgent
带 transcript store，否则 `New` 返回 `ErrInvalid`。


## 九、Eino Snapshot/Restore 回填（Step 5 批 5，2026-10-20）

`askernel/runtimeadapter` 已实现 `runtime.Snapshotter`：

- `askernel.Session.Restore(messages, usage)`：替换 DTO/usage；有 transcript 时用
  `transcript.Store.Replace` 原子重写会话文件并重建 ResumeWriter。
- `Snapshot`：保存 askernel DTO、累计 usage、SPI turns/pending；`State.Version=1`，
  `State.Seq` 为该 session 最后事件序号。
- `Restore`：校验版本/session 后调用 `Session.Restore`，再恢复 turns/pending；运行中返回
  `ErrConflict`。
- 支持 `SessionSpec.Options["session_id"]` 重挂既有 id；路径型 id 返回 `ErrInvalid`。
- `Health().Snapshot=true`。

两套 adapter 的恢复粒度：
- native：ChatAgent JSONL transcript + SPI turns/pending。
- Eino：askernel DTO/usage + transcript（可选）+ SPI turns/pending。


## 十、生产装配缝（Step 5 批 6，2026-10-20）

`server/runtime_assembly.go` 新增 `Server.RuntimeAdapterForAgent(agentKey)`：从现役
`ChatAgent`、agent 预算与 web_search 配置装配 findu application adapter。server 级测试用
真实 ChatAgent + fake model 跑通 Health/CreateSession/AppendTurn/StreamEvents。

状态：批 7 已把 `runConversationTurn` 接到 `chatConversation` 开关；默认仍走
`ChatAgent.Chat`，设置 `FINDU_RUNTIME_SPI=1` 才切到 adapter。Activity/Result 已回填，
未新增 API 路由，未重建。默认启用前仍需双跑观察。


## 十一、切流开关与回填（Step 5 批 7，2026-10-20）

native adapter 新增 `ActivitySink` / `ResultSink`，`server.chatConversation` 在
`FINDU_RUNTIME_SPI=1/true/on/yes` 时切到 runtime-spi，否则保持 `ChatAgent.Chat`。

- `ActivitySink`：原样转发 ChatAgent 的 `model.Activity`，现有 conversation_activities/UI 字段不变。
- `ResultSink`：回传最终 text/err，路由沿用原 finishStatus/finishReason 与错误活动逻辑。
- `session_id=conv-<id>` 重挂既有 transcript；默认路径不加环境变量。
- 默认未切流、未重建；线上仍 merge43。


## 十二、merge44 部署状态（Step 5 批 8，2026-10-20）

`p8.9-merge44` 已部署，默认 `FINDU_RUNTIME_SPI` 未设置，现有对话继续走 `ChatAgent.Chat`。
部署前 21 表行数快照、部署后 `scripts/verify_deploy.sh verify` 均通过（`VERIFY_OK`），
冒烟字节与基线一致。runtime-spi 切流开关仍关闭，等待显式 canary。


## 十三、路由归属核查（Step 5 批 9，2026-10-20）

重要更正：`/api/conversations` 属于 `finduapp.OwnedAPIPaths()`，线上由 findu 兜底处理；
`server.chatConversation` 不是 findu 页面的实际对话入口。当前实际聊天运行时是 findu
`internal/handler.AgentHandler`（`/api/eino-agent*`），不是 `agent.ChatAgent`。

`FINDU_RUNTIME_SPI` 的 canary 尝试未能命中发送路由（`POST /api/conversations/{id}/messages`
返回 404），已恢复开关为未设置；`VERIFY_OK`。Step 5 的 adapter 层/装配层/快照能力保留，但
生产切流尚未完成。后续必须为 findu 内部 AgentHandler 做 adapter，或单独评审路由归属调整。


## 十四、findu 实际聊天入口桥接（Step 5 批 10，2026-10-20）

`/api/eino-agent*` 已从 findu `OwnedAPIPaths` 移出，改由 findu `server/findu_eino_bridge.go`
处理。桥接保留 findu 的 conversations/messages/process_details 数据与 SSE UI 契约，但单轮执行
使用 findu `ChatAgent`；首次桥接会把 findu 历史消息回填 findu transcript。

已部署 `p8.9-merge47` 并 canary 成功（response OK）。当前仅覆盖 `eino_single`；CRUD/历史 UI
仍由 findu 处理，附件、role 工具、停止、deep/多代理与数据迁移未完成。


## 十五、bridge 停止（Step 5 批 11，2026-10-20）

`/api/conversations/{id}/stop` 在 findu bridge 运行时由 findu 处理，取消 ChatAgent 运行并发送
findu SSE `cancelled`；非 bridge 会话继续由 findu 处理。`agent_key` 为 NULL 的 findu 历史会话
通过 `COALESCE(...,'findu_default')` 兼容。已部署 merge48，停止 canary 通过。


## 十六、附件与 role 映射（Step 5 批 12，2026-10-20）

bridge 已消费 findu attachment 字段并把 `serverPath`/`content` 拼入 ChatAgent 输入；role 按静态表
映射到 findu agent key（默认→findu_default、渗透测试→pentest、报告撰写→reporter、
漏洞复测→retester、审计取证→findu_audit）。已部署 merge49，基础 canary/VERIFY_OK。
上传接口、完整 role 工具白名单、deep/多代理、CRUD/数据迁移仍未接管。


## 十七、会话详情读路径（Step 5 批 13，2026-10-20）

`GET /api/conversations/{id}` 已改由 findu 从共享 PG 返回 findu DTO；`include_process_details=1`
时包含 messages/processDetails。list/create/delete 等仍由 findu。已部署 merge50，详情 canary
返回 assistant OK + processDetails，VERIFY_OK。


## 十八、会话写路径（Step 5 批 14，2026-10-20）

`PUT /api/conversations/{id}`、`/pinned`、`/project` 已由 findu 写共享 PG；list/create/delete
仍 findu。merge51 写路径 canary 通过，VERIFY_OK。delete-turn/plan-tasks/token-usage/results、
list/create/delete、deep/多代理与数据迁移仍未接管。


## 十九、delete-turn（Step 5 批 15，2026-10-20）

`POST /api/conversations/{id}/delete-turn` 已由 findu 事务处理：按 anchor 所在轮次删除
messages、process_details，清空 last_react_*。merge52 canary 通过，VERIFY_OK。


## 二十、会话 CRUD（Step 5 批 16，2026-10-20）

`GET/POST /api/conversations` 与 `DELETE /api/conversations/{id}` 已由 findu 接管共享 PG；
DTO 同时包含 findu camelCase 与 findu snake_case。merge53 CRUD canary 通过，VERIFY_OK。
plan/token/results、batch delete、deep/多代理与数据迁移仍未接管。


## 二十一、会话扩展读路径（Step 5 批 17，2026-10-21）

`GET /{id}/token-usage` 聚合共享 `model_token_usage`；`GET /{id}/results` 返回
messages + vulnerabilities；`GET /{id}/plan-tasks` 仅在本机 bridge 运行中由 findu
读取共享 plantask 文件，其余时间回落 findu。merge54 extras canary 通过，VERIFY_OK。


## 二十二、批量删除（Step 5 批 18，2026-10-21）

`POST /api/conversations/delete/batch` 由 findu 接管，事务清理
`process_details/messages/conversation_activities/conversations`；merge55 canary 通过，VERIFY_OK。


## 二十三、bridge 绑定（Step 5 批 19，2026-10-21）

`/api/eino-agent*` 新建会话写入 project/role/agent_mode/webshell_connection_id；
WebShell AI eino_single 不再丢失项目或连接绑定。merge56 canary 通过，VERIFY_OK。
