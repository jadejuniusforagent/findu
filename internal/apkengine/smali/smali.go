// Package smali — 最小 Dalvik 字节码 → smali 风格文本渲染器。
// 用途:反编译运行时(ASC/androguard)未部署时,代码工作台的方法级
// 兜底视图;纯 Go,零外部依赖。
// 覆盖全部标准指令格式(10x..51l、45cc/4rcc)与三种 payload 伪指令。
package smali

import (
	"fmt"
	"strings"

	"finduai/internal/apkengine/dex"
	"finduai/internal/apkengine/findrefs"
)

// format 指令格式(对齐 Dalvik 字节码文档命名)。
type format uint8

const (
	f10x  format = iota // op
	f10t                // op +AA
	f12x                // op vA, vB
	f11n                // op vA, #+B
	f11x                // op vAA
	f20t                // op +AAAA
	f22x                // op vAA, vBBBB
	f21t                // op vAA, +BBBB
	f21s                // op vAA, #+BBBB
	f21h                // op vAA, #+BBBB<<shift
	f21c                // op vAA, kind@BBBB
	f23x                // op vAA, vBB, vCC
	f22b                // op vAA, vBB, #+CC
	f22t                // op vA, vB, +CCCC
	f22s                // op vA, vB, #+CCCC
	f22c                // op vA, vB, kind@CCCC
	f30t                // op +AAAAAAAA
	f31i                // op vAA, #+BBBBBBBB
	f31t                // op vAA, +BBBBBBBB
	f31c                // op vAA, kind@BBBBBBBB
	f32x                // op vAAAA, vBBBB
	f35c                // op {vC..vG}, kind@BBBB
	f3rc                // op {vCCCC..vNNNN}, kind@BBBB
	f51l                // op vAA, #+BBBBBBBBBBBBBBBB
	f45cc               // op {vC..vG}, meth@BBBB, proto@HHHH
	f4rcc               // op {vCCCC..vNNNN}, meth@BBBB, proto@HHHH
)

// indexKind 操作码引用的索引类型。
type indexKind uint8

const (
	kindNone indexKind = iota
	kindString
	kindType
	kindField
	kindMeth
	kindProto
)

