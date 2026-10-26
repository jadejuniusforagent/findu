package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

func (s *Server) deleteAssetNative(w http.ResponseWriter, r *http.Request) {
	as := s.assetStore()
	if as == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeErr(w, http.StatusBadRequest, "id required")
		return
	}
	deleted, err := as.DeleteByIDs([]string{id})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if deleted == 0 {
		writeErr(w, http.StatusNotFound, "资产不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) assetStatsNative(w http.ResponseWriter, r *http.Request) {
	as := s.assetStore()
	if as == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	days := 30
	if raw := strings.TrimSpace(r.URL.Query().Get("days")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || (n != 7 && n != 30 && n != 90) {
			writeErr(w, http.StatusBadRequest, "days 仅支持 7、30 或 90")
			return
		}
		days = n
	}
	stats, err := as.Stats(days)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) batchDeleteAssetsNative(w http.ResponseWriter, r *http.Request) {
	as := s.assetStore()
	if as == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	var req struct {
		AssetIDs []string `json:"asset_ids"`
		IDs      []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	ids := req.AssetIDs
	if len(ids) == 0 {
		ids = req.IDs
	}
	if len(ids) == 0 {
		writeErr(w, http.StatusBadRequest, "asset_ids required")
		return
	}
	deleted, err := as.DeleteByIDs(ids)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": deleted})
}

func (s *Server) updateAssetsProjectNative(w http.ResponseWriter, r *http.Request) {
	as := s.assetStore()
	if as == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	var req struct {
		AssetIDs  []string `json:"asset_ids"`
		ProjectID string   `json:"project_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if len(req.AssetIDs) == 0 {
		writeErr(w, http.StatusBadRequest, "asset_ids required")
		return
	}
	req.ProjectID = strings.TrimSpace(req.ProjectID)
	if req.ProjectID != "" {
		ps := s.projectsNative()
		if ps == nil {
			writeErr(w, http.StatusServiceUnavailable, "database unavailable")
			return
		}
		if _, err := ps.Get(req.ProjectID); errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusBadRequest, "项目不存在")
			return
		} else if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	updated, err := as.UpdateProjectIDs(req.AssetIDs, req.ProjectID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": updated, "project_id": req.ProjectID})
}

// listAssetsFinduNative serves GET /api/assets（原生化读侧）：分页返回 findu
// 共表资产，形状与旧 /findu/api/assets 对齐 {assets,total,page,page_size,total_pages}。
func (s *Server) listAssetsFinduNative(w http.ResponseWriter, r *http.Request) {
	as := s.assetStore()
	if as == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	page := 1
	pageSize := 50
	if v, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && v >= 1 {
		page = v
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("page_size")); err == nil && v >= 1 && v <= 500 {
		pageSize = v
	}
	offset := (page - 1) * pageSize
	total, err := as.CountAssetsFinduNative(q, status)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	assets, err := as.ListAssetsFinduNative(q, status, pageSize, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	totalPages := 0
	if pageSize > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"assets": assets, "total": total, "page": page,
		"page_size": pageSize, "total_pages": totalPages,
	})
}
