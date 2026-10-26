package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadWSLConfigRewriteAndRerank 校验 config.wsl.yaml 中的
// knowledge.retrieval.multi_query.rewrite 与 knowledge.retrieval.rerank.provider 能被正确反序列化。
func TestLoadWSLConfigRewriteAndRerank(t *testing.T) {
	path := filepath.Join("..", "..", "config.wsl.yaml")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("config.wsl.yaml not found: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	rc := cfg.Knowledge.Retrieval.MultiQuery.Rewrite
	if !rc.Enabled() {
		t.Fatalf("multi_query.rewrite not parsed (enabled=false, %+v)", rc)
	}
	if rc.Model == "" || rc.BaseURL == "" {
		t.Fatalf("rewrite base_url/model empty: %+v", rc)
	}
	if got := rc.TimeoutEffective(); got != 30 {
		t.Errorf("timeout effective = %d, want 30", got)
	}
	if got := rc.MaxCompletionTokensEffective(); got != 512 {
		t.Errorf("max completion tokens effective = %d, want 512", got)
	}
	t.Logf("rewrite LLM: provider=%s base_url=%s model=%s timeout=%ds max_tokens=%d",
		rc.Provider, rc.BaseURL, rc.Model, rc.TimeoutEffective(), rc.MaxCompletionTokensEffective())

	rp := cfg.Knowledge.Retrieval.Rerank.Provider
	t.Logf("rerank provider=%q model=%s base_url=%s", rp, cfg.Knowledge.Retrieval.Rerank.Model, cfg.Knowledge.Retrieval.Rerank.BaseURL)
	switch strings.ToLower(strings.TrimSpace(rp)) {
	case "", "local", "local_cosine", "local-cosine", "cosine", "cohere", "dashscope":
		// 合法取值：空=关闭（Nop），local_cosine=本机余弦精排，其余=远程 /v1/rerank。
	default:
		t.Errorf("unexpected rerank provider: %q", rp)
	}

	// 主通道不应被改写配置污染。
	ch, ok := cfg.AI.Channels["deepseek"]
	if ok && ch.BaseURL != "" {
		t.Logf("main channel: base_url=%s model=%s", ch.BaseURL, ch.Model)
	}
}
