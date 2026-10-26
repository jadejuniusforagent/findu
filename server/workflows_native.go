package server

// Step 6d Phase B5：workflows 10 条原生。
//
// 定义侧 CRUD（5）+ 校验/导出/运行读侧（5）：
//   - List/Get 不带 RBAC 过滤（宿主 Bearer 等价 scope=all 管理员，B1 同记录）；
//   - save（Create/Update）复用同进程 finduai/internal/workflow 的
//     ValidateGraphJSON + InvalidateCompiledCache（包级单例，与 findu handler
//     同语义——同一 compiled cache 被清空）；audit category=workflow；
//   - validate 同上纯函数；export 复用 internal/workflow/package.Export
//     （纯函数，ETag/X-Workflow-Package-SHA256 头镜像）；
//   - list pending runs（awaiting_hitl）/ get run（run+nodeRuns）/ replay
//     （纯 DB 步骤投影）为 PG 读侧；
//   - 兼容保留：DryRun（进程内执行）、GenerateDraft（LLM）、ResumeRun
//     （agent 运行时续跑）——见交接表 B5 行。

import (
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"strings"
	"sync"

	workflowrunner "finduai/internal/workflow"
	workflowpkg "finduai/internal/workflow/package"

	"finduai/db"
)

// wfOnce/wfStore 进程级懒加载 PG workflow store（与 kbStore 同模式）。
var (
	wfOnce  sync.Once
	wfStore *db.WorkflowStore
	wfErr   error
)

// wfStore 返回 workflow store（false = 打开失败，handler 映射 500）。
func (s *Server) wfStore() (*db.WorkflowStore, bool) {
	wfOnce.Do(func() {
		if s.m == nil || s.m.pg == nil {
			wfErr = fmt.Errorf("PG 数据面未可用")
			return
		}
		wfStore = s.m.pg.Workflows()
	})
	return wfStore, wfErr == nil && wfStore != nil
}

type workflowSaveRequest struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Version     int             `json:"version,omitempty"`
	Enabled     *bool           `json:"enabled,omitempty"`
	Graph       json.RawMessage `json:"graph,omitempty"`
	GraphJSON   json.RawMessage `json:"graph_json,omitempty"`
}

