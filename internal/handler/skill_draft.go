package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"finduai/internal/config"
	"finduai/internal/database"
	"finduai/internal/openai"
	"finduai/internal/skillpackage"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// 缺口驱动生成（P0-2.2）
//
// 候选达阈值后自动（或手动）用 LLM 生成 SKILL.md 草稿，落库到 skill_candidates.draft_body；
// **绝不直接写进 skills_dir**（Eino 每轮按 */SKILL.md 重扫，写进去就立即生效），
// 必须由人工在 UI「从实践项目提炼的 Skill」分类里审核通过后，才转正写入 skills_dir。

const skillDraftSystemPrompt = `你是 FindUAI 的「技能提炼器」。给定一条来自真实渗透实践的候选信号（技能缺口，或某工具反复失败），判断是否值得沉淀为一个可复用技能，并产出 SKILL.md 草稿。
严格只输出一个 JSON 对象，不要前言、不要代码块围栏。字段：
{"capability":"该技能能做到的功能（中文一句话，<=80字）","source_ref":"出处（来自哪个实践场景/信号，中文一句话）","priority":0到100的整数，越大越优先","completion":0到100的整数，草稿完整度自评","body":"SKILL.md 正文（Markdown，不含 front matter；应含适用场景、步骤、命令示例、注意事项）"}
若判断不值得沉淀，则 priority 设 0 且 body 留空。`

func buildSkillDraftUserPrompt(c *database.SkillCandidate) string {
	var b strings.Builder
	b.WriteString("候选信号：\n")
	b.WriteString("- 信号来源：")
	switch c.Source {
	case database.SkillCandidateSourceMiss:
		b.WriteString("技能缺口（模型点名了不存在的技能）\n")
	case database.SkillCandidateSourceToolFailure:
		b.WriteString("工具反复失败（同一工具在本轮被反复失败/拦截）\n")
	default:
		b.WriteString(c.Source + "\n")
	}
	b.WriteString("- 建议技能名（英文 slug）：" + c.SkillName + "\n")
	if c.Evidence != "" {
		b.WriteString("- 证据：" + c.Evidence + "\n")
	}
	b.WriteString(fmt.Sprintf("- 出现次数：%d，评分：%d\n", c.Occurrences, c.Score))
	if c.ConversationID != "" {
		b.WriteString("- 来源会话：" + c.ConversationID + "\n")
	}
	if c.ProjectID != "" {
		b.WriteString("- 关联项目：" + c.ProjectID + "\n")
	}
	b.WriteString("\n请判断并输出 JSON。")
	return b.String()
}

type skillDraftLLMResult struct {
	Capability string `json:"capability"`
	SourceRef  string `json:"source_ref"`
	Priority   int    `json:"priority"`
	Completion int    `json:"completion"`
	Body       string `json:"body"`
}

func stripJSONFence(raw string) string {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```")
		if i := strings.LastIndex(s, "```"); i >= 0 {
			s = s[:i]
		}
	}
	return strings.TrimSpace(s)
}

