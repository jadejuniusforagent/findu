# capability-providers（第 10 份契约）

状态：已起草（Step 1 第一批）。回应第 7 条确认：C2 / WebShell / 流量录制 必须可替换，不只是可禁用。

> **2026-10-20 范围调整**：C2 与流量录制改为**外部接入槽位**，核心仓库不提供生产实现、
> 不迁移现役 `internal/c2` / `traffic` 数据、不切 `/api/c2` 路由。接口、`provider.Registry`、
> 清单 kind、事件契约与 mock 继续保留。详见 `docs/refactor/EXTERNAL-PROVIDER-SCOPE.md`。

落地（Step 4 批 2）：Go 接口见 `core/provider`，方法按能力前缀扁平化（`Listeners.Start` →
`StartListener`、`Tasks.Dispatch` → `Dispatch`）；内存 mock 见 `core/provider/mock`。
生产实现由外部 provider 插件提供；C2/流量不在核心仓库交付。

## 一、通用规则

1 每个 provider 是独立插件，自持表与迁移，core 只认接口
2 provider 之间不得直接调用，跨能力协作走 event-contract
3 每个 provider 必须提供一个内存 mock 实现；CI 用 mock 跑通链路，验证「换 provider 不换 core」
4 卸载语义：默认保留表与数据；DROP 必须走显式命令并二次确认
5 provider 事件名统一 `<capability>.<verb>`，经 `Events(from_seq)` 暴露；事件总线桥接已落地
  `core/eventbus`（`PublishProviderEvent` / `Bridge`），C2 六个事件已进 event-contract 枚举；
  WebShell/流量当前接口无 Events，待其实现需要回放时再加类型

## 二、C2 provider

    Listeners: Start(ListenerSpec) (ListenerID, error)
               Stop(ListenerID) error
               List() ([]Listener, error)

    Sessions:  List(filter) ([]Session, error)
               Get(SessionID) (Session, error)
               Kill(SessionID) error

    Tasks:     Dispatch(SessionID, TaskSpec) (TaskID, error)
               Poll(TaskID) (TaskState, error)
               Result(TaskID) (TaskResult, error)

    Payloads:  Build(PayloadSpec) (Artifact, error)
               List() ([]Artifact, error)

    Events:    Stream(from_seq) (<-chan C2Event, error)

    自持表：c2_listeners, c2_sessions, c2_tasks, c2_files, c2_payload_artifacts, c2_profiles, c2_events（七张）

## 三、WebShell provider

    Conn:      Open(ConnSpec) (ConnID, error)
               Close(ConnID) error
               Test(ConnID) (Health, error)

    Exec:      Exec(ConnID, Command) (ExecResult, error)   // stdout/stderr/exit_code

    Files:     List(ConnID, Path) ([]FileEntry, error)
               Read(ConnID, Path) ([]byte, error)
               Write(ConnID, Path, Content) error
               Delete(ConnID, Path) error
               Upload(ConnID, Path, []byte) error

    自持表：webshell_connections, webshell_connection_states（两张）

## 四、流量 provider

    Capture:   Start(filter) (CaptureID, error)
               Stop(CaptureID) error

    Query:     Query(criteria) ([]Flow, error)
               Get(FlowID) (Flow, error)

    Replay:    Replay(FlowID, Mutation) (Flow, error)

    自持表：traffic_evidence_snapshots（一张）

## 五、依赖风险（Step 0 实测，必须在 Step 2 处理）

外键方向违规两条：
    1 finding_traffic_bindings -> traffic_evidence_snapshots（领域表引用插件表，卸载流量插件会破坏漏洞取证关联）
    2 sync_state -> mcp_servers（核心表引用插件表，违反 core 不依赖 plugin 的红线）
处置：改为逻辑关联（去掉外键，保留 id 字段 + 应用层校验），或在 Step 2 反转归属。不得保留现状。