// workflowSaveBody 保存（POST/PUT 共用，pathID 非空表示 PUT 路径 id 优先）。
func (s *Server) workflowSaveBody(w http.ResponseWriter, r *http.Request, pathID string) {
	var req workflowSaveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "无效的请求参数: "+err.Error())
		return
	}
	store, ok := s.wfStore()
	if !ok {
		// 存储不可用属内部错误；findu 此处不存在 CSV 分支，直接 500。
		writeErr(w, http.StatusInternalServerError, "工作流数据库不可用")
		return
	}
	id := strings.TrimSpace(req.ID)
	if strings.TrimSpace(pathID) != "" {
		id = strings.TrimSpace(pathID)
	}
	name := strings.TrimSpace(req.Name)
	if id == "" || name == "" {
		writeErr(w, http.StatusBadRequest, "工作流 id 和 name 不能为空")
		return
	}
	graph := req.Graph
	if len(graph) == 0 {
		graph = req.GraphJSON
	}
	if len(graph) == 0 {
		graph = []byte(`{"nodes":[],"edges":[],"config":{}}`)
	}
	if !json.Valid(graph) {
		writeErr(w, http.StatusBadRequest, "graph 必须是合法 JSON")
		return
	}
	if err := workflowrunner.ValidateGraphJSON(r.Context(), string(graph)); err != nil {
		writeErr(w, http.StatusBadRequest, "工作流图无法编译: "+err.Error())
		return
	}
	var probe any
	if err := json.Unmarshal(graph, &probe); err != nil {
		writeErr(w, http.StatusBadRequest, "graph JSON 解析失败: "+err.Error())
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	wf := &db.WorkflowDefinition{
		ID:          id,
		Name:        name,
		Description: strings.TrimSpace(req.Description),
		Version:     req.Version,
		GraphJSON:   string(graph),
		Enabled:     enabled,
	}
	if err := store.WorkflowDefinitionUpsert(r.Context(), wf); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	saved, _ := store.WorkflowDefinitionByID(r.Context(), id)
	workflowrunner.InvalidateCompiledCache(id)
	if s.m != nil && s.m.pg != nil {
		_ = s.m.pg.AuditLogs().Append(&db.AuditLog{
			Category:     "workflow",
			Action:       "save",
			Result:       "success",
			Actor:        loginUsername,
			ResourceType: "workflow",
			ResourceID:   id,
			Message:      "保存工作流",
			Detail:       map[string]any{"name": name},
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "工作流已保存", "workflow": saved})
}

// listWorkflowsNative serves GET /api/workflows。
func (s *Server) listWorkflowsNative(w http.ResponseWriter, r *http.Request) {
	store, ok := s.wfStore()
	if !ok {
		writeErr(w, http.StatusInternalServerError, "工作流数据库不可用")
		return
	}
	includeDisabled := strings.EqualFold(r.URL.Query().Get("includeDisabled"), "true") || r.URL.Query().Get("include_disabled") == "1"
	items, err := store.WorkflowDefinitions(r.Context(), includeDisabled)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflows": items})
}

// getWorkflowNative serves GET /api/workflows/{id}。
func (s *Server) getWorkflowNative(w http.ResponseWriter, r *http.Request) {
	store, ok := s.wfStore()
	if !ok {
		writeErr(w, http.StatusInternalServerError, "工作流数据库不可用")
		return
	}
	wf, err := store.WorkflowDefinitionByID(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if wf == nil {
		writeErr(w, http.StatusNotFound, "工作流不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflow": wf})
}

// workflowCreateNative serves POST /api/workflows（save，路径 id 为空）。
func (s *Server) workflowCreateNative(w http.ResponseWriter, r *http.Request) {
	s.workflowSaveBody(w, r, "")
}

// workflowUpdateNative serves PUT /api/workflows/{id}（save，路径 id 优先）。
func (s *Server) workflowUpdateNative(w http.ResponseWriter, r *http.Request) {
	s.workflowSaveBody(w, r, r.PathValue("id"))
}

// deleteWorkflowNative serves DELETE /api/workflows/{id}。
func (s *Server) deleteWorkflowNative(w http.ResponseWriter, r *http.Request) {
	store, ok := s.wfStore()
	if !ok {
		writeErr(w, http.StatusInternalServerError, "工作流数据库不可用")
		return
	}
	id := r.PathValue("id")
	if err := store.WorkflowDefinitionDelete(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	workflowrunner.InvalidateCompiledCache(id)
	if s.m != nil && s.m.pg != nil {
		_ = s.m.pg.AuditLogs().Append(&db.AuditLog{
			Category:     "workflow",
			Action:       "delete",
			Result:       "success",
			Actor:        loginUsername,
			ResourceType: "workflow",
			ResourceID:   id,
			Message:      "删除工作流",
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "工作流已删除"})
}

// validateWorkflowNative serves POST /api/workflows/validate（不落库）。
func (s *Server) validateWorkflowNative(w http.ResponseWriter, r *http.Request) {
	var req workflowSaveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "无效的请求参数: " + err.Error()})
		return
	}
	graph := req.Graph
	if len(graph) == 0 {
		graph = req.GraphJSON
	}
	if len(graph) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "graph 不能为空"})
		return
	}
	if !json.Valid(graph) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "graph 必须是合法 JSON"})
		return
	}
	if err := workflowrunner.ValidateGraphJSON(r.Context(), string(graph)); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// listWorkflowPendingRunsNative serves GET /api/workflows/runs/pending（awaiting_hitl）。