// formats: opcode → (格式, 索引类型)。默认零值 = f10x + none,只登记例外。
var formats = [256]struct {
	f format
	k indexKind
}{
	0x01: {f12x, kindNone}, 0x02: {f22x, kindNone}, 0x03: {f32x, kindNone},
	0x04: {f12x, kindNone}, 0x05: {f22x, kindNone}, 0x06: {f32x, kindNone},
	0x07: {f12x, kindNone}, 0x08: {f22x, kindNone}, 0x09: {f32x, kindNone},
	0x0a: {f11x, kindNone}, 0x0b: {f11x, kindNone}, 0x0c: {f11x, kindNone}, 0x0d: {f11x, kindNone},
	0x0f: {f11x, kindNone}, 0x10: {f11x, kindNone}, 0x11: {f11x, kindNone},
	0x12: {f11n, kindNone}, 0x13: {f21s, kindNone}, 0x14: {f31i, kindNone}, 0x15: {f21h, kindNone},
	0x16: {f21s, kindNone}, 0x17: {f31i, kindNone}, 0x18: {f51l, kindNone}, 0x19: {f21h, kindNone},
	0x1a: {f21c, kindString}, 0x1b: {f31c, kindString}, 0x1c: {f21c, kindType},
	0x1f: {f21c, kindType}, 0x20: {f22c, kindType}, 0x21: {f11x, kindNone},
	0x22: {f21c, kindType}, 0x23: {f22c, kindType},
	0x24: {f35c, kindType}, 0x25: {f3rc, kindType}, 0x26: {f31t, kindNone},
	0x28: {f10t, kindNone}, 0x29: {f20t, kindNone}, 0x2a: {f30t, kindNone},
	0x2b: {f31t, kindNone}, 0x2c: {f31t, kindNone},
	0x2d: {f23x, kindNone}, 0x2e: {f23x, kindNone}, 0x2f: {f23x, kindNone},
	0x30: {f23x, kindNone}, 0x31: {f23x, kindNone},
	0x32: {f22t, kindNone}, 0x33: {f22t, kindNone}, 0x34: {f22t, kindNone},
	0x35: {f22t, kindNone}, 0x36: {f22t, kindNone}, 0x37: {f22t, kindNone},
	0x38: {f21t, kindNone}, 0x39: {f21t, kindNone}, 0x3a: {f21t, kindNone},
	0x3b: {f21t, kindNone}, 0x3c: {f21t, kindNone}, 0x3d: {f21t, kindNone},
	0x44: {f23x, kindNone}, 0x45: {f23x, kindNone}, 0x46: {f23x, kindNone}, 0x47: {f23x, kindNone},
	0x48: {f23x, kindNone}, 0x49: {f23x, kindNone}, 0x4a: {f23x, kindNone}, 0x4b: {f23x, kindNone},
	0x4c: {f23x, kindNone}, 0x4d: {f23x, kindNone}, 0x4e: {f23x, kindNone}, 0x4f: {f23x, kindNone},
	0x50: {f23x, kindNone}, 0x51: {f23x, kindNone},
	0x52: {f22c, kindField}, 0x53: {f22c, kindField}, 0x54: {f22c, kindField}, 0x55: {f22c, kindField},
	0x56: {f22c, kindField}, 0x57: {f22c, kindField}, 0x58: {f22c, kindField}, 0x59: {f22c, kindField},
	0x5a: {f22c, kindField}, 0x5b: {f22c, kindField}, 0x5c: {f22c, kindField}, 0x5d: {f22c, kindField},
	0x5e: {f22c, kindField}, 0x5f: {f22c, kindField},
	0x60: {f21c, kindField}, 0x61: {f21c, kindField}, 0x62: {f21c, kindField}, 0x63: {f21c, kindField},
	0x64: {f21c, kindField}, 0x65: {f21c, kindField}, 0x66: {f21c, kindField}, 0x67: {f21c, kindField},
	0x68: {f21c, kindField}, 0x69: {f21c, kindField}, 0x6a: {f21c, kindField}, 0x6b: {f21c, kindField},
	0x6c: {f21c, kindField}, 0x6d: {f21c, kindField},
	0x6e: {f35c, kindMeth}, 0x6f: {f35c, kindMeth}, 0x70: {f35c, kindMeth},
	0x71: {f35c, kindMeth}, 0x72: {f35c, kindMeth},
	0x74: {f3rc, kindMeth}, 0x75: {f3rc, kindMeth}, 0x76: {f3rc, kindMeth},
	0x77: {f3rc, kindMeth}, 0x78: {f3rc, kindMeth},
	0xfa: {f45cc, kindMeth}, 0xfb: {f4rcc, kindMeth},
}

func init() {
	// 机械段批量登记:unop / binop / binop-2addr / lit16 / lit8
	for i := 0x7b; i <= 0x8f; i++ {
		formats[i] = struct {
			f format
			k indexKind
		}{f12x, kindNone}
	}
	for i := 0x90; i <= 0xaf; i++ {
		formats[i] = struct {
			f format
			k indexKind
		}{f23x, kindNone}
	}
	for i := 0xb0; i <= 0xcf; i++ {
		formats[i] = struct {
			f format
			k indexKind
		}{f12x, kindNone}
	}
	for i := 0xd0; i <= 0xd7; i++ {
		formats[i] = struct {
			f format
			k indexKind
		}{f22s, kindNone}
	}
	for i := 0xd8; i <= 0xe2; i++ {
		formats[i] = struct {
			f format
			k indexKind
		}{f22b, kindNone}
	}
}

// Oplen 指令长度(16-bit unit;0 表示非法操作码)。
func Oplen(op byte) int { return int(findrefs.OpcodeOplens[op]) }

