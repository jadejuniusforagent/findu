package dex

// minidex_layout.go — mini.dex 布局与输出(索引收集/排序见 minidex.go)。

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"hash/adler32"
	"sort"
)

type minidexWriter struct {
	buf []byte
}

func (w *minidexWriter) skip(n int) int {
	off := len(w.buf)
	w.buf = append(w.buf, make([]byte, n)...)
	return off
}

func (w *minidexWriter) place(b []byte) int {
	off := len(w.buf)
	w.buf = append(w.buf, b...)
	return off
}

func (w *minidexWriter) align4() {
	for len(w.buf)%4 != 0 {
		w.buf = append(w.buf, 0)
	}
}

func (w *minidexWriter) offset() int { return len(w.buf) }

func (w *minidexWriter) patchU32(off int, v uint32) {
	binary.LittleEndian.PutUint32(w.buf[off:], v)
}

func (w *minidexWriter) patchU16(off int, v uint16) {
	binary.LittleEndian.PutUint16(w.buf[off:], v)
}

func (w *minidexWriter) putU16(v uint16) { w.buf = append(w.buf, byte(v), byte(v>>8)) }

func (w *minidexWriter) putU32(v uint32) {
	w.buf = append(w.buf, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

// layout 布局并输出 mini.dex 字节(逐段偏移回填 + 校验和)。
func (e *minidexBuilder) layout(classes []*Class) ([]byte, error) {
	d := e.d
	w := &minidexWriter{}

	w.skip(0x70) // header 占位
	stringIdsOff := w.skip(len(e.newStrings) * 4)
	typeIdsOff := w.skip(len(e.sortedTypes) * 4)
	protoIdsOff := w.skip(len(e.sortedProtos) * 12)
	fieldIdsOff := w.skip(len(e.sortedFields) * 8)
	methodIdsOff := w.skip(len(e.sortedMethods) * 8)
	classDefsOff := w.skip(len(classes) * 32)

	w.align4()
	dataOff := w.offset()

	// string_data(原样拷贝:uleb utf16_size + mutf8 + NUL)
	strDataOffs := make([]uint32, len(e.newStringOlds))
	for i, old := range e.newStringOlds {
		start := int(d.u32(d.hdr.Strings.Off + old*4))
		size, n := d.readUlebBounded(start)
		strDataOffs[i] = uint32(w.place(d.buf[start : start+n+int(size)+1]))
	}
	for i, off := range strDataOffs {
		w.patchU32(stringIdsOff+i*4, off)
	}

	// type_ids
	for newIdx, old := range e.sortedTypes {
		w.patchU32(typeIdsOff+newIdx*4, e.nStr(d.typeStrIdx(old)))
	}

	// proto_ids(参数 type_list 内联)
	typeListCount, firstTypeListOff := 0, uint32(0)
	for newIdx, old := range e.sortedProtos {
		p := d.GetPrototype(old)
		off := protoIdsOff + newIdx*12
		w.patchU32(off, e.nStr(p.ShortyIdx))
		w.patchU32(off+4, e.nType(p.ReturnTypeIdx))
		params := d.protoParamTypeIdxs(old)
		if len(params) == 0 {
			w.patchU32(off+8, 0)
			continue
		}
		w.align4()
		listOff := uint32(w.offset())
		if firstTypeListOff == 0 {
			firstTypeListOff = listOff
		}
		w.patchU32(off+8, listOff)
		w.putU32(uint32(len(params)))
		for _, t := range params {
			w.putU16(uint16(e.nType(t)))
		}
		typeListCount++
	}

	// field_ids(class, type, name)
	for newIdx, old := range e.sortedFields {
		r := e.fields[old]
		off := fieldIdsOff + newIdx*8
		w.patchU16(off, uint16(e.nType(r.class)))
		w.patchU16(off+2, uint16(e.nType(r.typ)))
		w.patchU32(off+4, e.nStr(r.name))
	}

	// method_ids(class, proto, name)
	for newIdx, old := range e.sortedMethods {
		r := e.meths[old]
		off := methodIdsOff + newIdx*8
		w.patchU16(off, uint16(e.nType(r.class)))
		w.patchU16(off+2, uint16(e.protoMap[r.proto]))
		w.patchU32(off+4, e.nStr(r.name))
	}

	// 逐类发射 data 并回填 class_defs
	codeCount, classDataCount := 0, 0
	firstCodeOff, firstClassDataOff := uint32(0), uint32(0)
	for ci, c := range classes {
		base := c.classDefOff
		entry := classDefsOff + ci*32

		var ifListOff uint32
		if ifOff := d.u32(base + 12); ifOff != 0 {
			n := int(d.u32(ifOff))
			w.align4()
			ifListOff = uint32(w.offset())
			if firstTypeListOff == 0 {
				firstTypeListOff = ifListOff
			}
			w.putU32(uint32(n))
			for i := 0; i < n; i++ {
				w.putU16(uint16(e.nType(uint32(d.u16(ifOff + 4 + uint32(i*2))))))
			}
			typeListCount++
		}

		newSrc := uint32(noIndex)
		if srcIdx := d.u32(base + 16); srcIdx != noIndex {
			newSrc = e.nStr(srcIdx)
		}

		// static_values:encoded_array 逐值重写索引
		var svOff uint32
		if so := d.u32(base + 28); so != 0 {
			count, n := d.readUlebBounded(int(so))
			if n == 0 {
				return nil, fmt.Errorf("minidex: malformed static_values at %d", so)
			}
			pos := int(so) + n
			w.align4()
			svOff = uint32(w.offset())
			w.place(miniULEB(count))
			for i := uint32(0); i < count; i++ {
				var err error
				if pos, err = e.rewriteEncodedValue(w, d.buf, pos); err != nil {
					return nil, err
				}
			}
			e.noteAnno(0x2005, svOff)
		}
		// annotations:目录整链重写
		if dirOff := d.u32(base + 20); dirOff != 0 {
			newDir, err := e.emitAnnotationsDir(w, int(dirOff))
			if err != nil {
				return nil, err
			}
			w.patchU32(entry+20, newDir)
		}

		cd := d.parseClassDataRaw(d.u32(base + 24))
		w.align4()
		emitErr := func(ms []cdMethod) error {
			for i := range ms {
				if ms[i].codeOff == 0 {
					continue
				}
				off, err := e.emitCode(w, e.codeByOff[ms[i].codeOff])
				if err != nil {
					return err
				}
				ms[i].newCodeOff = off // 原位回填,writeClassData 消费
				if firstCodeOff == 0 {
					firstCodeOff = off
				}
				codeCount++
			}
			return nil
		}
		if err := emitErr(cd.directs); err != nil {
			return nil, err
		}
		if err := emitErr(cd.virtuals); err != nil {
			return nil, err
		}

		w.align4()
		cdDataOff := uint32(w.offset())
		if n := len(cd.statics) + len(cd.instances) + len(cd.directs) + len(cd.virtuals); n > 0 {
			if firstClassDataOff == 0 {
				firstClassDataOff = cdDataOff
			}
			classDataCount++
			e.writeClassData(w, cd)
		} else {
			cdDataOff = 0
		}

		w.patchU32(entry, e.nType(c.ClassIdx))
		w.patchU32(entry+4, d.u32(base+4)) // access_flags 原样
		super := d.u32(base + 8)
		if super != noIndex {
			super = e.nType(super)
		}
		w.patchU32(entry+8, super)
		w.patchU32(entry+12, ifListOff)
		w.patchU32(entry+16, newSrc)
		// entry+20 annotations_off 已在注解发射期回填
		w.patchU32(entry+24, cdDataOff)
		w.patchU32(entry+28, svOff)
	}

	// map_list(按偏移升序)
	w.align4()
	mapOff := uint32(w.offset())
	type mapEnt struct {
		typ  uint16
		size uint32
		off  uint32
	}
	entries := []mapEnt{
		{0x0000, 1, 0},
		{0x0001, uint32(len(e.newStrings)), uint32(stringIdsOff)},
		{0x0002, uint32(len(e.sortedTypes)), uint32(typeIdsOff)},
		{0x0003, uint32(len(e.sortedProtos)), uint32(protoIdsOff)},
		{0x0006, uint32(len(classes)), uint32(classDefsOff)},
		{0x2002, uint32(len(e.newStrings)), strDataOffs[0]},
		{0x1000, 1, mapOff},
	}
	if len(e.sortedFields) > 0 {
		entries = append(entries, mapEnt{0x0004, uint32(len(e.sortedFields)), uint32(fieldIdsOff)})
	}
	entries = append(entries, mapEnt{0x0005, uint32(len(e.sortedMethods)), uint32(methodIdsOff)})
	if typeListCount > 0 {
		entries = append(entries, mapEnt{0x1001, uint32(typeListCount), firstTypeListOff})
	}
	if classDataCount > 0 {
		entries = append(entries, mapEnt{0x2000, uint32(classDataCount), firstClassDataOff})
	}
	if codeCount > 0 {
		entries = append(entries, mapEnt{0x2001, uint32(codeCount), firstCodeOff})
	}
	for _, typ := range []uint16{0x1002, 0x1003, 0x2003, 0x2004, 0x2005, 0x2006} {
		if st := e.annoStats[typ]; st != nil {
			entries = append(entries, mapEnt{typ, st.count, st.first})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].off < entries[j].off })

	w.putU32(uint32(len(entries)))
	for _, ent := range entries {
		w.putU16(ent.typ)
		w.putU16(0)
		w.putU32(ent.size)
		w.putU32(ent.off)
	}

	// header
	h := w.buf
	copy(h, d.buf[0:8]) // 保留源 magic
	fileSize := uint32(len(h))
	binary.LittleEndian.PutUint32(h[0x20:], fileSize)
	binary.LittleEndian.PutUint32(h[0x24:], 0x70)
	binary.LittleEndian.PutUint32(h[0x28:], 0x12345678)
	binary.LittleEndian.PutUint32(h[0x34:], mapOff)
	binary.LittleEndian.PutUint32(h[0x38:], uint32(len(e.newStrings)))
	binary.LittleEndian.PutUint32(h[0x3C:], uint32(stringIdsOff))
	binary.LittleEndian.PutUint32(h[0x40:], uint32(len(e.sortedTypes)))
	binary.LittleEndian.PutUint32(h[0x44:], uint32(typeIdsOff))
	binary.LittleEndian.PutUint32(h[0x48:], uint32(len(e.sortedProtos)))
	binary.LittleEndian.PutUint32(h[0x4C:], uint32(protoIdsOff))
	binary.LittleEndian.PutUint32(h[0x50:], uint32(len(e.sortedFields)))
	binary.LittleEndian.PutUint32(h[0x54:], uint32(fieldIdsOff))
	binary.LittleEndian.PutUint32(h[0x58:], uint32(len(e.sortedMethods)))
	binary.LittleEndian.PutUint32(h[0x5C:], uint32(methodIdsOff))
	binary.LittleEndian.PutUint32(h[0x60:], uint32(len(classes)))
	binary.LittleEndian.PutUint32(h[0x64:], uint32(classDefsOff))
	binary.LittleEndian.PutUint32(h[0x68:], fileSize-uint32(dataOff))
	binary.LittleEndian.PutUint32(h[0x6C:], uint32(dataOff))

	sum := sha1.Sum(h[32:])
	copy(h[12:32], sum[:])
	binary.LittleEndian.PutUint32(h[8:12], adler32.Checksum(h[12:]))
	return h, nil
}

// emitCode 输出一个 code_item(指令 + try + handler 列表),返回新 code_off。
// debug_info 先发射(重映射字符串索引),其新偏移写入 code 头。
func (e *minidexBuilder) emitCode(w *minidexWriter, ci *mdCode) (uint32, error) {
	var dbgOff uint32
	if ci.debugOff != 0 {
		dbg, err := e.emitDebugInfo(w, ci.debugOff)
		if err != nil {
			return 0, err
		}
		dbgOff = dbg
	}
	w.align4()
	off := uint32(w.offset())
	w.putU16(ci.registers)
	w.putU16(ci.ins)
	w.putU16(ci.outs)
	w.putU16(ci.triesSize)
	w.putU32(dbgOff)
	w.putU32(uint32(len(ci.insns) / 2))
	w.place(ci.insns)
	if ci.triesSize > 0 {
		if len(ci.insns)%4 != 0 {
			w.putU16(0) // 奇数 insns 的 2 字节对齐填充
		}
		hb := &bytes.Buffer{}
		newHandlerOff := map[uint16]uint16{}
		for _, ti := range ci.tries {
			if _, ok := newHandlerOff[ti.HandlerOff]; ok {
				continue
			}
			newHandlerOff[ti.HandlerOff] = uint16(hb.Len())
			h := ci.handlers.Handler(ti.HandlerOff)
			if h == nil {
				hb.Write(miniSLEB(0))
				hb.Write(miniULEB(0))
				continue
			}
			n := len(h.Typed)
			if h.CatchAllAddr >= 0 {
				n = -n
			}
			hb.Write(miniSLEB(int32(n)))
			for _, ta := range h.Typed {
				hb.Write(miniULEB(e.typeMap[ta.TypeIdx]))
				hb.Write(miniULEB(ta.Addr))
			}
			if h.CatchAllAddr >= 0 {
				hb.Write(miniULEB(uint32(h.CatchAllAddr)))
			}
		}
		for _, ti := range ci.tries {
			w.putU32(ti.StartAddr)
			w.putU16(ti.InsnCount)
			w.putU16(newHandlerOff[ti.HandlerOff])
		}
		w.place(hb.Bytes())
	}
	return off, nil
}

// writeClassData 输出 class_data_item(按新索引序重编码差分)。
func (e *minidexBuilder) writeClassData(w *minidexWriter, cd classDataRaw) {
	sortRefs := func(refs []cdRef) []cdRef {
		out := append([]cdRef{}, refs...)
		sort.Slice(out, func(i, j int) bool { return e.fieldMap[out[i].idx] < e.fieldMap[out[j].idx] })
		return out
	}
	sortMeths := func(ms []cdMethod) []cdMethod {
		out := append([]cdMethod{}, ms...)
		sort.Slice(out, func(i, j int) bool { return e.methMap[out[i].idx] < e.methMap[out[j].idx] })
		return out
	}
	writeRefs := func(refs []cdRef) {
		prev := uint32(0)
		for _, r := range refs {
			ni := e.fieldMap[r.idx]
			w.place(miniULEB(ni - prev))
			prev = ni
			w.place(miniULEB(r.access))
		}
	}
	writeMeths := func(ms []cdMethod) {
		prev := uint32(0)
		for _, m := range ms {
			ni := e.methMap[m.idx]
			w.place(miniULEB(ni - prev))
			prev = ni
			w.place(miniULEB(m.access))
			w.place(miniULEB(m.newCodeOff))
		}
	}
	// 规范布局:四个 count 全部在前,随后字段条目、方法条目
	w.place(miniULEB(uint32(len(cd.statics))))
	w.place(miniULEB(uint32(len(cd.instances))))
	w.place(miniULEB(uint32(len(cd.directs))))
	w.place(miniULEB(uint32(len(cd.virtuals))))
	writeRefs(sortRefs(cd.statics))
	writeRefs(sortRefs(cd.instances))
	writeMeths(sortMeths(cd.directs))
	writeMeths(sortMeths(cd.virtuals))
}
