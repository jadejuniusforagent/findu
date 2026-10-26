// Package zipprobe — APK 的零预处理读取层,移植自 ASC asc_client/apk_handler.py。
// 把 APK 当只读数据库:定位 EOCD/中央目录,按需 inflate DEX 条目,
// 直接在字节层做类定位(不建全局索引)。
package zipprobe

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// Reader 抽象 APK 字节源:*os.File(ReadAt)或内存字节。
type Reader interface {
	ReadAt(p []byte, off int64) (int, error)
	Size() int64
	Close() error
}

type fileReader struct {
	f    *os.File
	size int64
}

// Open 打开磁盘 APK。
func Open(path string) (Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	return fileReader{f: f, size: st.Size()}, nil
}

func (r fileReader) ReadAt(p []byte, off int64) (int, error) { return r.f.ReadAt(p, off) }
func (r fileReader) Size() int64                             { return r.size }

// Close 关闭底层文件(内存源为空操作)。
func (r fileReader) Close() error { return r.f.Close() }

// BytesReader 包装内存字节(测试与已归档小样本)。
type BytesReader struct{ Data []byte }

func (b BytesReader) ReadAt(p []byte, off int64) (int, error) {
	if off > int64(len(b.Data)) {
		return 0, io.EOF
	}
	n := copy(p, b.Data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}
func (b BytesReader) Size() int64  { return int64(len(b.Data)) }
func (b BytesReader) Close() error { return nil }

// Entry 对齐原版 _parse_cd_dex_entries 的五元组。
type Entry struct {
	Name       string
	UncompSize uint32
	CompSize   uint32
	LocalOff   uint32
	Method     uint16
}

var (
	sigLocalHeader = []byte("PK\x03\x04")
	sigCentralDir  = []byte("PK\x01\x02")
	sigEOCD        = []byte("PK\x05\x06")
	dexSuffix      = []byte(".dex")
)

func u16(b []byte, off int) uint16 { return binary.LittleEndian.Uint16(b[off:]) }
func u32(b []byte, off int) uint32 { return binary.LittleEndian.Uint32(b[off:]) }

func (r fileReader) close() {} // 见 Close

// FindEOCD 对齐 _find_eocd:在尾部 64KB+22 窗口内 rfind。
func FindEOCD(r Reader) (int64, error) {
	size := r.Size()
	start := size - 65536 - 22
	if start < 0 {
		start = 0
	}
	win := make([]byte, size-start)
	if _, err := r.ReadAt(win, start); err != nil && err != io.EOF {
		return -1, err
	}
	idx := bytes.LastIndex(win, sigEOCD)
	if idx < 0 {
		return -1, fmt.Errorf("EOCD not found")
	}
	return start + int64(idx), nil
}

// ParseCDDexEntries 对齐 _parse_cd_dex_entries:先"classes"快速扫描,
// 空结果回退整个中央目录线性步进。条目按出现顺序返回(排序由调用方做)。
func ParseCDDexEntries(r Reader) ([]Entry, error) {
	eocd, err := FindEOCD(r)
	if err != nil {
		return nil, err
	}
	head := make([]byte, 20)
	if _, err := r.ReadAt(head, eocd+12); err != nil && err != io.EOF {
		return nil, err
	}
	cdSize := int64(u32(head, 0))
	cdOff := int64(u32(head, 4))
	cdEnd := cdOff + cdSize
	if cdEnd > r.Size() {
		return nil, fmt.Errorf("central directory out of range")
	}
	cd := make([]byte, cdSize)
	if _, err := r.ReadAt(cd, cdOff); err != nil && err != io.EOF {
		return nil, err
	}

	entries := []Entry{}
	seen := map[string]bool{}

	// 快速路径:扫描 "classes" 字面量,回溯 46 字节验证 CD 头
	pos := 0
	for {
		i := bytes.Index(cd[pos:], []byte("classes"))
		if i < 0 {
			break
		}
		pos += i
		headerOff := pos - 46
		nameStart := pos
		pos += 7
		if headerOff < 0 || !bytes.Equal(cd[headerOff:headerOff+4], sigCentralDir) {
			continue
		}
		nameLen := int(u16(cd, headerOff+28))
		nameEnd := nameStart + nameLen
		if nameEnd > len(cd) {
			continue
		}
		name := cd[nameStart:nameEnd]
		if !bytes.HasSuffix(name, dexSuffix) || bytes.IndexByte(name, '/') >= 0 || seen[string(name)] {
			continue
		}
		seen[string(name)] = true
		entries = append(entries, Entry{
			Name:       string(name),
			UncompSize: u32(cd, headerOff+24),
			CompSize:   u32(cd, headerOff+20),
			LocalOff:   u32(cd, headerOff+42),
			Method:     u16(cd, headerOff+10),
		})
	}
	if len(entries) > 0 {
		return entries, nil
	}

	// 回退:全量中央目录线性步进后按 DEX 过滤
	for _, e := range mustParseCDEntries(r) {
		if strings.HasSuffix(e.Name, ".dex") && !strings.Contains(e.Name, "/") {
			entries = append(entries, e)
		}
	}
	return entries, nil
}

func mustParseCDEntries(r Reader) []Entry {
	entries, err := ParseCDEntries(r)
	if err != nil {
		return nil
	}
	return entries
}

// ParseCDEntries 返回中央目录全部条目(不做 DEX 过滤),供壳特征文件名扫描、
// 资产清单等按需消费。线性步进,条目按出现顺序返回。
func ParseCDEntries(r Reader) ([]Entry, error) {
	eocd, err := FindEOCD(r)
	if err != nil {
		return nil, err
	}
	head := make([]byte, 20)
	if _, err := r.ReadAt(head, eocd+12); err != nil && err != io.EOF {
		return nil, err
	}
	cdSize := int64(u32(head, 0))
	cdOff := int64(u32(head, 4))
	if cdOff+cdSize > r.Size() {
		return nil, fmt.Errorf("central directory out of range")
	}
	cd := make([]byte, cdSize)
	if _, err := r.ReadAt(cd, cdOff); err != nil && err != io.EOF {
		return nil, err
	}
	var entries []Entry
	ptr := 0
	for ptr+46 <= len(cd) {
		if !bytes.Equal(cd[ptr:ptr+4], sigCentralDir) {
			break
		}
		nameLen := int(u16(cd, ptr+28))
		extraLen := int(u16(cd, ptr+30))
		commentLen := int(u16(cd, ptr+32))
		nameStart := ptr + 46
		nameEnd := nameStart + nameLen
		if nameEnd > len(cd) {
			break
		}
		entries = append(entries, Entry{
			Name:       string(cd[nameStart:nameEnd]),
			UncompSize: u32(cd, ptr+24),
			CompSize:   u32(cd, ptr+20),
			LocalOff:   u32(cd, ptr+42),
			Method:     u16(cd, ptr+10),
		})
		ptr = nameEnd + extraLen + commentLen
	}
	return entries, nil
}

// SortByCompSize 对齐原版调用点的 entries.sort(key=comp_size)。
func SortByCompSize(entries []Entry) {
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].CompSize < entries[j].CompSize })
}

