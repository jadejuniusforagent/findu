// Package dex — DEX 结构解析,移植自 ASC asc_core/utils/tinydex.py。
// 语义对齐原版:惰性解析 + 缓存,字符串按 UTF-8 replace 解码。
package dex

import (
	"fmt"
	"sync"
	"unicode/utf8"

	"finduai/internal/apkengine"
)

// IdTable = (off, size),对齐原版 DEXHeader 的二元组。
type IdTable struct {
	Off  uint32
	Size uint32
}

type Header struct {
	Strings    IdTable // 0x3C off / 0x38 size
	Types      IdTable // 0x44 / 0x40
	Prototypes IdTable // 0x4C / 0x48
	Fields     IdTable // 0x54 / 0x50
	Methods    IdTable // 0x5C / 0x58
	Classes    IdTable // 0x64 / 0x60
	MapOff     uint32  // 0x34
}

type PrimitiveType int

const (
	VoidT PrimitiveType = iota
	Boolean
	Byte
	Short
	Char
	Int
	Long
	Float
	Double
)

type TypeKind int

const (
	KindPrimitive TypeKind = iota + 1
	KindClass
	KindArray
)

// Type 对齐原版 Type,描述符在构造时即时解析(原版惰性,结果等价)。
type Type struct {
	Descriptor string
	Dim        int
	Kind       TypeKind
	PrimValue  PrimitiveType // KindPrimitive 时有效
	ClassValue string        // KindClass 时有效
	Underlying *Type         // KindArray 时有效
}

func newType(descriptor string) *Type {
	t := &Type{Descriptor: descriptor}
	desc := descriptor
	for len(desc) > 0 && desc[0] == '[' {
		t.Dim++
		desc = desc[1:]
	}
	if t.Dim > 0 {
		t.Kind = KindArray
		t.Underlying = newType(desc)
		return t
	}
	switch desc {
	case "V":
		t.Kind, t.PrimValue = KindPrimitive, VoidT
	case "Z":
		t.Kind, t.PrimValue = KindPrimitive, Boolean
	case "B":
		t.Kind, t.PrimValue = KindPrimitive, Byte
	case "S":
		t.Kind, t.PrimValue = KindPrimitive, Short
	case "C":
		t.Kind, t.PrimValue = KindPrimitive, Char
	case "I":
		t.Kind, t.PrimValue = KindPrimitive, Int
	case "J":
		t.Kind, t.PrimValue = KindPrimitive, Long
	case "F":
		t.Kind, t.PrimValue = KindPrimitive, Float
	case "D":
		t.Kind, t.PrimValue = KindPrimitive, Double
	default:
		t.Kind = KindClass
		t.ClassValue = desc
	}
	return t
}

type Prototype struct {
	ShortyIdx      uint32
	ReturnTypeIdx  uint32
	ParametersOff  uint32
	dex            *DEX
	paramsOnce     sync.Once
	parametersType []*Type
}

// Parameters 对齐原版 parameters_type:空 type_list 返回空切片。
func (p *Prototype) Parameters() []*Type {
	p.paramsOnce.Do(func() {
		if p.ParametersOff == 0 || p.dex == nil {
			return
		}
		size := p.dex.u32(p.ParametersOff)
		off := p.ParametersOff + 4
		params := make([]*Type, 0, size)
		for i := uint32(0); i < size; i++ {
			params = append(params, p.dex.GetType(uint32(p.dex.u16(off+i*2))))
		}
		p.parametersType = params
	})
	return p.parametersType
}

func (p *Prototype) ReturnType() *Type { return p.dex.GetType(p.ReturnTypeIdx) }

type Field struct {
	Index       uint32
	ClassIdx    uint32
	TypeIdx     uint32
	NameIdx     uint32
	AccessFlags uint32
	IsStatic    bool
	dex         *DEX
	clsOnce     sync.Once
	cls         string
}

// Cls 对齐原版 cls.fullname(宿主类描述符)。
func (f *Field) Cls() string {
	f.clsOnce.Do(func() { f.cls = f.dex.GetType(f.ClassIdx).Descriptor })
	return f.cls
}

func (f *Field) Type() *Type  { return f.dex.GetType(f.TypeIdx) }
func (f *Field) Name() string { return f.dex.GetString(f.NameIdx) }

type Method struct {
	Index       uint32
	ClassIdx    uint32
	ProtoIdx    uint32
	NameIdx     uint32
	AccessFlags uint32
	IsDirect    bool
	IsVirtual   bool
	codeOff     uint32
	dex         *DEX
	clsOnce     sync.Once
	cls         string
	protoOnce   sync.Once
	proto       *Prototype
	triesOnce   sync.Once
	tries       *ExceptionTable
}

