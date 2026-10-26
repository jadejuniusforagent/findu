package db

// Step 6d Phase B7：知识库读侧 + 纯 DB/FS 清理层（SQLite 文件 data-findu/knowledge.db）。
//
// 数据面：与 findu Manager（internal/knowledge/manager.go）共用同一 SQLite 文件，
// SQL、时间多格式解析、分页/排序/分组语义逐字段对齐；JSON 形状镜像
// internal/knowledge/types.go（camelCase + RFC3339 时间 + zero→""）。
//
// 范围取舍（见 docs/refactor/STEP6D-AB-HANDOFF.md）：
//   - 原生：categories / items / items:{id} / stats / retrieval-logs / 删除项 /
//     删除检索日志（DELETE 走 FK 级联清 knowledge_embeddings 向量行）；
//   - 兼容保留：items 创建/更新（异步 IndexItem 进程内索引）、scan、index、
//     search（进程内 Eino Retriever + 嵌入模型）、index-status（重建进度在
//     indexer 内存）——纯 DB 写无法触发这些进程内副作用；
//   - knowledge_db_path / knowledge_base 目录取容器内固定相对约定
//     （config.yaml 不可达，归 B3 config 批次的既有缺口说明）。

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3" // findu 已有依赖；驱动名 "sqlite3"

	"github.com/google/uuid"
)

// KnowledgeDBPath 容器内相对约定（config-findu.yaml knowledge_db_path）。
const KnowledgeDBPath = "data-findu/knowledge.db"

// KnowledgeBasePath 容器内相对约定（config-findu.yaml base_path）。
const KnowledgeBasePath = "knowledge_base"

// KnowledgeSQLiteDSN 镜像 findu NewKnowledgeDB 的连接参数（WAL + 5s busy + FK 级联）。
const KnowledgeSQLiteDSN = "?_journal_mode=WAL&_foreign_keys=1&_busy_timeout=5000&_synchronous=NORMAL"

// KnowledgeStore 知识库 SQLite 直连（与其余 db 层的 PG 存储并存）。
type KnowledgeStore struct {
	db *sql.DB
}

// KnowledgeItem 镜像 findu knowledge.KnowledgeItem（Content 无 omitempty）。
type KnowledgeItem struct {
	ID        string    `json:"id"`
	Category  string    `json:"category"`
	Title     string    `json:"title"`
	FilePath  string    `json:"filePath"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"-"`
	UpdatedAt time.Time `json:"-"`
}

// MarshalJSON 时间走 RFC3339（zero→""），镜像 findu 自定义序列化。
func (k KnowledgeItem) MarshalJSON() ([]byte, error) {
	type alias struct {
		ID        string `json:"id"`
		Category  string `json:"category"`
		Title     string `json:"title"`
		FilePath  string `json:"filePath"`
		Content   string `json:"content"`
		CreatedAt string `json:"createdAt"`
		UpdatedAt string `json:"updatedAt"`
	}
	return json.Marshal(alias{k.ID, k.Category, k.Title, k.FilePath, k.Content, KnowledgeFormatTime(k.CreatedAt), KnowledgeFormatTime(k.UpdatedAt)})
}

// KnowledgeItemSummary 镜像 findu knowledge.KnowledgeItemSummary（Content omitempty）。
type KnowledgeItemSummary struct {
	ID        string    `json:"id"`
	Category  string    `json:"category"`
	Title     string    `json:"title"`
	FilePath  string    `json:"filePath"`
	Content   string    `json:"content,omitempty"`
	CreatedAt time.Time `json:"-"`
	UpdatedAt time.Time `json:"-"`
}

// MarshalJSON 时间走 RFC3339（zero→""），并动态补中文标题 cn_title。
func (k KnowledgeItemSummary) MarshalJSON() ([]byte, error) {
	type alias struct {
		ID        string `json:"id"`
		Category  string `json:"category"`
		Title     string `json:"title"`
		CnTitle   string `json:"cn_title,omitempty"`
		FilePath  string `json:"filePath"`
		Content   string `json:"content,omitempty"`
		CreatedAt string `json:"createdAt"`
		UpdatedAt string `json:"updatedAt"`
	}
	return json.Marshal(alias{k.ID, k.Category, k.Title, KnowledgeCNTitle(k.Category, k.Title), k.FilePath, k.Content, KnowledgeFormatTime(k.CreatedAt), KnowledgeFormatTime(k.UpdatedAt)})
}

