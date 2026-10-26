package server

// Step 6d Phase A：assets selection / import / scan-links / merge 原生 handler。
// 行为与 findu internal/handler/asset.go 对应入口对齐（校验文案、状态码、
// 响应形状）；数据走宿主 db/assets_selection.go 的 PG 原生 store。

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"finduai/db"
)

const (
	maxAssetSelectionBatch = 10000
	maxAssetImportBatch    = 100000
)

func (s *Server) assetSelectionStore() *db.AssetStore {
	if s.m == nil || s.m.pg == nil {
		return nil
	}
	return s.m.pg.Assets()
}

// assetSelectionNative serves GET /api/assets/selection (A1).
func (s *Server) assetSelectionNative(w http.ResponseWriter, r *http.Request) {
	as := s.assetSelectionStore()
	if as == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	q := r.URL.Query()
	filter := db.AssetSelectionFilter{
		Q:         strings.TrimSpace(q.Get("q")),
		Type:      strings.TrimSpace(q.Get("type")),
		Status:    strings.ToLower(strings.TrimSpace(q.Get("status"))),
		Protocol:  strings.ToLower(strings.TrimSpace(q.Get("protocol"))),
		ProjectID: strings.TrimSpace(q.Get("project_id")),
		Source:    strings.TrimSpace(q.Get("source")),
		Tag:       strings.TrimSpace(q.Get("tag")),
		Host:      strings.TrimSpace(q.Get("host")),
		IP:        strings.TrimSpace(q.Get("ip")),
		Domain:    strings.TrimSpace(q.Get("domain")),
		SortBy:    strings.ToLower(strings.TrimSpace(q.Get("sort_by"))),
		SortOrder: strings.ToLower(strings.TrimSpace(q.Get("sort_order"))),
	}
	assets, total, err := as.SelectAssetsForOperation(maxAssetSelectionBatch, filter)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if assets == nil {
		assets = []*db.AssetRowDTO{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"assets": assets, "total": total})
}

