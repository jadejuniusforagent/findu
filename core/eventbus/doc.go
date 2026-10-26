// Package eventbus 事件总线：按 contracts/event-contract.schema.json 投递，至少一次语义。
//
// 已实现（Step 4 收尾刀）：
//
//	1 Envelope：id/type/ts/seq 必填，type 与 schema 枚举双向对齐（测试读契约比对）
//	2 Bus：Publish/Subscribe/Close；seq 由总线分配，投递失败返回 ErrSlowConsumer
//	   供调用方重试（重复由 idempotency_key 去重）
//	3 Filter：按事件类型与会话 id 过滤，零值全收
//	4 Provider 桥接：core/provider.Event → Envelope（type=kind、payload=data、
//	   id=source:seq），Bridge 持续转发 Events channel
//
// 不提供跨进程/重启持久化：进程退出即丢失未消费事件；需要回放的场景由
// provider 自己的 Events(fromSeq) 或数据库表承担。
//
// 分层约束：属于 core，禁止 import finduai/domain/*、finduai/plugins/*、finduai/cmd/*。
// 校验：cmd/layercheck（CI 闸门）。
package eventbus
