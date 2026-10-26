package store

import "bytes"

// JSONBClean makes marshaled JSON safe for a PostgreSQL jsonb column. json.Marshal
// faithfully encodes a NUL byte (U+0000) as the 6-byte escape sequence \u0000; the
// json type stores it, but jsonb rejects it with "unsupported Unicode escape
// sequence" (SQLSTATE 22P05). Captured HTTP/tool bytes can carry NULs, so drop the
// escape — this also covers NULs nested inside json.RawMessage fields, which are
// copied verbatim into the output. Only a *real* escape is stripped: a \u0000 is
// genuine when preceded by an even number of backslashes, so an escaped-backslash
// run such as \\u0000 (the literal text u0000) is left intact.
//
// 原定义在 db/exploration.go（Step 3 批 3 第五刀上提）：写 jsonb 属存储基础设施，
// 任何 domain（会话侧的 side snapshot 等）都可能需要，放 core/store 谁都能用。
func JSONBClean(b []byte) []byte {
	if !bytes.Contains(b, []byte("\\u0000")) {
		return b
	}
	out := make([]byte, 0, len(b))
	bs := 0 // consecutive backslashes already emitted before position i
	for i := 0; i < len(b); i++ {
		if b[i] == '\\' && bs%2 == 0 && i+5 < len(b) &&
			b[i+1] == 'u' && b[i+2] == '0' && b[i+3] == '0' && b[i+4] == '0' && b[i+5] == '0' {
			i += 5 // skip the whole \u0000
			bs = 0
			continue
		}
		if b[i] == '\\' {
			bs++
		} else {
			bs = 0
		}
		out = append(out, b[i])
	}
	return out
}
