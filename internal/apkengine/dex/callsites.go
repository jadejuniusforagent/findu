package dex

// callsites.go — map_list / method_handle_item / call_site_id_item 解析,
// 为 invoke-custom(0xfc/0xfd) 提供调用点语义(函数接口方法名 + 调用原型)。
// 表位置仅能经 map_list 定位(规范机制,DEX v039+);map_off 缺失(合成
// fixture)或畸形数据时安全降级返回 nil。
//
// call_site_item 布局:method_handle(bootstrap linker)+ method_name +
// method_type + 零或多个附加静态参数。附加参数无独立长度字段(由相邻
// 表项界定),且 dx 产物中极少出现,此处只解码固定前三项——语义上足以
// 还原 `接口类型.方法名(实参)` 形态。

// map_list 条目类型(规范 0x0007/0x0008,DEX v039 引入)。
const (
	mapTypeCallSiteID   uint16 = 0x0007
	mapTypeMethodHandle uint16 = 0x0008
)

// MethodHandleItem 对齐 method_handle_item。
type MethodHandleItem struct {
	Type             uint16 // 句柄种类(0x00-0x06 字段类,0x70-0x77 方法类)
	FieldOrMethodIdx uint32 // 按种类指向 field_ids 或 method_ids
}

// CallSite invoke-custom 调用点(已解码)。
type CallSite struct {
	Linker *MethodHandleItem // bootstrap linker 方法句柄
	Name   string            // 函数接口方法名(apply/accept/run/...)
	Proto  *Prototype        // 调用点方法类型(invoke-custom 实参与此原型匹配)
}

// GetMethodHandle 解析 method_handle_idx;表缺失或越界返回 nil。
func (d *DEX) GetMethodHandle(idx uint32) *MethodHandleItem {
	d.mhOnce.Do(func() { d.methodHandles = d.parseMethodHandles() })
	if idx >= uint32(len(d.methodHandles)) {
		return nil
	}
	return d.methodHandles[idx]
}

// GetCallSite 解析 call_site_idx;表缺失或越界返回 nil。
func (d *DEX) GetCallSite(idx uint32) *CallSite {
	d.csOnce.Do(func() { d.callSites = d.parseCallSites() })
	if idx >= uint32(len(d.callSites)) {
		return nil
	}
	return d.callSites[idx]
}

// findMapSection 在 map_list 中定位指定类型的 (offset, size)。
func (d *DEX) findMapSection(typ uint16) (uint32, uint32, bool) {
	if d.hdr.MapOff == 0 || uint64(d.hdr.MapOff)+4 > uint64(len(d.buf)) {
		return 0, 0, false
	}
	n := d.u32(d.hdr.MapOff)
	if n == 0 || n > 0xffff {
		return 0, 0, false
	}
	base := uint64(d.hdr.MapOff) + 4
	for i := uint64(0); i < uint64(n); i++ {
		o := base + i*12
		if o+12 > uint64(len(d.buf)) {
			return 0, 0, false
		}
		if d.u16(uint32(o)) == typ {
			// map_item:type u16 | unused u16 | size u32 | offset u32
			return d.u32(uint32(o + 8)), d.u32(uint32(o + 4)), true
		}
	}
	return 0, 0, false
}

func (d *DEX) parseMethodHandles() []*MethodHandleItem {
	off, size, ok := d.findMapSection(mapTypeMethodHandle)
	if !ok || size == 0 {
		return nil
	}
	out := make([]*MethodHandleItem, 0, size)
	for i := uint64(0); i < uint64(size); i++ {
		base := uint64(off) + i*8
		if base+8 > uint64(len(d.buf)) {
			break
		}
		out = append(out, &MethodHandleItem{
			Type:             d.u16(uint32(base)),
			FieldOrMethodIdx: d.u32(uint32(base + 4)),
		})
	}
	return out
}

func (d *DEX) parseCallSites() []*CallSite {
	off, size, ok := d.findMapSection(mapTypeCallSiteID)
	if !ok || size == 0 {
		return nil
	}
	out := make([]*CallSite, 0, size)
	for i := uint64(0); i < uint64(size); i++ {
		entry := uint64(off) + i*4
		if entry+4 > uint64(len(d.buf)) {
			break
		}
		itemOff := d.u32(uint32(entry))
		if itemOff == 0 || itemOff >= uint32(len(d.buf)) {
			out = append(out, nil)
			continue
		}
		out = append(out, d.decodeCallSiteItem(itemOff))
	}
	return out
}

// decodeCallSiteItem 解码 call_site_item 固定前三项。
// 表索引越界(畸形)返回 nil,由调用方按缺失处理。
func (d *DEX) decodeCallSiteItem(off uint32) *CallSite {
	mhIdx, n := d.readUlebBounded(int(off))
	if n == 0 {
		return nil
	}
	pos := int(off) + n
	nameIdx, n := d.readUlebBounded(pos)
	if n == 0 {
		return nil
	}
	pos += n
	protoIdx, n := d.readUlebBounded(pos)
	if n == 0 {
		return nil
	}
	// 表索引校验(GetString/GetPrototype 不做越界检查)
	if nameIdx >= d.hdr.Strings.Size || protoIdx >= d.hdr.Prototypes.Size {
		return nil
	}
	cs := &CallSite{
		Linker: d.GetMethodHandle(mhIdx),
		Name:   d.GetString(nameIdx),
	}
	if protoIdx < d.hdr.Prototypes.Size {
		cs.Proto = d.GetPrototype(protoIdx)
	}
	return cs
}