// generateSkillDraftForCandidate 调 LLM 生成某个候选的 SKILL.md 草稿并落库。
// 返回 (是否写入了草稿, error)。LLM 未配置或判断不值得沉淀时返回 (false, nil)。
func generateSkillDraftForCandidate(ctx context.Context, db *database.DB, cfg *config.Config, logger *zap.Logger, cand *database.SkillCandidate) (bool, error) {
	if db == nil || cfg == nil || cand == nil {
		return false, nil
	}
	llmCfg := cfg.OpenAI
	if strings.TrimSpace(llmCfg.APIKey) == "" || strings.TrimSpace(llmCfg.Model) == "" {
		return false, fmt.Errorf("LLM 未配置（ai.channels 主模型缺少 key/model）")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	callCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	reqBody := map[string]interface{}{
		"model": strings.TrimSpace(llmCfg.Model),
		"messages": []map[string]interface{}{
			{"role": "system", "content": skillDraftSystemPrompt},
			{"role": "user", "content": buildSkillDraftUserPrompt(cand)},
		},
		"temperature":           0.2,
		"max_completion_tokens": 4096,
		"thinking":              map[string]interface{}{"type": "disabled"},
	}
	var resp struct {
		Choices []struct {
			Message struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
			} `json:"message"`
		} `json:"choices"`
	}
	client := openai.NewClient(&llmCfg, nil, logger)
	if err := client.ChatCompletion(callCtx, reqBody, &resp); err != nil {
		return false, err
	}
	if len(resp.Choices) == 0 {
		return false, fmt.Errorf("LLM 无有效响应")
	}
	raw := strings.TrimSpace(resp.Choices[0].Message.Content)
	if raw == "" {
		raw = strings.TrimSpace(resp.Choices[0].Message.ReasoningContent)
	}
	var out skillDraftLLMResult
	if err := json.Unmarshal([]byte(stripJSONFence(raw)), &out); err != nil {
		snippet := raw
		if len(snippet) > 240 {
			snippet = snippet[:240] + "..."
		}
		return false, fmt.Errorf("草稿 JSON 解析失败: %w; snippet=%s", err, snippet)
	}
	if out.Priority <= 0 || strings.TrimSpace(out.Body) == "" {
		logger.Info("技能提炼判定为不值得沉淀", zap.String("candidate", cand.SkillName))
		return false, nil
	}
	if out.Priority > 100 {
		out.Priority = 100
	}
	if out.Completion < 0 {
		out.Completion = 0
	}
	if out.Completion > 100 {
		out.Completion = 100
	}
	if err := db.SaveSkillDraft(cand.ID, strings.TrimSpace(out.Capability), strings.TrimSpace(out.SourceRef),
		out.Priority, out.Completion, strings.TrimSpace(out.Body), strings.TrimSpace(llmCfg.Model)); err != nil {
		return false, err
	}
	logger.Info("已生成技能草稿", zap.String("candidate", cand.SkillName), zap.Int("priority", out.Priority))
	return true, nil
}

// autoDraftEligibleCandidates 自动为「达阈值且尚无草稿」的候选生成草稿。每轮最多 maxPerRun 条，控制成本。
func (h *AgentHandler) autoDraftEligibleCandidates() {
	if h == nil || h.db == nil || h.config == nil || !h.config.Agent.SkillDraftEffective() {
		return
	}
	threshold := h.config.Agent.SkillDraftThresholdEffective()
	list, err := h.db.LoadSkillCandidates(false)
	if err != nil {
		h.logger.Warn("自动生成技能草稿: 加载候选失败", zap.Error(err))
		return
	}
	const maxPerRun = 3
	generated := 0
	for _, c := range list {
		if generated >= maxPerRun {
			break
		}
		if c.Status != database.SkillCandidateStatusNew || strings.TrimSpace(c.DraftBody) != "" {
			continue
		}
		n := c.Score
		if c.Occurrences > n {
			n = c.Occurrences
		}
		if n < threshold {
			continue
		}
		ok, gerr := generateSkillDraftForCandidate(context.Background(), h.db, h.config, h.logger, c)
		if gerr != nil {
			h.logger.Warn("自动生成技能草稿失败", zap.String("candidate", c.SkillName), zap.Error(gerr))
			continue
		}
		if ok {
			generated++
		}
	}
}

// ---------------- HTTP 端点（SkillsHandler）----------------

// GetSkillCandidate GET /api/skills/candidates/:id —— 单条候选详情（含草稿全文）。
func (h *SkillsHandler) GetSkillCandidate(c *gin.Context) {
	if h.db == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "数据库未就绪"})
		return
	}
	cand, err := h.db.GetSkillCandidate(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if cand == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "候选不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"candidate": skillCandidateJSON(cand, true)})
}

