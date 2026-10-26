package apkengine

// LEB128 变长编码读写,语义对齐 ASC asc_core/utils/leb128.py。

// ReadUleb128 读取 data[pos:] 处的 uleb128,返回数值与消费字节数。
// 快速路径与原版 read_uleb128_fast 一致:最多消费 5 字节(28 位载荷),
// 第 5 字节不再检查延续位;第 6 字节及以后见 Uleb128Len。
func ReadUleb128(data []byte, pos int) (uint32, int) {
	b := data[pos]
	if b < 0x80 {
		return uint32(b), 1
	}

	res := uint32(b & 0x7f)
	b = data[pos+1]
	res |= uint32(b&0x7f) << 7
	if b < 0x80 {
		return res, 2
	}

	b = data[pos+2]
	res |= uint32(b&0x7f) << 14
	if b < 0x80 {
		return res, 3
	}

	b = data[pos+3]
	res |= uint32(b&0x7f) << 21
	if b < 0x80 {
		return res, 4
	}

	b = data[pos+4]
	res |= uint32(b&0x7f) << 28
	return res, 5
}

// Uleb128Len 对齐原版 read_uleb128_len:前 5 字节逐个判断,超过 5 字节的
// 罕见编码继续扫描延续位。
func Uleb128Len(data []byte, pos int) int {
	if data[pos] < 0x80 {
		return 1
	} else if data[pos+1] < 0x80 {
		return 2
	} else if data[pos+2] < 0x80 {
		return 3
	} else if data[pos+3] < 0x80 {
		return 4
	} else if data[pos+4] < 0x80 {
		return 5
	}
	p := pos + 5
	for data[p] >= 0x80 {
		p++
	}
	return p - pos + 1
}

// ReadSleb128 读取 sleb128,返回数值与消费字节数。
func ReadSleb128(data []byte, pos int) (int32, int) {
	var result int32
	shift := uint(0)
	start := pos
	for {
		b := data[pos]
		pos++
		result |= int32(b&0x7f) << shift
		shift += 7
		if b&0x80 == 0 {
			if shift < 32 && b&0x40 != 0 {
				result |= -(1 << shift)
			}
			return result, pos - start
		}
	}
}

// AppendUleb128 对齐原版 write_uleb128。
func AppendUleb128(dst []byte, value uint32) []byte {
	if value == 0 {
		return append(dst, 0)
	}
	for value > 0x7f {
		dst = append(dst, byte(value&0x7f)|0x80)
		value >>= 7
	}
	return append(dst, byte(value))
}

// AppendSleb128 对齐原版 write_sleb128。
func AppendSleb128(dst []byte, value int32) []byte {
	more := true
	for more {
		b := byte(value & 0x7f)
		value >>= 7
		if (value == 0 && b&0x40 == 0) || (value == -1 && b&0x40 != 0) {
			more = false
		} else {
			b |= 0x80
		}
		dst = append(dst, b)
	}
	return dst
}
