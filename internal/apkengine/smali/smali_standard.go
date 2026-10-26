package smali

// smali_standard — 标准 smali 文本渲染器(可被 smali.jar 汇编)。
// 与 smali.go 的"风格视图"区别:
//   - 无 i0000 地址前缀(标准 smali 不带指令地址)
//   - 操作数写完整引用句法(去掉 kind@ 前缀)
//   - 分支/跳转用 :label 标签(预扫描偏移→标签映射)
//   - .class 带 access flags; .super 声明父类
//   - .registers N(无 ins/outs 注释)
//   - payload 伪指令用标准格式

import (
	"fmt"
	"strings"

	"finduai/internal/apkengine/dex"
)

// StandardClass 输出标准 smali 文本(可被 smali.jar assemble)。
func StandardClass(d *dex.DEX, class *dex.Class) []string {
	lines := []string{}
	// .class access ClassName
	clsAccess := classAccessNames(class.ClassAccessFlags())
	lines = append(lines, fmt.Sprintf(".class %s %s", clsAccess, class.Fullname()))
	// .super
	lines = append(lines, fmt.Sprintf(".super %s", class.Super()))

	for _, f := range class.Fields() {
		lines = append(lines, fmt.Sprintf(".field %s %s:%s", accessNames(f.AccessFlags), f.Name(), f.Type().Descriptor))
	}
	for _, m := range class.Methods() {
		lines = append(lines, "")
		lines = append(lines, fmt.Sprintf(".method %s %s%s", accessNames(m.AccessFlags), m.Name(), renderProtoText(m.Prototype())))
		if code := m.Code(); code != nil {
			lines = append(lines, fmt.Sprintf("    .registers %d", code.RegistersSize))
			lines = append(lines, standardInsns(d, code)...)
		} else {
			lines = append(lines, "    .locals 0")
		}
		lines = append(lines, ".end method")
	}
	return lines
}

// classAccessNames 类 access_flags → smali 格式(去掉 abstract/interface/enum 对位置有影响的)。
func classAccessNames(flags uint32) string {
	var parts []string
	bits := []struct {
		v    uint32
		name string
	}{
		{0x0001, "public"}, {0x0002, "private"}, {0x0004, "protected"}, {0x0008, "static"},
		{0x0010, "final"}, {0x0020, "super"}, {0x0200, "interface"},
		{0x0400, "abstract"}, {0x1000, "synthetic"}, {0x2000, "annotation"}, {0x4000, "enum"},
	}
	for _, b := range bits {
		if flags&b.v != 0 {
			parts = append(parts, b.name)
		}
	}
	return strings.Join(parts, " ")
}

// standardInsns 渲染标准 smali 指令流:带 label 预扫描、无地址前缀、操作数完整引用。
func standardInsns(d *dex.DEX, code *dex.CodeHeader) []string {
	units := code.Insns
	n := len(units) / 2
	if n == 0 {
		return nil
	}

	// 第一遍:收集所有分支目标偏移,生成 label 映射
	labels := map[int]string{}
	lblIdx := 0
	pc := 0
	for pc < n {
		unit := u16at(units, pc*2)
		op := byte(unit & 0xff)
		if op == 0x00 && unit>>8 != 0 {
			size := payloadUnits(units, pc)
			pc += size
			continue
		}
		ft := formats[op]
		AA := byte(unit >> 8)
		BBBB := u16at(units, pc*2+2)

		addTarget := func(off int) {
			target := pc + off
			if target >= 0 && target < n {
				if _, ok := labels[target]; !ok {
					labels[target] = fmt.Sprintf("label_%d", lblIdx)
					lblIdx++
				}
			}
		}

		switch ft.f {
		case f10t:
			addTarget(int(int8(AA)))
		case f20t:
			addTarget(int(int16(BBBB)))
		case f21t:
			addTarget(int(int16(BBBB)))
		case f30t:
			off := int32(u16at(units, pc*2+6))<<16 | int32(u16at(units, pc*2+4))
			addTarget(int(off))
		case f22t:
			addTarget(int(int16(BBBB)))
		case f31t:
			off := int32(u16at(units, pc*2+6))<<16 | int32(u16at(units, pc*2+4))
			addTarget(int(off))
		}
		size := Oplen(op)
		if size <= 0 {
			size = 1
		}
		pc += size
	}

	// 第二遍:渲染指令(带 label)
	out := make([]string, 0, n)
	pc = 0
	for pc < n {
		unit := u16at(units, pc*2)
		op := byte(unit & 0xff)
		// payload 伪指令
		if op == 0x00 && unit>>8 != 0 {
			kind := unit >> 8
			size := payloadUnits(units, pc)
			if lbl, ok := labels[pc]; ok {
				out = append(out, "    "+lbl+":")
			}
			switch kind {
			case 0x01:
				out = append(out, fmt.Sprintf("    :pswitch_data_%d", pc))
			case 0x02:
				out = append(out, fmt.Sprintf("    :sswitch_data_%d", pc))
			default:
				out = append(out, fmt.Sprintf("    :array_data_%d", pc))
			}
			pc += size
			continue
		}
		if lbl, ok := labels[pc]; ok {
			out = append(out, "    "+lbl+":")
		}
		size := Oplen(op)
		if size <= 0 {
			size = 1
		}
		out = append(out, "    "+decodeStandard(d, units, pc, op, unit, labels))
		pc += size
	}
	return out
}

