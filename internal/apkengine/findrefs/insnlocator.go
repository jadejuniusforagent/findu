package findrefs

// InsnLocator — 指令偏移 → 方法索引的 O(1) 定位,移植自 insn_locator.py。
// 原理:每个方法的 insns 区按 16 字节桶登记(methods 天然分桶,跨桶方法
// 首尾桶全填);R8 指令去重使多方法共享同一段指令体,桶值可能是方法列表。

import (
	"sync"

	"finduai/internal/apkengine/dex"
)

type insnEntry struct {
	mids []uint32 // 空切片 = 桶不存在
}

type InsnLocator struct {
	dex       *dex.DEX
	parseOnce sync.Once
	parsed    bool
	insnMaps  map[uint32]*insnEntry // {insn_off_bucket: mids}
	bounds    map[uint32]uint32     // {midx: insn_off_start}
	CodeStart int
	CodeEnd   int
}

func NewInsnLocator(d *dex.DEX) *InsnLocator {
	return &InsnLocator{dex: d, insnMaps: map[uint32]*insnEntry{}, bounds: map[uint32]uint32{}}
}

// verify 对齐 INSN_VERIFY.fullmatch(buf, start, off):判定
// [start, off) 是否恰为一条条合法指令的连续序列。指令边界由操作码
// 唯一确定(无歧义回溯),等价于原版的正则全文匹配。
func (l *InsnLocator) verify(start, off int) bool {
	if start > off {
		return false
	}
	buf := l.dex.Buf()
	pos := start
	for pos < off {
		oplen := int(OpcodeOplens[buf[pos]])
		if oplen == 0 {
			return false
		}
		pos += oplen * 2
	}
	return pos == off
}

func (l *InsnLocator) encodedMethodParse(codeOff uint32, midx uint32) {
	if codeOff == 0 {
		return // 无方法体
	}
	buf := l.dex.Buf()
	insnSize := u32at(buf, int(codeOff)+12)
	insnOff := codeOff + 16
	bucketStart := insnOff >> 4
	bucketEnd := (insnOff + insnSize*2 - 1) >> 4

	l.bounds[midx] = insnOff

	// 合并顺序与原版一致:后到者插前 [midx, old...]
	var entry *insnEntry
	if old, ok := l.insnMaps[bucketStart]; ok {
		entry = &insnEntry{mids: append([]uint32{midx}, old.mids...)}
	} else {
		entry = &insnEntry{mids: []uint32{midx}}
	}
	for b := bucketStart; b <= bucketEnd; b++ {
		l.insnMaps[b] = entry
	}
}

// classDataParse 对齐 _class_data_parse:字段对以 uleb 长度跳过,
// 方法登记 code_off。返回下一 class_data 起始位置。
func (l *InsnLocator) classDataParse(buf []byte, pos int) int {
	staticFieldsSize, n := uleb(buf, pos)
	pos += n
	instanceFieldsSize, n := uleb(buf, pos)
	pos += n
	directMethodsSize, n := uleb(buf, pos)
	pos += n
	virtualMethodsSize, n := uleb(buf, pos)
	pos += n

	for i := 0; i < staticFieldsSize+instanceFieldsSize; i++ {
		pos += uleb128Len(buf, pos)
		pos += uleb128Len(buf, pos)
	}
	methodIdx := uint32(0)
	for i := 0; i < directMethodsSize; i++ {
		diff, n := uleb(buf, pos)
		pos += n
		methodIdx += uint32(diff)
		pos += uleb128Len(buf, pos)
		codeOff, n := uleb(buf, pos)
		pos += n
		l.encodedMethodParse(uint32(codeOff), methodIdx)
	}
	methodIdx = 0
	for i := 0; i < virtualMethodsSize; i++ {
		diff, n := uleb(buf, pos)
		pos += n
		methodIdx += uint32(diff)
		pos += uleb128Len(buf, pos)
		codeOff, n := uleb(buf, pos)
		pos += n
		l.encodedMethodParse(uint32(codeOff), methodIdx)
	}
	return pos
}

