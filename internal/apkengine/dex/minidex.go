package dex

// minidex.go — 最小 DEX 重组(设计文档 §3.4)。
// 从已有 DEX 抽取指定类,重建仅含其引用的自洽 mini.dex:
//   1. 重建五大索引区(string/type/proto/field/method)+ class_defs
//   2. 重映射指令操作数中全部 string@/type@/field@/method@/proto@ 索引
//   3. 重建 map_list 与 header 偏移
//   4. 全程 4 字节对齐
//   5. 重算 SHA-1 签名与 Adler32 校验和
//
// v1 边界(有意降级,不影响代码主体还原;调用方在失败时回退全量 DEX):
//   - 引用 call_site / method_handle 的指令(invoke-custom、
//     const-method-handle)不支持,返回错误
//
// 用途:反编译子进程(jadx)的热路径——只反编译目标类时避免喂全量 APK。

import (
	"fmt"
	"sort"
)

// insnUnits 指令长度表(16-bit unit 数)。
// 与 findrefs.OpcodeOplens 同源生成(ASC asc_core/models/dvm_opcode.py);
// dex 包不可反向 import findrefs(循环依赖),故此处复制。
// 0 = 非法操作码,扫描时按 1 处理避免死循环。
var insnUnits = [256]byte{
	1, 1, 2, 3, 1, 2, 3, 1, 2, 3, 1, 1, 1, 1, 1, 1,
	1, 1, 1, 2, 3, 2, 2, 3, 5, 2, 2, 3, 2, 1, 1, 2,
	2, 1, 2, 2, 3, 3, 3, 1, 1, 2, 3, 3, 3, 2, 2, 2,
	2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 0, 0,
	0, 0, 0, 0, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
	2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
	2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 3, 3,
	3, 3, 3, 0, 3, 3, 3, 3, 3, 0, 0, 1, 1, 1, 1, 1,
	1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1,
	2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
	2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
	1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1,
	1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1,
	2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
	2, 2, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 4, 4, 3, 3, 2, 2,
}

const noIndex = 0xffffffff

func miniULEB(n uint32) []byte {
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

func miniSLEB(v int32) []byte {
	var out []byte
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if (v == 0 && b&0x80 == 0) || (v == -1 && b&0x80 != 0) {
			return append(out, b)
		}
		out = append(out, b|0x80)
	}
}

// ExtractClasses 重组仅包含指定类的最小 DEX(dalvik 描述符形态)。
// 类间外部引用保留为索引(不必抽取被引用类)。任何不支持的结构返回错误。
func (d *DEX) ExtractClasses(classDescs ...string) (out []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, fmt.Errorf("minidex: malformed source dex: %v", r)
		}
	}()
	if len(classDescs) == 0 {
		return nil, fmt.Errorf("minidex: no classes requested")
	}
	var classes []*Class
	for _, desc := range classDescs {
		c := d.GetClassByName(desc)
		if c == nil {
			return nil, fmt.Errorf("minidex: class %s not found", desc)
		}
		classes = append(classes, c)
	}

	e := &minidexBuilder{
		d:              d,
		strs:           map[uint32]bool{},
		types:          map[uint32]bool{},
		protos:         map[uint32]bool{},
		fields:         map[uint32]mdField{},
		meths:          map[uint32]mdMethod{},
		codeByOff:      map[uint32]*mdCode{},
		annoDirMap:     map[int]uint32{},
		annoSetMap:     map[int]uint32{},
		annoItemMap:    map[int]uint32{},
		annoRefListMap: map[int]uint32{},
		annoStats:      map[uint16]*annoStat{},
		debugMap:       map[int]uint32{},
	}
	// ── 收集:遍历类定义与指令,登记全部被引用的索引条目 ──
	for _, c := range classes {
		if err := e.collectClass(c); err != nil {
			return nil, err
		}
	}
	// ── 排序并分配新索引 ──
	if err := e.assignIndexes(); err != nil {
		return nil, err
	}
	// ── 指令重映射(需新索引表)──
	for _, ci := range e.codeItems {
		if _, err := e.scanOrPatchInsns(ci.insns, true); err != nil {
			return nil, err
		}
	}
	// ── 布局输出 ──
	return e.layout(classes)
}

