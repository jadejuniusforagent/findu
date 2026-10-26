package dex

// minidex_anno.go — mini.dex 的注解与静态初始值完整拷贝。
// encoded_value 逐值重写:string/type/field/method/enum/method_type 索引
// 经对应映射表换新;method_handle 值不支持(返回错误,调用方回退全量);
// 纯数值载荷原样拷贝。注解结构链:annotations_directory →(set_ref_list)→
// annotation_set_item → annotation_item → encoded_annotation/encoded_value。
// 目录条目按新索引升序重排并重编码差分(规范要求升序)。

import (
	"fmt"
	"sort"
)

// ────────────────────────── 收集阶段 ──────────────────────────

// collectAnnotationsDir 登记注解目录引用的全部类型/字段/方法/字符串。
func (e *minidexBuilder) collectAnnotationsDir(dirOff int) error {
	d := e.d
	if setOff := d.u32(uint32(dirOff)); setOff != 0 {
		if err := e.collectAnnoSet(int(setOff)); err != nil {
			return err
		}
	}
	fsz := d.u32(uint32(dirOff + 4))
	msz := d.u32(uint32(dirOff + 8))
	psz := d.u32(uint32(dirOff + 12))
	ents, ok := d.parseAnnoDirEntries(dirOff+16, fsz, msz, psz)
	if !ok {
		return fmt.Errorf("minidex: malformed annotations_directory at %d", dirOff)
	}
	e.collectAnnotationsDirEntries(ents)
	return nil
}

// ────────────── 目录条目双模式解析 ──────────────
// 实测 dx 产物为 u32 绝对索引 + u32 偏移;部分规范文本为 uleb 差分。
// 两种形态按注解集可见性字节校验自动判定:u32 优先,uleb 兜底。

type annoDirEntry struct {
	idx    uint32 // 旧 field/method 索引
	refOff uint32 // annotation_set_item 偏移
}

type annoParamEntry struct {
	idx      uint32
	refLists []uint32 // annotation_set_ref_list 偏移
}

type annoDirEntries struct {
	fields, methods []annoDirEntry
	params          []annoParamEntry
}

// annoSetPlausible 校验候选偏移确为 annotation_set_item:
// 元素数有界,且每个 annotation_item 首字节为合法 visibility(0-3)。
func (d *DEX) annoSetPlausible(off int) bool {
	if off <= 0 || off+4 > len(d.buf) {
		return false
	}
	sz := d.u32(uint32(off))
	if sz > 0xffff {
		return false
	}
	for i := uint32(0); i < sz; i++ {
		io := d.u32(uint32(off) + 4 + i*4)
		if io <= 0 || io >= uint32(len(d.buf)) || d.buf[io] > 3 {
			return false
		}
	}
	return true
}

func (d *DEX) annoRefListPlausible(off int) bool {
	if off <= 0 || off+4 > len(d.buf) {
		return false
	}
	sz := d.u32(uint32(off))
	if sz > 0xffff {
		return false
	}
	for i := uint32(0); i < sz; i++ {
		if !d.annoSetPlausible(int(d.u32(uint32(off) + 4 + i*4))) {
			return false
		}
	}
	return true
}

// parseAnnoDirEntries 解析目录条目:u32 形态优先,uleb 形态兜底。
func (d *DEX) parseAnnoDirEntries(pos int, fsz, msz, psz uint32) (*annoDirEntries, bool) {
	ents := &annoDirEntries{}
	try := func(u32Mode bool) bool {
		pos := pos
		readIdx := func() (uint32, bool) {
			if u32Mode {
				if pos+4 > len(d.buf) {
					return 0, false
				}
				v := d.u32(uint32(pos))
				pos += 4
				return v, true
			}
			v, n := d.readUlebBounded(pos)
			if n == 0 {
				return 0, false
			}
			pos += n
			return v, true
		}
		readOff := readIdx
		prev := uint32(0)
		for i := uint32(0); i < fsz; i++ {
			diff, ok := readIdx()
			if !ok {
				return false
			}
			prev += diff
			off, ok := readOff()
			if !ok || !d.annoSetPlausible(int(off)) {
				return false
			}
			ents.fields = append(ents.fields, annoDirEntry{idx: prev, refOff: off})
		}
		prev = 0
		for i := uint32(0); i < msz; i++ {
			diff, ok := readIdx()
			if !ok {
				return false
			}
			prev += diff
			off, ok := readOff()
			if !ok || !d.annoSetPlausible(int(off)) {
				return false
			}
			ents.methods = append(ents.methods, annoDirEntry{idx: prev, refOff: off})
		}
		prev = 0
		for i := uint32(0); i < psz; i++ {
			diff, ok := readIdx()
			if !ok {
				return false
			}
			prev += diff
			n, ok := readOff()
			if !ok {
				return false
			}
			pe := annoParamEntry{idx: prev}
			for j := uint32(0); j < n; j++ {
				off, ok := readOff()
				if !ok || !d.annoRefListPlausible(int(off)) {
					return false
				}
				pe.refLists = append(pe.refLists, off)
			}
			ents.params = append(ents.params, pe)
		}
		return true
	}
	if try(true) {
		return ents, true
	}
	*ents = annoDirEntries{}
	if try(false) {
		return ents, true
	}
	return nil, false
}