func (s *Server) listWorkflowPendingRunsNative(w http.ResponseWriter, r *http.Request) {
	store, ok := s.wfStore()
	if !ok {
		writeErr(w, http.StatusInternalServerError, "工作流数据库不可用")
		return
	}
	conversationID := strings.TrimSpace(r.URL.Query().Get("conversationId"))
	runs, err := store.WorkflowRunsAwaitingHITL(r.Context(), conversationID, 50)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// findu 经 filterSlice（scope=all 管理员逐条放行）；保持非 nil 以输出 "runs": []。
	if runs == nil {
		runs = []*db.WorkflowRun{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

// getWorkflowRunNative serves GET /api/workflows/runs/{runId}。
func (s *Server) getWorkflowRunNative(w http.ResponseWriter, r *http.Request) {
	s.workflowRunGet(w, r, r.PathValue("runId"))
}

// workflowRunPackageNative serves GET /api/workflows/runs/package —— Go 1.22
// 注册消歧需要显式登记该面；gin 静态优先会把 "package" 落到 runs/:runId 参数，
// 因此与 findu 一致按 runID="package" 查询（通常 404 工作流运行不存在）。
func (s *Server) workflowRunPackageNative(w http.ResponseWriter, r *http.Request) {
	s.workflowRunGet(w, r, "package")
}

// workflowRunGet run+nodeRuns 投影共用体。
func (s *Server) workflowRunGet(w http.ResponseWriter, r *http.Request, runID string) {
	store, ok := s.wfStore()
	if !ok {
		writeErr(w, http.StatusInternalServerError, "工作流数据库不可用")
		return
	}
	run, err := store.WorkflowRunByID(r.Context(), runID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if run == nil {
		writeErr(w, http.StatusNotFound, "工作流运行不存在")
		return
	}
	nodeRuns, err := store.WorkflowNodeRuns(r.Context(), run.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": run, "nodeRuns": nodeRuns})
}

// replayWorkflowRunNative serves GET /api/workflows/runs/{runId}/replay（纯读投影）。
func (s *Server) replayWorkflowRunNative(w http.ResponseWriter, r *http.Request) {
	store, ok := s.wfStore()
	if !ok {
		writeErr(w, http.StatusInternalServerError, "工作流数据库不可用")
		return
	}
	runID := r.PathValue("runId")
	nodeRuns, err := store.WorkflowNodeRuns(r.Context(), runID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	steps := make([]map[string]any, 0, len(nodeRuns))
	for i, nodeRun := range nodeRuns {
		var input any
		var output any
		_ = json.Unmarshal([]byte(nodeRun.InputJSON), &input)
		_ = json.Unmarshal([]byte(nodeRun.OutputJSON), &output)
		steps = append(steps, map[string]any{
			"step":       i + 1,
			"nodeRunId":  nodeRun.ID,
			"nodeId":     nodeRun.NodeID,
			"status":     nodeRun.Status,
			"input":      input,
			"output":     output,
			"error":      nodeRun.Error,
			"startedAt":  nodeRun.StartedAt,
			"finishedAt": nodeRun.FinishedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflowRunId": runID, "steps": steps})
}

// exportWorkflowPackageNative serves GET /api/workflows/{id}/package（zip 附件）。
func (s *Server) exportWorkflowPackageNative(w http.ResponseWriter, r *http.Request, id string) {
	store, ok := s.wfStore()
	if !ok {
		writeWorkflowPackageError(w, http.StatusInternalServerError, "WFPKG_EXPORT_FAILED", "导出工作流包失败")
		return
	}
	wf, err := store.WorkflowDefinitionByID(r.Context(), id)
	if err != nil {
		writeWorkflowPackageError(w, http.StatusInternalServerError, "WFPKG_EXPORT_FAILED", "导出工作流包失败")
		return
	}
	if wf == nil {
		writeWorkflowPackageError(w, http.StatusNotFound, "WFPKG_WORKFLOW_NOT_FOUND", "工作流不存在")
		return
	}
	pkg, meta, err := workflowpkg.Export(workflowpkg.Document{
		ID: wf.ID, Name: wf.Name, Description: wf.Description,
		Version: wf.Version, GraphJSON: wf.GraphJSON, Enabled: wf.Enabled,
		UpdatedAt: wf.UpdatedAt,
	})
	if err != nil {
		writeWorkflowPackageError(w, http.StatusInternalServerError, "WFPKG_EXPORT_FAILED", "导出工作流包失败")
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": meta.FileName}))
	w.Header().Set("ETag", `"`+meta.PackageHash+`"`)
	w.Header().Set("X-Workflow-Package-SHA256", meta.PackageHash)
	if s.m != nil && s.m.pg != nil {
		_ = s.m.pg.AuditLogs().Append(&db.AuditLog{
			Category:     "workflow_package",
			Action:       "export",
			Result:       "success",
			Actor:        loginUsername,
			ResourceType: "workflow",
			ResourceID:   wf.ID,
			Message:      "导出工作流包",
			Detail:       map[string]any{"package_hash": meta.PackageHash},
		})
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(pkg)
}

// writeWorkflowPackageError 镜像 writeWorkflowPackageError（简化：错误体 code/message）。
func writeWorkflowPackageError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"code": code, "message": message}})
}

// workflowGetDispatchNative serves GET /api/workflows/…（托底分发）。
// 命中 {id}/package 形态走原生包导出；其余未注册 GET 回 findu 兼容。
func (s *Server) workflowGetDispatchNative(w http.ResponseWriter, r *http.Request) {
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/api/workflows/"), "/", 3)
	if len(parts) == 2 && parts[1] == "package" && parts[0] != "" && parts[0] != "runs" {
		s.exportWorkflowPackageNative(w, r, parts[0])
		return
	}
	s.serveFinduCompat(w, r)
}