// knowledgeCatCN 知识库分类 → 中文。
var knowledgeCatCN = map[string]string{
	"SQL Injection":    "SQL 注入",
	"Prompt Injection": "提示注入",
}

// knowledgeTypeSuffix 分类对应的类型后缀（拼在专有名后，如 BigQuery → BigQuery 注入）。
var knowledgeTypeSuffix = map[string]string{
	"SQL Injection":    "注入",
	"Prompt Injection": "注入",
}

// KnowledgeCNTitle 按分类 + 标题生成中文标题（动态生成，不落库）。
// title 形如 "BigQuery Injection" → 去英文类型词 → "BigQuery 注入"；README 归为分类说明。
func KnowledgeCNTitle(category, title string) string {
	t := strings.TrimSpace(title)
	if t == "" {
		return ""
	}
	catCN := knowledgeCatCN[category]
	if strings.EqualFold(t, "README") {
		if catCN != "" {
			return catCN + " · 说明"
		}
		return "说明"
	}
	base := t
	for _, w := range []string{"Injection", "Injection"} {
		if strings.HasSuffix(base, w) {
			base = strings.TrimSpace(strings.TrimSuffix(base, w))
			break
		}
	}
	if strings.EqualFold(base, "SQLmap") {
		return "SQLmap 工具"
	}
	if base == "" {
		if catCN != "" {
			return catCN
		}
		return t
	}
	if suffix := knowledgeTypeSuffix[category]; suffix != "" {
		return base + " " + suffix
	}
	return base
}

// KnowledgeCategoryWithItems 镜像 findu knowledge.CategoryWithItems。
type KnowledgeCategoryWithItems struct {
	Category  string                  `json:"category"`
	ItemCount int                     `json:"itemCount"`
	Items     []*KnowledgeItemSummary `json:"items"`
}

// KnowledgeRetrievalLog 镜像 findu knowledge.RetrievalLog（RetrievedItems 无 omitempty）。
type KnowledgeRetrievalLog struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversationId,omitempty"`
	MessageID      string    `json:"messageId,omitempty"`
	Query          string    `json:"query"`
	RiskType       string    `json:"riskType,omitempty"`
	RetrievedItems []string  `json:"retrievedItems"`
	CreatedAt      time.Time `json:"-"`
}

// MarshalJSON 时间走 RFC3339（zero→""）。
func (r KnowledgeRetrievalLog) MarshalJSON() ([]byte, error) {
	type alias struct {
		ID             string   `json:"id"`
		ConversationID string   `json:"conversationId,omitempty"`
		MessageID      string   `json:"messageId,omitempty"`
		Query          string   `json:"query"`
		RiskType       string   `json:"riskType,omitempty"`
		RetrievedItems []string `json:"retrievedItems"`
		CreatedAt      string   `json:"createdAt"`
	}
	return json.Marshal(alias{r.ID, r.ConversationID, r.MessageID, r.Query, r.RiskType, r.RetrievedItems, KnowledgeFormatTime(r.CreatedAt)})
}

// KnowledgeFormatTime 镜像 findu formatTime：RFC3339，零值返回空串。
func KnowledgeFormatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

// knowledgeTimeFormats 镜像 findu 的多格式时间解析。
var knowledgeTimeFormats = []string{
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02T15:04:05.999999999Z07:00",
	"2006-01-02T15:04:05Z",
	"2006-01-02 15:04:05",
	time.RFC3339,
	time.RFC3339Nano,
}

// KnowledgeParseTime 多格式解析（全部失败返回零值，由调用方按 findu 语义兜底）。
func KnowledgeParseTime(raw string) time.Time {
	for _, format := range knowledgeTimeFormats {
		if parsed, err := time.Parse(format, raw); err == nil && !parsed.IsZero() {
			return parsed
		}
	}
	return time.Time{}
}

// OpenKnowledgeStore 打开知识库 SQLite（文件由 findu 侧 EnsureSchema/初始化保证存在）。
func OpenKnowledgeStore(path string) (*KnowledgeStore, error) {
	handle, err := sql.Open("sqlite3", path+KnowledgeSQLiteDSN)
	if err != nil {
		return nil, fmt.Errorf("打开知识库数据库失败: %w", err)
	}
	// 与 findu configureDBPool 同级的保守连接数（单写者，避免锁竞争）。
	handle.SetMaxOpenConns(2)
	handle.SetMaxIdleConns(2)
	handle.SetConnMaxLifetime(30 * time.Minute)
	if err := handle.Ping(); err != nil {
		_ = handle.Close()
		return nil, fmt.Errorf("连接知识库数据库失败: %w", err)
	}
	return &KnowledgeStore{db: handle}, nil
}

