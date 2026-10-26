// Package log 结构化日志：core 内统一输出，禁止各包自行 log.Printf。
//
// 分层约束：属于 core，禁止 import finduai/domain/*、finduai/plugins/*、finduai/cmd/*。
// 校验：scripts/check-layering.sh（CI 闸门）。
package log