// collectAnnotationsDirEntries 走条目登记引用。
func (e *minidexBuilder) collectAnnotationsDirEntries(ents *annoDirEntries) {
	for _, ent := range ents.fields {
		e.addField(ent.idx)
		if err := e.collectAnnoSet(int(ent.refOff)); err != nil {
			return
		}
	}
	for _, ent := range ents.methods {
		e.addMethod(ent.idx)
		if err := e.collectAnnoSet(int(ent.refOff)); err != nil {
			return
		}
	}
	for _, ent := range ents.params {
		e.addMethod(ent.idx)
		for _, rl := range ent.refLists {
			if err := e.collectAnnoSetRefList(int(rl)); err != nil {
				return
			}
		}
	}
}
func (e *minidexBuilder) collectAnnoSetRefList(off int) error {
	d := e.d
	sz := d.u32(uint32(off))
	for i := uint32(0); i < sz; i++ {
		setOff := d.u32(uint32(off) + 4 + i*4)
		if setOff == 0 {
			continue
		}
		if err := e.collectAnnoSet(int(setOff)); err != nil {
			return err
		}
	}
	return nil
}

func (e *minidexBuilder) collectAnnoSet(off int) error {
	d := e.d
	sz := d.u32(uint32(off))
	for i := uint32(0); i < sz; i++ {
		itemOff := d.u32(uint32(off) + 4 + i*4)
		if itemOff == 0 {
			continue
		}
		if err := e.collectAnnoItem(int(itemOff)); err != nil {
			return err
		}
	}
	return nil
}

func (e *minidexBuilder) collectAnnoItem(off int) error {
	d := e.d
	pos := off + 1 // visibility 1 字节
	typ, n := d.readUlebBounded(pos)
	if n == 0 {
		return fmt.Errorf("minidex: malformed annotation_item at %d", off)
	}
	pos += n
	e.addType(typ)
	sz, n := d.readUlebBounded(pos)
	if n == 0 {
		return fmt.Errorf("minidex: malformed annotation_item at %d", off)
	}
	pos += n
	for i := uint32(0); i < sz; i++ {
		name, n := d.readUlebBounded(pos)
		if n == 0 {
			return fmt.Errorf("minidex: malformed annotation_item at %d", off)
		}
		pos += n
		e.addStr(name)
		var err error
		if pos, err = e.collectValueRefs(pos); err != nil {
			return err
		}
	}
	return nil
}

// collectValueRefs 递归登记 encoded_value 中的索引引用。
// method_handle 值不支持,返回错误(与指令级 call site 引用同策略)。
func (e *minidexBuilder) collectValueRefs(pos int) (int, error) {
	d := e.d
	if pos >= len(d.buf) {
		return 0, fmt.Errorf("minidex: encoded_value out of range")
	}
	header := d.buf[pos]
	vt := header & 0x1f
	arg := int(header >> 5)
	pos++
	switch vt {
	case 0x1e, 0x1f: // null / boolean
		return pos, nil
	case 0x1c: // array:value_arg 为元素个数
		for i := 0; i < arg; i++ {
			var err error
			if pos, err = e.collectValueRefs(pos); err != nil {
				return 0, err
			}
		}
		return pos, nil
	case 0x1d: // annotation
		typ, n := d.readUlebBounded(pos)
		if n == 0 {
			return 0, fmt.Errorf("minidex: malformed encoded_annotation")
		}
		pos += n
		e.addType(typ)
		sz, n := d.readUlebBounded(pos)
		if n == 0 {
			return 0, fmt.Errorf("minidex: malformed encoded_annotation")
		}
		pos += n
		for i := uint32(0); i < sz; i++ {
			name, n := d.readUlebBounded(pos)
			if n == 0 {
				return 0, fmt.Errorf("minidex: malformed encoded_annotation")
			}
			pos += n
			e.addStr(name)
			var err error
			if pos, err = e.collectValueRefs(pos); err != nil {
				return 0, err
			}
		}
		return pos, nil
	default:
		if vt == 0x16 { // method_handle
			return 0, fmt.Errorf("minidex: method_handle value not supported")
		}
		width := arg + 1
		if pos+width > len(d.buf) {
			return 0, fmt.Errorf("minidex: encoded_value out of range")
		}
		payload := d.buf[pos : pos+width]
		old := leIdx(payload)
		switch vt {
		case 0x15:
			e.addProto(old)
		case 0x17:
			e.addStr(old)
		case 0x18:
			e.addType(old)
		case 0x19, 0x1b: // field / enum
			e.addField(old)
		case 0x1a:
			e.addMethod(old)
		}
		return pos + width, nil
	}
}

