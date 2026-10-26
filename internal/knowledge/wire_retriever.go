package knowledge

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"finduai/internal/config"
	"finduai/internal/llm"
	"finduai/internal/openai"

	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/flow/retriever/multiquery"
	"go.uber.org/zap"
)

// WireRetrieverPipeline builds Eino MultiQuery + HTTP rerank + post-process pipeline on r.
// Call once after NewRetriever; UpdateConfig re-invokes when wireOpenAI is set.
func WireRetrieverPipeline(ctx context.Context, r *Retriever, openAI *config.OpenAIConfig) error {
	if r == nil {
		return fmt.Errorf("retriever is nil")
	}
	if openAI == nil {
		return fmt.Errorf("openai config is nil")
	}
	if r.config == nil {
		return fmt.Errorf("retrieval config is nil")
	}
	r.wireOpenAI = openAI

	baseHTTPClient := &http.Client{Timeout: 120 * time.Second}
	// 查询改写通道：若显式配置了 knowledge.multi_query.rewrite，则改用独立（通常为本机
	// Ollama）小模型，避免每次知识检索都额外调用主对话的昂贵/强制推理大模型。
	rewriteCfg := openAI
	if rc := r.config.MultiQuery.Rewrite; rc.Enabled() {
		applied := rc.ApplyTo(*openAI)
		rewriteCfg = &applied
		if r.logger != nil {
			r.logger.Info("知识检索查询改写已切换为独立模型",
				zap.String("rewrite_base_url", strings.TrimRight(strings.TrimSpace(applied.BaseURL), "/")),
				zap.String("rewrite_model", strings.TrimSpace(applied.Model)),
			)
		}
	}
	var rewriteLLM model.ChatModel
	if llm.IsClaudeProvider(rewriteCfg.Provider) {
		nativeModel, err := llm.NewClaudeAgenticModel(
			ctx,
			*rewriteCfg,
			baseHTTPClient,
			rewriteCfg.MaxCompletionTokensEffective(),
			nil,
		)
		if err != nil {
			return fmt.Errorf("multi_query native Claude rewrite model: %w", err)
		}
		rewriteLLM = llm.NewAgenticChatModelAdapter(nativeModel)
	} else {
		httpClient := openai.NewEinoHTTPClient(rewriteCfg, baseHTTPClient)
		maxCompletionTokens := rewriteCfg.MaxCompletionTokensEffective()
		chatCfg := &einoopenai.ChatModelConfig{
			APIKey:              strings.TrimSpace(rewriteCfg.APIKey),
			BaseURL:             strings.TrimSuffix(strings.TrimSpace(rewriteCfg.BaseURL), "/"),
			Model:               strings.TrimSpace(rewriteCfg.Model),
			HTTPClient:          httpClient,
			MaxCompletionTokens: &maxCompletionTokens,
		}
		if chatCfg.Model == "" {
			chatCfg.Model = "gpt-4o"
		}
		var err error
		rewriteLLM, err = einoopenai.NewChatModel(ctx, chatCfg)
		if err != nil {
			return fmt.Errorf("multi_query rewrite model: %w", err)
		}
	}

	// 精排为可选能力（默认关闭，避免每次检索都对不存在的端点发一次必然失败的请求）：
	//   - provider = local | local_cosine | cosine → 本地余弦精排（复用知识库嵌入模型）；
	//   - 其它显式配置 → HTTP 精排（DashScope gte-rerank / Cohere rerank）；
	//   - 未配置 → Nop（保持融合序）。
	switch {
	case isLocalCosineRerank(r.config.Rerank):
		if r.embedder == nil {
			if r.logger != nil {
				r.logger.Warn("本地余弦精排不可用（嵌入器为空），已降级为融合序")
			}
			r.SetDocumentReranker(NopDocumentReranker{})
		} else {
			r.SetDocumentReranker(NewLocalCosineReranker(r.embedder))
			if r.logger != nil {
				r.logger.Info("知识检索已启用本地余弦精排")
			}
		}
	case rerankEnabled(r.config.Rerank):
		reranker, rerr := NewHTTPReranker(&r.config.Rerank, openAI, r.logger)
		if rerr != nil {
			if r.logger != nil {
				r.logger.Warn("知识检索精排初始化失败，已降级为融合序", zap.Error(rerr))
			}
			r.SetDocumentReranker(NopDocumentReranker{})
		} else {
			r.SetDocumentReranker(reranker)
		}
	default:
		r.SetDocumentReranker(NopDocumentReranker{})
	}

	vec := NewVectorEinoRetriever(r)
	mq, err := multiquery.NewRetriever(ctx, &multiquery.Config{
		RewriteLLM:    rewriteLLM,
		MaxQueriesNum: r.config.MultiQuery.MaxQueriesEffective(),
		OrigRetriever: vec,
	})
	if err != nil {
		return fmt.Errorf("multi_query: %w", err)
	}

	r.pipeline = newKnowledgePipelineRetriever(mq, r)
	if rerankStrayBaseURL(r.config.Rerank) && r.logger != nil {
		r.logger.Warn("检测到 rerank.base_url 但未显式配置 provider/model，已按「关闭精排」处理（避免由 base_url 误推断 provider 产生必然失败的远程精排请求）",
			zap.String("rerank_base_url", strings.TrimSpace(r.config.Rerank.BaseURL)))
	}
	if r.logger != nil {
		rerankOn := rerankEnabled(r.config.Rerank) || isLocalCosineRerank(r.config.Rerank)
		rerankProvider := "disabled"
		rerankModel := ""
		if rerankOn {
			rerankProvider = r.config.Rerank.ProviderEffective(strings.TrimSpace(openAI.BaseURL))
			rerankModel = r.config.Rerank.ModelEffective(rerankProvider)
		}
		r.logger.Info("知识库检索流水线已启用",
			zap.String("pipeline", "MultiQuery→Vector→PostRetrieve"),
			zap.Int("multi_query_max", r.config.MultiQuery.MaxQueriesEffective()),
			zap.Bool("rerank_enabled", rerankOn),
			zap.String("rerank_provider", rerankProvider),
			zap.String("rerank_model", rerankModel),
		)
	}
	return nil
}

// isLocalCosineRerank 精排是否使用「本地余弦」模式（复用知识库嵌入模型，无外部依赖）。
func isLocalCosineRerank(rc config.RerankConfig) bool {
	switch strings.ToLower(strings.TrimSpace(rc.Provider)) {
	case "local", "local_cosine", "local-cosine", "cosine":
		return true
	}
	return false
}

// rerankEnabled 判定是否启用「远程 HTTP 精排」。
//
// 必须显式配置 provider 或 model 才启用：**单独残留 base_url 不构成启用条件**。
// 原因：ProviderEffective 在 provider 为空时会回落 "cohere"，而 Web UI 保存配置会写回残留的
// base_url，于是「仅残留 base_url」会把精排重新打开、指向没有 /v1/rerank 的端点，产生每次
// 检索必然失败的 HTTP 往返。详见 finding/rerank-failure-still-live。
func rerankEnabled(rc config.RerankConfig) bool {
	return strings.TrimSpace(rc.Provider) != "" || strings.TrimSpace(rc.Model) != ""
}

// rerankStrayBaseURL 判断「只残留 base_url、未显式配置 provider/model」这一危险状态，用于输出告警。
func rerankStrayBaseURL(rc config.RerankConfig) bool {
	return !rerankEnabled(rc) && strings.TrimSpace(rc.BaseURL) != ""
}
