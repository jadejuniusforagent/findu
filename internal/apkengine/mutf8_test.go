package apkengine

import (
	"bytes"
	"testing"
)

func TestEncodeMUTF8(t *testing.T) {
	cases := []struct {
		in   string
		want []byte
	}{
		{"", []byte{}},
		{"abc", []byte{'a', 'b', 'c'}},
		{"\x00", []byte{0xC0, 0x80}},                               // NUL 特殊编码
		{"a\x00b", []byte{'a', 0xC0, 0x80, 'b'}},                   // 嵌入 NUL
		{"é", []byte{0xC3, 0xA9}},                                  // U+00E9 两字节
		{"€", []byte{0xE2, 0x82, 0xAC}},                            // U+20AC 三字节
		{"\U0001F600", []byte{0xED, 0xA0, 0xBD, 0xED, 0xB8, 0x80}}, // U+1F600 代理对
	}
	for _, c := range cases {
		got := EncodeMUTF8(c.in)
		if !bytes.Equal(got, c.want) {
			t.Errorf("EncodeMUTF8(%q) = % x, want % x", c.in, got, c.want)
		}
	}
}

func TestDecodeMUTF8(t *testing.T) {
	cases := []struct {
		in   []byte
		want string
	}{
		{[]byte{}, ""},
		{[]byte{'a', 'b', 'c'}, "abc"},
		{[]byte{0xC0, 0x80}, "\x00"},
		{[]byte{'a', 0xC0, 0x80, 'b'}, "a\x00b"},
		{[]byte{0xC3, 0xA9}, "é"},
		{[]byte{0xE2, 0x82, 0xAC}, "€"},
		{[]byte{0xED, 0xA0, 0xBD, 0xED, 0xB8, 0x80}, "\U0001F600"},
		// 孤立高代理:容错为 RuneError,不 panic
		{[]byte{0xED, 0xA0, 0xBD}, "�"},
		// 截断序列:容错
		{[]byte{0xE2, 0x82}, "��"},
	}
	for _, c := range cases {
		got := DecodeMUTF8(c.in)
		if got != c.want {
			t.Errorf("DecodeMUTF8(% x) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestMUTF8Roundtrip(t *testing.T) {
	for _, s := range []string{
		"", "abc", "Lcom/poc/Main;", "\x00", "a\x00b", "é", "€", "\U0001F600",
		"中文测试", "Lcom/\u4e2d\u6587/Main;->foo\u20ac(I)V",
		"null...\x00mixed é😀 strings",
	} {
		enc := EncodeMUTF8(s)
		if got := DecodeMUTF8(enc); got != s {
			t.Errorf("MUTF8 roundtrip %q -> % x -> %q", s, enc, got)
		}
	}
}

// 标准 BMP 字符的 MUTF-8 编码必须与标准 UTF-8 逐字节一致(除 NUL 外)
func TestBMPMatchesUTF8(t *testing.T) {
	for _, s := range []string{"abc", "hello world", "中文", "é€", "Lcom/x/Y;->z()V"} {
		if !bytes.Equal(EncodeMUTF8(s), []byte(s)) {
			t.Errorf("EncodeMUTF8(%q) deviates from UTF-8", s)
		}
	}
}
