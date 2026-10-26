package intel

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

// 本文件是插件的 HTTP 层。响应结构与迁移前 server/vulnkb.go、server/vulnintel.go
// 完全一致，前端无需改动；差异只在「谁注册路由」以及停止后由宿主统一回 503。

func (p *Plugin) listVulnKB(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	entries, total, err := p.deps.Store.ListVulnKB(
		q.Get("q"), q.Get("type"), atoiDefault(q.Get("page"), 0), atoiDefault(q.Get("size"), 50))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries, "total": total})
}

func (p *Plugin) getVulnKB(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	entry, err := p.deps.Store.GetVulnKB(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entry": entry})
}

func (p *Plugin) vulnKBStats(w http.ResponseWriter, _ *http.Request) {
	stats, err := p.deps.Store.VulnKBStats()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"stats": stats})
}

func (p *Plugin) listVulnIntel(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	entries, total, err := p.deps.Store.ListVulnIntel(
		q.Get("q"), q.Get("severity"), atoiDefault(q.Get("page"), 0), atoiDefault(q.Get("size"), 50))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries, "total": total})
}

func (p *Plugin) refreshVulnIntel(w http.ResponseWriter, _ *http.Request) {
	if p.deps.Intel == nil {
		writeErr(w, http.StatusServiceUnavailable, "情报管线未接入")
		return
	}
	p.deps.Intel.Refresh()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (p *Plugin) vulnIntelStatus(w http.ResponseWriter, _ *http.Request) {
	if p.deps.Intel == nil {
		writeErr(w, http.StatusServiceUnavailable, "情报管线未接入")
		return
	}
	polling, last, res, sources := p.deps.Intel.Status()
	writeJSON(w, http.StatusOK, map[string]any{
		"polling": polling,
		"last":    last.Format(time.RFC3339),
		"result":  res,
		"sources": sources,
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": msg})
}

func atoiDefault(s string, d int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return d
}