// Close 释放连接（进程生命周期由 server 持有）。
func (s *KnowledgeStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// DB 返回底层 SQLite 句柄，供宿主构建 knowledge 运行态
// （embedder/indexer/retriever，internal/knowledge 各构造函数）复用同一连接池。
func (s *KnowledgeStore) DB() *sql.DB {
	if s == nil {
		return nil
	}
	return s.db
}

// KnowledgeCategories 镜像 GetCategories：DISTINCT category ORDER BY category。
func (s *KnowledgeStore) KnowledgeCategories() ([]string, error) {
	rows, err := s.db.Query("SELECT DISTINCT category FROM knowledge_base_items ORDER BY category")
	if err != nil {
		return nil, fmt.Errorf("查询分类失败: %w", err)
	}
	defer rows.Close()
	var categories []string
	for rows.Next() {
		var category string
		if err := rows.Scan(&category); err != nil {
			return nil, fmt.Errorf("扫描分类失败: %w", err)
		}
		categories = append(categories, category)
	}
	return categories, rows.Err()
}

// KnowledgeStats 镜像 GetStats：分类数 + 条目数。
func (s *KnowledgeStore) KnowledgeStats() (int, int, error) {
	categories, err := s.KnowledgeCategories()
	if err != nil {
		return 0, 0, fmt.Errorf("获取分类失败: %w", err)
	}
	var totalItems int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM knowledge_base_items").Scan(&totalItems); err != nil {
		return len(categories), 0, fmt.Errorf("获取知识项总数失败: %w", err)
	}
	return len(categories), totalItems, nil
}

// knowledgeItemsCount 镜像 GetItemsCount。
func (s *KnowledgeStore) knowledgeItemsCount(category string) (int, error) {
	var count int
	var err error
	if category != "" {
		err = s.db.QueryRow("SELECT COUNT(*) FROM knowledge_base_items WHERE category = ?", category).Scan(&count)
	} else {
		err = s.db.QueryRow("SELECT COUNT(*) FROM knowledge_base_items").Scan(&count)
	}
	if err != nil {
		return 0, fmt.Errorf("查询知识项总数失败: %w", err)
	}
	return count, nil
}

// KnowledgeSearchItemsByKeyword 镜像 SearchItemsByKeyword（LIMIT 无，全部命中按分类+标题排序）。
func (s *KnowledgeStore) KnowledgeSearchItemsByKeyword(keyword, category string) ([]*KnowledgeItemSummary, error) {
	if keyword == "" {
		return nil, fmt.Errorf("搜索关键字不能为空")
	}
	searchPattern := "%" + keyword + "%"
	query := `
		SELECT id, category, title, file_path, created_at, updated_at
		FROM knowledge_base_items
		WHERE (LOWER(title) LIKE LOWER(?) OR LOWER(category) LIKE LOWER(?) OR LOWER(file_path) LIKE LOWER(?) OR LOWER(content) LIKE LOWER(?))`
	args := []any{searchPattern, searchPattern, searchPattern, searchPattern}
	if category != "" {
		query += " AND category = ?"
		args = append(args, category)
	}
	query += " ORDER BY category, title"
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("搜索知识项失败: %w", err)
	}
	defer rows.Close()
	items, err := scanKnowledgeSummaries(rows)
	if err != nil {
		return nil, fmt.Errorf("搜索知识项失败: %w", err)
	}
	return items, nil
}

// KnowledgeItemsSummary 镜像 GetItemsSummary（分页 + 计数）。
func (s *KnowledgeStore) KnowledgeItemsSummary(category string, limit, offset int) ([]*KnowledgeItemSummary, int, error) {
	total, err := s.knowledgeItemsCount(category)
	if err != nil {
		return nil, 0, err
	}
	query := "SELECT id, category, title, file_path, created_at, updated_at FROM knowledge_base_items"
	var args []any
	if category != "" {
		query += " WHERE category = ?"
		args = append(args, category)
	}
	query += " ORDER BY category, title"
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
		if offset > 0 {
			query += " OFFSET ?"
			args = append(args, offset)
		}
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("查询知识项失败: %w", err)
	}
	defer rows.Close()
	items, err := scanKnowledgeSummaries(rows)
	if err != nil {
		return nil, 0, fmt.Errorf("查询知识项失败: %w", err)
	}
	return items, total, nil
}

