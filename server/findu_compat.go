package server

import (
	"net/http"
)

// SetFinduCompatAPI installs the findu subsystem handler used by host-owned
// compatibility routes. The host owns the /api route and its auth boundary;
// the handler keeps the existing findu implementation until the resource is
// migrated into native findu stores.
func (s *Server) SetFinduCompatAPI(h http.Handler) {
	s.finduCompatAPI = h
}

// registerFinduCompatPrefix makes the host own an /api prefix while delegating
// its implementation to the findu compatibility handler. Prefixes registered
// here must not have conflicting host routes.
func (s *Server) registerFinduCompatPrefix(mux routeRegistrar, prefix string) {
	mux.HandleFunc(prefix, s.serveFinduCompat)
	mux.HandleFunc(prefix+"/", s.serveFinduCompat)
}

func (s *Server) serveFinduCompat(w http.ResponseWriter, r *http.Request) {
	if s.finduCompatAPI == nil {
		writeErr(w, http.StatusServiceUnavailable, "findu 兼容运行时不可用")
		return
	}
	// The findu session bridge reads findu_token from a cookie. Host API callers
	// authenticate with Bearer, so promote it before entering the findu handler.
	s.finduCompatAPI.ServeHTTP(w, promoteBearerCookie(r))
}
