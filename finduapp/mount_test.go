package finduapp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	finduconfig "finduai/internal/config"
)

func TestMultiAgentHandlerPromotesBearerToCookie(t *testing.T) {
	var got string
	sub := &Subsystem{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("findu_token"); err == nil {
			got = c.Value
		}
	})}
	req := httptest.NewRequest(http.MethodGet, "/api/multi-agent/markdown-agents", nil)
	req.Header.Set("Authorization", "Bearer console-token")
	sub.MultiAgentHandler().ServeHTTP(httptest.NewRecorder(), req)
	if got != "console-token" {
		t.Fatalf("cookie = %q, want console-token", got)
	}
}

func TestOwnedAPIPathsKeepsMigratedPrefixesOnHost(t *testing.T) {
	for _, p := range OwnedAPIPaths() {
		switch p {
		case "/api/multi-agent":
			t.Fatal("/api/multi-agent must be host-owned")
		case "/api/conversations":
			t.Fatal("/api/conversations migrated to host and must not be findu-owned")
		case "/api/skills":
			t.Fatal("/api/skills migrated to host and must not be findu-owned")
		case "/api/tasks":
			t.Fatal("/api/tasks migrated to host and must not be findu-owned")
		case "/api/assets":
			t.Fatal("/api/assets migrated to host and must not be findu-owned")
		case "/api/audit":
			t.Fatal("/api/audit migrated to host and must not be findu-owned")
		case "/api/projects":
			t.Fatal("/api/projects migrated to host and must not be findu-owned")
		case "/api/webshell":
			t.Fatal("/api/webshell migrated to host and must not be findu-owned")
		}
	}
}

func TestApplyModelOverridePreservesTuning(t *testing.T) {
	cfg := &finduconfig.Config{}
	cfg.OpenAI = finduconfig.OpenAIConfig{
		Provider:            "openai",
		APIKey:              "old-key",
		BaseURL:             "https://old.example/v1",
		Model:               "old-model",
		MaxCompletionTokens: 4096,
	}
	cfg.AI.DefaultChannel = "deepseek"
	cfg.AI.Channels = map[string]finduconfig.AIChannelConfig{
		"deepseek": {APIKey: "old-key", BaseURL: "https://old.example/v1", Model: "old-model"},
	}

	applyModelOverride(cfg, &ModelConfig{
		Provider: "openai",
		APIKey:   "new-key",
		BaseURL:  "https://new.example/v1",
		Model:    "new-model",
	})

	if cfg.OpenAI.APIKey != "new-key" || cfg.OpenAI.BaseURL != "https://new.example/v1" || cfg.OpenAI.Model != "new-model" {
		t.Fatalf("override not applied: %+v", cfg.OpenAI)
	}
	if cfg.OpenAI.MaxCompletionTokens != 4096 {
		t.Fatalf("tuning lost: %+v", cfg.OpenAI)
	}
	ch := cfg.AI.Channels["deepseek"]
	if ch.APIKey != "new-key" || ch.Model != "new-model" {
		t.Fatalf("default channel not updated: %+v", ch)
	}
}

func TestApplyModelOverrideRequiresKey(t *testing.T) {
	cfg := &finduconfig.Config{OpenAI: finduconfig.OpenAIConfig{APIKey: "keep", Model: "keep"}}
	applyModelOverride(cfg, &ModelConfig{Model: "new-model"})
	if cfg.OpenAI.APIKey != "keep" || cfg.OpenAI.Model != "keep" {
		t.Fatalf("override without key should be ignored: %+v", cfg.OpenAI)
	}
}

func TestMultiAgentHandlerKeepsExistingCookie(t *testing.T) {
	var got string
	sub := &Subsystem{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("findu_token"); err == nil {
			got = c.Value
		}
	})}
	req := httptest.NewRequest(http.MethodGet, "/api/multi-agent/markdown-agents", nil)
	req.AddCookie(&http.Cookie{Name: "findu_token", Value: "cookie-token"})
	req.Header.Set("Authorization", "Bearer other-token")
	sub.MultiAgentHandler().ServeHTTP(httptest.NewRecorder(), req)
	if got != "cookie-token" {
		t.Fatalf("cookie = %q, want cookie-token", got)
	}
}