func (m *Method) Cls() string {
	m.clsOnce.Do(func() { m.cls = m.dex.GetType(m.ClassIdx).Descriptor })
	return m.cls
}

func (m *Method) Name() string { return m.dex.GetString(m.NameIdx) }

func (m *Method) Prototype() *Prototype {
	m.protoOnce.Do(func() { m.proto = m.dex.GetPrototype(m.ProtoIdx) })
	return m.proto
}

func (m *Method) CodeOffset() uint32 { return m.codeOff }

func (m *Method) HasCode() bool { return m.codeOff != 0 }

// CodeHeader 对齐原版 bytecode() 解析的 code_item 头 + insns 原始字节。
type CodeHeader struct {
	RegistersSize uint16
	InsSize       uint16
	OutsSize      uint16
	TriesSize     uint16
	DebugInfoOff  uint32
	InsnsSize     uint32
	Insns         []byte
}

// Code 解析 code_item;code_off 为 0(无方法体)时返回 nil。
func (m *Method) Code() *CodeHeader {
	if m.codeOff == 0 {
		return nil
	}
	d := m.dex
	return &CodeHeader{
		RegistersSize: d.u16(m.codeOff),
		InsSize:       d.u16(m.codeOff + 2),
		OutsSize:      d.u16(m.codeOff + 4),
		TriesSize:     d.u16(m.codeOff + 6),
		DebugInfoOff:  d.u32(m.codeOff + 8),
		InsnsSize:     d.u32(m.codeOff + 12),
		Insns:         d.buf[m.codeOff+16 : m.codeOff+16+d.u32(m.codeOff+12)*2],
	}
}

type Class struct {
	dex          *DEX
	ClassIdx     uint32
	Index        uint32
	classDefOff  uint32
	classDataOff uint32
	parseOnce    sync.Once
	methods      []*Method
	fields       []*Field
}

func (c *Class) Fullname() string { return c.dex.GetType(c.ClassIdx).Descriptor }

// Super 返回父类描述符(如 "Ljava/lang/Object;")。
func (c *Class) Super() string {
	if c.classDefOff == 0 {
		return "Ljava/lang/Object;"
	}
	superIdx := c.dex.u32(c.classDefOff + 8)
	return c.dex.GetType(superIdx).Descriptor
}

// ClassAccessFlags 返回类级别的 access_flags(class_def_item offset 4)。
func (c *Class) ClassAccessFlags() uint32 {
	if c.classDefOff == 0 {
		return 0
	}
	return c.dex.u32(c.classDefOff + 4)
}

// parseClassData 对齐原版 _parse_class_data 的四段顺序与差分索引语义。
func (c *Class) parseClassData() {
	if c.classDataOff == 0 {
		return
	}
	d := c.dex
	pos := int(c.classDataOff)
	staticFieldsSize, n := apkengine.ReadUleb128(d.buf, pos)
	pos += n
	instanceFieldsSize, n := apkengine.ReadUleb128(d.buf, pos)
	pos += n
	directMethodsSize, n := apkengine.ReadUleb128(d.buf, pos)
	pos += n
	virtualMethodsSize, n := apkengine.ReadUleb128(d.buf, pos)
	pos += n

	fieldIdx := uint32(0)
	for i := uint32(0); i < staticFieldsSize; i++ {
		diff, n := apkengine.ReadUleb128(d.buf, pos)
		pos += n
		fieldIdx += diff
		accessFlags, n := apkengine.ReadUleb128(d.buf, pos)
		pos += n
		f := d.GetField(fieldIdx)
		f.AccessFlags = accessFlags
		f.IsStatic = true
		c.fields = append(c.fields, f)
	}
	fieldIdx = 0
	for i := uint32(0); i < instanceFieldsSize; i++ {
		diff, n := apkengine.ReadUleb128(d.buf, pos)
		pos += n
		fieldIdx += diff
		accessFlags, n := apkengine.ReadUleb128(d.buf, pos)
		pos += n
		f := d.GetField(fieldIdx)
		f.AccessFlags = accessFlags
		f.IsStatic = false
		c.fields = append(c.fields, f)
	}

	methodIdx := uint32(0)
	for i := uint32(0); i < directMethodsSize; i++ {
		diff, n := apkengine.ReadUleb128(d.buf, pos)
		pos += n
		methodIdx += diff
		accessFlags, n := apkengine.ReadUleb128(d.buf, pos)
		pos += n
		codeOff, n := apkengine.ReadUleb128(d.buf, pos)
		pos += n
		m := d.GetMethod(methodIdx)
		m.codeOff = codeOff
		m.AccessFlags = accessFlags
		m.IsDirect = true
		c.methods = append(c.methods, m)
	}
	methodIdx = 0
	for i := uint32(0); i < virtualMethodsSize; i++ {
		diff, n := apkengine.ReadUleb128(d.buf, pos)
		pos += n
		methodIdx += diff
		accessFlags, n := apkengine.ReadUleb128(d.buf, pos)
		pos += n
		codeOff, n := apkengine.ReadUleb128(d.buf, pos)
		pos += n
		m := d.GetMethod(methodIdx)
		m.codeOff = codeOff
		m.AccessFlags = accessFlags
		m.IsDirect = false
		m.IsVirtual = true
		c.methods = append(c.methods, m)
	}
}