// KnowledgeCategoriesWithItems 镜像 GetCategoriesWithItems：全分类计数 → 分类分页 → 每分类 take 全量摘要。
func (s *KnowledgeStore) KnowledgeCategoriesWithItems(limit, offset int) ([]*KnowledgeCategoryWithItems, int, error) {
	rows, err := s.db.Query(`
		SELECT category, COUNT(*) as item_count
		FROM knowledge_base_items
		GROUP BY category
		ORDER BY category
	`)
	if err != nil {
		return nil, 0, fmt.Errorf("查询分类失败: %w", err)
	}
	type categoryInfo struct {
		name      string
		itemCount int
	}
	var allCategories []categoryInfo
	for rows.Next() {
		var info categoryInfo
		if err := rows.Scan(&info.name, &info.itemCount); err != nil {
			rows.Close()
			return nil, 0, fmt.Errorf("扫描分类失败: %w", err)
		}
		allCategories = append(allCategories, info)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, 0, fmt.Errorf("扫描分类失败: %w", err)
	}
	rows.Close()

	totalCategories := len(allCategories)
	var paginated []categoryInfo
	if limit > 0 {
		start := offset
		end := offset + limit
		if start >= totalCategories {
			paginated = []categoryInfo{}
		} else {
			if end > totalCategories {
				end = totalCategories
			}
			paginated = allCategories[start:end]
		}
	} else {
		paginated = allCategories
	}

	result := make([]*KnowledgeCategoryWithItems, 0, len(paginated))
	for _, catInfo := range paginated {
		items, _, err := s.KnowledgeItemsSummary(catInfo.name, 0, 0)
		if err != nil {
			return nil, 0, fmt.Errorf("获取分类 %s 的知识项失败: %w", catInfo.name, err)
		}
		result = append(result, &KnowledgeCategoryWithItems{
			Category:  catInfo.name,
			ItemCount: catInfo.itemCount,
			Items:     items,
		})
	}
	return result, totalCategories, nil
}

// KnowledgeItemByID 镜像 GetItem（缺失行返回 errNoKnowledgeItem 语义让 handler 映射 404）。
// KnowledgeItemByID 按 ID 读取单个知识项。
func (s *KnowledgeStore) KnowledgeItemByID(id string) (*KnowledgeItem, error) {
	item := &KnowledgeItem{}
	var createdAt, updatedAt string
	err := s.db.QueryRow(
		"SELECT id, category, title, file_path, content, created_at, updated_at FROM knowledge_base_items WHERE id = ?",
		id,
	).Scan(&item.ID, &item.Category, &item.Title, &item.FilePath, &item.Content, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("知识项不存在")
	}
	if err != nil {
		return nil, fmt.Errorf("查询知识项失败: %w", err)
	}
	item.CreatedAt = KnowledgeParseTime(createdAt)
	item.UpdatedAt = KnowledgeParseTime(updatedAt)
	if item.UpdatedAt.IsZero() && !item.CreatedAt.IsZero() {
		item.UpdatedAt = item.CreatedAt
	}
	return item, nil
}

// CreateKnowledgeItem 镜像 findu Manager.CreateItem：写 base_path 文件 + 插库行，
// 返回创建的 KnowledgeItem（宿主 write 阶段不触发进程内异步索引，索引归后续批次）。
func (s *KnowledgeStore) CreateKnowledgeItem(category, title, content string) (*KnowledgeItem, error) {
	id := uuid.New().String()
	now := time.Now().UTC()
	filePath := filepath.Join(KnowledgeBasePath, category, title+".md")
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		return nil, fmt.Errorf("创建目录失败: %w", err)
	}
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		return nil, fmt.Errorf("写入文件失败: %w", err)
	}
	if _, err := s.db.Exec(
		"INSERT INTO knowledge_base_items (id, category, title, file_path, content, created_at, updated_at) VALUES (?,?,?,?,?,?,?)",
		id, category, title, filePath, content, now, now,
	); err != nil {
		return nil, fmt.Errorf("插入知识项失败: %w", err)
	}
	return &KnowledgeItem{ID: id, Category: category, Title: title, FilePath: filePath, Content: content, CreatedAt: now, UpdatedAt: now}, nil
}