// leIdx 小端定宽索引载荷 → uint32。
func leIdx(payload []byte) uint32 {
	var v uint32
	for i := len(payload) - 1; i >= 0; i-- {
		v = v<<8 | uint32(payload[i])
	}
	return v
}

// ────────────────────────── 发射阶段 ──────────────────────────

// emitAnnotationsDir 发射注解目录( memo 化),返回新偏移。
func (e *minidexBuilder) emitAnnotationsDir(w *minidexWriter, dirOff int) (uint32, error) {
	if newOff, ok := e.annoDirMap[dirOff]; ok {
		return newOff, nil
	}
	d := e.d
	classAnnOff := d.u32(uint32(dirOff))
	fsz := d.u32(uint32(dirOff + 4))
	msz := d.u32(uint32(dirOff + 8))
	psz := d.u32(uint32(dirOff + 12))
	ents, ok := d.parseAnnoDirEntries(dirOff+16, fsz, msz, psz)
	if !ok {
		return 0, fmt.Errorf("minidex: malformed annotations_directory at %d", dirOff)
	}

	// 先发射全部注解集/引用列表(数据追加在目录之后),记录新偏移,
	// 再写目录头与条目——保证条目连续不被数据插花。
	var classSetOff uint32
	if classAnnOff != 0 {
		off, err := e.emitAnnoSet(w, int(classAnnOff))
		if err != nil {
			return 0, err
		}
		classSetOff = off
	}
	type annoEnt struct {
		newIdx uint32
		setOff uint32
	}
	fieldEnts := make([]annoEnt, 0, len(ents.fields))
	for _, ent := range ents.fields {
		so, err := e.emitAnnoSet(w, int(ent.refOff))
		if err != nil {
			return 0, err
		}
		fieldEnts = append(fieldEnts, annoEnt{newIdx: e.fieldMap[ent.idx], setOff: so})
	}
	methodEnts := make([]annoEnt, 0, len(ents.methods))
	for _, ent := range ents.methods {
		so, err := e.emitAnnoSet(w, int(ent.refOff))
		if err != nil {
			return 0, err
		}
		fmt.Printf("DBG2 m: oldIdx=%d newIdx=%d setOff=%d wlen=%d\n", ent.idx, e.methMap[ent.idx], so, w.offset())
		methodEnts = append(methodEnts, annoEnt{newIdx: e.methMap[ent.idx], setOff: so})
	}
	paramEnts := make([]struct {
		newIdx  uint32
		setOffs []uint32
	}, 0, len(ents.params))
	for _, ent := range ents.params {
		pe := struct {
			newIdx  uint32
			setOffs []uint32
		}{newIdx: e.methMap[ent.idx]}
		for _, rl := range ent.refLists {
			so, err := e.emitAnnoSetRefList(w, int(rl))
			if err != nil {
				return 0, err
			}
			pe.setOffs = append(pe.setOffs, so)
		}
		paramEnts = append(paramEnts, pe)
	}
	sort.Slice(fieldEnts, func(i, j int) bool { return fieldEnts[i].newIdx < fieldEnts[j].newIdx })
	sort.Slice(methodEnts, func(i, j int) bool { return methodEnts[i].newIdx < methodEnts[j].newIdx })
	sort.Slice(paramEnts, func(i, j int) bool { return paramEnts[i].newIdx < paramEnts[j].newIdx })

	// 目录:16 字节头 + 连续条目(uleb 差分,新索引升序)
	w.align4()
	newOff := uint32(w.offset())
	w.putU32(classSetOff)
	w.putU32(uint32(len(fieldEnts)))
	w.putU32(uint32(len(methodEnts)))
	w.putU32(uint32(len(paramEnts)))
	prev := uint32(0)
	for _, ent := range fieldEnts {
		w.place(miniULEB(ent.newIdx - prev))
		prev = ent.newIdx
		w.place(miniULEB(ent.setOff))
	}
	prev = 0
	for _, ent := range methodEnts {
		w.place(miniULEB(ent.newIdx - prev))
		prev = ent.newIdx
		w.place(miniULEB(ent.setOff))
	}
	prev = 0
	for _, pe := range paramEnts {
		w.place(miniULEB(pe.newIdx - prev))
		prev = pe.newIdx
		w.place(miniULEB(uint32(len(pe.setOffs))))
		for _, so := range pe.setOffs {
			w.place(miniULEB(so))
		}
	}
	e.annoDirMap[dirOff] = newOff
	e.noteAnno(0x2006, newOff)
	return newOff, nil
}
func (e *minidexBuilder) emitAnnoSetRefList(w *minidexWriter, off int) (uint32, error) {
	if newOff, ok := e.annoRefListMap[off]; ok {
		return newOff, nil
	}
	d := e.d
	sz := d.u32(uint32(off))
	// 先发射全部被引用集合(追加尾部),记录偏移;再写 [size][offsets]
	setOffs := make([]uint32, 0, sz)
	for i := uint32(0); i < sz; i++ {
		old := d.u32(uint32(off) + 4 + i*4)
		if old == 0 {
			setOffs = append(setOffs, 0)
			continue
		}
		so, err := e.emitAnnoSet(w, int(old))
		if err != nil {
			return 0, err
		}
		setOffs = append(setOffs, so)
	}
	w.align4()
	newOff := uint32(w.offset())
	if e.firstRefListOff == 0 {
		e.firstRefListOff = newOff
	}
	e.refListCount++
	w.putU32(sz)
	for _, so := range setOffs {
		w.putU32(so)
	}
	e.annoRefListMap[off] = newOff
	e.noteAnno(0x1002, newOff)
	return newOff, nil
}