func (c *Class) Methods() []*Method {
	c.parseOnce.Do(c.parseClassData)
	return c.methods
}

func (c *Class) Fields() []*Field {
	c.parseOnce.Do(c.parseClassData)
	return c.fields
}

type DEX struct {
	buf  []byte
	name string
	hdr  Header

	mu        sync.Mutex
	strings   map[uint32]string
	types     map[uint32]*Type
	protos    map[uint32]*Prototype
	methods   map[uint32]*Method
	fields    map[uint32]*Field
	classes   map[uint32]*Class
	clsByName map[string]*Class

	mhOnce        sync.Once
	methodHandles []*MethodHandleItem
	csOnce        sync.Once
	callSites     []*CallSite
}

// Parse 顶层入口:校验 magic,惰性解析阶段的越界 panic 在此转 error。
func Parse(buf []byte, name string) (dex *DEX, err error) {
	if len(buf) < 0x70 {
		return nil, fmt.Errorf("dex %s: too short: %d", name, len(buf))
	}
	if string(buf[:3]) != "dex" {
		return nil, fmt.Errorf("dex %s: bad magic %q", name, buf[:8])
	}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("dex %s: malformed header: %v", name, r)
		}
	}()
	d := &DEX{
		buf:       buf,
		name:      name,
		strings:   make(map[uint32]string),
		types:     make(map[uint32]*Type),
		protos:    make(map[uint32]*Prototype),
		methods:   make(map[uint32]*Method),
		fields:    make(map[uint32]*Field),
		classes:   make(map[uint32]*Class),
		clsByName: make(map[string]*Class),
	}
	d.hdr = Header{
		Strings:    IdTable{Off: d.u32(0x3C), Size: d.u32(0x38)},
		Types:      IdTable{Off: d.u32(0x44), Size: d.u32(0x40)},
		Prototypes: IdTable{Off: d.u32(0x4C), Size: d.u32(0x48)},
		Fields:     IdTable{Off: d.u32(0x54), Size: d.u32(0x50)},
		Methods:    IdTable{Off: d.u32(0x5C), Size: d.u32(0x58)},
		Classes:    IdTable{Off: d.u32(0x64), Size: d.u32(0x60)},
		MapOff:     d.u32(0x34),
	}
	return d, nil
}

func (d *DEX) Name() string   { return d.name }
func (d *DEX) Header() Header { return d.hdr }
func (d *DEX) Buf() []byte    { return d.buf }

func (d *DEX) u32(off uint32) uint32 {
	b := d.buf[off : off+4]
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

func (d *DEX) u16(off uint32) uint16 {
	b := d.buf[off : off+2]
	return uint16(b[0]) | uint16(b[1])<<8
}

// decodeUTF8Replace 对齐 Python bytes.decode('utf-8', errors='replace'):
// 非法字节逐个以 U+FFFD 替代。
func decodeUTF8Replace(b []byte) string {
	for i := 0; i < len(b); {
		r, size := utf8.DecodeRune(b[i:])
		if r == utf8.RuneError && size == 1 {
			goto slow
		}
		i += size
	}
	return string(b)
slow:
	var sb []byte
	for i := 0; i < len(b); {
		r, size := utf8.DecodeRune(b[i:])
		if r == utf8.RuneError && size == 1 {
			sb = append(sb, 0xEF, 0xBF, 0xBD)
			i++
			continue
		}
		sb = append(sb, b[i:i+size]...)
		i += size
	}
	return string(sb)
}

// GetString 对齐原版 get_string:uleb utf16_size 以降的 utf16_size 字节按
// UTF-8 replace 解码(原版如此,不剥 NUL 终止符,语义保持一致)。
func (d *DEX) GetString(idx uint32) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.getStringLocked(idx)
}