// decodeStandard 标准 smali 指令解码:去掉 i0000 前缀、kind@ 标签,分支用 label。
func decodeStandard(d *dex.DEX, units []byte, pc int, op byte, unit uint16, labels map[int]string) string {
	name := OpcodeNames[op]
	if name == "" {
		name = fmt.Sprintf("op-0x%02x", op)
	}
	ft := formats[op]
	AA := byte(unit >> 8)
	BBBB := u16at(units, pc*2+2)
	CCCC := u16at(units, pc*2+4)

	labelRef := func(off int) string {
		target := pc + off
		if l, ok := labels[target]; ok {
			return ":" + l
		}
		return fmt.Sprintf(":L_%d", target)
	}
	kindRef := func(k indexKind, idx uint32) string {
		switch k {
		case kindString:
			return fmt.Sprintf(`"%s"`, d.GetString(idx))
		case kindType:
			return d.GetType(idx).Descriptor
		case kindField:
			f := d.GetField(idx)
			return fmt.Sprintf("%s->%s:%s", f.Cls(), f.Name(), f.Type().Descriptor)
		case kindMeth:
			m := d.GetMethod(idx)
			return fmt.Sprintf("%s->%s%s", m.Cls(), m.Name(), renderProtoText(m.Prototype()))
		case kindProto:
			return renderProtoText(d.GetPrototype(idx))
		}
		return fmt.Sprintf("@%d", idx)
	}

	switch ft.f {
	case f10x:
		return name
	case f10t:
		return fmt.Sprintf("%s %s", name, labelRef(int(int8(AA))))
	case f12x:
		return fmt.Sprintf("%s v%d, v%d", name, (unit>>8)&0xf, (unit>>12)&0xf)
	case f11n:
		nib := (unit >> 12) & 0xf
		lit := int32(nib)
		if nib > 7 {
			lit -= 16
		}
		return fmt.Sprintf("%s v%d, #+%d", name, (unit>>8)&0xf, lit)
	case f11x:
		return fmt.Sprintf("%s v%d", name, AA)
	case f20t:
		return fmt.Sprintf("%s %s", name, labelRef(int(int16(BBBB))))
	case f22x:
		return fmt.Sprintf("%s v%d, v%d", name, AA, BBBB)
	case f21t:
		return fmt.Sprintf("%s v%d, %s", name, AA, labelRef(int(int16(BBBB))))
	case f21s:
		return fmt.Sprintf("%s v%d, #+%d", name, AA, int32(int16(BBBB)))
	case f21h:
		shift := uint(16)
		if op == 0x19 {
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
		return fmt.Sprintf("%s v%d, v%d, %s", name, (unit>>8)&0xf, (unit>>12)&0xf, labelRef(int(int16(BBBB))))
	case f22s:
		return fmt.Sprintf("%s v%d, v%d, #+%d", name, (unit>>8)&0xf, (unit>>12)&0xf, int32(int16(BBBB)))
	case f22c:
		return fmt.Sprintf("%s v%d, v%d, %s", name, (unit>>8)&0xf, (unit>>12)&0xf, kindRef(ft.k, uint32(BBBB)))
	case f30t:
		off := int32(u16at(units, pc*2+6))<<16 | int32(u16at(units, pc*2+4))
		return fmt.Sprintf("%s %s", name, labelRef(int(off)))
	case f31i:
		v := int32(u16at(units, pc*2+6))<<16 | int32(u16at(units, pc*2+4))
		return fmt.Sprintf("%s v%d, #+%d", name, AA, v)
	case f31t:
		off := int32(u16at(units, pc*2+6))<<16 | int32(u16at(units, pc*2+4))
		return fmt.Sprintf("%s v%d, %s", name, AA, labelRef(int(off)))
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
		return fmt.Sprintf("%s v%d, #+%d", name, AA, int64(lo))
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