func (e *minidexBuilder) emitAnnoSet(w *minidexWriter, off int) (uint32, error) {
	if newOff, ok := e.annoSetMap[off]; ok {
		return newOff, nil
	}
	d := e.d
	sz := d.u32(uint32(off))
	// 先发射全部 annotation_item(追加尾部),记录偏移;再写 [size][offsets]
	itemOffs := make([]uint32, 0, sz)
	for i := uint32(0); i < sz; i++ {
		old := d.u32(uint32(off) + 4 + i*4)
		if old == 0 {
			itemOffs = append(itemOffs, 0)
			continue
		}
		io, err := e.emitAnnoItem(w, int(old))
		if err != nil {
			return 0, err
		}
		itemOffs = append(itemOffs, io)
	}
	w.align4()
	newOff := uint32(w.offset())
	if e.firstAnnoSetOff == 0 {
		e.firstAnnoSetOff = newOff
	}
	e.annoSetCount++
	w.putU32(sz)
	for _, io := range itemOffs {
		w.putU32(io)
	}
	e.annoSetMap[off] = newOff
	e.noteAnno(0x1003, newOff)
	return newOff, nil
}

func (e *minidexBuilder) emitAnnoItem(w *minidexWriter, off int) (uint32, error) {
	if newOff, ok := e.annoItemMap[off]; ok {
		return newOff, nil
	}
	d := e.d
	w.align4()
	newOff := uint32(w.offset())
	if e.firstAnnoItemOff == 0 {
		e.firstAnnoItemOff = newOff
	}
	e.annoItemCount++
	w.place(d.buf[off : off+1]) // visibility 原样
	pos := off + 1
	typ, n := d.readUlebBounded(pos)
	if n == 0 {
		return 0, fmt.Errorf("minidex: malformed annotation_item at %d", off)
	}
	pos += n
	w.place(miniULEB(e.typeMap[typ]))
	sz, n := d.readUlebBounded(pos)
	if n == 0 {
		return 0, fmt.Errorf("minidex: malformed annotation_item at %d", off)
	}
	pos += n
	w.place(miniULEB(sz))
	for i := uint32(0); i < sz; i++ {
		name, n := d.readUlebBounded(pos)
		if n == 0 {
			return 0, fmt.Errorf("minidex: malformed annotation_item at %d", off)
		}
		pos += n
		w.place(miniULEB(e.nStr(name)))
		var err error
		if pos, err = e.rewriteEncodedValue(w, d.buf, pos); err != nil {
			return 0, err
		}
	}
	e.annoItemMap[off] = newOff
	e.noteAnno(0x2004, newOff)
	return newOff, nil
}