// ────────────────────────── 收集阶段 ──────────────────────────

type mdField struct{ class, typ, name uint32 } // 旧 field_ids 条目内容
type mdMethod struct{ class, name, proto uint32 }
type mdCode struct {
	registers, ins, outs, triesSize uint16
	insns                           []byte
	tries                           []TryItem
	handlers                        *ExceptionTable
	debugOff                        uint32 // 旧 debug_info 偏移(0 = 无)
}

type minidexBuilder struct {
	d *DEX

	strs   map[uint32]bool // 旧 string idx
	types  map[uint32]bool
	protos map[uint32]bool
	fields map[uint32]mdField
	meths  map[uint32]mdMethod

	codeItems []*mdCode
	codeByOff map[uint32]*mdCode

	// 注解结构 memo(旧偏移 → 新偏移)与统计
	annoDirMap       map[int]uint32
	annoSetMap       map[int]uint32
	annoItemMap      map[int]uint32
	annoRefListMap   map[int]uint32
	firstDirOff      uint32
	firstAnnoSetOff  uint32
	firstAnnoItemOff uint32
	firstRefListOff  uint32
	annoDirCount     uint32
	annoSetCount     uint32
	annoItemCount    uint32
	refListCount     uint32
	debugMap         map[int]uint32
	debugCount       uint32
	firstDebugOff    uint32
	annoStats        map[uint16]*annoStat

	// 排序产物(assignIndexes 填充,layout 消费)
	newStrings    []string
	newStringOlds []uint32 // 每个新 string 槽位的原始 idx(原样拷贝 string_data)
	sortedTypes   []uint32
	sortedProtos  []uint32
	sortedFields  []uint32
	sortedMethods []uint32

	strMap   map[uint32]uint32
	typeMap  map[uint32]uint32
	protoMap map[uint32]uint32
	fieldMap map[uint32]uint32
	methMap  map[uint32]uint32
}

// annoStat map_list 注解类条目统计(type → 数量 + 首偏移)。
type annoStat struct {
	count uint32
	first uint32
}

func (e *minidexBuilder) noteAnno(typ uint16, off uint32) {
	st, ok := e.annoStats[typ]
	if !ok {
		st = &annoStat{}
		e.annoStats[typ] = st
	}
	if st.first == 0 {
		st.first = off
	}
	st.count++
}

func (e *minidexBuilder) nStr(old uint32) uint32  { return e.strMap[old] }
func (e *minidexBuilder) nType(old uint32) uint32 { return e.typeMap[old] }

func (e *minidexBuilder) addStr(old uint32) {
	if old != noIndex {
		e.strs[old] = true
	}
}

func (e *minidexBuilder) addType(old uint32) {
	if old == noIndex || e.types[old] {
		return
	}
	e.types[old] = true
	e.addStr(e.d.typeStrIdx(old))
}

func (e *minidexBuilder) addProto(old uint32) {
	if e.protos[old] {
		return
	}
	e.protos[old] = true
	p := e.d.GetPrototype(old)
	e.addStr(p.ShortyIdx)
	e.addType(p.ReturnTypeIdx)
	for _, t := range e.d.protoParamTypeIdxs(old) {
		e.addType(t)
	}
}

func (e *minidexBuilder) addField(old uint32) {
	if _, dup := e.fields[old]; dup {
		return
	}
	idOff := e.d.hdr.Fields.Off + old*8
	ref := mdField{
		class: uint32(e.d.u16(idOff)),
		typ:   uint32(e.d.u16(idOff + 2)),
		name:  e.d.u32(idOff + 4),
	}
	e.fields[old] = ref
	e.addType(ref.class)
	e.addType(ref.typ)
	e.addStr(ref.name)
}

func (e *minidexBuilder) addMethod(old uint32) {
	if _, dup := e.meths[old]; dup {
		return
	}
	idOff := e.d.hdr.Methods.Off + old*8
	ref := mdMethod{
		class: uint32(e.d.u16(idOff)),
		proto: uint32(e.d.u16(idOff + 2)),
		name:  e.d.u32(idOff + 4),
	}
	e.meths[old] = ref
	e.addType(ref.class)
	e.addProto(ref.proto)
	e.addStr(ref.name)
}

