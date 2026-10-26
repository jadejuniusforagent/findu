package zipprobe

// dex041 复合容器拆分,移植自 ASC asc_client/dex_container.py。

import (
	"encoding/binary"
	"fmt"
)

var dex041Magic = []byte("dex\n041\x00")

const dexHeaderMinSize = 0x70

// LogicalDex 一个逻辑 DEX:name 为 "classes.dex!classes1.dex" 形态的复合名。
type LogicalDex struct {
	Name string
	Buf  []byte
}

func isDex041Container(data []byte) bool {
	return len(data) >= dexHeaderMinSize && string(data[:8]) == string(dex041Magic)
}

// Dex041LogicalOffsets 对齐 dex041_logical_offsets:沿 file_size 链推进;
// 非容器/解析失败回退 [0]。
func Dex041LogicalOffsets(data []byte) []int {
	if !isDex041Container(data) {
		return []int{0}
	}
	var offsets []int
	off := 0
	for off+dexHeaderMinSize <= len(data) && string(data[off:off+8]) == string(dex041Magic) {
		fileSize := int(binary.LittleEndian.Uint32(data[off+0x20:]))
		if fileSize < dexHeaderMinSize || off+fileSize > len(data) {
			break
		}
		offsets = append(offsets, off)
		off += fileSize
	}
	if len(offsets) == 0 {
		return []int{0}
	}
	return offsets
}

// NormalizeDex041 对齐 normalize_dex041_logical:把子 DEX 头搬到缓冲区开头,
// 使其内部相对偏移直接可用。header_off==0 时原样返回。
func NormalizeDex041(data []byte, headerOff int) []byte {
	if headerOff == 0 {
		return data
	}
	headerSize := int(binary.LittleEndian.Uint32(data[headerOff+0x24:]))
	if headerSize < dexHeaderMinSize || headerOff+headerSize > len(data) {
		headerSize = dexHeaderMinSize
	}
	out := make([]byte, len(data))
	copy(out, data)
	copy(out[:headerSize], data[headerOff:headerOff+headerSize])
	return out
}

// LogicalDexBuffers 对齐 iter_logical_dex_buffers。
func LogicalDexBuffers(name string, data []byte) []LogicalDex {
	offsets := Dex041LogicalOffsets(data)
	if len(offsets) <= 1 {
		return []LogicalDex{{Name: name, Buf: data}}
	}
	out := make([]LogicalDex, 0, len(offsets))
	for idx, headerOff := range offsets {
		out = append(out, LogicalDex{
			Name: fmt.Sprintf("%s!classes%d.dex", name, idx+1),
			Buf:  NormalizeDex041(data, headerOff),
		})
	}
	return out
}