// Class 渲染整个类的 smali 风格文本。
func Class(d *dex.DEX, class *dex.Class) []string {
	lines := []string{".class " + class.Fullname()}
	for _, f := range class.Fields() {
		lines = append(lines, fmt.Sprintf(".field %s %s : %s", accessNames(f.AccessFlags), f.Name(), f.Type().Descriptor))
	}
	for _, m := range class.Methods() {
		lines = append(lines, "")
		lines = append(lines, fmt.Sprintf(".method %s %s%s", accessNames(m.AccessFlags), m.Name(), renderProtoText(m.Prototype())))
		if code := m.Code(); code != nil {
			lines = append(lines, fmt.Sprintf("    .registers %d (ins %d, outs %d)",
				code.RegistersSize, code.InsSize, code.OutsSize))
			lines = append(lines, renderInsns(d, code)...)
		} else {
			lines = append(lines, "    .no-body (abstract/native)")
		}
		lines = append(lines, ".end method")
	}
	return lines
}

// renderInsns 解码 code_item 指令流(payload 按真实长度跳过)。
func renderInsns(d *dex.DEX, code *dex.CodeHeader) []string {
	units := code.Insns
	n := len(units) / 2
	out := make([]string, 0, n)
	pc := 0
	for pc < n {
		unit := u16at(units, pc*2)
		op := byte(unit & 0xff)
		if op == 0x00 && unit>>8 != 0 {
			size := payloadUnits(units, pc)
			kind := "fill-array-data-payload"
			switch unit >> 8 {
			case 0x01:
				kind = "packed-switch-payload"
			case 0x02:
				kind = "sparse-switch-payload"
			}
			out = append(out, fmt.Sprintf("p%04x: %s (%d units)", pc, kind, size))
			pc += size
			continue
		}
		size := Oplen(op)
		if size <= 0 {
			size = 1
		}
		out = append(out, fmt.Sprintf("i%04x: %s", pc, decode(d, units, pc, op, unit)))
		pc += size
	}
	return out
}

