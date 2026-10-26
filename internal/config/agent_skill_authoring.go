package config

// SkillAuthoringEffective 是否允许 agent 自扩展技能包（create_skill / update_skill_file）。
// 未显式配置时视为开启；置 false 可整体关闭这两个写盘工具。
func (c AgentConfig) SkillAuthoringEffective() bool {
	if c.SkillAuthoring == nil {
		return true
	}
	return *c.SkillAuthoring
}
