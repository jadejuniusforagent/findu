// Package llm 模型客户端：provider 中立，只暴露 InvokeModel 语义。
//
// 分层约束：属于 core，禁止 import finduai/domain/*、finduai/plugins/*、finduai/cmd/*。
// 校验：scripts/check-layering.sh（CI 闸门）。
package llm
