package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestListModelsProbesAndFallsBack 钉住 POST /api/llm/models 的探测语义：
//  1. OpenAI 路径直接命中并解析 {"data":[{"id":...}]}；
//  2. Anthropic 兼容服务的模型列表只在 OpenAI 路径暴露时，按候选顺序回退命中；
//  3. 缺 API key 时 ok=false 且 error 文案不变。
func TestListModelsProbesAndFallsBack(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("database unavailable (%v) — skipping", err)
	}
	if m.pg == nil {
		t.Skip("postgres unavailable — skipping")
	}
	t.Cleanup(func() { m.Close() })
	td := t.TempDir()
	s := New(context.Background(), m, td, td, td)
	h := s.Handler()
	tok, err := signJWT(s.jwtKey)
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}

	// Fake provider: only /v1/models exists (OpenAI-format path).
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"m1"},{"id":"m2"},{"id":""}]}`))
	}))
	defer upstream.Close()

	do := func(body any) (int, map[string]any) {
		b, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", "/api/llm/models", bytes.NewReader(b))
		r.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	// 1) OpenAI：base_url .../v1 → .../v1/models 命中。
	code, out := do(map[string]any{"provider": "openai", "base_url": upstream.URL + "/v1", "api_key": "k"})
	if code != 200 || out["ok"] != true {
		t.Fatalf("openai probe: code=%d out=%v", code, out)
	}
	models, _ := out["models"].([]any)
	if len(models) != 2 || models[0] != "m1" || models[1] != "m2" {
		t.Fatalf("openai models=%v, want [m1 m2] (empty ids dropped)", models)
	}

	// 2) Anthropic 兼容：/anthropic 前缀不存在该路径，回退到根 /v1/models。
	code, out = do(map[string]any{"provider": "anthropic", "base_url": upstream.URL + "/anthropic", "api_key": "k"})
	if code != 200 || out["ok"] != true {
		t.Fatalf("anthropic fallback: code=%d out=%v", code, out)
	}
	if models, _ := out["models"].([]any); len(models) != 2 {
		t.Fatalf("anthropic fallback models=%v", models)
	}

	// 3) 缺 key（无 profile 兜底）→ ok=false + 原文案。
	code, out = do(map[string]any{"provider": "openai"})
	if code != 200 || out["ok"] != false || out["error"] != "未提供 API Key" {
		t.Fatalf("missing key: code=%d out=%v", code, out)
	}
}
