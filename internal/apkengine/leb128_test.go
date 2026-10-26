package apkengine

import (
	"bytes"
	"testing"
)

func TestReadUleb128(t *testing.T) {
	cases := []struct {
		data []byte
		want uint32
		n    int
	}{
		{[]byte{0x00}, 0, 1},
		{[]byte{0x7f}, 127, 1},
		{[]byte{0x80, 0x01}, 128, 2},
		{[]byte{0xac, 0x02}, 300, 2},
		{[]byte{0xff, 0xff, 0xff, 0xff, 0x0f}, 0xffffffff, 5},
		{[]byte{0xe5, 0x8e, 0x26}, 624485, 3},
	}
	for _, c := range cases {
		got, n := ReadUleb128(c.data, 0)
		if got != c.want || n != c.n {
			t.Errorf("ReadUleb128(% x) = (%d, %d), want (%d, %d)", c.data, got, n, c.want, c.n)
		}
	}
}

func TestReadUleb128Offset(t *testing.T) {
	// 带前缀偏移,保证 pos 参数语义正确
	data := []byte{0xaa, 0xac, 0x02}
	got, n := ReadUleb128(data, 1)
	if got != 300 || n != 2 {
		t.Errorf("ReadUleb128 offset = (%d, %d), want (300, 2)", got, n)
	}
}

func TestUleb128Len(t *testing.T) {
	if n := Uleb128Len([]byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x01}, 0); n != 6 {
		t.Errorf("Uleb128Len 6-byte encoding = %d, want 6", n)
	}
	if n := Uleb128Len([]byte{0x05}, 0); n != 1 {
		t.Errorf("Uleb128Len single byte = %d, want 1", n)
	}
}

func TestSleb128(t *testing.T) {
	cases := []struct {
		data []byte
		want int32
		n    int
	}{
		{[]byte{0x00}, 0, 1},
		{[]byte{0x01}, 1, 1},
		{[]byte{0x7f}, -1, 1},
		{[]byte{0x3f}, 63, 1},
		{[]byte{0x40}, -64, 1},
		{[]byte{0xc0, 0x00}, 64, 2},
		{[]byte{0xbf, 0x7f}, -65, 2},
		{[]byte{0xff, 0x00}, 127, 2},
	}
	for _, c := range cases {
		got, n := ReadSleb128(c.data, 0)
		if got != c.want || n != c.n {
			t.Errorf("ReadSleb128(% x) = (%d, %d), want (%d, %d)", c.data, got, n, c.want, c.n)
		}
	}
}

func TestUleb128Roundtrip(t *testing.T) {
	for _, v := range []uint32{0, 1, 127, 128, 300, 624485, 0x7fffffff, 0xffffffff} {
		buf := AppendUleb128(nil, v)
		got, n := ReadUleb128(buf, 0)
		if got != v || n != len(buf) {
			t.Errorf("uleb128 roundtrip %d: got (%d, %d) len %d", v, got, n, len(buf))
		}
	}
}

func TestSleb128Roundtrip(t *testing.T) {
	for _, v := range []int32{0, 1, -1, 63, 64, -64, -65, 127, -128, 1<<24 - 1, -(1 << 24), 1<<31 - 1, -(1 << 31)} {
		buf := AppendSleb128(nil, v)
		got, n := ReadSleb128(buf, 0)
		if got != v || n != len(buf) {
			t.Errorf("sleb128 roundtrip %d: got (%d, %d) len %d", v, got, n, len(buf))
		}
	}
}

// 与 Python 版 write_uleb128 的字节输出逐字节对齐
func TestAppendUleb128Bytes(t *testing.T) {
	if got := AppendUleb128(nil, 0); !bytes.Equal(got, []byte{0x00}) {
		t.Errorf("AppendUleb128(0) = % x", got)
	}
	if got := AppendUleb128(nil, 624485); !bytes.Equal(got, []byte{0xe5, 0x8e, 0x26}) {
		t.Errorf("AppendUleb128(624485) = % x", got)
	}
}
