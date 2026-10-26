package knowledge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"

	"finduai/internal/config"
)

// TestLocalCosineRerankQualityLive 端到端验证「本地余弦精排」的排序质量与延迟。
//
// 重要结论（实测）：本地余弦精排的质量完全取决于嵌入模型对目标语言（中文）的区分度。
// 现有索引模型 nomic-embed-text 对中文几乎无区分度（同组文档目标仅排 4/5），
// 用它做精排会「越排越错」；embeddinggemma 对中文区分良好（目标排 1/5）。
// 因此：精排所用嵌入模型必须与索引一致，且必须具备中文能力。
//
// 运行：
//
//	FINDUAI_OLLAMA_TEST=1 go test ./internal/knowledge/ -run LocalCosineRerankQualityLive -v
//	FINDUAI_OLLAMA_TEST=1 FINDUAI_EMBED_MODEL=embeddinggemma:latest go test ./internal/knowledge/ -run LocalCosineRerankQualityLive -v
func TestLocalCosineRerankQualityLive(t *testing.T) {
	if os.Getenv("FINDUAI_OLLAMA_TEST") != "1" {
		t.Skip("set FINDUAI_OLLAMA_TEST=1 to run live test")
	}
	cfgPath := filepath.Join("..", "..", "config.wsl.yaml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Knowledge.Embedding.Model == "" {
		t.Fatalf("knowledge.embedding.model empty")
	}
	if override := strings.TrimSpace(os.Getenv("FINDUAI_EMBED_MODEL")); override != "" {
		cfg.Knowledge.Embedding.Model = override
	}

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	emb, err := NewEmbedder(ctx, &cfg.Knowledge, nil, zap.NewNop())
	if err != nil {
		t.Fatalf("NewEmbedder: %v", err)
	}
	model := cfg.Knowledge.Embedding.Model
	t.Logf("embedder model=%s base_url=%s", model, cfg.Knowledge.Embedding.BaseURL)

	query := "平台响应速度很慢，如何排查瓶颈"
	docs := []*schema.Document{
		{Content: "红烧肉的家常做法：五花肉切块焯水，加冰糖炒糖色，小火慢炖四十分钟。"},
		{Content: "SQLite 使用 WAL 模式并配置 busy_timeout 可显著降低写锁竞争，连接池 MaxOpen 建议不超过 25。"},
		{Content: "网球正手击球要领：转体引拍、蹬地转髋、随挥收拍至左肩上方。"},
		{Content: "响应延迟排查要点：先定位单次 LLM 调用耗时，再检查重试放大与观测采样开销，最后核对数据库热点查询 p95。"},
		{Content: "如何给绿萝浇水：见干见湿，避免盆内积水导致烂根。"},
	}
	const targetIdx = 3

	rr := NewLocalCosineReranker(emb)
	if _, err := emb.EmbedTexts(ctx, []string{"warmup"}); err != nil {
		t.Fatalf("warmup embed: %v", err)
	}

	start := time.Now()
	got, err := rr.Rerank(ctx, query, docs)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if len(got) != len(docs) {
		t.Fatalf("ranked length changed: %d -> %d", len(docs), len(got))
	}

	targetRank := 0
	for i, d := range got {
		if d == docs[targetIdx] {
			targetRank = i + 1
		}
		t.Logf("rank %d: %s", i+1, truncateRune(d.Content, 36))
	}
	t.Logf("model=%s 目标文档排名=%d/%d latency=%s", model, targetRank, len(docs), elapsed)

	if elapsed > 5*time.Second {
		t.Errorf("local cosine rerank too slow: %s", elapsed)
	}
	// 仅在显式使用 embeddinggemma（具备中文能力的多语言模型）时断言排序质量，
	// 避免对已知中文能力薄弱的 nomic-embed-text 产生误报。
	if strings.Contains(strings.ToLower(model), "gemma") {
		if targetRank != 1 {
			t.Errorf("gemma 精排质量不合格：目标文档排名 %d/%d，期望 1", targetRank, len(docs))
		}
	} else if targetRank != 1 {
		t.Logf("WARN 当前嵌入模型 %s 对中文区分度不足（目标排名 %d/%d），不建议启用本地余弦精排", model, targetRank, len(docs))
	}
}

func truncateRune(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
