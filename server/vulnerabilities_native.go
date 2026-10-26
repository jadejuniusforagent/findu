package server

// Step 6d Phase B1：vulnerabilities 九条路由的原生 handler。
// 行为与 findu internal/handler/vulnerability.go 对应入口对齐（校验文案、
// 状态码、响应形状，export 的 Markdown 生成逐段对齐）；数据走宿主
// db/vulnerabilities_native.go 的 PG 原生 store。
//
// 与 findu 的差异（见 STEP6D-AB-HANDOFF）：
//   - RBAC 细粒度（owner/project/conversation 传播过滤）暂不实现，
//     scope=all 管理员行为一致；
//   - 通知 hook（告警投递）暂不接；
//   - create/update/delete/batch-delete 写宿主 audit_logs（与 findu
//     audit.Service 同表同形状）。

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"finduai/db"
)

func (s *Server) vulnerabilityStore() *db.VulnerabilityStore {
	if s.m == nil || s.m.pg == nil {
		return nil
	}
	return s.m.pg.Vulnerabilities()
}

// recordVulnerabilityAudit 写一条漏洞操作审计（best-effort，不阻塞响应）。
func (s *Server) recordVulnerabilityAudit(action, resourceID, message string, detail map[string]interface{}) {
	if s.m == nil || s.m.pg == nil {
		return
	}
	_ = s.m.pg.AuditLogs().Append(&db.AuditLog{
		Category:     "vulnerability",
		Action:       action,
		Result:       "success",
		Actor:        loginUsername,
		ResourceType: "vulnerability",
		ResourceID:   resourceID,
		Message:      message,
		Detail:       detail,
	})
}

// vulnerabilityFilterFromQuery 解析列表/统计/导出/批量删除共用筛选参数。
func vulnerabilityFilterFromQuery(r *http.Request) db.VulnerabilityFilter {
	q := r.URL.Query()
	search := strings.TrimSpace(q.Get("q"))
	if search == "" {
		search = strings.TrimSpace(q.Get("search"))
	}
	return db.VulnerabilityFilter{
		ProjectID:       q.Get("project_id"),
		ID:              q.Get("id"),
		Search:          search,
		ConversationID:  q.Get("conversation_id"),
		Severity:        q.Get("severity"),
		Status:          q.Get("status"),
		TaskID:          q.Get("task_id"),
		ConversationTag: q.Get("conversation_tag"),
		TaskTag:         q.Get("task_tag"),
	}
}

// listVulnerabilitiesNative serves GET /api/vulnerabilities。
func (s *Server) listVulnerabilitiesNative(w http.ResponseWriter, r *http.Request) {
	vs := s.vulnerabilityStore()
	if vs == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	pageStr := q.Get("page")
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	page := 1
	if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
		page = p
		offset = (page - 1) * limit
	}
	filter := vulnerabilityFilterFromQuery(r)
	total, err := vs.CountVulnerabilitiesNative(filter)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	items, err := vs.ListVulnerabilitiesNative(limit, offset, filter)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if items == nil {
		items = []*db.VulnerabilityDTO{}
	}
	totalPages := (total + limit - 1) / limit
	if totalPages == 0 {
		totalPages = 1
	}
	if pageStr == "" {
		page = (offset / limit) + 1
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"vulnerabilities": items,
		"total":           total,
		"page":            page,
		"page_size":       limit,
		"total_pages":     totalPages,
	})
}

