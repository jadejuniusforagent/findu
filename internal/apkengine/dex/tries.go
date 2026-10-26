package dex

// tries.go — code_item 异常表解析:try_item 数组 + encoded_catch_handler_list。
// 布局(Dalvik 规范):insns [;padding if insns_size 奇数] tries[tries_size]
// encoded_catch_handler_list;handler_off 是相对列表起始的字节偏移
// (多个 try 可共享同一 handler,去重由编译器完成)。
// 畸形数据整体返回 nil,不影响主流程(godad 侧降级为注释保留)。

// TryItem 对齐 DEX try_item(8 字节定长)。
type TryItem struct {
	StartAddr  uint32 // try 覆盖起始指令地址(16-bit code unit)
	InsnCount  uint16 // 覆盖的 16-bit code unit 数
	HandlerOff uint16 // encoded_catch_handler_list 内字节偏移
}

// CatchTypeAddr 类型化处理器:type_idx → 处理器入口地址。
type CatchTypeAddr struct {
	TypeIdx uint32
	Addr    uint32
}

// CatchHandler 对齐 encoded_catch_handler(已解码)。
type CatchHandler struct {
	Typed        []CatchTypeAddr // 类型化处理器(有序)
	CatchAllAddr int             // catch-all(finally 风格)地址;-1 = 无
}

// ExceptionTable 方法异常表。
type ExceptionTable struct {
	Tries    []TryItem
	Handlers []*CatchHandler // 列表解析顺序
	offs     []uint16        // Handlers[i] 的列表内字节偏移(平行数组)
}

// Handler 按 try_item.handler_off 查找处理器;未命中返回 nil。
func (t *ExceptionTable) Handler(off uint16) *CatchHandler {
	for i, o := range t.offs {
		if o == off {
			return t.Handlers[i]
		}
	}
	return nil
}

// ExceptionTable 解析方法 try/catch 表;无 try(TriesSize == 0)或畸形时返回 nil。
// Method 级缓存,重复调用零开销。
func (m *Method) ExceptionTable() *ExceptionTable {
	m.triesOnce.Do(func() { m.tries = m.dex.parseExceptionTable(m.codeOff) })
	return m.tries
}

func (d *DEX) parseExceptionTable(codeOff uint32) *ExceptionTable {
	if codeOff == 0 {
		return nil
	}
	triesSize := int(d.u16(codeOff + 6))
	if triesSize == 0 {
		return nil
	}
	insnsSize := d.u32(codeOff + 12)
	insnsEnd := uint64(codeOff) + 16 + uint64(insnsSize)*2
	if insnsSize%2 == 1 {
		insnsEnd += 2 // padding
	}
	handlersStart := insnsEnd + uint64(triesSize)*8
	if handlersStart >= uint64(len(d.buf)) {
		return nil
	}
	t := &ExceptionTable{offs: make([]uint16, 0, triesSize)}
	// try_item 数组
	for i := 0; i < triesSize; i++ {
		base := insnsEnd + uint64(i)*8
		if base+8 > uint64(len(d.buf)) {
			return nil
		}
		t.Tries = append(t.Tries, TryItem{
			StartAddr:  d.u32(uint32(base)),
			InsnCount:  d.u16(uint32(base + 4)),
			HandlerOff: d.u16(uint32(base + 6)),
		})
	}
	// encoded_catch_handler_list:size + size 个 encoded_catch_handler
	pos := int(handlersStart)
	size, n := d.readUlebBounded(pos)
	if n == 0 || size > 0xffff {
		return nil
	}
	pos += n
	for i := uint32(0); i < size; i++ {
		off := pos - int(handlersStart)
		if off > 0xffff {
			return nil
		}
		sz, n := d.readSlebBounded(pos)
		if n == 0 || sz > 0xffff || sz < -0xffff {
			return nil
		}
		pos += n
		h := &CatchHandler{CatchAllAddr: -1}
		cnt := int(sz)
		if cnt < 0 {
			cnt = -cnt
		}
		for j := 0; j < cnt; j++ {
			typeIdx, n := d.readUlebBounded(pos)
			if n == 0 {
				return nil
			}
			pos += n
			addr, n := d.readUlebBounded(pos)
			if n == 0 {
				return nil
			}
			pos += n
			h.Typed = append(h.Typed, CatchTypeAddr{TypeIdx: typeIdx, Addr: addr})
		}
		if sz <= 0 { // 负 size:末尾带 catch_all_addr
			addr, n := d.readUlebBounded(pos)
			if n == 0 {
				return nil
			}
			pos += n
			h.CatchAllAddr = int(addr)
		}
		t.Handlers = append(t.Handlers, h)
		t.offs = append(t.offs, uint16(off))
	}
	return t
}

// readUlebBounded/readSlebBounded 带边界的 leb 读取:越界返回 (0, 0)。
// leb128 快速路径不做逐字节边界检查(最多前瞻 5 字节),文件尾部的
// uleb 会被误判越界,故此处按字节推进、逐字节检查边界。
// 语义与 ReadUleb128/ReadSleb128 一致:第 5 字节不再检查延续位。
func (d *DEX) readUlebBounded(pos int) (uint32, int) {
	var res uint32
	for i := 0; i < 4; i++ {
		if pos+i >= len(d.buf) {
			return 0, 0
		}
		b := d.buf[pos+i]
		res |= uint32(b&0x7f) << (7 * i)
		if b < 0x80 {
			return res, i + 1
		}
	}
	if pos+4 >= len(d.buf) {
		return 0, 0
	}
	return res | uint32(d.buf[pos+4]&0x7f)<<28, 5
}

func (d *DEX) readSlebBounded(pos int) (int32, int) {
	var res int32
	var shift uint
	for i := 0; i < 5; i++ {
		if pos+i >= len(d.buf) {
			return 0, 0
		}
		b := d.buf[pos+i]
		res |= int32(b&0x7f) << shift
		shift += 7
		if b < 0x80 {
			if shift < 32 && b&0x40 != 0 {
				res |= -1 << shift // 符号扩展
			}
			return res, i + 1
		}
	}
	return res, 5
}
