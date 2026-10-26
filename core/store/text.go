package store

import "strings"

// UTF8Clean makes a text string safe for a PostgreSQL text column in two steps:
// (1) replaces invalid UTF-8 byte sequences with U+FFFD and (2) strips NUL (0x00) bytes.
// Tool output (nmap/curl/… stdout) can carry raw/truncated bytes that PostgreSQL's
// UTF8 encoding rejects ("invalid byte sequence for encoding UTF8"); without this
// the INSERT fails and the activity record is silently lost.
//
// 原定义在 db/exploration.go（Step 3 批 3 第六刀上提，与 JSONBClean 同理：
// 文本落库清洗是存储基础设施）。JSONB 列请另用 JSONBClean。
func UTF8Clean(s string) string {
	if strings.IndexByte(s, 0) >= 0 {
		s = strings.ReplaceAll(s, "\x00", "")
	}
	return strings.ToValidUTF8(s, "�")
}