const deflateChunk = 1 << 19

// InflateDex 对齐 _inflate_dex:定位 local header 数据区,store 直取 /
// deflate 分块解压,并校验解压尺寸。
func InflateDex(r Reader, e Entry) ([]byte, error) {
	lh := make([]byte, 30)
	if _, err := r.ReadAt(lh, int64(e.LocalOff)); err != nil {
		return nil, err
	}
	if !bytes.Equal(lh[:4], sigLocalHeader) {
		return nil, fmt.Errorf("%s: bad local header signature", e.Name)
	}
	nameLen := int(u16(lh, 26))
	extraLen := int(u16(lh, 28))
	dataOff := int64(e.LocalOff) + 30 + int64(nameLen) + int64(extraLen)

	var data []byte
	switch e.Method {
	case 0:
		data = make([]byte, e.CompSize)
		if _, err := r.ReadAt(data, dataOff); err != nil && err != io.EOF {
			return nil, err
		}
	case 8:
		fr := flate.NewReader(io.NewSectionReader(readAtReader{r}, dataOff, int64(e.CompSize)))
		out, err := io.ReadAll(fr)
		fr.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: inflate: %w", e.Name, err)
		}
		data = out
	default:
		return nil, fmt.Errorf("%s: unsupported compression method: %d", e.Name, e.Method)
	}
	if e.UncompSize != 0 && uint32(len(data)) != e.UncompSize {
		return nil, fmt.Errorf("%s: size mismatch: expect %d, got %d", e.Name, e.UncompSize, len(data))
	}
	return data, nil
}

