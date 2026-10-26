// Package audit 审计日志：记录谁在何时对什么做了什么，只写不改。
//
// 分层约束：属于 core，禁止 import finduai/domain/*、finduai/plugins/*、finduai/cmd/*。
// 校验：scripts/check-layering.sh（CI 闸门）。
package audit
