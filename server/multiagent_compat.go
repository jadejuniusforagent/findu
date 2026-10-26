package server

import "net/http"

// SetMultiAgentAPI installs the deep / plan_execute / supervisor compatibility
// runtime. The host owns /api/multi-agent routing and authentication; the
// injected handler keeps the existing multi-agent implementation until a native
// findu runtime replaces it.
func (s *Server) SetMultiAgentAPI(h http.Handler) {
	s.multiAgentAPI = h
}

func (s *Server) serveMultiAgent(w http.ResponseWriter, r *http.Request) {
	if s.multiAgentAPI == nil {
		writeErr(w, http.StatusServiceUnavailable, "多代理运行时不可用")
		return
	}
	s.multiAgentAPI.ServeHTTP(w, r)
}
