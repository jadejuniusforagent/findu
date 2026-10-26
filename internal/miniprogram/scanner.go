package miniprogram

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// FindingCategory 敏感信息分类
type FindingCategory string

const (
	CategoryAPIEndpoint  FindingCategory = "api_endpoint"
	CategorySecret       FindingCategory = "secret"
	CategoryAppID        FindingCategory = "appid"
	CategoryCloudEnv     FindingCategory = "cloud_env"
	CategorySensitiveURL FindingCategory = "sensitive_url"
)

// FindingSeverity 敏感等级
type FindingSeverity string

const (
	SeverityCritical FindingSeverity = "critical"
	SeverityHigh     FindingSeverity = "high"
	SeverityMedium   FindingSeverity = "medium"
	SeverityLow      FindingSeverity = "low"
	SeverityInfo     FindingSeverity = "info"
)

// ScanRule 扫描规则
type ScanRule struct {
	Name     string
	Category FindingCategory
	Severity FindingSeverity
	Pattern  *regexp.Regexp
	Describe func(match string) string
	// Filter 可选:返回 false 表示该命中是误报(如压缩库里的 PEM 模板字符串),不产出发现
	Filter func(match string) bool
}

// 默认扫描规则集
var defaultScanRules = []ScanRule{
	// API 接口提取
	{
		Name:     "wx_request",
		Category: CategoryAPIEndpoint,
		Severity: SeverityInfo,
		Pattern:  regexp.MustCompile(`wx\.request\s*\(\s*\{[^}]*url\s*:\s*["']([^"']+)["']`),
		Describe: func(m string) string { return "发现 wx.request API 调用: " + m },
	},
	{
		Name:     "wx_upload",
		Category: CategoryAPIEndpoint,
		Severity: SeverityMedium,
		Pattern:  regexp.MustCompile(`wx\.uploadFile\s*\(\s*\{[^}]*url\s*:\s*["']([^"']+)["']`),
		Describe: func(m string) string { return "发现 wx.uploadFile 上传接口: " + m },
	},
	{
		Name:     "wx_download",
		Category: CategoryAPIEndpoint,
		Severity: SeverityInfo,
		Pattern:  regexp.MustCompile(`wx\.downloadFile\s*\(\s*\{[^}]*url\s*:\s*["']([^"']+)["']`),
		Describe: func(m string) string { return "发现 wx.downloadFile 下载接口: " + m },
	},
	{
		Name:     "fetch_api",
		Category: CategoryAPIEndpoint,
		Severity: SeverityInfo,
		Pattern:  regexp.MustCompile(`fetch\s*\(\s*["']([^"']+)["']`),
		Describe: func(m string) string { return "发现 fetch API 调用: " + m },
	},
	{
		Name:     "cloud_call",
		Category: CategoryCloudEnv,
		Severity: SeverityHigh,
		Pattern:  regexp.MustCompile(`cloud\.callFunction\s*\(\s*\{[^}]*name\s*:\s*["']([^"']+)["']`),
		Describe: func(m string) string { return "发现云函数调用: " + m },
	},

	// 硬编码密钥
	{
		Name:     "aws_key",
		Category: CategorySecret,
		Severity: SeverityCritical,
		Pattern:  regexp.MustCompile(`(?:AKIA|ASIA)[A-Z0-9]{16}`),
		Describe: func(m string) string { return "发现 AWS Access Key: " + m },
	},
	{
		Name:     "generic_key",
		Category: CategorySecret,
		Severity: SeverityHigh,
		Pattern:  regexp.MustCompile(`(?i)(?:api[_-]?key|secret[_-]?key|app[_-]?secret|access[_-]?token|auth[_-]?token)\s*[:=]\s*["']([^"']{8,})["']`),
		Describe: func(m string) string { return "发现硬编码密钥: " + m },
	},
	{
		Name:     "private_key",
		Category: CategorySecret,
		Severity: SeverityCritical,
		// 必须跟真实 base64 正文;库里的 "-----BEGIN RSA PRIVATE KEY-----\n"+wordwrap(...) 模板不算
		Pattern:  regexp.MustCompile(`-----BEGIN (?:RSA |EC )?PRIVATE KEY-----[\s\r\n]+[A-Za-z0-9+/=\s\r\n]{64,}`),
		Describe: func(m string) string { return "发现内嵌私钥(PEM)" },
	},
	{
		Name:     "password_literal",
		Category: CategorySecret,
		Severity: SeverityHigh,
		Pattern:  regexp.MustCompile(`(?i)(?:password|passwd|pwd)\s*[:=]\s*["']([^"']{4,})["']`),
		Describe: func(m string) string { return "发现硬编码密码: " + m },
		// 值里带 / 的多是接口路径常量(如 PASSWORD="passports/wSetPasswordApi"),不算密码
		Filter: func(m string) bool { return !strings.Contains(m, "/") },
	},

	// AppID 泄露
	{
		Name:     "appid_config",
		Category: CategoryAppID,
		Severity: SeverityInfo,
		Pattern:  regexp.MustCompile(`"appid"\s*:\s*"([a-f0-9]{18})"`),
		Describe: func(m string) string { return "发现小程序 AppID 配置: " + m },
	},
	{
		Name:     "appid_hardcoded",
		Category: CategoryAppID,
		Severity: SeverityMedium,
		Pattern:  regexp.MustCompile(`wx[a-f0-9]{16}`),
		Describe: func(m string) string { return "发现硬编码 AppID: " + m },
	},

	// 云开发环境
	{
		Name:     "cloud_env",
		Category: CategoryCloudEnv,
		Severity: SeverityMedium,
		Pattern:  regexp.MustCompile(`(?:env|environment)\s*[:=]\s*["']([a-zA-Z0-9_-]{4,})["']`),
		Describe: func(m string) string { return "发现云开发环境配置: " + m },
	},

	// 敏感 URL
	{
		Name:     "internal_url",
		Category: CategorySensitiveURL,
		Severity: SeverityHigh,
		Pattern:  regexp.MustCompile(`(?:https?://(?:10\.|172\.(?:1[6-9]|2[0-9]|3[01])\.|192\.168\.|localhost|127\.0\.0\.1)[^\s"']*)`),
		Describe: func(m string) string { return "发现内网地址: " + m },
	},
	{
		Name:     "debug_endpoint",
		Category: CategorySensitiveURL,
		Severity: SeverityMedium,
		Pattern:  regexp.MustCompile(`(?:https?://[^\s"']*(?:/debug|/admin|/console|/api/internal)[^\s"']*)`),
		Describe: func(m string) string { return "发现管理/调试接口: " + m },
	},
	{
		// 压缩/配置里的裸地址(游戏类小程序接口常写在运行时配置里,无 wx.request 字面量)
		Name:     "external_url",
		Category: CategoryAPIEndpoint,
		Severity: SeverityInfo,
		Pattern:  regexp.MustCompile(`(?:https?|wss?)://[a-zA-Z0-9][a-zA-Z0-9._-]*(?:\.[a-zA-Z]{2,})+(?::\d+)?(?:/[^\s"'\\<>()\[\]{}]*)??`),
		Describe: func(m string) string { return "发现接口地址: " + m },
	},
}