// typeStrIdx type_ids 条目 → 描述符字符串索引。
func (d *DEX) typeStrIdx(typeIdx uint32) uint32 {
	return d.u32(d.hdr.Types.Off + typeIdx*4)
}

// protoParamTypeIdxs 原型参数表(type_list)的旧类型索引序列。
func (d *DEX) protoParamTypeIdxs(protoIdx uint32) []uint32 {
	paramOff := d.u32(d.hdr.Prototypes.Off + protoIdx*12 + 8)
	if paramOff == 0 {
		return nil
	}
	n := int(d.u32(paramOff))
	out := make([]uint32, n)
	for i := 0; i < n; i++ {
		out[i] = uint32(d.u16(paramOff + 4 + uint32(i*2)))
	}
	return out
}

// cdRef class_data_item 中的字段条目(原索引形态)。
type cdRef struct{ idx, access uint32 }

// cdMethod class_data_item 中的方法条目。
// newCodeOff 布局期回填的新 code_off(writeClassData 消费)。
type cdMethod struct {
	idx, access, codeOff uint32
	newCodeOff           uint32
}

type classDataRaw struct {
	statics, instances []cdRef
	directs, virtuals  []cdMethod
}

func (d *DEX) parseClassDataRaw(off uint32) classDataRaw {
	var cd classDataRaw
	pos := int(off)
	readU := func() uint32 {
		v, n := d.readUlebBounded(pos)
		pos += n
		return v
	}
	nStatic, nInst := readU(), readU()
	nDirect, nVirtual := readU(), readU()
	prev := uint32(0)
	for i := uint32(0); i < nStatic; i++ {
		prev += readU()
		cd.statics = append(cd.statics, cdRef{idx: prev, access: readU()})
	}
	prev = 0
	for i := uint32(0); i < nInst; i++ {
		prev += readU()
		cd.instances = append(cd.instances, cdRef{idx: prev, access: readU()})
	}
	prev = 0
	for i := uint32(0); i < nDirect; i++ {
		prev += readU()
		cd.directs = append(cd.directs, cdMethod{idx: prev, access: readU(), codeOff: readU()})
	}
	prev = 0
	for i := uint32(0); i < nVirtual; i++ {
		prev += readU()
		cd.virtuals = append(cd.virtuals, cdMethod{idx: prev, access: readU(), codeOff: readU()})
	}
	return cd
}

