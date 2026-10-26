package server

// 模型列表探测（批 4 第五刀自 server_mgmt.go pgListModels 下沉）。
//
// handler 只做 JSON 解码与响应编码；本文件负责 API key 解析（表单值 > profile 存量）、
// 候选端点回退（Anthropic 兼容服务可能只在 OpenAI 路径暴露模型列表）、可选代理与响应解析。

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// modelListRequest 是 POST /api/llm/models 的请求体。
type modelListRequest struct {
	Provider  string `json:"provider"` // "openai" | "anthropic"
	BaseURL   string `json:"base_url"`
	APIKey    string `json:"api_key"`
	Proxy     string `json:"proxy"`
	ProfileID *int64 `json:"profile_id"` // fallback: use stored key from this profile
}

// probeModels returns (models, "") on success — a 200 with an empty list is a
// success too — and ("", errMsg) when no model list could be obtained.
func (s *Server) probeModels(ctx context.Context, req modelListRequest) ([]string, string) {
	// Resolve API key: form input > profile stored key.
	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey == "" && req.ProfileID != nil {
		if p, err := s.m.pg.ProfileByID(*req.ProfileID); err == nil && p != nil {
			apiKey = p.APIKey
		}
	}
	if apiKey == "" {
		return nil, "未提供 API Key"
	}

	baseURL := strings.TrimRight(strings.TrimSpace(req.BaseURL), "/")
	provider := strings.TrimSpace(req.Provider)

	// Candidate endpoints to try in order. Some Anthropic-compatible providers
	// (e.g. DeepSeek) implement /v1/messages under an /anthropic path but expose
	// the model list only on their OpenAI-format path — so for anthropic we fall
	// back to the OpenAI endpoint at the stripped root.
	type candidate struct {
		url string
		hdr http.Header
	}
	bearerHdr := func() http.Header {
		h := http.Header{}
		h.Set("Authorization", "Bearer "+apiKey)
		return h
	}
	anthropicHdr := func() http.Header {
		h := http.Header{}
		h.Set("x-api-key", apiKey)
		h.Set("anthropic-version", "2023-06-01")
		return h
	}

	var candidates []candidate
	switch provider {
	case "openai", "openai-responses":
		// Responses API shares the OpenAI model list at /v1/models; tolerate a full
		// endpoint URL of either format.
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
		b := strings.TrimRight(strings.TrimSuffix(strings.TrimSuffix(baseURL, "/chat/completions"), "/responses"), "/")
		candidates = append(candidates, candidate{b + "/models", bearerHdr()})
	default: // anthropic
		if baseURL == "" {
			baseURL = "https://api.anthropic.com"
		}
		b := strings.TrimRight(strings.TrimSuffix(baseURL, "/v1/messages"), "/")
		candidates = append(candidates, candidate{b + "/v1/models", anthropicHdr()})
		// Fallback for Anthropic-compatible third parties whose model list lives on
		// the OpenAI path: strip a trailing /anthropic and try the OpenAI endpoint.
		if root := strings.TrimRight(strings.TrimSuffix(b, "/anthropic"), "/"); root != b {
			candidates = append(candidates,
				candidate{root + "/models", bearerHdr()},
				candidate{root + "/v1/models", bearerHdr()},
			)
		}
	}

	// Build HTTP client with optional proxy.
	transport := &http.Transport{}
	if p := strings.TrimSpace(req.Proxy); p != "" {
		if pu, err := url.Parse(p); err == nil {
			transport.Proxy = http.ProxyURL(pu)
		}
	}
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second}

	// Try each candidate; return the first that yields a non-empty model list.
	var lastErr string
	emptyOK := false
	for _, c := range candidates {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
		if err != nil {
			lastErr = "构建请求失败: " + err.Error()
			continue
		}
		httpReq.Header = c.hdr
		resp, err := client.Do(httpReq)
		if err != nil {
			lastErr = "请求失败: " + err.Error()
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Sprintf("API 返回 %d: %s", resp.StatusCode, string(body[:min(len(body), 512)]))
			continue
		}
		// Both OpenAI and Anthropic return {"data": [{"id": "..."},...]}.
		var parsed struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &parsed); err != nil {
			lastErr = "解析响应失败: " + err.Error()
			continue
		}
		models := make([]string, 0, len(parsed.Data))
		for _, m := range parsed.Data {
			if m.ID != "" {
				models = append(models, m.ID)
			}
		}
		if len(models) > 0 {
			return models, ""
		}
		emptyOK = true // 200 but no model ids — keep trying other candidates
	}
	if emptyOK {
		return []string{}, ""
	}
	if lastErr == "" {
		lastErr = "未获取到模型列表"
	}
	return nil, lastErr
}
