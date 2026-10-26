// Package rbac 权限：唯一决定能不能做的地方；agent 角色不承载权限。
//
// 分层约束：属于 core，禁止 import finduai/domain/*、finduai/plugins/*、finduai/cmd/*。
// 校验：scripts/check-layering.sh（CI 闸门）。
package rbac