// buildByMapList 对齐 _build_map_bymap:经 map_list 一次定位全部
// code_data 项;map_list 缺失/异常时回退 class_defs 线性扫描。
func (l *InsnLocator) buildByMapList() bool {
	buf := l.dex.Buf()
	hdr := l.dex.Header()
	mapOff := int(hdr.MapOff)
	if mapOff <= 0 || mapOff+4 > len(buf) {
		return l.buildByClassDefs()
	}
	mapSize := int(u32at(buf, mapOff))
	p := mapOff + 4
	found := false
	for i := 0; i < mapSize; i++ {
		if p+12 > len(buf) {
			break
		}
		mtype := u16at(buf, p)
		if mtype == 0x2000 {
			found = true
			break
		}
		p += 0xc
	}
	if !found {
		return l.buildByClassDefs()
	}
	classDataSize := int(u32at(buf, p+4))
	classDataOff := int(u32at(buf, p+8))
	for i := 0; i < classDataSize; i++ {
		classDataOff = l.classDataParse(buf, classDataOff)
	}
	return true
}

func (l *InsnLocator) buildByClassDefs() bool {
	buf := l.dex.Buf()
	hdr := l.dex.Header()
	for i := uint32(0); i < hdr.Classes.Size; i++ {
		classDataOff := int(u32at(buf, int(hdr.Classes.Off+i*32+24)))
		if classDataOff == 0 {
			continue
		}
		l.classDataParse(buf, classDataOff)
	}
	return true
}

// Parse 对齐 parse():构建桶表并推导 code item 扫描区间。
func (l *InsnLocator) Parse() {
	l.parseOnce.Do(func() {
		l.buildByMapList()
		minB, maxB := ^uint32(0), uint32(0)
		for b := range l.insnMaps {
			if b < minB {
				minB = b
			}
			if b > maxB {
				maxB = b
			}
		}
		if len(l.insnMaps) > 0 {
			l.CodeStart = int(minB << 4)
			l.CodeEnd = int((maxB + 1) << 4)
		}
		l.parsed = true
	})
}

// LocateOffset 对齐 locate(offsets):输入必须是升序偏移(扫描器保证)。
// 返回与输入等长的结果;nil = 未能归位到方法;多个方法共享指令体时
// 返回全部(R8 去重语义,与原版列表分支一致)。verify 通过后推进该
// 方法的起始边界,避免方法内回溯重复全文匹配(原版 20260729 修复语义)。
func (l *InsnLocator) LocateOffset(offsets []int) [][]uint32 {
	bounds := make(map[uint32]uint32, len(l.bounds))
	for k, v := range l.bounds {
		bounds[k] = v
	}
	ret := make([][]uint32, len(offsets))
	for i, off := range offsets {
		entry, ok := l.insnMaps[uint32(off)>>4]
		if !ok || len(entry.mids) == 0 {
			continue
		}
		first := entry.mids[0]
		if start, ok := bounds[first]; ok && l.verify(int(start), off) {
			mids := make([]uint32, len(entry.mids))
			copy(mids, entry.mids)
			ret[i] = mids
			bounds[first] = uint32(off)
		}
	}
	return ret
}

func uleb(buf []byte, pos int) (int, int) {
	result := 0
	shift := uint(0)
	start := pos
	for {
		b := buf[pos]
		pos++
		result |= int(b&0x7f) << shift
		if b < 0x80 {
			return result, pos - start
		}
		shift += 7
	}
}

// uleb128Len 对齐 read_uleb128_len。
func uleb128Len(buf []byte, pos int) int {
	if buf[pos] < 0x80 {
		return 1
	} else if buf[pos+1] < 0x80 {
		return 2
	} else if buf[pos+2] < 0x80 {
		return 3
	} else if buf[pos+3] < 0x80 {
		return 4
	} else if buf[pos+4] < 0x80 {
		return 5
	}
	p := pos + 5
	for buf[p] >= 0x80 {
		p++
	}
	return p - pos + 1
}
