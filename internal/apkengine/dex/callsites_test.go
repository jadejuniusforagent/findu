package dex

// callsites_test — invoke-custom 调用点解析测试:手工构造含 map_list +
// call_site_ids + method_handles 的最小合法 DEX,验证 GetCallSite 全链路;
// 以及 map_off 缺失/畸形数据的降级行为。

import (
	"encoding/binary"
	"testing"
)

// ulebEncode 变长编码(测试构造用)。
func ulebEncode(n uint32) []byte {
	var out []byte
	if n == 0 {
		return []byte{0}
	}
	for n > 0x7f {
		out = append(out, byte(n&0x7f)|0x80)
		n >>= 7
	}
	return append(out, byte(n))
}

// buildCallSiteDEX 手工构造最小 DEX:
//
//	strings: 0="<fixture>" 1="apply" 2="Ljava/util/function/Function;"
//	types:   0 = Function 描述符
//	protos:  0 = 返回 Function、无参
//	method_handles: 1 项(invoke-static 0x70, idx 0)
//	call_site_ids: 1 项 → call_site_item(mh@0, name="apply", proto@0)
//	map_list: 含 0x0007/0x0008 条目
func buildCallSiteDEX(t *testing.T, withMap bool) []byte {
	t.Helper()

	const headerSize = uint32(0x70)
	str := func(s string) []byte {
		out := ulebEncode(uint32(len(s)))
		out = append(out, s...)
		return append(out, 0x00)
	}

	var cur uint32 = headerSize
	type blob struct {
		off  uint32
		data []byte
	}
	var blobs []blob
	place := func(data []byte) uint32 {
		off := cur
		blobs = append(blobs, blob{off, data})
		cur += uint32(len(data))
		return off
	}

	stringIdsBuf := make([]byte, 3*4)
	stringIds := place(stringIdsBuf)
	sdFix := place(str("<fixture>"))
	sdApply := place(str("apply"))
	sdFunc := place(str("Ljava/util/function/Function;"))
	binary.LittleEndian.PutUint32(stringIdsBuf[0:], sdFix)
	binary.LittleEndian.PutUint32(stringIdsBuf[4:], sdApply)
	binary.LittleEndian.PutUint32(stringIdsBuf[8:], sdFunc)

	typeIdsBuf := make([]byte, 4)
	typeIds := place(typeIdsBuf)
	binary.LittleEndian.PutUint32(typeIdsBuf, 2) // Function 描述符字符串

	protoIdsBuf := make([]byte, 12)
	protoIds := place(protoIdsBuf)
	binary.LittleEndian.PutUint32(protoIdsBuf[0:], 0) // shorty
	binary.LittleEndian.PutUint32(protoIdsBuf[4:], 0) // return type = Function
	binary.LittleEndian.PutUint32(protoIdsBuf[8:], 0) // 无参数

	mhBuf := make([]byte, 8)
	methodHandles := place(mhBuf)
	binary.LittleEndian.PutUint16(mhBuf, 0x70) // invoke-static
	binary.LittleEndian.PutUint32(mhBuf[4:], 0)

	csi := place(append(append(ulebEncode(0), ulebEncode(1)...), ulebEncode(0)...)) // mh@0, name@1, proto@0
	callSiteIdsBuf := make([]byte, 4)
	callSiteIds := place(callSiteIdsBuf)
	binary.LittleEndian.PutUint32(callSiteIdsBuf, csi)

	// map_list:header + string_id + type_id + proto_id + method_handle + call_site_id
	mapBuf := make([]byte, 4+5*12)
	mapList := place(mapBuf)
	binary.LittleEndian.PutUint32(mapBuf, 5)
	entry := func(i int, typ uint16, size, off uint32) {
		base := 4 + i*12
		binary.LittleEndian.PutUint16(mapBuf[base:], typ)
		binary.LittleEndian.PutUint32(mapBuf[base+4:], size)
		binary.LittleEndian.PutUint32(mapBuf[base+8:], off)
	}
	entry(0, 0x0001, 3, stringIds)
	entry(1, 0x0002, 1, typeIds)
	entry(2, 0x0003, 1, protoIds)
	entry(3, mapTypeMethodHandle, 1, methodHandles)
	entry(4, mapTypeCallSiteID, 1, callSiteIds)

	out := make([]byte, cur)
	for _, b := range blobs {
		copy(out[b.off:], b.data)
	}
	copy(out, "dex\n035\x00")
	binary.LittleEndian.PutUint32(out[0x20:], cur)
	if withMap {
		binary.LittleEndian.PutUint32(out[0x34:], mapList)
	}
	binary.LittleEndian.PutUint32(out[0x38:], 3) // strings
	binary.LittleEndian.PutUint32(out[0x3C:], stringIds)
	binary.LittleEndian.PutUint32(out[0x40:], 1) // types
	binary.LittleEndian.PutUint32(out[0x44:], typeIds)
	binary.LittleEndian.PutUint32(out[0x48:], 1) // protos
	binary.LittleEndian.PutUint32(out[0x4C:], protoIds)
	return out
}

func TestGetCallSite(t *testing.T) {
	d, err := Parse(buildCallSiteDEX(t, true), "test.dex")
	if err != nil {
		t.Fatal(err)
	}
	cs := d.GetCallSite(0)
	if cs == nil {
		t.Fatal("call site 0 should resolve")
	}
	if cs.Name != "apply" {
		t.Errorf("name = %q, want apply", cs.Name)
	}
	if cs.Proto == nil || cs.Proto.ReturnType().Descriptor != "Ljava/util/function/Function;" {
		t.Errorf("proto = %+v, want return Function", cs.Proto)
	}
	if cs.Linker == nil || cs.Linker.Type != 0x70 {
		t.Errorf("linker handle = %+v, want invoke-static", cs.Linker)
	}
	if d.GetCallSite(1) != nil {
		t.Error("out-of-range call site should be nil")
	}
	if d.GetCallSite(0) != cs { // 缓存一致性
		t.Error("GetCallSite should return the cached instance")
	}
	mh := d.GetMethodHandle(0)
	if mh == nil || mh.Type != 0x70 {
		t.Errorf("method handle = %+v", mh)
	}
}

func TestGetCallSiteNoMap(t *testing.T) {
	// map_off 缺失(合成 fixture 常态):安全降级为 nil,不 panic
	d, err := Parse(buildCallSiteDEX(t, false), "test.dex")
	if err != nil {
		t.Fatal(err)
	}
	if d.GetCallSite(0) != nil {
		t.Error("call site should be nil without map_list")
	}
	if d.GetMethodHandle(0) != nil {
		t.Error("method handle should be nil without map_list")
	}
}