// collectClass 登记单个类的全部引用(类元信息 + 字段 + 方法 + code)。
func (e *minidexBuilder) collectClass(c *Class) error {
	d := e.d
	base := c.classDefOff
	e.addType(c.ClassIdx)
	e.addType(d.u32(base + 8)) // superclass(NO_INDEX 安全)
	e.addStr(d.u32(base + 16)) // source_file
	if ifOff := d.u32(base + 12); ifOff != 0 {
		n := int(d.u32(ifOff))
		for i := 0; i < n; i++ {
			e.addType(uint32(d.u16(ifOff + 4 + uint32(i*2))))
		}
	}
	if dirOff := d.u32(base + 20); dirOff != 0 {
		if err := e.collectAnnotationsDir(int(dirOff)); err != nil {
			return err
		}
	}
	cd := d.parseClassDataRaw(d.u32(base + 24))
	for _, f := range cd.statics {
		e.addField(f.idx)
	}
	for _, f := range cd.instances {
		e.addField(f.idx)
	}
	collect := func(ms []cdMethod) error {
		for _, m := range ms {
			e.addMethod(m.idx)
			if m.codeOff != 0 {
				if err := e.collectCode(m.codeOff); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := collect(cd.directs); err != nil {
		return err
	}
	return collect(cd.virtuals)
}

// collectCode 登记一个 code_item 的指令索引引用与异常表类型。
func (e *minidexBuilder) collectCode(codeOff uint32) error {
	d := e.d
	insns := append([]byte{}, d.buf[codeOff+16:codeOff+16+d.u32(codeOff+12)*2]...)
	if _, err := e.scanOrPatchInsns(insns, false); err != nil {
		return err
	}
	ci := &mdCode{
		registers: d.u16(codeOff),
		ins:       d.u16(codeOff + 2),
		outs:      d.u16(codeOff + 4),
		triesSize: d.u16(codeOff + 6),
		insns:     insns,
		debugOff:  d.u32(codeOff + 8),
	}
	if ci.debugOff != 0 {
		if err := e.collectDebugInfoRefs(int(ci.debugOff)); err != nil {
			return err
		}
	}
	if ci.triesSize > 0 {
		et := d.parseExceptionTable(codeOff)
		if et == nil {
			return fmt.Errorf("minidex: malformed exception table at %d", codeOff)
		}
		ci.tries = et.Tries
		ci.handlers = et
		for _, ti := range et.Tries {
			h := et.Handler(ti.HandlerOff)
			if h == nil {
				continue
			}
			for _, ta := range h.Typed {
				e.addType(ta.TypeIdx)
			}
		}
	}
	e.codeByOff[codeOff] = ci
	e.codeItems = append(e.codeItems, ci)
	return nil
}

// encodedArraySpanInfo encoded_array 原样拷贝判定。
type encodedArraySpanInfo struct {
	length    int
	indexFree bool // 不含 string/type/field/method/enum 等索引型值
}

// encodedArraySpan 解析 encoded_array 的字节跨度与索引依赖。
// 索引型值经重映射会变化,无法原样拷贝;注解值含 type/name 索引按依赖算。
func (d *DEX) encodedArraySpan(pos int) (encodedArraySpanInfo, bool) {
	info := encodedArraySpanInfo{indexFree: true}
	origin := pos
	count, n := d.readUlebBounded(pos)
	if n == 0 || count > 0xffff {
		return info, false
	}
	pos += n
	for i := uint32(0); i < count; i++ {
		next, free, ok := d.walkEncodedValue(pos)
		if !ok {
			return info, false
		}
		info.indexFree = info.indexFree && free
		pos = next
	}
	info.length = pos - origin
	return info, true
}

// walkEncodedValue 解析一个 encoded_value:返回下一位置与索引依赖。
func (d *DEX) walkEncodedValue(pos int) (int, bool, bool) {
	if pos >= len(d.buf) {
		return 0, false, false
	}
	header := d.buf[pos]
	vt := header & 0x1f
	arg := int(header >> 5)
	pos++
	switch vt {
	case 0x1e, 0x1f: // null / boolean:零字节载荷
		return pos, true, true
	case 0x1c: // array:value_arg 为元素个数
		free := true
		for i := 0; i < arg; i++ {
			next, f, ok := d.walkEncodedValue(pos)
			if !ok {
				return 0, false, false
			}
			free = free && f
			pos = next
		}
		return pos, free, true
	case 0x1d: // annotation:含 type/name 索引
		_, n1 := d.readUlebBounded(pos)
		if n1 == 0 {
			return 0, false, false
		}
		pos += n1
		sz, n2 := d.readUlebBounded(pos)
		if n2 == 0 {
			return 0, false, false
		}
		pos += n2
		for i := uint32(0); i < sz; i++ {
			_, nn := d.readUlebBounded(pos)
			if nn == 0 {
				return 0, false, false
			}
			pos += nn
			next, _, ok := d.walkEncodedValue(pos)
			if !ok {
				return 0, false, false
			}
			pos = next
		}
		return pos, false, true
	default:
		if pos+arg+1 > len(d.buf) {
			return 0, false, false
		}
		// 0x15 method_type / 0x16 method_handle / 0x17 string / 0x18 type /
		// 0x19 field / 0x1a method / 0x1b enum 均携带索引
		return pos + arg + 1, vt < 0x15 || vt > 0x1b, true
	}
}

// scanOrPatchInsns 扫描(收集引用 + 不支持操作码检测)或原位重写指令索引。
func (e *minidexBuilder) scanOrPatchInsns(insns []byte, patch bool) ([]byte, error) {
	units := len(insns) / 2
	get := func(i int) uint16 { return uint16(insns[2*i]) | uint16(insns[2*i+1])<<8 }
	set := func(i int, v uint16) { insns[2*i] = byte(v); insns[2*i+1] = byte(v >> 8) }
	apply := func(unitPos int, m map[uint32]uint32, kind string, old uint32) error {
		if !patch {
			switch kind {
			case "string":
				e.addStr(old)
			case "type":
				e.addType(old)
			case "field":
				e.addField(old)
			case "method":
				e.addMethod(old)
			case "proto":
				e.addProto(old)
			}
			return nil
		}
		nv, ok := m[old]
		if !ok {
			return fmt.Errorf("minidex: unmapped %s index %d", kind, old)
		}
		set(unitPos, uint16(nv))
		return nil
	}
	pc := 0
	for pc < units {
		unit := get(pc)
		op := byte(unit & 0xff)
		if op == 0x00 && unit>>8 != 0 { // payload 数据:不含索引
			pc += payloadUnitsMini(insns, pc)
			continue
		}
		size := int(insnUnits[op])
		if size <= 0 {
			size = 1
		}
		var err error
		switch {
		case op == 0x1a: // const-string
			err = apply(pc+1, e.strMap, "string", uint32(get(pc+1)))
		case op == 0x1b: // const-string/jumbo(32 位索引)
			old := uint32(get(pc+1)) | uint32(get(pc+2))<<16
			if patch {
				nv, ok := e.strMap[old]
				if !ok {
					err = fmt.Errorf("minidex: unmapped string index %d", old)
					break
				}
				set(pc+1, uint16(nv))
				set(pc+2, uint16(nv>>16))
			} else {
				e.addStr(old)
			}
		case op == 0x1c || op == 0x1f || op == 0x20 || op == 0x22 || op == 0x23 || op == 0x24 || op == 0x25:
			err = apply(pc+1, e.typeMap, "type", uint32(get(pc+1)))
		case op >= 0x52 && op <= 0x6d: // iget/iput/sget/sput
			err = apply(pc+1, e.fieldMap, "field", uint32(get(pc+1)))
		case (op >= 0x6e && op <= 0x72) || (op >= 0x74 && op <= 0x78): // invoke 族
			err = apply(pc+1, e.methMap, "method", uint32(get(pc+1)))
		case op == 0xfa || op == 0xfb: // invoke-polymorphic:method + proto
			if err = apply(pc+1, e.methMap, "method", uint32(get(pc+1))); err == nil {
				err = apply(pc+3, e.protoMap, "proto", uint32(get(pc+3)))
			}
		case op == 0xff: // const-method-type
			err = apply(pc+1, e.protoMap, "proto", uint32(get(pc+1)))
		case op == 0xfc || op == 0xfd || op == 0xfe: // call site / method handle 引用
			return nil, fmt.Errorf("minidex: unsupported opcode 0x%02x (call site / method handle)", op)
		}
		if err != nil {
			return nil, err
		}
		pc += size
	}
	return insns, nil
}

// payloadUnitsMini payload 伪指令总长(16-bit unit 数)。
func payloadUnitsMini(insns []byte, pc int) int {
	if (pc+1)*2+2 > len(insns) {
		return 1
	}
	unit1 := uint16(insns[(pc+1)*2]) | uint16(insns[(pc+1)*2+1])<<8
	switch uint16(insns[pc*2+1]) {
	case 0x01: // packed-switch-payload
		return int(unit1)*2 + 4
	case 0x02: // sparse-switch-payload
		return int(unit1)*4 + 2
	default: // fill-array-data-payload
		width := int(unit1)
		if (pc+4)*2+2 > len(insns) {
			return 1
		}
		lo := uint32(insns[(pc+2)*2]) | uint32(insns[(pc+2)*2+1])<<8
		hi := uint32(insns[(pc+3)*2]) | uint32(insns[(pc+3)*2+1])<<8
		size := int(hi<<16 | lo)
		return (size*width+1)/2 + 4
	}
}

// ────────────────────────── 索引分配 ──────────────────────────

// assignIndexes 按规范排序各索引区并建立旧→新映射:
// string 按内容序;type 按 string 索引;proto 按(返回类型, 参数, shorty);
// field 按(class, name, type);method 按(class, name, proto)。
func (e *minidexBuilder) assignIndexes() error {
	// string:按解码内容排序(UTF-16 码点序的等价近似)
	contents := make([]string, 0, len(e.strs))
	seenContent := map[string]bool{}
	for old := range e.strs {
		c := e.d.GetString(old)
		if !seenContent[c] {
			seenContent[c] = true
			contents = append(contents, c)
		}
	}
	sort.Strings(contents)
	contentIdx := map[string]uint32{}
	for i, c := range contents {
		contentIdx[c] = uint32(i)
	}
	e.strMap = map[uint32]uint32{}
	// 每个新 string 槽位选一个原始 idx(原样拷贝 string_data 字节)
	oldByContent := map[string]uint32{}
	for old := range e.strs {
		oldByContent[e.d.GetString(old)] = old
	}
	for _, c := range contents {
		e.newStringOlds = append(e.newStringOlds, oldByContent[c])
	}
	for old := range e.strs {
		e.strMap[old] = contentIdx[e.d.GetString(old)]
	}
	e.newStrings = contents
	newStrOf := func(old uint32) uint32 { return e.strMap[old] }

	// type
	typeOld := make([]uint32, 0, len(e.types))
	for t := range e.types {
		typeOld = append(typeOld, t)
	}
	sort.Slice(typeOld, func(i, j int) bool {
		return newStrOf(e.d.typeStrIdx(typeOld[i])) < newStrOf(e.d.typeStrIdx(typeOld[j]))
	})
	e.typeMap = map[uint32]uint32{}
	for newIdx, old := range typeOld {
		e.typeMap[old] = uint32(newIdx)
	}
	e.sortedTypes = typeOld
	newTypeOf := func(old uint32) uint32 { return e.typeMap[old] }

	// proto:(返回类型, 参数序列, shorty)
	type protoKey struct {
		ret    uint32
		params string
		shorty uint32
	}
	protoOld := make([]uint32, 0, len(e.protos))
	keys := map[uint32]protoKey{}
	for p := range e.protos {
		protoOld = append(protoOld, p)
		pk := protoKey{ret: newTypeOf(e.d.GetPrototype(p).ReturnTypeIdx)}
		for _, t := range e.d.protoParamTypeIdxs(p) {
			pk.params += string(rune(newTypeOf(t))) + ","
		}
		pk.shorty = newStrOf(e.d.GetPrototype(p).ShortyIdx)
		keys[p] = pk
	}
	sort.Slice(protoOld, func(i, j int) bool {
		a, b := keys[protoOld[i]], keys[protoOld[j]]
		if a.ret != b.ret {
			return a.ret < b.ret
		}
		if a.params != b.params {
			return a.params < b.params
		}
		return a.shorty < b.shorty
	})
	e.protoMap = map[uint32]uint32{}
	for newIdx, old := range protoOld {
		e.protoMap[old] = uint32(newIdx)
	}
	e.sortedProtos = protoOld

	// field:(class, name, type)
	fieldOld := make([]uint32, 0, len(e.fields))
	for f := range e.fields {
		fieldOld = append(fieldOld, f)
	}
	refOf := func(old uint32) mdField { return e.fields[old] }
	sort.Slice(fieldOld, func(i, j int) bool {
		a, b := refOf(fieldOld[i]), refOf(fieldOld[j])
		ac, bc := newTypeOf(a.class), newTypeOf(b.class)
		if ac != bc {
			return ac < bc
		}
		an, bn := newStrOf(a.name), newStrOf(b.name)
		if an != bn {
			return an < bn
		}
		return newTypeOf(a.typ) < newTypeOf(b.typ)
	})
	e.fieldMap = map[uint32]uint32{}
	for newIdx, old := range fieldOld {
		e.fieldMap[old] = uint32(newIdx)
	}
	e.sortedFields = fieldOld

	// method:(class, name, proto)
	methOld := make([]uint32, 0, len(e.meths))
	for m := range e.meths {
		methOld = append(methOld, m)
	}
	mrefOf := func(old uint32) mdMethod { return e.meths[old] }
	sort.Slice(methOld, func(i, j int) bool {
		a, b := mrefOf(methOld[i]), mrefOf(methOld[j])
		ac, bc := newTypeOf(a.class), newTypeOf(b.class)
		if ac != bc {
			return ac < bc
		}
		an, bn := newStrOf(a.name), newStrOf(b.name)
		if an != bn {
			return an < bn
		}
		return e.protoMap[a.proto] < e.protoMap[b.proto]
	})
	e.methMap = map[uint32]uint32{}
	for newIdx, old := range methOld {
		e.methMap[old] = uint32(newIdx)
	}
	e.sortedMethods = methOld
	return nil
}
