package knowledge

import (
	"testing"

	"finduai/internal/config"
)

// TestRerankEnabledRequiresExplicitProviderOrModel 固化「残留 base_url 不再启用精排」这一修复：
// 这是消除「Web UI 保存配置 → rerank 反向物化 → 必然失败的 HTTP 往返」复现的关键判据。
func TestRerankEnabledRequiresExplicitProviderOrModel(t *testing.T) {
	cases := []struct {
		name      string
		rc        config.RerankConfig
		enabled   bool
		wantStray bool
	}{
		{
			name:      "全空=关闭",
			rc:        config.RerankConfig{},
			enabled:   false,
			wantStray: false,
		},
		{
			name:      "仅残留 base_url=不启用（旧实现会误启用并产生 404）",
			rc:        config.RerankConfig{BaseURL: "http://127.0.0.1:11434/v1", APIKey: "ollama"},
			enabled:   false,
			wantStray: true,
		},
		{
			name:      "显式 provider=启用",
			rc:        config.RerankConfig{Provider: "cohere", BaseURL: "http://x/v1"},
			enabled:   true,
			wantStray: false,
		},
		{
			name:      "显式 model=启用",
			rc:        config.RerankConfig{Model: "gte-rerank"},
			enabled:   true,
			wantStray: false,
		},
		{
			name:      "local_cosine=本地余弦启用",
			rc:        config.RerankConfig{Provider: "local_cosine"},
			enabled:   true,
			wantStray: false,
		},
		{
			name:      "空白字符串不算配置",
			rc:        config.RerankConfig{Provider: "  ", Model: "\t", BaseURL: " http://y/v1 "},
			enabled:   false,
			wantStray: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := rerankEnabled(c.rc); got != c.enabled {
				t.Fatalf("rerankEnabled()=%v, want %v", got, c.enabled)
			}
			if got := rerankStrayBaseURL(c.rc); got != c.wantStray {
				t.Fatalf("rerankStrayBaseURL()=%v, want %v", got, c.wantStray)
			}
		})
	}
}
