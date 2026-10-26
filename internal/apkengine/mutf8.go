package apkengine

// MUTF-8(Dalvik 字符串表使用的 Modified UTF-8)编解码,语义对齐 mutf8 pip 包:
//   - U+0000 编码为 0xC0 0x80(而非 0x00)
//   - 增补平面(U+10000 以上)以 UTF-16 代理对逐个做 3 字节编码(CESU-8 形态)
//   - 其余 BMP 字符与标准 UTF-8 相同

import (
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// DecodeMUTF8 将 MUTF-8 字节序列解码为 Go 字符串。
// 非法序列以 utf8.RuneError 占位,不 panic(损坏 DEX 的容错路径)。
func DecodeMUTF8(b []byte) string {
	var sb strings.Builder
	sb.Grow(len(b))
	for i := 0; i < len(b); {
		c0 := b[i]
		switch {
		case c0 < 0x80:
			sb.WriteByte(c0)
			i++
		case c0&0xE0 == 0xC0:
			if i+1 >= len(b) || b[i+1]&0xC0 != 0x80 {
				sb.WriteRune(utf8.RuneError)
				i++
				continue
			}
			r := rune(c0&0x1F)<<6 | rune(b[i+1]&0x3F)
			sb.WriteRune(r)
			i += 2
		case c0&0xF0 == 0xE0:
			if i+2 >= len(b) || b[i+1]&0xC0 != 0x80 || b[i+2]&0xC0 != 0x80 {
				sb.WriteRune(utf8.RuneError)
				i++
				continue
			}
			r := rune(c0&0x0F)<<12 | rune(b[i+1]&0x3F)<<6 | rune(b[i+2]&0x3F)
			if utf16.IsSurrogate(rune(r)) {
				// 代理对:仅当紧跟低代理才合并为增补字符
				if i+5 < len(b) && b[i+3]&0xF0 == 0xE0 && b[i+4]&0xC0 == 0x80 && b[i+5]&0xC0 == 0x80 {
					lo := rune(b[i+3]&0x0F)<<12 | rune(b[i+4]&0x3F)<<6 | rune(b[i+5]&0x3F)
					if combined := utf16.DecodeRune(rune(r), lo); combined != utf8.RuneError {
						sb.WriteRune(combined)
						i += 6
						continue
					}
				}
				sb.WriteRune(utf8.RuneError)
			} else {
				sb.WriteRune(r)
			}
			i += 3
		default:
			sb.WriteRune(utf8.RuneError)
			i++
		}
	}
	return sb.String()
}

// EncodeMUTF8 将 Go 字符串编码为 MUTF-8 字节序列。
func EncodeMUTF8(s string) []byte {
	out := make([]byte, 0, len(s)+8)
	for _, r := range s {
		switch {
		case r == 0:
			out = append(out, 0xC0, 0x80)
		case r < 0x80:
			out = append(out, byte(r))
		case r < 0x800:
			out = append(out, byte(0xC0|r>>6), byte(0x80|r&0x3F))
		case r <= 0xFFFF:
			out = append(out, byte(0xE0|r>>12), byte(0x80|(r>>6)&0x3F), byte(0x80|r&0x3F))
		default:
			hi, lo := utf16.EncodeRune(r)
			out = append(out,
				byte(0xE0|hi>>12), byte(0x80|(hi>>6)&0x3F), byte(0x80|hi&0x3F),
				byte(0xE0|lo>>12), byte(0x80|(lo>>6)&0x3F), byte(0x80|lo&0x3F))
		}
	}
	return out
}
