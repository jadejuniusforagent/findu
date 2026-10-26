// Package strutil 通用字符串工具（重复实现清单收敛单点，#21-P4 配套）。
//
// 收敛策略：调用方保留原有薄包装函数（零调用点改动），函数体委托本包，
// 消除跨包复制的实现体；逐字节等价者直接替换，语义分叉者（如 containsAny
// 的大小写敏感/不敏感两版）各自映射到语义正确的入口。
package strutil

import (
	"net/url"
	"strconv"
	"strings"
)

// TruncateRunes 按 rune 截断字符串（保证 UTF-8 边界安全）。
// maxRunes <= 0 或字符串不超长时原样返回；截断时不追加省略标记（与原实现一致）。
func TruncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes])
}

// TruncateRunesWithSuffix 按 rune 截断并追加 suffix（截断发生时）。
func TruncateRunesWithSuffix(s string, maxRunes int, suffix string) string {
	if maxRunes <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + suffix
}

// ContainsAny 是否包含任一子串（大小写敏感）。
func ContainsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// ContainsAnyFold 是否包含任一子串（大小写不敏感，两侧统一小写后比较）。
func ContainsAnyFold(s string, subs []string) bool {
	lower := strings.ToLower(s)
	for _, m := range subs {
		if strings.Contains(lower, strings.ToLower(m)) {
			return true
		}
	}
	return false
}

// DedupeTrimmed 去重并清理空白：按出现顺序保留首次出现的非空值（大小写敏感）。
func DedupeTrimmed(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		t := strings.TrimSpace(v)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// DedupeTrimmedFold 同 DedupeTrimmed 但大小写不敏感（工具名/标识符去重场景）。
func DedupeTrimmedFold(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		t := strings.TrimSpace(v)
		if t == "" {
			continue
		}
		key := strings.ToLower(t)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, t)
	}
	return out
}

// DedupeExact 精确去重（不 trim、不折叠大小写），按序保留首次出现。
func DedupeExact(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// SanitizePathSegment 文件名/路径段清洗：空白回退 default、分隔符与 ..
// 归一为 - 与 __、上限 180 字节。收敛自 4 份逐字节相同的实现
// （project/database/tooloutput/multiagent）。
func SanitizePathSegment(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "default"
	}
	s = strings.ReplaceAll(s, "/", "-")
	s = strings.ReplaceAll(s, "\\", "-")
	s = strings.ReplaceAll(s, "..", "__")
	if len(s) > 180 {
		s = s[:180]
	}
	return s
}

// SanitizeWatermark 水印清洗：仅保留字母/数字/-/_，上限 64 字节。
// 收敛自 memshellgen 与 webshellgen 两份逐字节相同的实现。
func SanitizeWatermark(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	if b.Len() > 64 {
		return b.String()[:64]
	}
	return b.String()
}

// ShortID 取 ID 前 n 个字符（不足原样返回）。收敛自 relay/plan 与 handler/relay
// 两份逐字节相同的实现；原实现固定 n=8。
func ShortID(id string, n int) string {
	if n > 0 && len(id) > n {
		return id[:n]
	}
	return id
}

// HostOf 从 URL 或裸 host 中取 host[:port]（保留端口，与 Go url.Host 惯例一致）。
// 解析失败或无可识别 host 时返回空串（调用方据此跳过，见 apkanalysis 的判空用法）。
// 收敛自 apkanalysis/webshell_deliver/evidence_tools 三份行为互异的实现：
// 统一到 url.Parse 路径（修复手写 TrimPrefix 版对 "HTTP://" 大小写不敏感的缺陷）。
func HostOf(raw string) string {
	v := strings.TrimSpace(raw)
	if v == "" {
		return ""
	}
	if u, err := url.Parse(v); err == nil && u.Host != "" {
		return u.Host
	}
	// 无 scheme 的裸 host[:port][/path]
	if u, err := url.Parse("//" + v); err == nil && u.Host != "" {
		return u.Host
	}
	return ""
}

// Paginate 分页参数归一：page 至少 1、size 归属 [1, maxPages]、非法回退默认值。
// 收敛自 apkanalysis/traffic/audit/enterprise 等多份同构 clamp。
func Paginate(page, size, defSize, maxSize int) (int, int) {
	if page < 1 {
		page = 1
	}
	if size <= 0 {
		size = defSize
	}
	if maxSize > 0 && size > maxSize {
		size = maxSize
	}
	return page, size
}

// HumanSize 字节数人类可读化（B/KB/MB/GB，一位小数，非 B 单位）。
// 收敛自 report/builder 与 vulnerability_packet_display 等近似实现。
func HumanSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return strconv.FormatInt(bytes, 10) + " B"
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	return strconv.FormatFloat(float64(bytes)/float64(div), 'f', 1, 64) + " " + units[exp]
}