// rewriteEncodedValue 解析 encoded_value 并重写索引后追加到 dst。
// 返回源中的下一位置。
func (e *minidexBuilder) rewriteEncodedValue(dst *minidexWriter, src []byte, pos int) (int, error) {
	if pos >= len(src) {
		return 0, fmt.Errorf("minidex: encoded_value out of range")
	}
	header := src[pos]
	vt := header & 0x1f
	arg := int(header >> 5)
	pos++
	switch vt {
	case 0x1e, 0x1f: // null / boolean(值在 value_arg)
		dst.place([]byte{header})
		return pos, nil
	case 0x1c: // array:value_arg 为元素个数
		dst.place([]byte{header})
		for i := 0; i < arg; i++ {
			var err error
			if pos, err = e.rewriteEncodedValue(dst, src, pos); err != nil {
				return 0, err
			}
		}
		return pos, nil
	case 0x1d: // annotation
		typ, n := readUAt(src, pos)
		if n == 0 {
			return 0, fmt.Errorf("minidex: malformed encoded_annotation")
		}
		pos += n
		dst.place(miniULEB(e.typeMap[typ]))
		sz, n := readUAt(src, pos)
		if n == 0 {
			return 0, fmt.Errorf("minidex: malformed encoded_annotation")
		}
		pos += n
		dst.place(miniULEB(sz))
		for i := uint32(0); i < sz; i++ {
			name, n := readUAt(src, pos)
			if n == 0 {
				return 0, fmt.Errorf("minidex: malformed encoded_annotation")
			}
			pos += n
			dst.place(miniULEB(e.nStr(name)))
			var err error
			if pos, err = e.rewriteEncodedValue(dst, src, pos); err != nil {
				return 0, err
			}
		}
		return pos, nil
	default:
		width := arg + 1
		if pos+width > len(src) {
			return 0, fmt.Errorf("minidex: encoded_value out of range")
		}
		payload := src[pos : pos+width]
		pos += width
		if vt == 0x16 {
			return 0, fmt.Errorf("minidex: method_handle value not supported")
		}
		if vt >= 0x15 && vt <= 0x1b {
			var newIdx uint32
			switch vt {
			case 0x15:
				newIdx = e.protoMap[leIdx(payload)]
			case 0x17:
				newIdx = e.strMap[leIdx(payload)]
			case 0x18:
				newIdx = e.typeMap[leIdx(payload)]
			case 0x19, 0x1b:
				newIdx = e.fieldMap[leIdx(payload)]
			case 0x1a:
				newIdx = e.methMap[leIdx(payload)]
			}
			e.writeIdxValue(dst, vt, newIdx)
			return pos, nil
		}
		// 纯数值:头 + 载荷原样
		dst.place([]byte{header})
		dst.place(payload)
		return pos, nil
	}
}

// writeIdxValue 按新索引的最小字节宽重编码索引型 encoded_value。
func (e *minidexBuilder) writeIdxValue(dst *minidexWriter, vt byte, newIdx uint32) {
	width := 1
	for i := 3; i >= 1; i-- {
		if newIdx >= uint32(1)<<(8*i) {
			width = i + 1
			break
		}
	}
	dst.place([]byte{byte(width-1)<<5 | vt})
	buf := make([]byte, width)
	for i := 0; i < width; i++ {
		buf[i] = byte(newIdx >> (8 * i))
	}
	dst.place(buf)
}

// readUAt 无接收者的 uleb 读取(minidex_anno 内部使用)。
func readUAt(src []byte, pos int) (uint32, int) {
	var res uint32
	for i := 0; i < 4; i++ {
		if pos+i >= len(src) {
			return 0, 0
		}
		b := src[pos+i]
		res |= uint32(b&0x7f) << (7 * i)
		if b < 0x80 {
			return res, i + 1
		}
	}
	if pos+4 >= len(src) {
		return 0, 0
	}
	return res | uint32(src[pos+4]&0x7f)<<28, 5
}
