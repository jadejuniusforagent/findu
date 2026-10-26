// Package findrefs — 跨 DEX 代码引用检索,移植自 ASC asc_core/findrefs。
// 语义对齐原版:字符串按字节匹配 string_data 区,指令按 opcode 字节类
// 扫描 + 16 字节桶 O(1) 定位回方法。
package findrefs

import (
	"bytes"
	"sync"

	"finduai/internal/apkengine/dex"
)

// StringLocator 对齐 string_locator.py。
type StringLocator struct {
	dex       *dex.DEX
	buildOnce sync.Once
	stridxMap map[uint32]uint32 // {string_data_off : string_idx}
	strdata   [2]int            // [start, end)
}

func NewStringLocator(d *dex.DEX) *StringLocator {
	return &StringLocator{dex: d, stridxMap: map[uint32]uint32{}}
}

// BuildMap 对齐 _build_map:一次性扫 string_ids,建 data_off → idx 反查表。
func (s *StringLocator) BuildMap() {
	s.buildOnce.Do(func() {
		buf := s.dex.Buf()
		hdr := s.dex.Header()
		off := int(hdr.Strings.Off)
		for idx := uint32(0); idx < hdr.Strings.Size; idx++ {
			dataOffset := u32at(buf, off)
			off += 4
			s.stridxMap[dataOffset] = idx
		}
		s.strdata[0] = int(u32at(buf, int(hdr.Strings.Off)))
		s.strdata[1] = int(u32at(buf, off-4))
	})
}

// matchStringOffset 对齐 _match_string_offset:在 string_data 区间内
// 非重叠搜索 utf-8 needle,命中后跳到 NUL 终止符之后(即该字符串的
// string_data_off + 1,原版以此对齐 stridx_map 的键)。
func (s *StringLocator) matchStringOffset(needle []byte, yield func(int)) {
	buf := s.dex.Buf()
	start, end := s.strdata[0], s.strdata[1]
	sub := buf[start:end]
	pos := 0
	for {
		i := bytes.Index(sub[pos:], needle)
		if i < 0 {
			return
		}
		pos += i
		offset := pos + len(needle) // 相对 sub
		nul := bytes.IndexByte(sub[offset:], 0)
		if nul < 0 {
			return
		}
		yield(start + offset + nul + 1)
		pos++
	}
}

// Locate 对齐 locate:返回命中 string_idx-1 的集合(原版 -1 对齐
// string_ids 顺序引用约定,语义保持)。
func (s *StringLocator) Locate(str string) map[uint32]struct{} {
	s.BuildMap()
	ret := map[uint32]struct{}{}
	s.matchStringOffset([]byte(str), func(off int) {
		if idx, ok := s.stridxMap[uint32(off)]; ok {
			ret[idx-1] = struct{}{}
		}
	})
	return ret
}

// TypeLocator 对齐 type_locator.py。
type TypeLocator struct {
	dex       *dex.DEX
	str       *StringLocator
	buildOnce sync.Once
	typeMaps  map[uint32]map[uint32]struct{} // {str_idx : {type_idx}}
}

func NewTypeLocator(d *dex.DEX, str *StringLocator) *TypeLocator {
	return &TypeLocator{dex: d, str: str, typeMaps: map[uint32]map[uint32]struct{}{}}
}

func (t *TypeLocator) BuildMap() {
	t.buildOnce.Do(func() {
		buf := t.dex.Buf()
		hdr := t.dex.Header()
		off := int(hdr.Types.Off)
		for idx := uint32(0); idx < hdr.Types.Size; idx++ {
			strIdx := u32at(buf, off)
			off += 4
			set, ok := t.typeMaps[strIdx]
			if !ok {
				set = map[uint32]struct{}{}
				t.typeMaps[strIdx] = set
			}
			set[idx] = struct{}{}
		}
	})
}

func (t *TypeLocator) Locate(typeStr string) map[uint32]struct{} {
	t.BuildMap()
	ret := map[uint32]struct{}{}
	if typeStr == "" {
		return ret
	}
	for strIdx := range t.str.Locate(typeStr) {
		for typeIdx := range t.typeMaps[strIdx] {
			ret[typeIdx] = struct{}{}
		}
	}
	return ret
}

func u32at(buf []byte, off int) uint32 {
	return uint32(buf[off]) | uint32(buf[off+1])<<8 | uint32(buf[off+2])<<16 | uint32(buf[off+3])<<24
}

func u16at(buf []byte, off int) uint16 {
	return uint16(buf[off]) | uint16(buf[off+1])<<8
}