func (d *DEX) getStringLocked(idx uint32) string {
	if s, ok := d.strings[idx]; ok {
		return s
	}
	stringOff := d.u32(d.hdr.Strings.Off + idx*4)
	utf16Size, c := apkengine.ReadUleb128(d.buf, int(stringOff))
	dataStart := int(stringOff) + c
	end := dataStart + int(utf16Size)
	s := decodeUTF8Replace(d.buf[dataStart:end])
	d.strings[idx] = s
	return s
}

func (d *DEX) NumStrings() uint32 { return d.hdr.Strings.Size }

func (d *DEX) GetType(idx uint32) *Type {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.getTypeLocked(idx)
}

func (d *DEX) getTypeLocked(idx uint32) *Type {
	if t, ok := d.types[idx]; ok {
		return t
	}
	strIdx := d.u32(d.hdr.Types.Off + idx*4)
	t := newType(d.getStringLocked(strIdx))
	d.types[idx] = t
	return t
}

func (d *DEX) NumTypes() uint32 { return d.hdr.Types.Size }

func (d *DEX) GetPrototype(idx uint32) *Prototype {
	d.mu.Lock()
	defer d.mu.Unlock()
	if p, ok := d.protos[idx]; ok {
		return p
	}
	off := d.hdr.Prototypes.Off + idx*12
	p := &Prototype{
		ShortyIdx:     d.u32(off),
		ReturnTypeIdx: d.u32(off + 4),
		ParametersOff: d.u32(off + 8),
		dex:           d,
	}
	d.protos[idx] = p
	return p
}

func (d *DEX) GetMethod(idx uint32) *Method {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.getMethodLocked(idx)
}

func (d *DEX) getMethodLocked(idx uint32) *Method {
	if m, ok := d.methods[idx]; ok {
		return m
	}
	off := d.hdr.Methods.Off + idx*8
	m := &Method{
		Index:    idx,
		ClassIdx: uint32(d.u16(off)),
		ProtoIdx: uint32(d.u16(off + 2)),
		NameIdx:  d.u32(off + 4),
		dex:      d,
	}
	d.methods[idx] = m
	return m
}

func (d *DEX) NumMethods() uint32 { return d.hdr.Methods.Size }

func (d *DEX) GetField(idx uint32) *Field {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.getFieldLocked(idx)
}

func (d *DEX) getFieldLocked(idx uint32) *Field {
	if f, ok := d.fields[idx]; ok {
		return f
	}
	off := d.hdr.Fields.Off + idx*8
	f := &Field{
		Index:    idx,
		ClassIdx: uint32(d.u16(off)),
		TypeIdx:  uint32(d.u16(off + 2)),
		NameIdx:  d.u32(off + 4),
		dex:      d,
	}
	d.fields[idx] = f
	return f
}

func (d *DEX) NumFields() uint32 { return d.hdr.Fields.Size }

// GetClassByIdx 对齐原版 classes[idx](class_def 32 字节,class_data_off 在 +24)。
func (d *DEX) GetClassByIdx(idx uint32) *Class {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.getClassByIdxLocked(idx)
}

func (d *DEX) getClassByIdxLocked(idx uint32) *Class {
	if c, ok := d.classes[idx]; ok {
		return c
	}
	classDefOff := d.hdr.Classes.Off + idx*32
	c := &Class{
		dex:          d,
		ClassIdx:     d.u32(classDefOff),
		Index:        idx,
		classDefOff:  classDefOff,
		classDataOff: d.u32(classDefOff + 24),
	}
	d.classes[idx] = c
	return c
}

func (d *DEX) NumClasses() uint32 { return d.hdr.Classes.Size }

// GetClassByName 对齐原版 get_class:type_ids 二分(描述符即排序键),
// 再线性扫 class_defs 匹配 class_idx。
func (d *DEX) GetClassByName(fullname string) *Class {
	d.mu.Lock()
	defer d.mu.Unlock()
	if c, ok := d.clsByName[fullname]; ok {
		return c
	}
	if d.hdr.Types.Size == 0 {
		return nil
	}
	left, right := int64(0), int64(d.hdr.Types.Size)-1
	typeIdx := int64(-1)
	for left <= right {
		mid := (left + right) >> 1
		descIdx := d.u32(d.hdr.Types.Off + uint32(mid)*4)
		s := d.getStringLocked(descIdx)
		if s == fullname {
			typeIdx = mid
			break
		} else if s < fullname {
			left = mid + 1
		} else {
			right = mid - 1
		}
	}
	if typeIdx < 0 {
		return nil
	}
	for classIdx := uint32(0); classIdx < d.hdr.Classes.Size; classIdx++ {
		if d.u32(d.hdr.Classes.Off+classIdx*32) != uint32(typeIdx) {
			continue
		}
		c := d.getClassByIdxLocked(classIdx)
		d.clsByName[fullname] = c
		return c
	}
	return nil
}
