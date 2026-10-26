package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"finduai/internal/config"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func ginTestMode(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)
}

func TestHunterSearchCtxURLSafeBase64AndPaging(t *testing.T) {
	ginTestMode(t)
	t.Setenv("HUNTER_API_KEY", "")

	hunterServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/openApi/search" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("api-key"); got != "test-hunter-key" {
			t.Fatalf("Hunter api-key = %q, want test-hunter-key", got)
		}
		if got := r.URL.Query().Get("is_web_search"); got != "3" {
			t.Fatalf("is_web_search = %q, want 3", got)
		}
		if got := r.URL.Query().Get("page_size"); got != "100" {
			t.Fatalf("page_size = %q, want 100", got)
		}
		if got := r.URL.Query().Get("search"); got == "" {
			t.Fatalf("search query param missing")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":20000,"msg":"success","data":{"total":1,"arr":[{"url":"https://example.com:443","ip":"1.2.3.4","port":443,"domain":"example.com","web_title":"Example","protocol":"https"}]}}`))
	}))
	defer hunterServer.Close()

	h := NewFofaHandler(&config.Config{
		Hunter: config.SpaceSearchConfig{
			BaseURL: hunterServer.URL + "/openApi/search",
			APIKey:  "test-hunter-key",
		},
	}, zap.NewNop())

	resp, err := h.SearchProviderCtx(context.Background(), "hunter", fofaSearchRequest{Query: "domain=\"example.com\"", Size: 10, Page: 1})
	if err != nil {
		t.Fatalf("SearchProviderCtx() error = %v", err)
	}
	if resp.Provider != "hunter" || resp.Total != 1 || resp.ResultsCount != 1 {
		t.Fatalf("resp = %+v, want hunter/1/1", resp)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("results len = %d, want 1", len(resp.Results))
	}
	if resp.Results[0]["domain"] != "example.com" {
		t.Fatalf("result domain = %v, want example.com", resp.Results[0]["domain"])
	}
}

func TestHunterSearchCtxErrorCodeSurfaced(t *testing.T) {
	ginTestMode(t)
	t.Setenv("HUNTER_API_KEY", "")

	hunterServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":42900,"msg":"积分不足，请先充值"}`))
	}))
	defer hunterServer.Close()

	h := NewFofaHandler(&config.Config{
		Hunter: config.SpaceSearchConfig{
			BaseURL: hunterServer.URL,
			APIKey:  "test-hunter-key",
		},
	}, zap.NewNop())

	_, err := h.SearchProviderCtx(context.Background(), "hunter", fofaSearchRequest{Query: "ip=\"1.1.1.1\"", Size: 10, Page: 1})
	if err == nil {
		t.Fatal("expected error for Hunter error code")
	}
	if err.Error() != "积分不足，请先充值" {
		t.Fatalf("error = %q, want surfaced message", err.Error())
	}
}

func TestSourceConfiguredAndLedgerCallback(t *testing.T) {
	ginTestMode(t)
	t.Setenv("ZOOMEYE_API_KEY", "")

	var recorded []string
	h := NewFofaHandler(&config.Config{
		ZoomEye: config.SpaceSearchConfig{APIKey: "zk"},
	}, zap.NewNop())
	h.SetSourceRecorder(func(provider, status, errMsg string) {
		recorded = append(recorded, provider+"|"+status)
	})

	if !h.SourceConfigured("zoomeye") {
		t.Fatal("zoomeye should be configured")
	}
	if h.SourceConfigured("hunter") {
		t.Fatal("hunter should not be configured")
	}

	// not-configured provider triggers ledger callback with not_configured
	_, err := h.SearchProviderCtx(context.Background(), "hunter", fofaSearchRequest{Query: "x", Size: 1})
	if err == nil {
		t.Fatal("expected not-configured error")
	}
	if len(recorded) != 1 || recorded[0] != "hunter|not_configured" {
		t.Fatalf("recorded = %v, want [hunter|not_configured]", recorded)
	}
}

func TestSearchEngineAssetsNormalizesRows(t *testing.T) {
	ginTestMode(t)
	t.Setenv("FOFA_API_KEY", "")

	fofaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":false,"size":1,"page":1,"total":1,"results":[["1.1.1.1","1.1.1.1","80","example.com","Example","http","CN","","",""]]}`))
	}))
	defer fofaServer.Close()

	h := NewFofaHandler(&config.Config{
		FOFA: config.FofaConfig{
			BaseURL: fofaServer.URL,
			APIKey:  "test-key",
		},
	}, zap.NewNop())

	assets, rows, total, err := h.SearchEngineAssets(context.Background(), "fofa", "ip=\"1.1.1.1\"", 10, 1)
	if err != nil {
		t.Fatalf("SearchEngineAssets() error = %v", err)
	}
	if total != 1 || len(assets) != 1 || len(rows) != 1 {
		t.Fatalf("assets=%d rows=%d total=%d, want 1/1/1", len(assets), len(rows), total)
	}
	if assets[0].IP != "1.1.1.1" || assets[0].Port != 80 || assets[0].Domain != "example.com" {
		t.Fatalf("asset = %+v", assets[0])
	}
	if assets[0].Source != "FOFA" {
		t.Fatalf("asset source = %q, want FOFA", assets[0].Source)
	}
}