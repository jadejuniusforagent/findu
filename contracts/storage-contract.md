# storage-contract（第 5 份契约）

状态：已起草。Step 2 实现时以本文件为准。

## 一、canonical 五实体（唯一持久化真相）

任何 runtime adapter、任何插件都不得自建会话/消息类表；只能通过下列实体落库。

1 session
    session_id (pk)  project_id  adapter  title  status  created_at  updated_at  meta(jsonb)
    adapter：产生该会话的运行时标识（eino / native / 其他），用于回放与排障

2 turn
    turn_id (pk)  session_id (fk)  role(user|agent|system)  status  created_at  seq
    一个 turn 是一次输入或一次完整输出，不是单条消息

3 message
    message_id (pk)  turn_id (fk)  role  content(text)  content_parts(jsonb)  created_at
    content_parts 承载多模态与工具调用块，避免为每种模型加列

4 tool_call
    call_id (pk)  turn_id (fk)  tool_name  input(jsonb)  output(jsonb)  status  error  started_at  finished_at  duration_ms
    工具执行统计、审计与回放都从这里取，不再分别读 tool_executions 与 tool_usage

5 artifact
    artifact_id (pk)  session_id  turn_id  kind(report|upload|screenshot|pcap|other)  path  size  sha256  created_at

## 二、repository 接口清单（core/store）

    SessionRepo:  Create / Get / List / UpdateStatus / Close
    TurnRepo:     Append / ListBySession / GetLatest
    MessageRepo:  Append / ListByTurn / ListBySession
    ToolCallRepo: Start / Finish / ListByTurn / Stats
    ArtifactRepo: Put / Get / ListBySession

约束：
    1 所有接口以 ctx 为一参，错误可判定（ErrNotFound / ErrConflict / ErrRetryable）
    2 分页统一用 (limit, cursor)，不用 offset
    3 事务边界由 core 控制，repository 不内嵌跨表业务事务

## 三、表归属规则

    1 core 表：所有能力共用，不可卸载。当前 13 张（含 RBAC 七表）
    2 domain 表：跨插件共享的领域实体，不可卸载，随产品形态演进。当前 66 张
    3 plugin 表：插件私有，必须在本插件 manifest 的 owns_tables 中声明。当前 29 张
    4 判定口径：一张表若被两个以上插件读写，必须上提为 domain；若只被一个插件读写，必须下沉为 plugin

## 四、迁移归属

    core:   migrations/core/NNNN_name.up.sql 与 .down.sql，只含 core 表与 canonical 表
    plugin: plugins/<id>/migrations/NNNN_*.sql，只能出现本插件 owns_tables 里的表
    CI 校验：解析插件迁移里的表名，越界即失败

## 五、外键规则（Step 0 实测已发现两条违规）

允许方向：
    plugin -> core      允许（插件引用用户、会话）
    plugin -> domain    允许（插件引用漏洞、资产）
    plugin -> 同插件    允许
    domain -> domain    允许
禁止方向：
    core -> plugin      禁止（核心不得依赖可卸载物）
    domain -> plugin    禁止（领域不得依赖可卸载物）

当前违规（Step 2 必须处理完）：
    1 sync_state -> mcp_servers
       现状：核心表外键指向插件表
       处置：去掉外键，datasource_id 保留为逻辑引用，取值前先查插件是否安装
    2 finding_traffic_bindings -> traffic_evidence_snapshots
       现状：领域表外键指向插件表
       处置：同上改为逻辑引用；若流量插件未安装，字段保留但标记 unavailable

## 六、卸载语义

    1 默认：停用插件，保留表与数据，页面显示能力未安装
    2 DROP：必须显式命令 + 二次确认 + 先导出备份
    3 卸载前检查：plugin 表是否被 core/domain 以逻辑引用方式依赖，有则列出受影响行数