// importAssetsNative serves POST /api/assets/import (A2).
func (s *Server) importAssetsNative(w http.ResponseWriter, r *http.Request) {
	as := s.assetSelectionStore()
	if as == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	var req struct {
		Assets      []*db.AssetImportInput `json:"assets"`
		Source      string                 `json:"source"`
		SourceQuery string                 `json:"source_query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if len(req.Assets) == 0 || len(req.Assets) > maxAssetImportBatch {
		writeErr(w, http.StatusBadRequest, "assets 数量必须在 1-100000 之间")
		return
	}
	for _, asset := range req.Assets {
		if asset == nil {
			continue
		}
		if strings.TrimSpace(asset.Source) == "" {
			asset.Source = strings.TrimSpace(req.Source)
		}
		if strings.TrimSpace(asset.SourceQuery) == "" {
			asset.SourceQuery = strings.TrimSpace(req.SourceQuery)
		}
	}
	// owner 授权行（rbac_resource_assignments）属 findu RBAC 域：actor 取宿主
	// JWT subject → rbac_users 映射（对不上按内置管理员 admin，与 findu 侧
	// SessionForUsername 的控制台单账号桥接一致）。Step 6d Phase R3 缺口①补全，
	// 授权语义与 findu UpsertAssets 相同（owner 存在才写，幂等）。
	result, err := as.ImportAssetsNative(req.Assets, s.finduConsoleActorID(r))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// finduConsoleActorID —— findu RBAC 域的 actor 归属 id：宿主 JWT subject →
// rbac_users.username，对不上时回退内置管理员 admin；findu 运行时未挂载或
// 无法解析身份时返回空（调用方按与 findu 无会话导入一致的空 owner 处理）。
func (s *Server) finduConsoleActorID(r *http.Request) string {
	if s.rtHandles.DB == nil {
		return ""
	}
	sub, _ := s.FinduTokenSubject(extractToken(r))
	sub = strings.ToLower(strings.TrimSpace(sub))
	if sub == "" {
		return ""
	}
	user, err := s.rtHandles.DB.GetRBACUserByUsername(sub)
	if (err != nil || user == nil) && sub != "admin" {
		user, err = s.rtHandles.DB.GetRBACUserByUsername("admin")
	}
	if err != nil || user == nil || !user.Enabled {
		return ""
	}
	return user.ID
}

type recordAssetScan struct {
	AssetID        string `json:"asset_id"`
	ConversationID string `json:"conversation_id"`
	QueueID        string `json:"queue_id"`
	TaskID         string `json:"task_id"`
}

// recordAssetScansNative serves POST /api/assets/scan-links (A3).
func (s *Server) recordAssetScansNative(w http.ResponseWriter, r *http.Request) {
	as := s.assetSelectionStore()
	if as == nil || s.m == nil || s.m.pg == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	var req struct {
		Scans []recordAssetScan `json:"scans"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if len(req.Scans) == 0 || len(req.Scans) > maxAssetSelectionBatch {
		writeErr(w, http.StatusBadRequest, "scans 数量必须在 1-10000 之间")
		return
	}
	for _, scan := range req.Scans {
		conversationID := strings.TrimSpace(scan.ConversationID)
		queueID := strings.TrimSpace(scan.QueueID)
		taskID := strings.TrimSpace(scan.TaskID)
		if conversationID == "" && taskID == "" {
			writeErr(w, http.StatusBadRequest, "conversation_id 或 task_id 至少需要一个")
			return
		}
		if taskID != "" {
			var belongs bool
			if err := s.m.pg.QueryRow(`SELECT EXISTS (SELECT 1 FROM batch_tasks bt WHERE bt.id=$1 AND bt.queue_id=$2)`,
				taskID, queueID).Scan(&belongs); err != nil || !belongs {
				writeErr(w, http.StatusBadRequest, "任务不属于指定队列")
				return
			}
		}
		if _, err := as.GetAssetRow(strings.TrimSpace(scan.AssetID)); err != nil {
			writeErr(w, http.StatusForbidden, "资产不存在或无权扫描")
			return
		}
	}
	for _, scan := range req.Scans {
		if err := as.MarkAssetScannedNative(scan.AssetID, scan.ConversationID, scan.QueueID, scan.TaskID); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": len(req.Scans)})
}

// mergeAssetsNative serves POST /api/assets/merge (A4).
func (s *Server) mergeAssetsNative(w http.ResponseWriter, r *http.Request) {
	as := s.assetSelectionStore()
	if as == nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	var req struct {
		AssetIDs  []string `json:"asset_ids"`
		PrimaryID string   `json:"primary_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if len(req.AssetIDs) < 2 || len(req.AssetIDs) > 100 {
		writeErr(w, http.StatusBadRequest, "合并资产数量必须在 2-100 之间")
		return
	}
	primaryID := strings.TrimSpace(req.PrimaryID)
	if primaryID == "" {
		primaryID = strings.TrimSpace(req.AssetIDs[0])
	}
	primary, err := as.GetAssetRow(primaryID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "主资产不存在或无权访问")
		return
	}
	identity := func(a *db.AssetRowDTO) map[string]struct{} {
		keys := map[string]struct{}{}
		if v := strings.ToLower(strings.TrimSpace(a.Domain)); v != "" {
			keys["domain:"+v] = struct{}{}
		}
		if v := strings.ToLower(strings.Trim(strings.TrimSpace(a.IP), "[]")); v != "" {
			keys["ip:"+v] = struct{}{}
		}
		if v := strings.ToLower(strings.TrimSpace(a.Host)); v != "" {
			keys["host:"+v] = struct{}{}
		}
		return keys
	}
	others := make([]*db.AssetRowDTO, 0, len(req.AssetIDs)-1)
	seen := map[string]struct{}{primaryID: {}}
	for _, id := range req.AssetIDs {
		id = strings.TrimSpace(id)
		if id == "" || id == primaryID {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		item, err := as.GetAssetRow(id)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "部分资产不存在或无权访问")
			return
		}
		primaryKeys := identity(primary)
		shared := false
		for key := range identity(item) {
			if _, ok := primaryKeys[key]; ok {
				shared = true
				break
			}
		}
		if !shared {
			writeErr(w, http.StatusBadRequest, "所选资产没有共同域名、IP 或 Host，不能判定为重复资产")
			return
		}
		others = append(others, item)
	}
	if len(others) == 0 {
		writeErr(w, http.StatusBadRequest, "至少需要两个不同资产")
		return
	}
	mergeText := func(dst *string, src string) {
		if strings.TrimSpace(*dst) == "" && strings.TrimSpace(src) != "" {
			*dst = src
		}
	}
	tagSet := map[string]struct{}{}
	for _, tag := range primary.Tags {
		tagSet[tag] = struct{}{}
	}
	for _, item := range others {
		mergeText(&primary.ProjectID, item.ProjectID)
		mergeText(&primary.Host, item.Host)
		mergeText(&primary.IP, item.IP)
		mergeText(&primary.Domain, item.Domain)
		mergeText(&primary.Protocol, item.Protocol)
		mergeText(&primary.Title, item.Title)
		mergeText(&primary.Server, item.Server)
		mergeText(&primary.Country, item.Country)
		mergeText(&primary.Province, item.Province)
		mergeText(&primary.City, item.City)
		mergeText(&primary.ResponsiblePerson, item.ResponsiblePerson)
		mergeText(&primary.Department, item.Department)
		mergeText(&primary.BusinessSystem, item.BusinessSystem)
		mergeText(&primary.Environment, item.Environment)
		mergeText(&primary.Criticality, item.Criticality)
		for _, tag := range item.Tags {
			tagSet[tag] = struct{}{}
		}
	}
	primary.Tags = primary.Tags[:0]
	for tag := range tagSet {
		primary.Tags = append(primary.Tags, tag)
	}
	if len(primary.Tags) > 30 {
		writeErr(w, http.StatusBadRequest, "合并后标签超过 30 个")
		return
	}
	ids := make([]string, 0, len(others))
	for _, item := range others {
		ids = append(ids, item.ID)
	}
	merged, err := as.MergeAssetRowsNative(primary, ids)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusBadRequest, "主资产不存在")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	updated, _ := as.GetAssetRow(primary.ID)
	writeJSON(w, http.StatusOK, map[string]any{"merged": merged, "asset": updated})
}
