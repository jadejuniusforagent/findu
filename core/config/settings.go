package config

import (
	"os"
	"strconv"
	"strings"
)

// env 收口：本文件是全仓唯一的环境变量读取点。
//
// 约定
//   - 变量名统一前缀 FINDU_（历史遗留 FINDU_* 继续兼容，读取顺序在前）。
//   - 返回值的「空」语义交给调用方：本层不做业务默认值，只做格式与范围校验，
//     避免配置包反向依赖领域包（如 vulnkb.DefaultDir）。
//   - 未设置与设置非法返回同一个零值，调用方无需区分。

// Settings 是一次性从环境变量解析出来的运行期配置快照。
// 新增配置项时在此加字段 + 在 LoadSettings 里解析，不要散落到业务代码。
type Settings struct {
	// VulnKBDir 漏洞知识库目录；空表示调用方使用自身默认目录。
	VulnKBDir string
	// SideQuestionMaxTokens 侧问（BTW）单次回复的最大输出 token；0 表示未设置。
	// 合法区间 256..32768，越界按未设置处理。
	SideQuestionMaxTokens int
	// KeyDir JWT 签名密钥目录；空表示调用方按自身规则推导（dataDir 同级 keys/）。
	KeyDir string
	// NotifyAllowLocal 允许通知投递到本机/链路本地地址的显式逃生口。
	NotifyAllowLocal bool

	// —— findu 子系统（迁移期挂载在原 findu 应用上）——
	// FinduEnable 是否把 findu 子系统挂到 /findu/。
	FinduEnable bool
	// FinduConfig findu 的 YAML 配置路径；空表示调用方用默认文件名。
	FinduConfig string
	// FinduSkipRobots 跳过飞书/钉钉/微信长连接机器人（避免与独立实例抢单点连接）。
	FinduSkipRobots bool
	// FinduWebDir findu 前端静态与模板目录；空表示调用方自动探测。
	FinduWebDir string
	// FinduDataDir findu 的 artifacts 落盘根目录；空表示调用方用默认目录。
	FinduDataDir string
	// FinduDBDSN findu 子系统的 PG DSN；空表示走 SQLite。
	FinduDBDSN string

	// LLM 智能体使用的模型后端配置。
	LLM LLMSettings
}

// LLMSettings 是 LLM 后端的原始配置值（不做默认值与推断，推断留给 agent 包）。
type LLMSettings struct {
	// Provider anthropic | openai | openai-responses；空表示由 key 推断。
	Provider string
	// Model 模型 id；空表示按 provider 取默认。
	Model string
	// BaseURL 自定义端点；空表示 SDK 默认。
	BaseURL string
	// Proxy 代理 URL（已去空白）；空表示直连，不回退 *_PROXY。
	Proxy string
	// AnthropicKey / OpenAIKey 各家的 API key。
	AnthropicKey string
	OpenAIKey    string
	// Stream 是否流式；默认 true，显式 0/false/off/no 才为 false。
	Stream bool
}

// LoadSettings 读取并校验环境变量。纯函数、无副作用，可重复调用。
func LoadSettings() Settings {
	return Settings{
		VulnKBDir:             strings.TrimSpace(envFirst("FINDU_VULNKB_DIR")),
		SideQuestionMaxTokens: maxTokens(envFirst("FINDU_BTW_MAX_OUTPUT_TOKENS")),
		KeyDir:                strings.TrimSpace(envFirst("FINDU_KEY_DIR")),
		NotifyAllowLocal:      isTrue(envFirst("FINDU_NOTIFY_ALLOW_LOCAL")),

		FinduEnable:     isTrue(envFirst("FINDU_ENABLE")),
		FinduConfig:     strings.TrimSpace(envFirst("FINDU_CONFIG")),
		FinduSkipRobots: envFirst("FINDU_SKIP_ROBOTS") == "1",
		FinduWebDir:     strings.TrimSpace(envFirst("FINDU_WEB_DIR")),
		FinduDataDir:    strings.TrimSpace(envFirst("FINDU_DATA_DIR")),
		FinduDBDSN:      strings.TrimSpace(envFirst("FINDU_DB_DSN")),

		LLM: LLMSettings{
			Provider:     envFirst("FINDU_LLM_PROVIDER"),
			Model:        envFirst("FINDU_LLM_MODEL"),
			BaseURL:      envFirst("FINDU_LLM_BASE_URL"),
			Proxy:        strings.TrimSpace(envFirst("FINDU_LLM_PROXY")),
			AnthropicKey: envFirst("ANTHROPIC_API_KEY"),
			OpenAIKey:    envFirst("OPENAI_API_KEY"),
			Stream:       !isFalsy(envFirst("FINDU_LLM_STREAM")),
		},
	}
}

// envFirst 返回第一个非空环境变量值（去掉首尾空白）。
func envFirst(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

// maxTokens 解析 token 上限，非法或越界返回 0（=未设置）。
func maxTokens(raw string) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n < 256 || n > 32768 {
		return 0
	}
	return n
}

// isTrue 报告布尔开关是否打开：只认 1 / true（大小写不敏感），与既有代码语义一致。
func isTrue(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true":
		return true
	}
	return false
}

// isFalsy 报告是否被显式关闭（0/false/off/no）；空值与无法识别的值返回 false，
// 以保持「未设置即沿用默认开启」的语义。
func isFalsy(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "0", "false", "off", "no":
		return true
	}
	return false
}
