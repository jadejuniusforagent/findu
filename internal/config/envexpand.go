package config

import (
	"os"
	"strings"
)

// expandEnvVar 展开字符串中的 ${VAR} 和 ${VAR:-default} 环境变量引用。
// 与官方 MCP 配置格式一致（Claude Desktop / Cursor / VS Code 均支持此语法）。
func expandEnvVar(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		// 查找 ${
		idx := strings.Index(s[i:], "${")
		if idx < 0 {
			b.WriteString(s[i:])
			break
		}
		b.WriteString(s[i : i+idx])
		i += idx + 2 // skip ${

		// 查找对应的 }
		end := strings.IndexByte(s[i:], '}')
		if end < 0 {
			// 没有 }，原样保留
			b.WriteString("${")
			continue
		}
		expr := s[i : i+end]
		i += end + 1 // skip }

		// 解析 VAR:-default
		varName := expr
		defaultVal := ""
		hasDefault := false
		if colonIdx := strings.Index(expr, ":-"); colonIdx >= 0 {
			varName = expr[:colonIdx]
			defaultVal = expr[colonIdx+2:]
			hasDefault = true
		}

		val := os.Getenv(varName)
		if val == "" && hasDefault {
			val = defaultVal
		}
		b.WriteString(val)
	}
	return b.String()
}

// ExpandConfigEnv 展开 ExternalMCPServerConfig 中所有支持环境变量的字段。
// 展开范围：Command、Args、Env values、URL、Headers values。
func ExpandConfigEnv(cfg *ExternalMCPServerConfig) {
	cfg.Command = expandEnvVar(cfg.Command)
	for i, arg := range cfg.Args {
		cfg.Args[i] = expandEnvVar(arg)
	}
	for k, v := range cfg.Env {
		cfg.Env[k] = expandEnvVar(v)
	}
	cfg.URL = expandEnvVar(cfg.URL)
	for k, v := range cfg.Headers {
		cfg.Headers[k] = expandEnvVar(v)
	}
}

// ExpandAIConfigEnv 展开 AI 通道 / Vision / 审计模型中的  引用。
// 与 MCP 一致：支持  和 default。用于避免把 api_key 明文写进仓库。
func ExpandAIConfigEnv(cfg *Config) {
	if cfg.AI.Channels != nil {
		for id, ch := range cfg.AI.Channels {
			ch.APIKey = expandEnvVar(ch.APIKey)
			ch.BaseURL = expandEnvVar(ch.BaseURL)
			ch.Model = expandEnvVar(ch.Model)
			ch.Provider = expandEnvVar(ch.Provider)
			cfg.AI.Channels[id] = ch
		}
	}
	cfg.Vision.APIKey = expandEnvVar(cfg.Vision.APIKey)
	cfg.Vision.BaseURL = expandEnvVar(cfg.Vision.BaseURL)
	cfg.Vision.Model = expandEnvVar(cfg.Vision.Model)
	cfg.Vision.Provider = expandEnvVar(cfg.Vision.Provider)
	cfg.Hitl.AuditModel.APIKey = expandEnvVar(cfg.Hitl.AuditModel.APIKey)
	cfg.Hitl.AuditModel.BaseURL = expandEnvVar(cfg.Hitl.AuditModel.BaseURL)
	cfg.Hitl.AuditModel.Model = expandEnvVar(cfg.Hitl.AuditModel.Model)
	cfg.Hitl.AuditModel.Provider = expandEnvVar(cfg.Hitl.AuditModel.Provider)
}