// getVulnerabilityNative serves GET /api/vulnerabilities/{id}。
func (s *Server) getVulnerabilityNative(w http.ResponseWriter, r *http.Request) {
	vs := s.vulnerabilityStore()
	if vs == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	v, err := vs.GetVulnerabilityNative(r.PathValue("id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || strings.Contains(err.Error(), "漏洞不存在") {
			writeErr(w, http.StatusNotFound, "漏洞不存在")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// createVulnerabilityNative serves POST /api/vulnerabilities。
func (s *Server) createVulnerabilityNative(w http.ResponseWriter, r *http.Request) {
	vs := s.vulnerabilityStore()
	if vs == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	var req struct {
		ConversationID  string `json:"conversation_id"`
		ProjectID       string `json:"project_id"`
		ConversationTag string `json:"conversation_tag"`
		TaskTag         string `json:"task_tag"`
		Title           string `json:"title"`
		Description     string `json:"description"`
		Severity        string `json:"severity"`
		Status          string `json:"status"`
		Type            string `json:"type"`
		Target          string `json:"target"`
		Preconditions   string `json:"preconditions"`
		ReproSteps      string `json:"reproduction_steps"`
		Evidence        string `json:"evidence"`
		Impact          string `json:"impact"`
		Recommendation  string `json:"recommendation"`
		RetestNotes     string `json:"retest_notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if strings.TrimSpace(req.ConversationID) == "" {
		writeErr(w, http.StatusBadRequest, "conversation_id 必填")
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		writeErr(w, http.StatusBadRequest, "title 必填")
		return
	}
	if strings.TrimSpace(req.Severity) == "" {
		writeErr(w, http.StatusBadRequest, "severity 必填")
		return
	}
	created, err := vs.CreateVulnerabilityNative(&db.VulnerabilityDTO{
		ConversationID:  req.ConversationID,
		ProjectID:       strings.TrimSpace(req.ProjectID),
		ConversationTag: req.ConversationTag,
		TaskTag:         req.TaskTag,
		Title:           req.Title,
		Description:     req.Description,
		Severity:        req.Severity,
		Status:          req.Status,
		Type:            req.Type,
		Target:          req.Target,
		Preconditions:   req.Preconditions,
		ReproSteps:      req.ReproSteps,
		Evidence:        req.Evidence,
		Impact:          req.Impact,
		Recommendation:  req.Recommendation,
		RetestNotes:     req.RetestNotes,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Step 6d Phase R3（缺口④补全）：与 findu 原生创建相同的归属注入
	// （owner_user_id + rbac_resource_assignments）与机器人通知钩子
	// （NotifyVulnerabilityCreated → robotHandler.NotifyNewVulnerability，
	// 无 hook 时为 no-op）。RBAC 细粒度过滤维持「单管理员会话≡scope=all，
	// 过滤为 no-op」的一致性决策，待真实多用户接入再启用。
	if h := s.rtHandles; h.DB != nil {
		if owner := s.finduConsoleActorID(r); owner != "" {
			_ = h.DB.SetResourceOwner("vulnerability", created.ID, owner)
			_ = h.DB.AssignResourceToUser(owner, "vulnerability", created.ID)
		}
		if v, err := h.DB.GetVulnerability(created.ID); err == nil && v != nil {
			h.DB.NotifyVulnerabilityCreated(v)
		}
	}
	s.recordVulnerabilityAudit("create", created.ID, "创建漏洞记录", map[string]interface{}{
		"severity": created.Severity, "title": created.Title,
	})
	writeJSON(w, http.StatusOK, created)
}

// updateVulnerabilityNative serves PUT /api/vulnerabilities/{id}（指针字段
// 部分更新，合并后整体写回，与 findu 一致）。
func (s *Server) updateVulnerabilityNative(w http.ResponseWriter, r *http.Request) {
	vs := s.vulnerabilityStore()
	if vs == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	id := r.PathValue("id")
	existing, err := vs.GetVulnerabilityNative(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "漏洞不存在")
		return
	}
	var req struct {
		ProjectID       *string `json:"project_id"`
		ConversationTag *string `json:"conversation_tag"`
		TaskTag         *string `json:"task_tag"`
		Title           *string `json:"title"`
		Description     *string `json:"description"`
		Severity        *string `json:"severity"`
		Status          *string `json:"status"`
		Type            *string `json:"type"`
		Target          *string `json:"target"`
		Preconditions   *string `json:"preconditions"`
		ReproSteps      *string `json:"reproduction_steps"`
		Evidence        *string `json:"evidence"`
		Impact          *string `json:"impact"`
		Recommendation  *string `json:"recommendation"`
		RetestNotes     *string `json:"retest_notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.ProjectID != nil {
		existing.ProjectID = strings.TrimSpace(*req.ProjectID)
	}
	if req.ConversationTag != nil {
		existing.ConversationTag = *req.ConversationTag
	}
	if req.TaskTag != nil {
		existing.TaskTag = *req.TaskTag
	}
	if req.Title != nil {
		existing.Title = *req.Title
	}
	if req.Description != nil {
		existing.Description = *req.Description
	}
	if req.Severity != nil {
		existing.Severity = *req.Severity
	}
	if req.Status != nil {
		existing.Status = *req.Status
	}
	if req.Type != nil {
		existing.Type = *req.Type
	}
	if req.Target != nil {
		existing.Target = *req.Target
	}
	if req.Preconditions != nil {
		existing.Preconditions = *req.Preconditions
	}
	if req.ReproSteps != nil {
		existing.ReproSteps = *req.ReproSteps
	}
	if req.Evidence != nil {
		existing.Evidence = *req.Evidence
	}
	if req.Impact != nil {
		existing.Impact = *req.Impact
	}
	if req.Recommendation != nil {
		existing.Recommendation = *req.Recommendation
	}
	if req.RetestNotes != nil {
		existing.RetestNotes = *req.RetestNotes
	}
	if err := vs.UpdateVulnerabilityNative(id, existing); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	updated, err := vs.GetVulnerabilityNative(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordVulnerabilityAudit("update", id, "更新漏洞记录", map[string]interface{}{
		"severity": updated.Severity, "status": updated.Status, "project_id": updated.ProjectID,
	})
	writeJSON(w, http.StatusOK, updated)
}

// deleteVulnerabilityNative serves DELETE /api/vulnerabilities/{id}。
func (s *Server) deleteVulnerabilityNative(w http.ResponseWriter, r *http.Request) {
	vs := s.vulnerabilityStore()
	if vs == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	id := r.PathValue("id")
	if err := vs.DeleteVulnerabilityNative(id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "漏洞不存在")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordVulnerabilityAudit("delete", id, "删除漏洞记录", nil)
	writeJSON(w, http.StatusOK, map[string]any{"message": "删除成功"})
}

// batchDeleteVulnerabilitiesNative serves DELETE /api/vulnerabilities/batch。
func (s *Server) batchDeleteVulnerabilitiesNative(w http.ResponseWriter, r *http.Request) {
	vs := s.vulnerabilityStore()
	if vs == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	filter := vulnerabilityFilterFromQuery(r)
	total, err := vs.CountVulnerabilitiesNative(filter)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if total == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"message": "当前筛选条件下没有可删除的漏洞", "deleted": 0})
		return
	}
	deleted, err := vs.DeleteVulnerabilitiesByFilterNative(filter)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordVulnerabilityAudit("delete_batch", "", "批量删除漏洞记录", map[string]interface{}{
		"deleted": deleted,
	})
	writeJSON(w, http.StatusOK, map[string]any{"message": "批量删除成功", "deleted": deleted})
}

// getVulnerabilityStatsNative serves GET /api/vulnerabilities/stats。
func (s *Server) getVulnerabilityStatsNative(w http.ResponseWriter, r *http.Request) {
	vs := s.vulnerabilityStore()
	if vs == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	stats, err := vs.GetVulnerabilityStatsNative(vulnerabilityFilterFromQuery(r))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// getVulnerabilityFilterOptionsNative serves GET /api/vulnerabilities/filter-options。
func (s *Server) getVulnerabilityFilterOptionsNative(w http.ResponseWriter, r *http.Request) {
	vs := s.vulnerabilityStore()
	if vs == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	options, err := vs.GetVulnerabilityFilterOptionsNative()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, options)
}

// exportVulnerabilitiesNative serves GET /api/vulnerabilities/export。
func (s *Server) exportVulnerabilitiesNative(w http.ResponseWriter, r *http.Request) {
	vs := s.vulnerabilityStore()
	if vs == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	groupBy := r.URL.Query().Get("group_by")
	if groupBy == "" {
		groupBy = "conversation"
	}
	mode := r.URL.Query().Get("mode")
	if mode == "" {
		mode = "summary"
	}
	if groupBy != "conversation" && groupBy != "task" {
		writeErr(w, http.StatusBadRequest, "group_by 仅支持 conversation 或 task")
		return
	}
	if mode != "summary" && mode != "split" {
		writeErr(w, http.StatusBadRequest, "mode 仅支持 summary 或 split")
		return
	}
	filter := vulnerabilityFilterFromQuery(r)
	total, err := vs.CountVulnerabilitiesNative(filter)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if total == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"mode": mode, "group_by": groupBy, "total": 0, "files": []any{}})
		return
	}
	items, err := vs.ListVulnerabilitiesNative(total, 0, filter)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	type exportFile struct {
		FileName string `json:"filename"`
		Content  string `json:"content"`
	}
	grouped := map[string][]*db.VulnerabilityDTO{}
	for _, v := range items {
		key := v.ConversationID
		if groupBy == "conversation" {
			if strings.TrimSpace(v.ConversationTag) != "" {
				key = strings.TrimSpace(v.ConversationTag)
			}
		} else {
			key = firstNonEmptyNative(v.TaskTag, v.TaskID, v.TaskQueueID, "unassigned-task")
		}
		grouped[key] = append(grouped[key], v)
	}
	files := make([]exportFile, 0)
	nowStr := time.Now().Format("20060102-150405")
	if mode == "summary" {
		var b strings.Builder
		b.WriteString("# 漏洞批量导出报告\n\n")
		b.WriteString(fmt.Sprintf("- 导出时间: %s\n", time.Now().Format("2006-01-02 15:04:05")))
		b.WriteString(fmt.Sprintf("- 分组维度: %s\n", groupBy))
		b.WriteString(fmt.Sprintf("- 漏洞总数: %d\n", len(items)))
		b.WriteString(fmt.Sprintf("- 分组数: %d\n\n", len(grouped)))
		for group, list := range grouped {
			b.WriteString(fmt.Sprintf("## %s (%d)\n\n", group, len(list)))
			for _, v := range list {
				appendVulnerabilityMarkdownNative(&b, v, "###")
			}
		}
		files = append(files, exportFile{
			FileName: fmt.Sprintf("vulnerability-report-%s-%s.md", groupBy, nowStr),
			Content:  b.String(),
		})
	} else {
		for group, list := range grouped {
			var b strings.Builder
			b.WriteString(fmt.Sprintf("# 漏洞报告 - %s\n\n", group))
			b.WriteString(fmt.Sprintf("- 导出时间: %s\n", time.Now().Format("2006-01-02 15:04:05")))
			b.WriteString(fmt.Sprintf("- 漏洞数量: %d\n\n", len(list)))
			for _, v := range list {
				appendVulnerabilityMarkdownNative(&b, v, "##")
			}
			files = append(files, exportFile{
				FileName: fmt.Sprintf("vulnerability-%s-%s.md", sanitizeExportNameNative(group), nowStr),
				Content:  b.String(),
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"mode":     mode,
		"group_by": groupBy,
		"total":    len(items),
		"files":    files,
	})
}

// appendVulnerabilityMarkdownNative 单条漏洞的 Markdown 片段（与 findu
// appendVulnerabilityMarkdown 逐行对齐，缺省字段不写）。
func appendVulnerabilityMarkdownNative(b *strings.Builder, v *db.VulnerabilityDTO, titleHeading string) {
	b.WriteString(fmt.Sprintf("%s %s\n\n", titleHeading, v.Title))
	b.WriteString(fmt.Sprintf("- 漏洞ID: `%s`\n", v.ID))
	b.WriteString(fmt.Sprintf("- 严重程度: %s\n", v.Severity))
	b.WriteString(fmt.Sprintf("- 状态: %s\n", v.Status))
	if v.Type != "" {
		b.WriteString(fmt.Sprintf("- 类型: %s\n", v.Type))
	}
	if v.Target != "" {
		b.WriteString(fmt.Sprintf("- 目标: %s\n", v.Target))
	}
	b.WriteString(fmt.Sprintf("- 对话ID: `%s`\n", v.ConversationID))
	if v.ConversationTag != "" {
		b.WriteString(fmt.Sprintf("- 对话标签: %s\n", v.ConversationTag))
	}
	if v.TaskTag != "" {
		b.WriteString(fmt.Sprintf("- 任务标签: %s\n", v.TaskTag))
	}
	if v.TaskID != "" {
		b.WriteString(fmt.Sprintf("- 任务ID: `%s`\n", v.TaskID))
	}
	if v.TaskQueueID != "" {
		b.WriteString(fmt.Sprintf("- 任务队列ID: `%s`\n", v.TaskQueueID))
	}
	if !v.CreatedAt.IsZero() {
		b.WriteString(fmt.Sprintf("- 创建时间: %s\n", v.CreatedAt.Format("2006-01-02 15:04:05")))
	}
	if !v.UpdatedAt.IsZero() {
		b.WriteString(fmt.Sprintf("- 更新时间: %s\n", v.UpdatedAt.Format("2006-01-02 15:04:05")))
	}
	if v.Description != "" {
		b.WriteString("\n#### 描述\n\n")
		b.WriteString(v.Description)
		b.WriteString("\n")
	}
	if v.Preconditions != "" {
		b.WriteString("\n#### 前置条件\n\n")
		b.WriteString(v.Preconditions)
		b.WriteString("\n")
	}
	if v.ReproSteps != "" {
		b.WriteString("\n#### 复现步骤\n\n")
		b.WriteString(v.ReproSteps)
		b.WriteString("\n")
	}
	if v.Evidence != "" {
		b.WriteString("\n#### 证据 / POC\n\n```\n")
		b.WriteString(v.Evidence)
		b.WriteString("\n```\n")
	}
	if v.Impact != "" {
		b.WriteString("\n#### 影响\n\n")
		b.WriteString(v.Impact)
		b.WriteString("\n")
	}
	if v.Recommendation != "" {
		b.WriteString("\n#### 修复建议\n\n")
		b.WriteString(v.Recommendation)
		b.WriteString("\n")
	}
	if v.RetestNotes != "" {
		b.WriteString("\n#### 复测方式\n\n")
		b.WriteString(v.RetestNotes)
		b.WriteString("\n")
	}
	b.WriteString("\n")
}

func firstNonEmptyNative(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func sanitizeExportNameNative(raw string) string {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "unknown"
	}
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-", "*", "-", "?", "-", "\"", "-", "<", "-", ">", "-", "|", "-")
	return replacer.Replace(name)
}
