package knowledge

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/schema"

	"finduai/internal/config"
)

// TestRewriteLLMConfigApplyTo 验证改写通道配置覆盖主通道、且清空推理设置。
func TestRewriteLLMConfigApplyTo(t *testing.T) {
	base := config.OpenAIConfig{
		Provider:            "openai_compatible",
		APIKey:              "sk-main",
		BaseURL:             "https://api.deepseek.com/v1",
		Model:               "deepseek-flash",
		MaxCompletionTokens: 32768,
		Reasoning: config.OpenAIReasoningConfig{
			Mode:    "on",
			Effort:  "high",
			Profile: "deepseek",
		},
	}

	// 未配置时 Enabled()==false，且不改变主通道。
	empty := config.RewriteLLMConfig{}
	if empty.Enabled() {
		t.Fatalf("empty rewrite config must be disabled")
	}
	if got := empty.ApplyTo(base); got.BaseURL != base.BaseURL || got.Model != base.Model {
		t.Fatalf("ApplyTo must keep base when disabled, got %+v", got)
	}

	rc := config.RewriteLLMConfig{
		Provider: "ollama",
		BaseURL:  "http://127.0.0.1:11434/v1",
		APIKey:   "ollama",
		Model:    "qwen2.5:1.5b",
	}
	if !rc.Enabled() {
		t.Fatalf("configured rewrite must be enabled")
	}
	got := rc.ApplyTo(base)
	if got.BaseURL != rc.BaseURL {
		t.Fatalf("base_url not overridden: %s", got.BaseURL)
	}
	if got.Model != rc.Model {
		t.Fatalf("model not overridden: %s", got.Model)
	}
	if strings.TrimSpace(got.APIKey) != "ollama" {
		t.Fatalf("api_key not overridden: %s", got.APIKey)
	}
	if got.Reasoning.Mode != "" || got.Reasoning.Effort != "" || got.Reasoning.Profile != "" {
		t.Fatalf("reasoning must be cleared for local small model, got %+v", got.Reasoning)
	}
	if got.MaxCompletionTokens != 512 {
		t.Fatalf("max_completion_tokens default should be 512, got %d", got.MaxCompletionTokens)
	}
	// 原始 base 不被修改。
	if base.BaseURL != "https://api.deepseek.com/v1" || base.Reasoning.Mode != "on" {
		t.Fatalf("ApplyTo must not mutate input, got %+v", base)
	}
}

// TestRewriteLLMConfigApplyToKeepsAPIKey 主通道有 key 且改写未配 key 时保留主 key。
func TestRewriteLLMConfigApplyToKeepsAPIKey(t *testing.T) {
	base := config.OpenAIConfig{APIKey: "sk-main", BaseURL: "https://x/v1", Model: "m"}
	rc := config.RewriteLLMConfig{BaseURL: "http://127.0.0.1:11434/v1", Model: "qwen2.5:1.5b"}
	got := rc.ApplyTo(base)
	if got.APIKey != "sk-main" {
		t.Fatalf("api_key should be inherited, got %s", got.APIKey)
	}

	base2 := config.OpenAIConfig{BaseURL: "https://x/v1", Model: "m"}
	got2 := rc.ApplyTo(base2)
	if got2.APIKey != "ollama" {
		t.Fatalf("api_key should fall back to ollama, got %s", got2.APIKey)
	}
}

// TestIsLocalCosineRerank 验证本地余弦精排的 provider 识别。
func TestIsLocalCosineRerank(t *testing.T) {
	for _, p := range []string{"local", "LOCAL", "local_cosine", "local-cosine", "cosine", " cosine "} {
		if !isLocalCosineRerank(config.RerankConfig{Provider: p}) {
			t.Fatalf("provider %q should enable local cosine rerank", p)
		}
	}
	for _, p := range []string{"", "cohere", "dashscope", "gte-rerank"} {
		if isLocalCosineRerank(config.RerankConfig{Provider: p}) {
			t.Fatalf("provider %q should NOT enable local cosine rerank", p)
		}
	}
}

