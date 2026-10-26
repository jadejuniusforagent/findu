package server

// Step 6d Phase B7：知识库读侧原生（categories/items/items:{id}/stats/
// retrieval-logs/删除项/删除检索日志，共 7 条）。
//
// 语义对齐：
//   - 取数走 db.KnowledgeStore（SQLite 直连 data-findu/knowledge.db，与 findu
//     Manager 同文件同语义）；
//   - items 创建/更新、scan、index、search、index-status 仍是 /findu/ 兼容
//     通道（进程内 IndexItem/Eino Retriever/indexer 内存态，见交接表）；
//   - 知识库未启用（文件不存在/连接失败）时，按 findu app.go 各路由的
//     `enabled:false` 形状返回 200；
//   - 删除条目落 audit_logs（category=knowledge, action=item_delete），与
//     findu audit.RecordOK 一致；检索日志删除 findu 无审计，同样不记。

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"finduai/db"
)

const knowledgeDisabledMessage = "知识库功能未启用，请前往系统设置启用知识检索功能"

var (
	kbOnce   sync.Once
	kbStore  *db.KnowledgeStore
	kbErrMsg string
)

// kbShared 进程级懒加载（与 findu 侧同容器同文件；出站只读 + 少量删除写）。
func (s *Server) kbStore() (*db.KnowledgeStore, bool) {
	kbOnce.Do(func() {
		store, err := db.OpenKnowledgeStore(db.KnowledgeDBPath)
		if err != nil {
			kbErrMsg = err.Error()
			return
		}
		kbStore = store
	})
	return kbStore, kbStore != nil
}

// kbWriteDisabled 按 findu 禁用态形状回 200。
func kbWriteDisabled(w http.ResponseWriter, body map[string]any) {
	writeJSON(w, http.StatusOK, body)
}

// listKnowledgeCategoriesNative serves GET /api/knowledge/categories。
func (s *Server) listKnowledgeCategoriesNative(w http.ResponseWriter, r *http.Request) {
	store, ok := s.kbStore()
	if !ok {
		kbWriteDisabled(w, map[string]any{"categories": []string{}, "enabled": false, "message": knowledgeDisabledMessage})
		return
	}
	categories, err := store.KnowledgeCategories()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if categories == nil {
		categories = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"categories": categories})
}

