// Package scheduler 调度：任务排队与并发上限，不关心任务内容。
//
// 分层约束：属于 core，禁止 import finduai/domain/*、finduai/plugins/*、finduai/cmd/*。
// 校验：scripts/check-layering.sh（CI 闸门）。
package scheduler
