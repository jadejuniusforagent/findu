package db

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// TestKnowledgeStoreNative 覆盖 Phase B7 读侧投影 + 删除项/日志（SQLite 临时库，
// DDL 镜像 findu internal/database 的 initKnowledgeTables）。
func TestKnowledgeStoreNative(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "knowledge.db")
	store, err := OpenKnowledgeStore(path)
	if err != nil {
		t.Skipf("打开临时 SQLite 失败（cgo/sqlite 不可用）: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	_ = ctx

	// 镜像 findu schema（FK 级联由 DSN _foreign_keys=1 保证）。
	ddl := []string{
		`CREATE TABLE IF NOT EXISTS knowledge_base_items (
			id TEXT PRIMARY KEY, category TEXT NOT NULL, title TEXT NOT NULL,
			file_path TEXT NOT NULL, content TEXT,
			created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL);`,
		`CREATE TABLE IF NOT EXISTS knowledge_embeddings (
			id TEXT PRIMARY KEY, item_id TEXT NOT NULL, chunk_index INTEGER NOT NULL,
			chunk_text TEXT NOT NULL, embedding TEXT NOT NULL,
			sub_indexes TEXT NOT NULL DEFAULT '', embedding_model TEXT NOT NULL DEFAULT '',
			embedding_dim INTEGER NOT NULL DEFAULT 0, created_at DATETIME NOT NULL,
			FOREIGN KEY (item_id) REFERENCES knowledge_base_items(id) ON DELETE CASCADE);`,
		`CREATE TABLE IF NOT EXISTS knowledge_retrieval_logs (
			id TEXT PRIMARY KEY, conversation_id TEXT, message_id TEXT,
			query TEXT NOT NULL, risk_type TEXT, retrieved_items TEXT,
			created_at DATETIME NOT NULL);`,
		`CREATE INDEX IF NOT EXISTS idx_knowledge_items_category ON knowledge_base_items(category);`,
	}
	for _, stmt := range ddl {
		if _, err := store.db.Exec(stmt); err != nil {
			t.Fatalf("ddl: %v", err)
		}
	}

	seed := func(id, category, title, content string) {
		filePath := filepath.Join("knowledge_base", category, title+".md")
		_, err := store.db.Exec(
			`INSERT INTO knowledge_base_items (id, category, title, file_path, content, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			id, category, title, filePath, content,
			"2026-10-22 10:00:00", "2026-10-22 10:00:00",
		)
		if err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
		_, _ = store.db.Exec(
			`INSERT INTO knowledge_embeddings (id, item_id, chunk_index, chunk_text, embedding, created_at)
			 VALUES (?, ?, 0, ?, ?, ?)`, "chunk-"+id, id, content, "[]", "2026-10-22 10:00:00")
	}
	seed("k1", "SQL Injection", "README", "# SQL Injection basics")
	seed("k2", "SQL Injection", "Payloads", "' OR 1=1 --")
	seed("k3", "Prompt Injection", "README", "# Prompt Injection")

	cats, err := store.KnowledgeCategories()
	if err != nil || len(cats) != 2 || cats[0] != "Prompt Injection" || cats[1] != "SQL Injection" {
		t.Fatalf("categories: %v %v", cats, err)
	}
	totalCats, totalItems, err := store.KnowledgeStats()
	if err != nil || totalCats != 2 || totalItems != 3 {
		t.Fatalf("stats: %d/%d %v", totalCats, totalItems, err)
	}

	// 摘要列表（按 category,title 排序）。
	items, total, err := store.KnowledgeItemsSummary("", 0, 0)
	if err != nil || total != 3 || len(items) != 3 {
		t.Fatalf("summary: total=%d len=%d err=%v", total, len(items), err)
	}
	// ORDER BY category,title（BINARY）：Prompt Injection/README → SQL Injection/Payloads → SQL Injection/README。
	if items[0].ID != "k3" || items[1].ID != "k2" || items[2].ID != "k1" {
		t.Fatalf("summary order wrong: %s,%s,%s", items[0].ID, items[1].ID, items[2].ID)
	}
	// 空内容列不应出现在 content（summary 不查 content）。
	if items[2].Content != "" {
		t.Fatalf("summary should not carry content: %q", items[2].Content)
	}

	// 分页。
	page1, _, err := store.KnowledgeItemsSummary("", 2, 0)
	if err != nil || len(page1) != 2 {
		t.Fatalf("page1: %v %v", len(page1), err)
	}
	page2, _, err := store.KnowledgeItemsSummary("", 2, 2)
	if err != nil || len(page2) != 1 || page2[0].ID != "k1" {
		t.Fatalf("page2: %v err=%v", page2, err)
	}

	// 关键字搜索（LOWER LIKE）。
	found, err := store.KnowledgeSearchItemsByKeyword("OR 1=1", "")
	if err != nil || len(found) != 1 || found[0].ID != "k2" {
		t.Fatalf("keyword search: %v err=%v", found, err)
	}
	foundC, err := store.KnowledgeSearchItemsByKeyword("README", "Prompt Injection")
	if err != nil || len(foundC) != 1 || foundC[0].ID != "k3" {
		t.Fatalf("keyword+category search: %v err=%v", foundC, err)
	}
	if _, err := store.KnowledgeSearchItemsByKeyword("", ""); err == nil {
		t.Fatalf("empty keyword should error")
	}

	// 分类分页。
	cwi, totalCats, err := store.KnowledgeCategoriesWithItems(1, 0)
	if err != nil || totalCats != 2 || len(cwi) != 1 || cwi[0].Category != "Prompt Injection" || cwi[0].ItemCount != 1 || len(cwi[0].Items) != 1 {
		t.Fatalf("categoriesWithItems page0: %+v %v", cwi, err)
	}
	cwi2, _, err := store.KnowledgeCategoriesWithItems(1, 1)
	if err != nil || len(cwi2) != 1 || cwi2[0].Category != "SQL Injection" || cwi2[0].ItemCount != 2 {
		t.Fatalf("categoriesWithItems page1: %+v %v", cwi2, err)
	}

	// 单项 + 404 语义。
	item, err := store.KnowledgeItemByID("k1")
	if err != nil || item.Title != "README" || item.UpdatedAt.IsZero() {
		t.Fatalf("item: %+v err=%v", item, err)
	}
	if _, err := store.KnowledgeItemByID("nope"); err == nil || !strings.Contains(err.Error(), "知识项不存在") {
		t.Fatalf("missing item error: %v", err)
	}

	// 检索日志（检索项 JSON、无行 → nil → JSON null）。
	_, _ = store.db.Exec(
		`INSERT INTO knowledge_retrieval_logs (id, conversation_id, message_id, query, risk_type, retrieved_items, created_at)
		 VALUES ('log1','conv-a','msg-1','SQL*i','SQL Injection','["k1","k2"]','2026-10-22 11:00:00')`)
	logs, err := store.KnowledgeRetrievalLogs("", "", 10)
	if err != nil || len(logs) != 1 || logs[0].ID != "log1" || len(logs[0].RetrievedItems) != 2 {
		t.Fatalf("logs: %+v err=%v", logs, err)
	}
	logsByConv, err := store.KnowledgeRetrievalLogs("conv-a", "", 10)
	if err != nil || len(logsByConv) != 1 {
		t.Fatalf("logs by conversation: %v err=%v", logsByConv, err)
	}
	if err := store.KnowledgeDeleteRetrievalLog("nope"); err == nil || !strings.Contains(err.Error(), "检索日志不存在") {
		t.Fatalf("missing log error: %v", err)
	}
	if err := store.KnowledgeDeleteRetrievalLog("log1"); err != nil {
		t.Fatalf("delete log: %v", err)
	}
	if logs, _ := store.KnowledgeRetrievalLogs("", "", 10); len(logs) != 0 {
		t.Fatalf("logs after delete: %v", logs)
	}

	// 删除条目：FK 级联清向量 + 文件删除 + 空目录清理。
	if err := store.KnowledgeDeleteItem("k1"); err != nil {
		t.Fatalf("delete item: %v", err)
	}
	var chunkCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM knowledge_embeddings WHERE item_id='k1'`).Scan(&chunkCount); err != nil {
		t.Fatal(err)
	}
	if chunkCount != 0 {
		t.Fatalf("embeddings should cascade-delete, got %d", chunkCount)
	}
	if _, err := store.KnowledgeItemByID("k1"); err == nil {
		t.Fatalf("item should be gone")
	}
	if err := store.KnowledgeDeleteItem("k1"); err == nil || !strings.Contains(err.Error(), "查询知识项失败") {
		t.Fatalf("deleting missing item error: %v", err)
	}
}
