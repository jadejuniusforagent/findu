package server

// Step 6d Phase B6：hitl 十七条路由中的四条纯 DB 投影/清理路由（原生）。
//   GET  /api/hitl/pending            — 等待人工审批的中断列表
//   GET  /api/hitl/logs               — 已决策审计日志列表（含筛选/分页）
//   GET  /api/hitl/logs/{id}          — 审计日志详情
//   DELETE /api/hitl/logs             — 批量删除/按筛选清空（不删 pending）
//
// 其余 13 条（decision/dismiss/config/:id/PUT config/tool-whitelist×3/
// default-config/default-reviewer/audit-strategy×2）依赖 findu 进程内
// decideCh 运行态或容器内 config.yaml 真源，继续走 serveFinduCompat（同
// B4/WebShell exec 的运行时收口模式），见 docs/refactor/STEP6D-AB-HANDOFF.md。
//
// 与 findu 的差异：RBAC 细粒度（会话可见性）过滤暂不实现（同 B1），
// 删除与列表按 scope=all 管理员语义执行。

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"finduai/db"
)

func (s *Server) hitlStore() *db.HitlStore {
	if s.m == nil || s.m.pg == nil {
		return nil
	}
	return s.m.pg.Hitl()
}

// recordHitlAudit 写一条 HITL 操作审计（best-effort，同 findu audit.RecordOK）。
func (s *Server) recordHitlAudit(action, message string, detail map[string]interface{}) {
	if s.m == nil || s.m.pg == nil {
		return
	}
	_ = s.m.pg.AuditLogs().Append(&db.AuditLog{
		Category:     "hitl",
		Action:       action,
		Result:       "success",
		Actor:        loginUsername,
		ResourceType: "hitl_interrupt",
		Message:      message,
		Detail:       detail,
	})
}

// listHitlPendingNative serves GET /api/hitl/pending。
func (s *Server) listHitlPendingNative(w http.ResponseWriter, r *http.Request) {
	hs := s.hitlStore()
	if hs == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(q.Get("pageSize"))
	pageSize = hitlPageSize(pageSize)
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	items, total, err := hs.ListPending(ctx, hitlFilterFromQuery(q.Get("conversationId"), q.Get("toolName"),
		q.Get("decision"), q.Get("decidedBy"), q.Get("status"), q.Get("q")),
		pageSize, (page-1)*pageSize)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items, "page": page, "pageSize": pageSize, "total": total,
	})
}

// listHitlLogsNative serves GET /api/hitl/logs。
func (s *Server) listHitlLogsNative(w http.ResponseWriter, r *http.Request) {
	hs := s.hitlStore()
	if hs == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(q.Get("pageSize"))
	pageSize = hitlPageSize(pageSize)
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	items, total, err := hs.ListLogs(ctx, hitlFilterFromQuery(q.Get("conversationId"), q.Get("toolName"),
		q.Get("decision"), q.Get("decidedBy"), q.Get("status"), q.Get("q")),
		pageSize, (page-1)*pageSize)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items, "page": page, "pageSize": pageSize, "total": total,
		"retentionDays": db.RetentionDaysDefault,
	})
}

// getHitlLogNative serves GET /api/hitl/logs/{id}。
func (s *Server) getHitlLogNative(w http.ResponseWriter, r *http.Request) {
	hs := s.hitlStore()
	if hs == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeErr(w, http.StatusBadRequest, "id is required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	item, err := hs.Get(ctx, id)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

// deleteHitlLogsNative serves DELETE /api/hitl/logs。
func (s *Server) deleteHitlLogsNative(w http.ResponseWriter, r *http.Request) {
	hs := s.hitlStore()
	if hs == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	var req struct {
		// findu JSON 字段 ids / all（保持兼容大小写）。
		IDs []string `json:"ids"`
		All bool     `json:"all"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求参数无效: "+err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	var deleted int64
	var err error
	if req.All {
		q := r.URL.Query()
		deleted, err = hs.DeleteWhere(ctx, hitlFilterFromQuery(q.Get("conversationId"), q.Get("toolName"),
			q.Get("decision"), q.Get("decidedBy"), q.Get("status"), q.Get("q")))
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.recordHitlAudit("logs_clear", "清空人机协同审计日志", map[string]interface{}{
			"deleted": deleted,
		})
	} else {
		if len(req.IDs) == 0 {
			writeErr(w, http.StatusBadRequest, "审计日志 ID 列表不能为空")
			return
		}
		deleted, err = hs.DeleteByIDs(ctx, req.IDs)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.recordHitlAudit("logs_delete_batch", "批量删除人机协同审计日志", map[string]interface{}{
			"count":   len(req.IDs),
			"deleted": deleted,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "删除成功", "deleted": deleted})
}

// hitlPageSize 归一化 pageSize（findu：1..200，缺省 20）。
func hitlPageSize(v int) int {
	if v < 1 {
		return 20
	}
	if v > 200 {
		return 200
	}
	return v
}

// hitlFilterFromQuery 组装与 findu appendHitlListFilters 相同的筛选参数。
func hitlFilterFromQuery(conversationID, toolName, decision, decidedBy, status, search string) db.HitlFilter {
	return db.HitlFilter{
		ConversationID: conversationID,
		ToolName:       toolName,
		Decision:       decision,
		DecidedBy:      decidedBy,
		Status:         status,
		Search:         search,
	}
}