// decode 单条指令渲染。注意各格式的 nibble 布局互不相同:
//   - 12x/11n:      A|B|op(A 在 12..15,B 在 8..11)
//   - 22t/22s/22c:  B|A|op CCCC(A 在 8..11,B 在 12..15;第二单元为立即数/索引/分支)
//   - 23x:          AA|op CC|BB(第二单元低 8 位 BB,高 8 位 CC)
//   - 22b:          AA|op BBBB CC(第三单元低字节为有符号立即数)
//   - 35c:          A|G|op BBBB F|E|D|C(count=A,G=unit0 的 8..11)
//   - 3rc:          A|G|op BBBB CCCC(count=A,起始寄存器=CCCC,数量 count)
func decode(d *dex.DEX, units []byte, pc int, op byte, unit uint16) string {
	name := OpcodeNames[op]
	if name == "" {
		name = fmt.Sprintf("op-0x%02x", op)
	}
	ft := formats[op]
	AA := byte(unit >> 8)
	BBBB := u16at(units, pc*2+2) // 第二单元
	CCCC := u16at(units, pc*2+4) // 第三单元

	branch := func(off int32) string { return fmt.Sprintf("-> 0x%04x", pc+int(off)) }
	kindRef := func(k indexKind, idx uint32) string {
		switch k {
		case kindString:
			return "string@ " + quote(d.GetString(idx))
		case kindType:
			return "type@ " + d.GetType(idx).Descriptor
		case kindField:
			f := d.GetField(idx)
			return "field@ " + f.Cls() + "->" + f.Name() + ":" + f.Type().Descriptor
		case kindMeth:
			m := d.GetMethod(idx)
			return "meth@ " + m.Cls() + "->" + m.Name() + renderProtoText(m.Prototype())
		case kindProto:
			return "proto@ " + renderProtoText(d.GetPrototype(idx))
		}
		return fmt.Sprintf("@%d", idx)
	}

	switch ft.f {
	case f10x:
		return name
	case f10t:
		return fmt.Sprintf("%s -> 0x%04x", name, pc+int(int8(AA)))
	case f12x:
		vA := (unit >> 8) & 0xf
		vB := (unit >> 12) & 0xf
		return fmt.Sprintf("%s v%d, v%d", name, vA, vB)
	case f11n:
		vA := (unit >> 8) & 0xf
		nib := (unit >> 12) & 0xf
		lit := int32(nib)
		if nib > 7 {
			lit -= 16
		}
		return fmt.Sprintf("%s v%d, #+%d", name, vA, lit)
	case f11x:
		return fmt.Sprintf("%s v%d", name, AA)
	case f20t:
		return fmt.Sprintf("%s %s", name, branch(int32(int16(BBBB))))
	case f22x:
		return fmt.Sprintf("%s v%d, v%d", name, AA, BBBB)
	case f21t:
		return fmt.Sprintf("%s v%d, %s", name, AA, branch(int32(int16(BBBB))))
	case f21s:
		return fmt.Sprintf("%s v%d, #+%d", name, AA, int32(int16(BBBB)))
	case f21h:
		shift := uint(16)
		if op == 0x19 { // const-wide/high16
			shift = 48
		}
		return fmt.Sprintf("%s v%d, #+%d<<%d", name, AA, int32(int16(BBBB)), shift)
	case f21c:
		return fmt.Sprintf("%s v%d, %s", name, AA, kindRef(ft.k, uint32(BBBB)))
	case f23x:
		return fmt.Sprintf("%s v%d, v%d, v%d", name, AA, BBBB&0xff, BBBB>>8)
	case f22b:
		// AA|op CC|BB:vAA, vBB, #+CC
		return fmt.Sprintf("%s v%d, v%d, #+%d", name, AA, BBBB&0xff, int32(int8(byte(BBBB>>8))))
	case f22t:
		vA := (unit >> 8) & 0xf
		vB := (unit >> 12) & 0xf
		return fmt.Sprintf("%s v%d, v%d, %s", name, vA, vB, branch(int32(int16(BBBB))))
	case f22s:
		vA := (unit >> 8) & 0xf
		vB := (unit >> 12) & 0xf
		return fmt.Sprintf("%s v%d, v%d, #+%d", name, vA, vB, int32(int16(BBBB)))
	case f22c:
		vA := (unit >> 8) & 0xf
		vB := (unit >> 12) & 0xf
		return fmt.Sprintf("%s v%d, v%d, %s", name, vA, vB, kindRef(ft.k, uint32(BBBB)))
	case f30t:
		off := int32(u16at(units, pc*2+6))<<16 | int32(u16at(units, pc*2+4))
		return fmt.Sprintf("%s %s", name, branch(off))
	case f31i:
		v := int32(u16at(units, pc*2+6))<<16 | int32(u16at(units, pc*2+4))
		return fmt.Sprintf("%s v%d, #+%d (0x%08x)", name, AA, v, uint32(v))
	case f31t:
		off := int32(u16at(units, pc*2+6))<<16 | int32(u16at(units, pc*2+4))
		return fmt.Sprintf("%s v%d, %s", name, AA, branch(off))
	case f31c:
		idx := uint32(u16at(units, pc*2+6))<<16 | uint32(BBBB)
		return fmt.Sprintf("%s v%d, %s", name, AA, kindRef(ft.k, idx))
	case f32x:
		return fmt.Sprintf("%s v%d, v%d", name, BBBB, CCCC)
	case f35c:
		count := byte(unit >> 12)
		g := (unit >> 8) & 0xf
		c := CCCC & 0xf
		dd := (CCCC >> 4) & 0xf
		e := (CCCC >> 8) & 0xf
		f := (CCCC >> 12) & 0xf
		regs := regList(count, c, dd, e, f, g)
		return fmt.Sprintf("%s {%s}, %s", name, regs, kindRef(ft.k, uint32(BBBB)))
	case f3rc:
		count := byte(unit >> 12)
		lo := int(BBBB)
		hi := lo + int(count) - 1
		return fmt.Sprintf("%s {v%d .. v%d}, %s", name, lo, hi, kindRef(ft.k, uint32(BBBB)))
	case f51l:
		lo := uint64(u16at(units, pc*2+2)) | uint64(u16at(units, pc*2+4))<<16 |
			uint64(u16at(units, pc*2+6))<<32 | uint64(u16at(units, pc*2+8))<<48
		return fmt.Sprintf("%s v%d, #+%d (0x%016x)", name, AA, int64(lo), lo)
	case f45cc:
		count := byte(unit >> 12)
		g := (unit >> 8) & 0xf
		c := CCCC & 0xf
		dd := (CCCC >> 4) & 0xf
		e := (CCCC >> 8) & 0xf
		f := (CCCC >> 12) & 0xf
		HHHH := u16at(units, pc*2+6)
		regs := regList(count, c, dd, e, f, g)
		return fmt.Sprintf("%s {%s}, %s, %s", name, regs,
			kindRef(kindMeth, uint32(BBBB)), kindRef(kindProto, uint32(HHHH)))
	case f4rcc:
		count := byte(unit >> 12)
		lo := int(BBBB)
		hi := lo + int(count) - 1
		HHHH := u16at(units, pc*2+6)
		return fmt.Sprintf("%s {v%d .. v%d}, %s, %s", name, lo, hi,
			kindRef(kindMeth, uint32(BBBB)), kindRef(kindProto, uint32(HHHH)))
	}
	return name + " (unknown format)"
}

