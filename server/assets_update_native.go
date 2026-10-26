package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"finduai/db"
)

func (s *Server) updateAssetNative(w http.ResponseWriter, r *http.Request) {
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
	var patch db.AssetPatch
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if err := as.UpdateByID(id, patch); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "资产不存在")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	updated, err := as.GetByID(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) bulkUpdateAssetsNative(w http.ResponseWriter, r *http.Request) {
	as := s.assetStore()
	if as == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	var req struct {
		AssetIDs []string `json:"asset_ids"`
		db.AssetPatch
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if len(req.AssetIDs) == 0 {
		writeErr(w, http.StatusBadRequest, "asset_ids required")
		return
	}
	updated, err := as.BulkUpdate(req.AssetIDs, req.AssetPatch)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": updated})
}
