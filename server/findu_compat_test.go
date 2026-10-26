package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServeFinduCompatPromotesBearerAndDelegates(t *testing.T) {
	var gotPath, gotCookie string
	s := &Server{}
	s.SetFinduCompatAPI(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if c, err := r.Cookie("findu_token"); err == nil {
			gotCookie = c.Value
		}
		w.WriteHeader(http.StatusAccepted)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/assets/selection", nil)
	req.Header.Set("Authorization", "Bearer console-token")
	rec := httptest.NewRecorder()
	s.serveFinduCompat(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}
	if gotPath != "/api/assets/selection" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotCookie != "console-token" {
		t.Fatalf("cookie = %q, want console-token", gotCookie)
	}
}

func TestServeFinduCompatUnavailableWithoutHandler(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	s.serveFinduCompat(rec, httptest.NewRequest(http.MethodGet, "/api/audit/meta", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}
