# ui-bff-contract（第 9 份契约）

状态：已起草。回应第 8 条：前端收敛为一套，BFF 是唯一入口。

## 一、原则

1 前端（控制台 + 收敛后的主界面）只允许调用 BFF 端点，不直接调用 runtime、不直接调用插件路由
2 BFF 负责三件事：认证与权限、runtime 适配（调用哪个 adapter）、错误与分页规范化
3 换 adapter 时 BFF 的对外形状不变；换 BFF 时前端不动

## 二、端点分组（对外稳定面）

    /api/bff/sessions            会话列表、创建、关闭
    /api/bff/sessions/{id}/turns 回合追加与列表
    /api/bff/sessions/{id}/stream SSE 事件流（转发 runtime-spi 的 StreamEvents）
    /api/bff/tools               工具目录与调用（调用走 runtime-spi 的 CallTool）
    /api/bff/tasks               任务列表与状态
    /api/bff/assets              资产查询与范围
    /api/bff/findings            漏洞查询与状态流转
    /api/bff/reports             报告列表、生成、下载
    /api/bff/admin/*             配置、RBAC、插件管理（插件启停在此）

## 三、与 runtime-spi 的映射

    BFF 端点                      runtime-spi 方法
    /sessions 创建                CreateSession
    /sessions/{id}/turns 追加     AppendTurn
    /sessions/{id}/stream         StreamEvents
    /tools 调用                   CallTool
    模型调用（内部）              InvokeModel
    规划（内部）                  Plan
    恢复（内部）                  Snapshot / Restore
    能力探测                      Health

## 四、错误与分页

    统一响应：{ code, message, data, trace_id }
    code 取值：ok / unauthorized / forbidden / not_found / conflict / unavailable / internal
    unavailable 专供「插件未安装」场景，前端据此渲染能力未安装页
    分页：{ items, next_cursor }

## 五、现有 326 条路由的收敛策略

    1 前端有引用 67 条：逐条映射到上面的 BFF 端点，映射表在 Step 6a 产出
    2 无前端引用 259 条：标记 internal，不进入 BFF，不对外承诺稳定性
    3 插件自有路由：统一挂 /api/bff/plugins/{plugin_id}/*，由 BFF 转发并校验权限
    4 收敛期允许旧路径继续工作，但不得新增调用方；旧路径在 Step 6c 删除
