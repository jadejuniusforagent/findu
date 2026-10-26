package handler

import (
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"

	"finduai/internal/database"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// 任务结束自动蒸馏（P0-2.1）
//
// 目标：让「进化闭环」的度量侧每轮自动产出，而不是只靠模型主动 miss。
// 在每轮 run 结束时，从本轮窗口里抽取两类信号并落成「技能候选」：
//   1. 技能缺口：模型点名 skill 但技能不存在（skill_stats.miss 在本轮更新过）
//   2. 工具用法：同一工具在本轮失败/被拦截 >= 2 次（考虑沉淀该工具的用法技能）
//
// 边界：只写遥测表 skill_candidates，不改 agent 行为、不碰中间件链；
// 任何错误只记日志，绝不影响任务收尾。候选随后可由 2.2（缺口驱动生成）消费。

// distillMinToolFailures 同工具在本轮内失败/被拦截达到该次数才记为候选。
const distillMinToolFailures = 2

func slugifySkillName(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	var b strings.Builder
	lastDash := false
	for _, r := range raw {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "unknown"
	}
	if len(out) > 80 {
		out = out[:80]
	}
	return out
}

func (h *AgentHandler) distillSkillCandidates(conversationID, projectID string, since time.Time) {
	if h == nil || h.db == nil || h.config == nil {
		return
	}
	if !h.config.Agent.SkillDistillEffective() {
		return
	}
	if since.IsZero() {
		since = time.Now().Add(-1 * time.Hour)
	}

	// 信号 1：本轮技能缺口
	misses, err := h.db.LoadSkillMisses()
	if err != nil {
		h.logger.Warn("蒸馏技能候选: 读取缺口榜失败", zap.Error(err))
	} else {
		for _, m := range misses {
			if m == nil || m.LastMissTime == nil || m.LastMissTime.Before(since) {
				continue
			}
			if err := h.db.UpsertSkillCandidate(&database.SkillCandidate{
				SkillName:      m.SkillName,
				Source:         database.SkillCandidateSourceMiss,
				ConversationID: conversationID,
				ProjectID:      projectID,
				Evidence: fmt.Sprintf(
					"本轮模型调用 skill %q 但该技能不存在（累计 miss=%d）。",
					m.SkillName, m.MissCalls),
				Occurrences: 1,
				Score:       m.MissCalls,
			}); err != nil {
				h.logger.Warn("蒸馏技能候选(缺口)失败", zap.String("skill", m.SkillName), zap.Error(err))
			}
		}
	}

	// 信号 2：本轮同工具反复失败/被拦截
	fails, ferr := h.db.LoadConversationToolFailures(conversationID, since, distillMinToolFailures)
	if ferr != nil {
		h.logger.Warn("蒸馏技能候选: 读取工具失败聚合失败", zap.Error(ferr))
	} else {
		for _, f := range fails {
			sample := strings.TrimSpace(f.LastError)
			if len(sample) > 200 {
				sample = sample[:200]
			}
			if sample == "" {
				sample = "(无错误文本)"
			}
			if err := h.db.UpsertSkillCandidate(&database.SkillCandidate{
				SkillName:      "tool-usage-" + slugifySkillName(f.ToolName),
				Source:         database.SkillCandidateSourceToolFailure,
				ConversationID: conversationID,
				ProjectID:      projectID,
				Evidence: fmt.Sprintf(
					"本轮工具 %s 失败/被拦截 %d 次，考虑沉淀该工具的用法技能；最近错误：%s",
					f.ToolName, f.Count, sample),
				Occurrences: f.Count,
				Score:       f.Count,
			}); err != nil {
				h.logger.Warn("蒸馏技能候选(工具失败)失败", zap.String("tool", f.ToolName), zap.Error(err))
			}
		}
	}

	// 2.2 缺口驱动生成：达阈值且尚无草稿的候选，后台自动生成草稿（默认关闭，失败不影响收尾）
	if h.config.Agent.SkillDraftEffective() {
		go h.autoDraftEligibleCandidates()
	}
}

// GetSkillCandidates GET /api/skills/candidates
// 候选榜（默认排除 rejected 项），按 score / occurrences 降序。
func (h *SkillsHandler) GetSkillCandidates(c *gin.Context) {
	if h.db == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "数据库未就绪"})
		return
	}
	includeRejected := c.Query("include_rejected") == "1" || c.Query("includeRejected") == "1"
	list, err := h.db.LoadSkillCandidates(includeRejected)
	if err != nil {
		h.logger.Error("加载技能候选榜失败", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	items := make([]gin.H, 0, len(list))
	for _, it := range list {
		items = append(items, skillCandidateJSON(it, false))
	}
	c.JSON(http.StatusOK, gin.H{"candidates": items, "count": len(items)})
}

// ClearSkillCandidates DELETE /api/skills/candidates
func (h *SkillsHandler) ClearSkillCandidates(c *gin.Context) {
	if h.db == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "数据库未就绪"})
		return
	}
	if err := h.db.ClearSkillCandidates(); err != nil {
		h.logger.Error("清空技能候选榜失败", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "技能候选榜已清空"})
}
