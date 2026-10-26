package handler

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseChangelog(t *testing.T) {
	const text = "# FindUAI 变更记录\n" +
		"\n" +
		"> 说明行，不应被解析\n" +
		"\n" +
		"## 2026-10-13\n" +
		"\n" +
		"### 甲变更\n" +
		"\n" +
		"- **[新增]** 做了 A\n" +
		"- **[修复]** 修了 B\n" +
		"- 无标签的普通明细\n" +
		"\n" +
		"### 乙变更\n" +
		"\n" +
		"- **[决策]** 定了 C\n" +
		"\n" +
		"## 2026-10-10\n" +
		"\n" +
		"### 丙变更\n" +
		"\n" +
		"- **[评估]** 看过 D\n"

	days := ParseChangelog(text)
	if len(days) != 2 {
		t.Fatalf("天数 = %d, 期望 2（%+v）", len(days), days)
	}
	if days[0].Date != "2026-10-13" || days[1].Date != "2026-10-10" {
		t.Fatalf("日期顺序错: %q / %q（应保持文件顺序＝倒序）", days[0].Date, days[1].Date)
	}
	if len(days[0].Entries) != 2 {
		t.Fatalf("首日变更数 = %d, 期望 2", len(days[0].Entries))
	}
	if days[0].Entries[0].Title != "甲变更" {
		t.Errorf("首条标题 = %q", days[0].Entries[0].Title)
	}
	items := days[0].Entries[0].Items
	if len(items) != 3 {
		t.Fatalf("甲变更明细数 = %d, 期望 3", len(items))
	}
	if items[0].Type != "新增" || items[0].Text != "做了 A" {
		t.Errorf("明细[0] = %+v", items[0])
	}
	if items[1].Type != "修复" {
		t.Errorf("明细[1].Type = %q, 期望 修复", items[1].Type)
	}
	if items[2].Type != "" || items[2].Text != "无标签的普通明细" {
		t.Errorf("无标签明细解析错: %+v", items[2])
	}
	if len(days[1].Entries) != 1 || days[1].Entries[0].Items[0].Type != "评估" {
		t.Errorf("末日解析错: %+v", days[1])
	}

	// 引言里的 "## 引用" 之类不得被当成日期；正文出现在日期之前应被忽略
	if got := ParseChangelog("### 没有日期\n- **[新增]** 孤儿\n"); len(got) != 0 {
		t.Errorf("无日期标题应先被忽略，得到 %+v", got)
	}
	if got := ParseChangelog(""); len(got) != 0 {
		t.Errorf("空文本应返回空，得到 %+v", got)
	}
	if got := ParseChangelog("## 2026-10-13\n"); len(got) != 1 || len(got[0].Entries) != 0 {
		t.Errorf("只有日期无变更时应返回 1 天 0 条，得到 %+v", got)
	}
}

func TestParseChangelogRealFile(t *testing.T) {
	// 真文件守卫：仓库自带 docs/CHANGELOG.md 必须能被解析出至少 1 天若干变更，
	// 且每条变更都要有标题（防止格式漂移后 UI 空白）。
	// go test 的 CWD 是包目录（internal/handler），故用相对路径定位仓库根。
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "CHANGELOG.md"))
	if err != nil {
		t.Skipf("跳过（无法定位仓库 CHANGELOG.md）: %v", err)
	}
	days := ParseChangelog(string(data))
	if len(days) == 0 {
		t.Fatal("docs/CHANGELOG.md 未解析出任何日期 —— 格式契约被破坏")
	}
	total := 0
	for _, d := range days {
		for _, e := range d.Entries {
			total++
			if e.Title == "" {
				t.Errorf("日期 %s 下存在空标题变更", d.Date)
			}
		}
	}
	if total == 0 {
		t.Fatal("docs/CHANGELOG.md 未解析出任何变更条目")
	}
	t.Logf("CHANGELOG: %d 天 / %d 条变更", len(days), total)
}