// regList 35c/45cc 寄存器列表(C,D,E,F,G 按 count 截取)。
func regList(count byte, parts ...uint16) string {
	regs := make([]string, 0, count)
	for _, p := range parts {
		if byte(len(regs)) >= count {
			break
		}
		regs = append(regs, fmt.Sprintf("v%d", p))
	}
	return strings.Join(regs, ", ")
}

// payloadUnits payload 伪指令总长(16-bit unit 数)。
func payloadUnits(units []byte, pc int) int {
	unit1 := u16at(units, (pc+1)*2)
	switch u16at(units, pc*2) >> 8 {
	case 0x01: // packed-switch-payload: ident|size|... → size*2+4
		return int(unit1)*2 + 4
	case 0x02: // sparse-switch-payload: size*4 + 2
		return int(unit1)*4 + 2
	default: // fill-array-data-payload: (size*width+1)/2 + 4
		width := int(unit1)
		size := int(uint32(u16at(units, (pc+3)*2))<<16 | uint32(u16at(units, (pc+2)*2)))
		return (size*width+1)/2 + 4
	}
}

func renderProtoText(p *dex.Prototype) string {
	if p == nil {
		return "(?)"
	}
	params := p.Parameters()
	names := make([]string, 0, len(params))
	for _, pt := range params {
		names = append(names, pt.Descriptor)
	}
	return "(" + strings.Join(names, "") + ")" + p.ReturnType().Descriptor
}

func u16at(b []byte, off int) uint16 {
	if off+2 > len(b) {
		return 0
	}
	return uint16(b[off]) | uint16(b[off+1])<<8
}

func quote(s string) string {
	return `"` + strings.NewReplacer("\\", `\\`, `"`, `\"`, "\n", `\n`).Replace(s) + `"`
}

// accessNames access flags → 可读串。
func accessNames(flags uint32) string {
	var parts []string
	bits := []struct {
		v    uint32
		name string
	}{
		{0x1, "public"}, {0x2, "private"}, {0x4, "protected"}, {0x8, "static"},
		{0x10, "final"}, {0x20, "synchronized"}, {0x100, "interface"}, {0x400, "abstract"},
		{0x1000, "synthetic"}, {0x40, "enum"},
	}
	for _, b := range bits {
		if flags&b.v != 0 {
			parts = append(parts, b.name)
		}
	}
	return strings.Join(parts, " ")
}
