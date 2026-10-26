package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"finduai/acllm"
	"finduai/internal/config"
	workflowrunner "finduai/internal/workflow"

	"go.uber.org/zap"
)

// workflowDryRunRequest 与 findu workflowDryRunRequest 对齐。
type workflowDryRunRequest struct {
	Graph     json.RawMessage   `json:"graph,omitempty"`
	GraphJSON json.RawMessage   `json:"graph_json,omitempty"`
	Inputs    map[string]any    `json:"inputs,omitempty"`
}

// workflowGenerateDraftRequest 与 findu workflowGenerateDraftRequest 对齐。
type workflowGenerateDraftRequest struct {
	Prompt         string                      `json:"prompt"`
	Options        workflowrunner.DraftOptions `json:"options"`
	AvailableTools []workflowrunner.DraftTool  `json:"available_tools,omitempty"`
}

// hostProviderForFormat 把宿主 agent.Config.Format 映射为 OpenAIConfig.Provider。
func hostProviderForFormat(f acllm.Format) string {
	switch f {
	case acllm.FormatAnthropic:
		return "claude"
	default: // FormatOpenAI / FormatOpenAIResponses
		return "openai"
	}
}

// workflowDryRunNative serves POST /api/workflows/dry-run：
// findu WorkflowHandler.DryRun 的宿主原生实现（纯函数，无运行态依赖）。
func (s *Server) workflowDryRunNative(w http.ResponseWriter, r *http.Request) {
	var req workflowDryRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "无效的请求参数: "+err.Error())
		return
	}
	graph := req.Graph
	if len(graph) == 0 {
		graph = req.GraphJSON
	}
	if len(graph) == 0 || !json.Valid(graph) {
		writeErr(w, http.StatusBadRequest, "graph 必须是合法 JSON")
		return
	}
	inputs := make(map[string]any, len(req.Inputs))
	for k, v := range req.Inputs {
		inputs[k] = v
	}
	result, err := workflowrunner.DryRunGraphJSON(r.Context(), string(graph), inputs)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": result})
}

// workflowGenerateDraftNative serves POST /api/workflows/generate-draft：
// findu WorkflowHandler.GenerateDraft 的宿主原生实现（用宿主 LLM 配置构造 AI 通道）。
func (s *Server) workflowGenerateDraftNative(w http.ResponseWriter, r *http.Request) {
	var req workflowGenerateDraftRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "无效的请求参数: "+err.Error())
		return
	}
	draftReq := workflowrunner.DraftRequest{
		Prompt:         req.Prompt,
		Options:        req.Options,
		AvailableTools: req.AvailableTools,
	}
	cfg, ok := s.loadLLMConfig()
	if !ok || strings.TrimSpace(cfg.APIKey) == "" || strings.TrimSpace(cfg.Model) == "" {
		writeErr(w, http.StatusBadGateway, "大模型生成失败: AI 通道未配置 api_key 或 model")
		return
	}
	oa := config.OpenAIConfig{
		Provider: hostProviderForFormat(cfg.Format),
		APIKey:   cfg.APIKey,
		BaseURL:  cfg.BaseURL,
		Model:    cfg.Model,
	}
	result, err := workflowrunner.GenerateDraftFromLLM(r.Context(), draftReq, oa, zap.NewNop())
	if err != nil {
		writeErr(w, http.StatusBadGateway, "大模型生成失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": result})
}