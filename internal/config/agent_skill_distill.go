package config

// SkillDistillEffective 是否在每轮 run 结束后自动蒸馏「技能候选」（P0-2.1）。
//
// 蒸馏只读取本轮的工具执行/技能缺口遥测并写入候选榜，不改变 agent 的任何行为；
// 未显式配置时视为开启（与 skill_authoring 同口径），置 false 可整体关闭。
func (c AgentConfig) SkillDistillEffective() bool {
	if c.SkillDistill == nil {
		return true
	}
	return *c.SkillDistill
}
