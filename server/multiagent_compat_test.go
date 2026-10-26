package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServeMultiAgentWithoutRuntime(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	s.serveMultiAgent(rec, httptest.NewRequest(http.MethodPost, "/api/multi-agent", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestServeMultiAgentForwardsRequest(t *testing.T) {
	var gotMethod, gotPath string
	s := &Server{multiAgentAPI: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusCreated)
	})}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/multi-agent/stream", nil)
	s.serveMultiAgent(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusCreated)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/multi-agent/stream" {
		t.Fatalf("forwarded %s %s", gotMethod, gotPath)
	}
}
