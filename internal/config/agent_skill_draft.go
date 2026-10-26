package config

// SkillDraftEffective 是否允许「缺口驱动生成」（2.2）：候选达阈值时自动用 LLM 生成 SKILL.md 草稿。
// 默认关闭（会调用 LLM 消耗 token）；nil 视为关闭，须显式置 true 才开启。
func (c AgentConfig) SkillDraftEffective() bool {
	if c.SkillDraft == nil {
		return false
	}
	return *c.SkillDraft
}

// SkillDraftThresholdEffective 候选触发自动生成草稿的阈值（score 或 occurrences 取大者 >= 阈值）。
// 0 或负值取默认 3。
func (c AgentConfig) SkillDraftThresholdEffective() int {
	if c.SkillDraftThreshold <= 0 {
		return 3
	}
	return c.SkillDraftThreshold
}