// UpdateKnowledgeItem 镜像 findu Manager.UpdateItem：路径变化则移动文件，重写文件 + 更新库行。
func (s *KnowledgeStore) UpdateKnowledgeItem(id, category, title, content string) (*KnowledgeItem, error) {
	item, err := s.KnowledgeItemByID(id)
	if err != nil {
		return nil, err
	}
	newFilePath := filepath.Join(KnowledgeBasePath, category, title+".md")
	if item.FilePath != newFilePath {
		if err := os.MkdirAll(filepath.Dir(newFilePath), 0o755); err != nil {
			return nil, fmt.Errorf("创建目录失败: %w", err)
		}
		if err := os.Rename(item.FilePath, newFilePath); err != nil {
			return nil, fmt.Errorf("移动文件失败: %w", err)
		}
	}
	if err := os.WriteFile(newFilePath, []byte(content), 0o644); err != nil {
		return nil, fmt.Errorf("写入文件失败: %w", err)
	}
	if _, err := s.db.Exec(
		"UPDATE knowledge_base_items SET category=?, title=?, file_path=?, content=?, updated_at=? WHERE id=?",
		category, title, newFilePath, content, time.Now().UTC(), id,
	); err != nil {
		return nil, fmt.Errorf("更新知识项失败: %w", err)
	}
	item.Category, item.Title, item.FilePath, item.Content = category, title, newFilePath, content
	item.UpdatedAt = time.Now().UTC()
	return item, nil
}

// KnowledgeDeleteItem 镜像 DeleteItem：查 filePath → 删文件 → 删行（FK 级联清向量）→ 清空目录。
func (s *KnowledgeStore) KnowledgeDeleteItem(id string) error {
	var filePath string
	err := s.db.QueryRow("SELECT file_path FROM knowledge_base_items WHERE id = ?", id).Scan(&filePath)
	if err == sql.ErrNoRows {
		return fmt.Errorf("查询知识项失败: sql: no rows in result set")
	}
	if err != nil {
		return fmt.Errorf("查询知识项失败: %w", err)
	}
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		// findu 此处仅告警不中断，删除继续。
		_ = err
	}
	if _, err := s.db.Exec("DELETE FROM knowledge_base_items WHERE id = ?", id); err != nil {
		return fmt.Errorf("删除知识项失败: %w", err)
	}
	// findu：若目录空（忽略隐藏文件）且不是知识库根目录则删除。
	dir := filepath.Dir(filePath)
	if filepath.Clean(dir) == filepath.Clean(KnowledgeBasePath) {
		return nil
	}
	empty, _ := knowledgeIsEmptyDir(dir)
	if empty {
		_ = os.Remove(dir)
	}
	return nil
}

// knowledgeIsEmptyDir 镜像 isEmptyDir（忽略 . 开头隐藏项）。
func knowledgeIsEmptyDir(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".") {
			return false, nil
		}
	}
	return true, nil
}

// KnowledgeItemsCount 镜像 GetItemsCount。
func (s *KnowledgeStore) KnowledgeItemsCount(category string) (int, error) {
	return s.knowledgeItemsCount(category)
}

// KnowledgeItemsWithOptions 镜像 GetItemsWithOptions（includeContent 决定是否取 content 列）。
func (s *KnowledgeStore) KnowledgeItemsWithOptions(category string, limit, offset int, includeContent bool) ([]*KnowledgeItem, error) {
	var query string
	var args []any
	if includeContent {
		query = "SELECT id, category, title, file_path, content, created_at, updated_at FROM knowledge_base_items"
	} else {
		query = "SELECT id, category, title, file_path, created_at, updated_at FROM knowledge_base_items"
	}
	if category != "" {
		query += " WHERE category = ?"
		args = append(args, category)
	}
	query += " ORDER BY category, title"
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
		if offset > 0 {
			query += " OFFSET ?"
			args = append(args, offset)
		}
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询知识项失败: %w", err)
	}
	defer rows.Close()

	var items []*KnowledgeItem
	for rows.Next() {
		item := &KnowledgeItem{}
		var createdAt, updatedAt string
		if includeContent {
			if err := rows.Scan(&item.ID, &item.Category, &item.Title, &item.FilePath, &item.Content, &createdAt, &updatedAt); err != nil {
				return nil, fmt.Errorf("扫描知识项失败: %w", err)
			}
		} else {
			if err := rows.Scan(&item.ID, &item.Category, &item.Title, &item.FilePath, &createdAt, &updatedAt); err != nil {
				return nil, fmt.Errorf("扫描知识项失败: %w", err)
			}
			item.Content = ""
		}
		item.CreatedAt = KnowledgeParseTime(createdAt)
		item.UpdatedAt = KnowledgeParseTime(updatedAt)
		if item.UpdatedAt.IsZero() && !item.CreatedAt.IsZero() {
			item.UpdatedAt = item.CreatedAt
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("查询知识项失败: %w", err)
	}
	return items, nil
}

