package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"finduai/db"
)

func (s *Server) projectsNative() *db.ProjectStore {
	if s.m == nil || s.m.pg == nil {
		return nil
	}
	return s.m.pg.Projects()
}

func (s *Server) listProjectsNative(w http.ResponseWriter, r *http.Request) {
	ps := s.projectsNative()
	if ps == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	projects, total, err := ps.List(r.URL.Query().Get("status"), r.URL.Query().Get("search"), limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": projects, "total": total, "limit": limit, "offset": offset})
}

func (s *Server) createProjectNative(w http.ResponseWriter, r *http.Request) {
	ps := s.projectsNative()
	if ps == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		ScopeJSON   string `json:"scope_json"`
		Status      string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	p, err := ps.Create(&db.Project{Name: req.Name, Description: req.Description, ScopeJSON: req.ScopeJSON, Status: req.Status})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) getProjectNative(w http.ResponseWriter, r *http.Request) {
	ps := s.projectsNative()
	if ps == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	p, err := ps.Get(r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "项目不存在")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) updateProjectNative(w http.ResponseWriter, r *http.Request) {
	ps := s.projectsNative()
	if ps == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	p, err := ps.Get(r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "项目不存在")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var req struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		ScopeJSON   *string `json:"scope_json"`
		Status      *string `json:"status"`
		Pinned      *bool   `json:"pinned"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Name != nil {
		if v := strings.TrimSpace(*req.Name); v != "" {
			p.Name = v
		}
	}
	if req.Description != nil {
		p.Description = *req.Description
	}
	if req.ScopeJSON != nil {
		p.ScopeJSON = *req.ScopeJSON
	}
	if req.Status != nil {
		if v := strings.TrimSpace(*req.Status); v != "" {
			p.Status = v
		}
	}
	if req.Pinned != nil {
		p.Pinned = *req.Pinned
	}
	if err := ps.Update(p); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}