// Scanner 敏感信息扫描器
type Scanner struct {
	rules []ScanRule
}

// NewScanner 创建扫描器
func NewScanner() *Scanner {
	return &Scanner{rules: defaultScanRules}
}

// 扫描规模上限:防止超大/超长行文件拖垮扫描
const (
	maxScanFileBytes   = 16 << 20 // 单文件最多读 16MB
	maxFindingsPerFile = 200      // 单文件最多产出,防刷屏
	maxFindingContent  = 300      // 命中内容截断长度
)

// ScanFile 扫描单个文件。整文件读取后按规则全局匹配:
// 小程序(尤其游戏类)代码常压缩成超长单行,bufio.Scanner 的 64KB 行上限会静默漏掉,
// 因此这里按内容偏移计算行号,长行/压缩文件同样能命中。
func (s *Scanner) ScanFile(filePath string) ([]Finding, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	if len(data) > maxScanFileBytes {
		data = data[:maxScanFileBytes]
	}
	content := string(data)
	lineStarts := []int{0}
	for i := 0; i < len(content); i++ {
		if content[i] == '\n' {
			lineStarts = append(lineStarts, i+1)
		}
	}
	lineAt := func(off int) int {
		lo, hi := 0, len(lineStarts)-1
		for lo < hi {
			mid := (lo + hi + 1) / 2
			if lineStarts[mid] <= off {
				lo = mid
			} else {
				hi = mid - 1
			}
		}
		return lo + 1
	}

	var findings []Finding
	seen := map[string]bool{}
	for _, rule := range s.rules {
		for _, loc := range rule.Pattern.FindAllStringIndex(content, maxFindingsPerFile) {
			match := content[loc[0]:loc[1]]
			if rule.Filter != nil && !rule.Filter(match) {
				continue
			}
			key := rule.Name + "\x00" + match
			if seen[key] {
				continue
			}
			seen[key] = true
			desc := match
			if rule.Describe != nil {
				desc = rule.Describe(match)
			}
			if len(desc) > maxFindingContent {
				desc = desc[:maxFindingContent]
			}
			findings = append(findings, Finding{
				Category:    string(rule.Category),
				Severity:    string(rule.Severity),
				FilePath:    filePath,
				LineNumber:  lineAt(loc[0]),
				Content:     match,
				Description: desc,
			})
			if len(findings) >= maxFindingsPerFile {
				return findings, nil
			}
		}
	}
	return findings, nil
}

// ScanDirectory 递归扫描目录下所有文本文件
func (s *Scanner) ScanDirectory(dir string) ([]Finding, error) {
	// 目标目录必须存在（不存在时返回错误而非静默空结果）
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		if err == nil {
			err = fmt.Errorf("%s 不是目录", dir)
		}
		return nil, err
	}

	var allFindings []Finding
	// 支持的文本文件扩展名
	textExts := map[string]bool{
		".js": true, ".json": true, ".wxml": true, ".wxss": true,
		".ts": true, ".css": true, ".html": true, ".vue": true,
		".yaml": true, ".yml": true, ".env": true, ".config": true,
		".conf": true, ".xml": true, ".txt": true, ".md": true,
	}

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if !textExts[ext] {
			return nil
		}
		// 跳过 node_modules 和 .git
		if strings.Contains(path, "node_modules") || strings.Contains(path, "/.git/") {
			return nil
		}
		findings, scanErr := s.ScanFile(path)
		if scanErr != nil {
			return nil // 单文件扫描失败不阻断
		}
		allFindings = append(allFindings, findings...)
		return nil
	})
	return allFindings, err
}
