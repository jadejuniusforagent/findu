package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"finduai/db"

	"github.com/google/uuid"
)

func (s *Server) webshellsNative() *db.WebShellStore {
	if s.m == nil || s.m.pg == nil {
		return nil
	}
	return s.m.pg.Webshells()
}

func normalizeWebshellInput(c *db.WebShellConnection) {
	c.URL = strings.TrimSpace(c.URL)
	c.ProjectID = strings.TrimSpace(c.ProjectID)
	c.Password = strings.TrimSpace(c.Password)
	c.CmdParam = strings.TrimSpace(c.CmdParam)
	c.Remark = strings.TrimSpace(c.Remark)
	c.Encoding = strings.TrimSpace(c.Encoding)
	c.OS = strings.TrimSpace(c.OS)
	c.Type = strings.ToLower(strings.TrimSpace(c.Type))
	if c.Type == "" {
		c.Type = "php"
	}
	c.Method = strings.ToLower(strings.TrimSpace(c.Method))
	if c.Method != "get" && c.Method != "post" {
		c.Method = "post"
	}
}

func (s *Server) listWebshellsNative(w http.ResponseWriter, r *http.Request) {
	ws := s.webshellsNative()
	if ws == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	list, err := ws.List(r.URL.Query().Get("project_id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type webshellMutationRequest struct {
	ProjectID string `json:"project_id"`
	URL       string `json:"url"`
	Password  string `json:"password"`
	Type      string `json:"type"`
	Method    string `json:"method"`
	CmdParam  string `json:"cmd_param"`
	CmdCamel  string `json:"cmdParam"`
	Remark    string `json:"remark"`
	Encoding  string `json:"encoding"`
	OS        string `json:"os"`
}

func (r webshellMutationRequest) connection() *db.WebShellConnection {
	cmd := r.CmdParam
	if strings.TrimSpace(cmd) == "" {
		cmd = r.CmdCamel
	}
	return &db.WebShellConnection{
		ProjectID: r.ProjectID,
		URL:       r.URL,
		Password:  r.Password,
		Type:      r.Type,
		Method:    r.Method,
		CmdParam:  cmd,
		Remark:    r.Remark,
		Encoding:  r.Encoding,
		OS:        r.OS,
	}
}

func (s *Server) createWebshellNative(w http.ResponseWriter, r *http.Request) {
	ws := s.webshellsNative()
	if ws == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	var req webshellMutationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	conn := req.connection()
	normalizeWebshellInput(conn)
	if conn.URL == "" {
		writeErr(w, http.StatusBadRequest, "url is required")
		return
	}
	if _, err := url.Parse(conn.URL); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid url")
		return
	}
	conn.ID = "ws_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	conn.CreatedAt = time.Now().UTC()
	if err := ws.Create(conn); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, conn)
}

func (s *Server) updateWebshellNative(w http.ResponseWriter, r *http.Request) {
	ws := s.webshellsNative()
	if ws == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	existing, err := ws.Get(id)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "connection not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var req webshellMutationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	conn := req.connection()
	normalizeWebshellInput(conn)
	if conn.URL == "" {
		writeErr(w, http.StatusBadRequest, "url is required")
		return
	}
	if _, err := url.Parse(conn.URL); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid url")
		return
	}
	existing.ProjectID = conn.ProjectID
	existing.URL = conn.URL
	if conn.Password != "" {
		existing.Password = conn.Password
	}
	existing.Type = conn.Type
	existing.Method = conn.Method
	existing.CmdParam = conn.CmdParam
	existing.Remark = conn.Remark
	existing.Encoding = conn.Encoding
	existing.OS = conn.OS
	if err := ws.Update(existing); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "connection not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, existing)
}

func (s *Server) deleteWebshellNative(w http.ResponseWriter, r *http.Request) {
	ws := s.webshellsNative()
	if ws == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if err := ws.Delete(strings.TrimSpace(r.PathValue("id"))); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "connection not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": 1})
}
