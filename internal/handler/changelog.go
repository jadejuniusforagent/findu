package handler

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// ChangelogHandler 版本记录处理器：只读解析项目内 docs/CHANGELOG.md。
//
// 设计：CHANGELOG.md 是唯一数据源（Markdown，随代码走），本处理器只解析不写入，
// 因此改/加记录无需改代码或重启服务 —— 刷新页面即可见。
type ChangelogHandler struct {
	path   string
	logger *zap.Logger
}

// NewChangelogHandler 以 configPath 同级目录下的 docs/CHANGELOG.md 为数据源。
func NewChangelogHandler(configPath string, logger *zap.Logger) *ChangelogHandler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &ChangelogHandler{
		path:   filepath.Join(filepath.Dir(configPath), "docs", "CHANGELOG.md"),
		logger: logger,
	}
}

// ChangelogItem 一条变更明细。
type ChangelogItem struct {
	Type string `json:"type"` // 新增 / 修复 / 优化 / 决策 / 风险 / 评估 / 未决；未标注时为空
	Text string `json:"text"`
}

// ChangelogEntry 一次变更（含若干明细）。
type ChangelogEntry struct {
	Title string          `json:"title"`
	Items []ChangelogItem `json:"items"`
}

// ChangelogDay 一天的变更集合。
type ChangelogDay struct {
	Date    string           `json:"date"`
	Entries []ChangelogEntry `json:"entries"`
}

var (
	changelogDayRe   = regexp.MustCompile(`^##\s+(\d{4}-\d{2}-\d{2})\s*$`)
	changelogEntryRe = regexp.MustCompile(`^###\s+(.+?)\s*$`)
	changelogItemRe  = regexp.MustCompile(`^-\s+\*\*\[(.+?)\]\*\*\s*(.*)$`)
	changelogPlainRe = regexp.MustCompile(`^-\s+(.+)$`)
)

// List 返回按日期倒序分组的变更记录。
// GET /api/changelog
func (h *ChangelogHandler) List(c *gin.Context) {
	data, err := os.ReadFile(h.path)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"available": false,
			"message":   "变更记录尚未生成（docs/CHANGELOG.md 不存在）",
			"groups":    []any{},
		})
		return
	}

	days := ParseChangelog(string(data))
	updated, entryCount := "", 0
	for _, d := range days {
		entryCount += len(d.Entries)
	}
	if info, statErr := os.Stat(h.path); statErr == nil {
		updated = info.ModTime().Format("2006-01-02 15:04")
	}

	c.JSON(http.StatusOK, gin.H{
		"available":  true,
		"source":     "docs/CHANGELOG.md",
		"updatedAt":  updated,
		"dayCount":   len(days),
		"entryCount": entryCount,
		"groups":     days,
	})
}

// ParseChangelog 把 CHANGELOG.md 文本解析为按日期分组的结构（导出以便单测）。
//
// 解析契约（与 docs/CHANGELOG.md 文件头声明一致）：
//
//	## YYYY-MM-DD      一天
//	### 标题           一次变更
//	- **[类型]** 内容   一条明细（无 **[类型]** 的正为普通明细）
//
// 其它行（引言、空行、普通段落）一律忽略。
func ParseChangelog(text string) []ChangelogDay {
	days := make([]ChangelogDay, 0, 8)
	dayIdx, entryIdx := -1, -1
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, "\r")

		if m := changelogDayRe.FindStringSubmatch(line); m != nil {
			days = append(days, ChangelogDay{Date: m[1]})
			dayIdx = len(days) - 1
			entryIdx = -1
			continue
		}
		if m := changelogEntryRe.FindStringSubmatch(line); m != nil {
			if dayIdx < 0 {
				continue
			}
			days[dayIdx].Entries = append(days[dayIdx].Entries, ChangelogEntry{Title: m[1]})
			entryIdx = len(days[dayIdx].Entries) - 1
			continue
		}
		if dayIdx < 0 || entryIdx < 0 {
			continue
		}
		if m := changelogItemRe.FindStringSubmatch(line); m != nil {
			days[dayIdx].Entries[entryIdx].Items = append(days[dayIdx].Entries[entryIdx].Items,
				ChangelogItem{Type: m[1], Text: strings.TrimSpace(m[2])})
			continue
		}
		if m := changelogPlainRe.FindStringSubmatch(line); m != nil {
			days[dayIdx].Entries[entryIdx].Items = append(days[dayIdx].Entries[entryIdx].Items,
				ChangelogItem{Text: strings.TrimSpace(m[1])})
		}
	}
	return days
}
