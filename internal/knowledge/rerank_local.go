package knowledge

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/cloudwego/eino/schema"
)

// LocalCosineReranker 本地余弦精排：复用知识库同一嵌入模型，将 query 与候选文档
// 一次性批量嵌入后按余弦相似度重排。零外部服务、零 Token 成本、延迟稳定。
//
// 与 HTTP 精排（DashScope gte-rerank / Cohere rerank）的区别：
//   - 优势：不依赖第三方 rerank 端点；本机 Ollama 批量嵌入通常 10~300ms；无网络抖动。
//   - 局限：本质仍是「向量相似度」而非交叉编码器（cross-encoder），对「同义不同词」
//     这类细粒度相关性判断不如专业 rerank 模型。适合作为「比不精排好、比 HTTP 精排便宜」
//     的折中方案。
//
// 重要：必须使用与「文档入库时」相同的嵌入模型，否则向量空间不一致，
// 余弦分将失去意义（例如用 embeddinggemma 给 nomic 索引的文档打分是错误的）。
type LocalCosineReranker struct {
	embedder *Embedder
	// MaxCandidates 参与精排的最大候选数（超出部分保持原序追加在后），0 表示不限制。
	MaxCandidates int
}

// NewLocalCosineReranker 构造本地余弦精排器；embedder 为空时退化为 no-op。
func NewLocalCosineReranker(embedder *Embedder) *LocalCosineReranker {
	return &LocalCosineReranker{embedder: embedder, MaxCandidates: 64}
}

// Rerank implements [DocumentReranker].
//
// 失败时返回原始顺序 + nil 错误不是本实现的策略：调用方（knowledgePipelineRetriever）
// 已对 rerank 失败做「告警 + 回退融合序」的软处理，因此这里直接返回错误即可。
func (l *LocalCosineReranker) Rerank(ctx context.Context, query string, docs []*schema.Document) ([]*schema.Document, error) {
	if l == nil || l.embedder == nil || len(docs) < 2 {
		return docs, nil
	}
	q := strings.TrimSpace(query)
	if q == "" {
		return docs, nil
	}

	limit := len(docs)
	if l.MaxCandidates > 0 && l.MaxCandidates < limit {
		limit = l.MaxCandidates
	}

	texts := make([]string, 0, limit+1)
	texts = append(texts, q)
	for i := 0; i < limit; i++ {
		texts = append(texts, localRerankDocumentText(docs[i]))
	}

	vecs, err := l.embedder.EmbedTexts(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("本地余弦精排嵌入失败: %w", err)
	}
	if len(vecs) != len(texts) {
		return nil, fmt.Errorf("本地余弦精排嵌入数量不匹配: got %d want %d", len(vecs), len(texts))
	}

	qv := vecs[0]
	type scored struct {
		doc *schema.Document
		sim float64
	}
	ranked := make([]scored, 0, limit)
	for i := 0; i < limit; i++ {
		ranked = append(ranked, scored{doc: docs[i], sim: cosineSimilarity(qv, vecs[i+1])})
	}
	sort.SliceStable(ranked, func(a, b int) bool {
		return ranked[a].sim > ranked[b].sim
	})

	out := make([]*schema.Document, 0, len(docs))
	for _, s := range ranked {
		out = append(out, s.doc)
	}
	for i := limit; i < len(docs); i++ {
		out = append(out, docs[i])
	}
	return out, nil
}

// localRerankDocumentText 取用于精排的文本：优先使用入库时写下的原文 metadata.content，
// 其次使用 Document.Content。
func localRerankDocumentText(d *schema.Document) string {
	if d == nil {
		return ""
	}
	if d.MetaData != nil {
		if v, ok := d.MetaData["content"].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return d.Content
}