// PostSkillCandidateDraft POST /api/skills/candidates/:id/draft —— 手动触发生成草稿。
func (h *SkillsHandler) PostSkillCandidateDraft(c *gin.Context) {
	if h.db == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "数据库未就绪"})
		return
	}
	id := c.Param("id")
	cand, err := h.db.GetSkillCandidate(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if cand == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "候选不存在"})
		return
	}
	ok, gerr := generateSkillDraftForCandidate(c.Request.Context(), h.db, h.config, h.logger, cand)
	if gerr != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "生成草稿失败: " + gerr.Error()})
		return
	}
	if !ok {
		c.JSON(http.StatusOK, gin.H{"message": "模型判断该候选暂不值得沉淀（未生成草稿）"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "草稿已生成"})
}

// ApproveSkillCandidate POST /api/skills/candidates/:id/approve —— 人工审核通过，写入 skills_dir。
func (h *SkillsHandler) ApproveSkillCandidate(c *gin.Context) {
	if h.db == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "数据库未就绪"})
		return
	}
	id := c.Param("id")
	cand, err := h.db.GetSkillCandidate(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if cand == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "候选不存在"})
		return
	}
	if strings.TrimSpace(cand.DraftBody) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "该候选还没有草稿，请先生成草稿"})
		return
	}
	name := strings.TrimSpace(cand.SkillName)
	if !isValidSkillName(name) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "建议技能名不合法（须为小写字母、数字、连字符）：" + name})
		return
	}
	desc := strings.TrimSpace(cand.Capability)
	if desc == "" {
		desc = "从实践项目提炼的技能：" + name
	}
	manifest := &skillpackage.SkillManifest{Name: name, Description: desc}
	skillMD, err := skillpackage.BuildSkillMD(manifest, cand.DraftBody)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := skillpackage.ValidateSkillMDPackage(skillMD, name); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "草稿校验失败: " + err.Error()})
		return
	}
	skillDir := filepath.Join(h.skillsRootAbs(), name)
	if _, statErr := os.Stat(filepath.Join(skillDir, "SKILL.md")); statErr == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "同名技能已存在，请在草稿里改名或先删除已有技能"})
		return
	}
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建技能目录失败: " + err.Error()})
		return
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), skillMD, 0644); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "写入 SKILL.md 失败: " + err.Error()})
		return
	}
	reviewer := strings.TrimSpace(c.GetHeader("X-User-Id"))
	if err := h.db.UpdateSkillCandidateStatus(id, database.SkillCandidateStatusApproved, reviewer); err != nil {
		h.logger.Warn("技能已写入但状态更新失败", zap.Error(err), zap.String("id", id))
	}
	h.logger.Info("技能候选审核通过并转正", zap.String("skill", name), zap.String("dir", skillDir))
	if h.audit != nil {
		h.audit.RecordOK(c, "skill", "promote", "从实践提炼技能", "skill", name, nil)
	}
	c.JSON(http.StatusOK, gin.H{"message": "已通过并写入 skills_dir（下次运行即生效）", "skill": name})
}

// RejectSkillCandidate POST /api/skills/candidates/:id/reject —— 人工驳回。
func (h *SkillsHandler) RejectSkillCandidate(c *gin.Context) {
	if h.db == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "数据库未就绪"})
		return
	}
	id := c.Param("id")
	if err := h.db.UpdateSkillCandidateStatus(id, database.SkillCandidateStatusRejected, strings.TrimSpace(c.GetHeader("X-User-Id"))); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "已驳回"})
}

func skillCandidateJSON(c *database.SkillCandidate, withBody bool) gin.H {
	out := gin.H{
		"id":             c.ID,
		"skillName":      c.SkillName,
		"source":         c.Source,
		"conversationId": c.ConversationID,
		"projectId":      c.ProjectID,
		"evidence":       c.Evidence,
		"occurrences":    c.Occurrences,
		"score":          c.Score,
		"status":         c.Status,
		"capability":     c.Capability,
		"sourceRef":      c.SourceRef,
		"priority":       c.Priority,
		"completion":     c.Completion,
		"draftModel":     c.DraftModel,
		"hasDraft":       strings.TrimSpace(c.DraftBody) != "",
		"updatedAt":      c.UpdatedAt,
	}
	if withBody {
		out["draftBody"] = c.DraftBody
	}
	return out
}