type readAtReader struct{ r Reader }

func (x readAtReader) ReadAt(p []byte, off int64) (int, error) { return x.r.ReadAt(p, off) }

// DexDefinesClass 对齐 _dex_defines_class:raw 字节层 type_ids 二分 +
// class_defs 对齐扫描,全程不解码字符串。
func DexDefinesClass(dexBuf []byte, dalvikClass string) (bool, error) {
	typeIdx, err := FindTypeIdx(dexBuf, []byte(dalvikClass))
	if err != nil || typeIdx < 0 {
		return false, err
	}
	return classDefsContainsTypeIdx(dexBuf, typeIdx)
}

// FindTypeIdx 对齐 _find_type_idx(string_data 字节序即排序序)。
func FindTypeIdx(dexBuf []byte, target []byte) (int, error) {
	if len(dexBuf) < 0x70 || string(dexBuf[:3]) != "dex" {
		return -1, nil
	}
	stringIDsSize := int(u32(dexBuf, 0x38))
	stringIDsOff := int(u32(dexBuf, 0x3C))
	typeIDsSize := int(u32(dexBuf, 0x40))
	typeIDsOff := int(u32(dexBuf, 0x44))

	if stringIDsSize == 0 || typeIDsSize == 0 {
		return -1, nil
	}
	if stringIDsOff+stringIDsSize*4 > len(dexBuf) {
		return -1, fmt.Errorf("bad string_ids range")
	}
	if typeIDsOff+typeIDsSize*4 > len(dexBuf) {
		return -1, fmt.Errorf("bad type_ids range")
	}

	left, right := 0, typeIDsSize-1
	for left <= right {
		mid := (left + right) >> 1
		strIdx := int(u32(dexBuf, typeIDsOff+mid*4))
		if strIdx >= stringIDsSize {
			return -1, fmt.Errorf("bad type_id->string_idx")
		}
		strOff := int(u32(dexBuf, stringIDsOff+strIdx*4))
		if strOff >= len(dexBuf) {
			return -1, fmt.Errorf("bad string_data_off")
		}
		clsBytes, err := readStringDataBytes(dexBuf, strOff)
		if err != nil {
			return -1, err
		}
		if bytes.Equal(clsBytes, target) {
			return mid, nil
		}
		if bytes.Compare(clsBytes, target) < 0 {
			left = mid + 1
		} else {
			right = mid - 1
		}
	}
	return -1, nil
}

func skipUleb128(buf []byte, off int) int {
	for buf[off]&0x80 != 0 {
		off++
	}
	return off + 1
}

func readStringDataBytes(buf []byte, strOff int) ([]byte, error) {
	ptr := skipUleb128(buf, strOff)
	end := bytes.IndexByte(buf[ptr:], 0)
	if end < 0 {
		return nil, fmt.Errorf("unterminated string_data_item")
	}
	return buf[ptr : ptr+end], nil
}

func classDefsContainsTypeIdx(dexBuf []byte, typeIdx int) (bool, error) {
	classDefsSize := int(u32(dexBuf, 0x60))
	classDefsOff := int(u32(dexBuf, 0x64))
	if classDefsSize == 0 {
		return false, nil
	}
	classDefsEnd := classDefsOff + (classDefsSize << 5)
	if classDefsEnd > len(dexBuf) {
		return false, fmt.Errorf("bad class_defs range")
	}
	needle := make([]byte, 4)
	binary.LittleEndian.PutUint32(needle, uint32(typeIdx))
	pos := bytes.Index(dexBuf[classDefsOff:classDefsEnd], needle)
	for pos >= 0 {
		abs := classDefsOff + pos
		if (abs-classDefsOff)&0x1F == 0 {
			return true, nil
		}
		next := abs + 1
		if next >= classDefsEnd {
			break
		}
		idx := bytes.Index(dexBuf[next:classDefsEnd], needle)
		if idx < 0 {
			break
		}
		pos = next + idx - classDefsOff
	}
	return false, nil
}