// TestRerankEnabled 验证 HTTP 精排仅在显式配置 provider/model 时启用；
// 单独残留 base_url 不得启用——否则 Web UI 保存写回残留 base_url 会重新产生必然失败的远程精排请求。
func TestRerankEnabled(t *testing.T) {
	if rerankEnabled(config.RerankConfig{}) {
		t.Fatalf("empty rerank config must be disabled")
	}
	if !rerankEnabled(config.RerankConfig{Provider: "cohere"}) {
		t.Fatalf("provider should enable http rerank")
	}
	if rerankEnabled(config.RerankConfig{BaseURL: "http://127.0.0.1:11434/v1"}) {
		t.Fatalf("stray base_url alone must NOT enable http rerank (regression guard)")
	}
}

// TestLocalCosineRerankNilSafe 验证空嵌入器 / 单文档时安全退化为原序。
func TestLocalCosineRerankNilSafe(t *testing.T) {
	docs := []*schema.Document{
		{Content: "a"},
		{Content: "b"},
	}
	rr := &LocalCosineReranker{}
	got, err := rr.Rerank(context.Background(), "q", docs)
	if err != nil {
		t.Fatalf("nil embedder must not error: %v", err)
	}
	if len(got) != 2 || got[0] != docs[0] || got[1] != docs[1] {
		t.Fatalf("nil embedder must keep order")
	}

	rr2 := NewLocalCosineReranker(nil)
	got2, err := rr2.Rerank(context.Background(), "q", docs[:1])
	if err != nil || len(got2) != 1 {
		t.Fatalf("single doc must be returned as-is: %v", err)
	}
}

// TestOllamaRewriteModelLive 真实调用本机 Ollama 的 OpenAI 兼容端点，验证：
//  1. 改写模型（默认 qwen2.5:1.5b）可正常调用；
//  2. 返回非空正文（避免触发「空正文续跑」放大）；
//  3. 延迟显著低于主通道推理模型。
//
// 需显式开启：FINDUAI_OLLAMA_TEST=1 go test ./internal/knowledge/ -run Ollama -v
func TestOllamaRewriteModelLive(t *testing.T) {
	if os.Getenv("FINDUAI_OLLAMA_TEST") != "1" {
		t.Skip("set FINDUAI_OLLAMA_TEST=1 to run live Ollama test")
	}
	baseURL := strings.TrimSpace(os.Getenv("FINDUAI_OLLAMA_BASE_URL"))
	if baseURL == "" {
		baseURL = "http://127.0.0.1:11434/v1"
	}
	modelName := strings.TrimSpace(os.Getenv("FINDUAI_OLLAMA_MODEL"))
	if modelName == "" {
		modelName = "qwen2.5:1.5b"
	}

	base := config.OpenAIConfig{
		Provider: "openai_compatible",
		APIKey:   "sk-main",
		BaseURL:  "https://api.deepseek.com/v1",
		Model:    "deepseek-flash",
		Reasoning: config.OpenAIReasoningConfig{
			Mode:    "on",
			Effort:  "high",
			Profile: "deepseek",
		},
	}
	rc := config.RewriteLLMConfig{Provider: "ollama", BaseURL: baseURL, APIKey: "ollama", Model: modelName}
	applied := rc.ApplyTo(base)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	mt := applied.MaxCompletionTokens
	cm, err := einoopenai.NewChatModel(ctx, &einoopenai.ChatModelConfig{
		APIKey:              applied.APIKey,
		BaseURL:             strings.TrimSuffix(applied.BaseURL, "/"),
		Model:               applied.Model,
		MaxCompletionTokens: &mt,
	})
	if err != nil {
		t.Fatalf("build ollama chat model: %v", err)
	}

	msgs := []*schema.Message{
		schema.SystemMessage("你是知识库检索查询改写助手。把用户问题改写为 3 条中文检索关键词，每行一条，不要编号，不要解释。"),
		schema.UserMessage("平台响应速度很慢，如何排查瓶颈"),
	}

	// 预热一次（首次可能触发模型加载）。
	_, _ = cm.Generate(ctx, msgs)

	start := time.Now()
	msg, err := cm.Generate(ctx, msgs)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("ollama generate: %v", err)
	}
	if msg == nil {
		t.Fatalf("nil message from ollama")
	}
	if strings.TrimSpace(msg.Content) == "" {
		t.Fatalf("EMPTY content from ollama/%s (reasoning consumed budget?)", modelName)
	}
	t.Logf("OK ollama/%s latency=%s content=%q", modelName, elapsed, strings.TrimSpace(msg.Content))

	if elapsed > 10*time.Second {
		t.Errorf("rewrite latency too high: %s", elapsed)
	}
}