// listKnowledgeItemsNative serves GET /api/knowledge/items（搜索/单分类/分类分页/项分页四模式）。
func (s *Server) listKnowledgeItemsNative(w http.ResponseWriter, r *http.Request) {
	store, ok := s.kbStore()
	if !ok {
		kbWriteDisabled(w, map[string]any{"items": []any{}, "enabled": false, "message": knowledgeDisabledMessage})
		return
	}
	category := r.URL.Query().Get("category")
	searchKeyword := r.URL.Query().Get("search")

	if searchKeyword != "" {
		items, err := store.KnowledgeSearchItemsByKeyword(searchKeyword, category)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		grouped := make(map[string][]*db.KnowledgeItemSummary)
		for _, item := range items {
			cat := item.Category
			if cat == "" {
				cat = "未分类"
			}
			grouped[cat] = append(grouped[cat], item)
		}
		categoriesWithItems := make([]*db.KnowledgeCategoryWithItems, 0, len(grouped))
		for cat, catItems := range grouped {
			categoriesWithItems = append(categoriesWithItems, &db.KnowledgeCategoryWithItems{
				Category:  cat,
				ItemCount: len(catItems),
				Items:     catItems,
			})
		}
		// findu 侧冒泡排序按分类名升序。
		for i := 0; i < len(categoriesWithItems)-1; i++ {
			for j := i + 1; j < len(categoriesWithItems); j++ {
				if categoriesWithItems[i].Category > categoriesWithItems[j].Category {
					categoriesWithItems[i], categoriesWithItems[j] = categoriesWithItems[j], categoriesWithItems[i]
				}
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"categories": categoriesWithItems,
			"total":      len(categoriesWithItems),
			"search":     searchKeyword,
			"is_search":  true,
		})
		return
	}

	categoryPageMode := r.URL.Query().Get("categoryPage") != "false"

	limit := 50
	offset := 0
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if parsed, err := knowledgeParseInt(limitStr); err == nil && parsed > 0 && parsed <= 500 {
			limit = parsed
		}
	}
	if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
		if parsed, err := knowledgeParseInt(offsetStr); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	if category != "" && categoryPageMode {
		items, total, err := store.KnowledgeItemsSummary(category, 0, 0)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"categories": []*db.KnowledgeCategoryWithItems{{
				Category:  category,
				ItemCount: total,
				Items:     items,
			}},
			"total":  1,
			"limit":  limit,
			"offset": offset,
		})
		return
	}

	if categoryPageMode {
		if limit <= 0 || limit > 100 {
			limit = 10
		}
		categoriesWithItems, totalCategories, err := store.KnowledgeCategoriesWithItems(limit, offset)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"categories": categoriesWithItems,
			"total":      totalCategories,
			"limit":      limit,
			"offset":     offset,
		})
		return
	}

	includeContent := r.URL.Query().Get("includeContent") == "true"
	if includeContent {
		items, err := store.KnowledgeItemsWithOptions(category, limit, offset, true)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		total, err := store.KnowledgeItemsCount(category)
		if err != nil {
			total = len(items)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"items":  items,
			"total":  total,
			"limit":  limit,
			"offset": offset,
		})
		return
	}

	items, total, err := store.KnowledgeItemsSummary(category, limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":  items,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// getKnowledgeItemNative serves GET /api/knowledge/items/{id}（任何错误→404 与 findu 一致）。
func (s *Server) getKnowledgeItemNative(w http.ResponseWriter, r *http.Request) {
	store, ok := s.kbStore()
	if !ok {
		kbWriteDisabled(w, map[string]any{"enabled": false, "message": knowledgeDisabledMessage})
		return
	}
	id := r.PathValue("id")
	item, err := store.KnowledgeItemByID(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, item)
}

// deleteKnowledgeItemNative serves DELETE /api/knowledge/items/{id}。
func (s *Server) deleteKnowledgeItemNative(w http.ResponseWriter, r *http.Request) {
	store, ok := s.kbStore()
	if !ok {
		kbWriteDisabled(w, map[string]any{"enabled": false, "error": knowledgeDisabledMessage, "message": knowledgeDisabledMessage})
		return
	}
	id := r.PathValue("id")
	if err := store.KnowledgeDeleteItem(id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if s.m != nil && s.m.pg != nil {
		_ = s.m.pg.AuditLogs().Append(&db.AuditLog{
			Category:     "knowledge",
			Action:       "item_delete",
			Result:       "success",
			Actor:        loginUsername,
			ResourceType: "knowledge_item",
			ResourceID:   id,
			Message:      "删除知识项",
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "删除成功"})
}

// knowledgeWriteRequest 镜像 findu CreateItem/UpdateItem 的请求体。
type knowledgeWriteRequest struct {
	Category string `json:"category"`
	Title    string `json:"title"`
	Content  string `json:"content"`
}

// createKnowledgeItemNative serves POST /api/knowledge/items：
// findu KnowledgeHandler.CreateItem 的宿主原生实现（写库 + 写文件，不触发进程内异步索引）。
func (s *Server) createKnowledgeItemNative(w http.ResponseWriter, r *http.Request) {
	store, ok := s.kbStore()
	if !ok {
		kbWriteDisabled(w, map[string]any{"enabled": false, "error": knowledgeDisabledMessage, "message": knowledgeDisabledMessage})
		return
	}
	var req knowledgeWriteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Category = strings.TrimSpace(req.Category)
	req.Title = strings.TrimSpace(req.Title)
	if req.Category == "" || req.Title == "" || req.Content == "" {
		writeErr(w, http.StatusBadRequest, "category, title and content are required")
		return
	}
	item, err := store.CreateKnowledgeItem(req.Category, req.Title, req.Content)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, item)
}

// updateKnowledgeItemNative serves PUT /api/knowledge/items/{id}：
// findu KnowledgeHandler.UpdateItem 的宿主原生实现（写库 + 写文件，不触发进程内异步索引）。
func (s *Server) updateKnowledgeItemNative(w http.ResponseWriter, r *http.Request) {
	store, ok := s.kbStore()
	if !ok {
		kbWriteDisabled(w, map[string]any{"enabled": false, "error": knowledgeDisabledMessage, "message": knowledgeDisabledMessage})
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeErr(w, http.StatusBadRequest, "id is required")
		return
	}
	var req knowledgeWriteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Category = strings.TrimSpace(req.Category)
	req.Title = strings.TrimSpace(req.Title)
	if req.Category == "" || req.Title == "" || req.Content == "" {
		writeErr(w, http.StatusBadRequest, "category, title and content are required")
		return
	}
	item, err := store.UpdateKnowledgeItem(id, req.Category, req.Title, req.Content)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, item)
}

// listKnowledgeRetrievalLogsNative serves GET /api/knowledge/retrieval-logs。
func (s *Server) listKnowledgeRetrievalLogsNative(w http.ResponseWriter, r *http.Request) {
	store, ok := s.kbStore()
	if !ok {
		kbWriteDisabled(w, map[string]any{"enabled": false, "message": knowledgeDisabledMessage})
		return
	}
	conversationID := r.URL.Query().Get("conversationId")
	messageID := r.URL.Query().Get("messageId")
	limit := 50
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if parsed, err := knowledgeParseInt(limitStr); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	logs, err := store.KnowledgeRetrievalLogs(conversationID, messageID, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"logs": logs})
}

// deleteKnowledgeRetrievalLogNative serves DELETE /api/knowledge/retrieval-logs/{id}。
func (s *Server) deleteKnowledgeRetrievalLogNative(w http.ResponseWriter, r *http.Request) {
	store, ok := s.kbStore()
	if !ok {
		kbWriteDisabled(w, map[string]any{"enabled": false, "error": knowledgeDisabledMessage, "message": knowledgeDisabledMessage})
		return
	}
	id := r.PathValue("id")
	if err := store.KnowledgeDeleteRetrievalLog(id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "删除成功"})
}

// getKnowledgeStatsNative serves GET /api/knowledge/stats。
func (s *Server) getKnowledgeStatsNative(w http.ResponseWriter, r *http.Request) {
	store, ok := s.kbStore()
	if !ok {
		kbWriteDisabled(w, map[string]any{"enabled": false, "total_categories": 0, "total_items": 0, "message": knowledgeDisabledMessage})
		return
	}
	totalCategories, totalItems, err := store.KnowledgeStats()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":          true,
		"total_categories": totalCategories,
		"total_items":      totalItems,
	})
}

// knowledgeParseInt 镜像 handler parseInt（Sscanf %d 语义）。
func knowledgeParseInt(s string) (int, error) {
	var result int
	_, err := fmt.Sscanf(s, "%d", &result)
	return result, err
}