// KnowledgeRetrievalLogs 镜像 GetRetrievalLogs 三分支（messageID 优先于 conversationID）。
// 无命中返回 nil（JSON "logs":null 与 findu 一致）。
func (s *KnowledgeStore) KnowledgeRetrievalLogs(conversationID, messageID string, limit int) ([]*KnowledgeRetrievalLog, error) {
	var rows *sql.Rows
	var err error
	switch {
	case messageID != "":
		rows, err = s.db.Query(
			"SELECT id, conversation_id, message_id, query, risk_type, retrieved_items, created_at FROM knowledge_retrieval_logs WHERE message_id = ? ORDER BY created_at DESC LIMIT ?",
			messageID, limit,
		)
	case conversationID != "":
		rows, err = s.db.Query(
			"SELECT id, conversation_id, message_id, query, risk_type, retrieved_items, created_at FROM knowledge_retrieval_logs WHERE conversation_id = ? ORDER BY created_at DESC LIMIT ?",
			conversationID, limit,
		)
	default:
		rows, err = s.db.Query(
			"SELECT id, conversation_id, message_id, query, risk_type, retrieved_items, created_at FROM knowledge_retrieval_logs ORDER BY created_at DESC LIMIT ?",
			limit,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("查询检索日志失败: %w", err)
	}
	defer rows.Close()
	var logs []*KnowledgeRetrievalLog
	for rows.Next() {
		log := &KnowledgeRetrievalLog{}
		var createdAt string
		var itemsJSON sql.NullString
		if err := rows.Scan(&log.ID, &log.ConversationID, &log.MessageID, &log.Query, &log.RiskType, &itemsJSON, &createdAt); err != nil {
			return nil, fmt.Errorf("扫描检索日志失败: %w", err)
		}
		log.CreatedAt = KnowledgeParseTime(createdAt)
		if log.CreatedAt.IsZero() {
			// findu：解析失败告警并用当前时间兜底。
			log.CreatedAt = time.Now()
		}
		if itemsJSON.Valid {
			var items []string
			if err := json.Unmarshal([]byte(itemsJSON.String), &items); err == nil {
				log.RetrievedItems = items
			}
		}
		logs = append(logs, log)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("扫描检索日志失败: %w", err)
	}
	return logs, nil
}

// KnowledgeDeleteRetrievalLog 镜像 DeleteRetrievalLog（0 行 → “检索日志不存在”）。
func (s *KnowledgeStore) KnowledgeDeleteRetrievalLog(id string) error {
	result, err := s.db.Exec("DELETE FROM knowledge_retrieval_logs WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("删除检索日志失败: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("获取删除行数失败: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("检索日志不存在")
	}
	return nil
}

// scanKnowledgeSummaries 共用摘要扫描（时间解析 + UpdatedAt 空兜底 CreatedAt）。
func scanKnowledgeSummaries(rows *sql.Rows) ([]*KnowledgeItemSummary, error) {
	var items []*KnowledgeItemSummary
	for rows.Next() {
		item := &KnowledgeItemSummary{}
		var createdAt, updatedAt string
		if err := rows.Scan(&item.ID, &item.Category, &item.Title, &item.FilePath, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("扫描知识项失败: %w", err)
		}
		item.CreatedAt = KnowledgeParseTime(createdAt)
		item.UpdatedAt = KnowledgeParseTime(updatedAt)
		if item.UpdatedAt.IsZero() && !item.CreatedAt.IsZero() {
			item.UpdatedAt = item.CreatedAt
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("扫描知识项失败: %w", err)
	}
	return items, nil
}
