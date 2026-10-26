// Package auth 认证：会话与令牌校验，不绑定具体 runtime。
//
// 分层约束：属于 core，禁止 import finduai/domain/*、finduai/plugins/*、finduai/cmd/*。
// 校验：scripts/check-layering.sh（CI 闸门）。
package auth
