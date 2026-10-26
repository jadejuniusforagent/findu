package server

// 批次3-2 剩余：KnowledgeHandler 的索引/检索运行态宿主自建。
//
// findu KnowledgeHandler 的 StartIndex/ScanKnowledgeBase/GetIndexStatus/Search
// 依赖进程内运行态（manager/retriever/indexer，internal/knowledge）。宿主自建
// 同一套运行态：config 访问（finduConfig()）打通后，从 config.Knowledge/OpenAI
// + knowledge.db SQLite（db.OpenKnowledgeStore + DB()）懒构建 embedder→indexer
// →retriever→eino pipeline。未启用 / 构建失败时 knowledgeRT() 返回 false，
// 4 条路由回退 findu 的禁用形状（与 runtime_bridge 的 kb 包装逐字一致）。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"finduai/db"
	"finduai/internal/knowledge"

	"go.uber.org/zap"
)

// knowledgeRuntime 是宿主自建 knowledge 运行态的聚合。
type knowledgeRuntime struct {
	manager   *knowledge.Manager
	retriever *knowledge.Retriever
	indexer   *knowledge.Indexer
}

const kbDisabledMsg = "知识库功能未启用，请前往系统设置启用知识检索功能"

// knowledgeRT 懒构建 knowledge 运行态（并发安全）。
func (s *Server) knowledgeRuntime() (*knowledgeRuntime, bool) {
	s.knowledgeRTOnce.Do(func() {
		cfg, ok := s.finduConfig()
		if !ok || !cfg.Knowledge.Enabled {
			return
		}
		store, err := db.OpenKnowledgeStore(db.KnowledgeDBPath)
		if err != nil {
			return
		}
		kcfg := cfg.Knowledge
		oa := cfg.OpenAI
		lg := zap.NewNop()
		ctx := context.Background()
		embedder, err := knowledge.NewEmbedder(ctx, &kcfg, &oa, lg)
		if err != nil {
			return
		}
		indexer, err := knowledge.NewIndexer(ctx, store.DB(), embedder, lg, &kcfg)
		if err != nil {
			return
		}
		retr := knowledge.NewRetriever(store.DB(), embedder, &knowledge.RetrievalConfig{
			TopK:                kcfg.Retrieval.TopK,
			SimilarityThreshold: kcfg.Retrieval.SimilarityThreshold,
			SubIndexFilter:      kcfg.Retrieval.SubIndexFilter,
			MultiQuery:          kcfg.Retrieval.MultiQuery,
			Rerank:              kcfg.Retrieval.Rerank,
			PostRetrieve:        kcfg.Retrieval.PostRetrieve,
		}, lg)
		if err := knowledge.WireRetrieverPipeline(ctx, retr, &oa); err != nil {
			return
		}
		basePath := kcfg.BasePath
		if strings.TrimSpace(basePath) == "" {
			basePath = db.KnowledgeBasePath
		}
		s.knowledgeRT = &knowledgeRuntime{
			manager:   knowledge.NewManager(store.DB(), basePath, lg),
			retriever: retr,
			indexer:   indexer,
		}
	})
	if s.knowledgeRT == nil {
		return nil, false
	}
	return s.knowledgeRT, true
}

// knowledgeStartIndexNative serves POST /api/knowledge/index。
func (s *Server) knowledgeStartIndexNative(w http.ResponseWriter, r *http.Request) {
	rt, ok := s.knowledgeRuntime()
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "error": kbDisabledMsg})
		return
	}
	if err := rt.indexer.TryBeginIndexRun(); err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "已有索引任务正在进行，请等待完成"})
		return
	}
	mode := strings.TrimSpace(r.URL.Query().Get("mode"))
	if mode == "" {
		mode = "missing"
	}
	if mode != "full" && mode != "missing" {
		rt.indexer.FinishIndexRun()
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "无效的 mode 参数，可选值：missing、full"})
		return
	}
	fullRebuild := mode == "full"
	message := "索引构建已开始，将在后台进行"
	if fullRebuild {
		message = "全量索引重建已开始，将在后台进行"
	}
	go func() {
		defer rt.indexer.FinishIndexRun()
		ctx := context.Background()
		var err error
		if fullRebuild {
			err = rt.indexer.RunRebuildIndex(ctx)
		} else {
			err = rt.indexer.RunIndexMissing(ctx)
		}
		if err != nil {
			zap.NewNop().Error("构建知识库索引失败", zap.Error(err))
		}
	}()
	writeJSON(w, http.StatusOK, map[string]any{"message": message, "mode": mode})
}

// knowledgeScanNative serves POST /api/knowledge/scan。
func (s *Server) knowledgeScanNative(w http.ResponseWriter, r *http.Request) {
	rt, ok := s.knowledgeRuntime()
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "error": kbDisabledMsg})
		return
	}
	itemsToIndex, err := rt.manager.ScanKnowledgeBase()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(itemsToIndex) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"message": "扫描完成，没有需要索引的新项或更新项"})
		return
	}
	go func() {
		ctx := context.Background()
		failedCount := 0
		consecutiveFailures := 0
		for _, itemID := range itemsToIndex {
			if err := rt.indexer.IndexItem(ctx, itemID); err != nil {
				failedCount++
				consecutiveFailures++
				if consecutiveFailures >= 2 {
					break
				}
				continue
			}
			if consecutiveFailures > 0 {
				consecutiveFailures = 0
			}
		}
		_ = failedCount
	}()
	writeJSON(w, http.StatusOK, map[string]any{
		"message":        fmt.Sprintf("扫描完成，开始索引 %d 个新添加或更新的知识项", len(itemsToIndex)),
		"items_to_index": len(itemsToIndex),
	})
}

// knowledgeIndexStatusNative serves GET /api/knowledge/index-status。
func (s *Server) knowledgeIndexStatusNative(w http.ResponseWriter, r *http.Request) {
	rt, ok := s.knowledgeRuntime()
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{
			"enabled": false, "total_items": 0, "indexed_items": 0,
			"progress_percent": 0, "is_complete": false, "message": kbDisabledMsg,
		})
		return
	}
	status, err := rt.manager.GetIndexStatus()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rt.indexer != nil {
		lastError, lastErrorTime := rt.indexer.GetLastError()
		if lastError != "" && time.Since(lastErrorTime) < 5*time.Minute {
			status["last_error"] = lastError
			status["last_error_time"] = lastErrorTime.Format(time.RFC3339)
		}
		isRebuilding, totalItems, current, failed, lastItemID, lastChunks, startTime := rt.indexer.GetRebuildStatus()
		if isRebuilding {
			status["is_rebuilding"] = true
			status["rebuild_total"] = totalItems
			status["rebuild_current"] = current
			status["rebuild_failed"] = failed
			status["rebuild_start_time"] = startTime.Format(time.RFC3339)
			if lastItemID != "" {
				status["rebuild_last_item_id"] = lastItemID
			}
			if lastChunks > 0 {
				status["rebuild_last_chunks"] = lastChunks
			}
			status["is_complete"] = false
			if totalItems > 0 {
				status["progress_percent"] = float64(current) / float64(totalItems) * 100
			}
		}
	}
	writeJSON(w, http.StatusOK, status)
}

// knowledgeSearchNative serves POST /api/knowledge/search。
func (s *Server) knowledgeSearchNative(w http.ResponseWriter, r *http.Request) {
	rt, ok := s.knowledgeRuntime()
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"results": []interface{}{}, "enabled": false, "message": kbDisabledMsg})
		return
	}
	var req knowledge.SearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	results, err := rt.retriever.Search(r.Context(), &req)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}