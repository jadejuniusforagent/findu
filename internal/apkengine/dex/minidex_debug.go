package dex

// minidex_debug.go — mini.dex 的 debug_info_item 重映射。
// 行号表/局部变量名随代码拷贝(jadx 输出行号注释所依赖):
//   头部:line_start uleb + parameters_size uleb + 参数名字符串索引 uleb[]
//   字节码流:DBG 操作码状态机——寄存器号不重映射,全部字符串索引
//   (名称/类型/签名/文件)经 strMap 换新后按新宽度重编码。

import (
	"fmt"
)

// DBG 操作码(Dalvik 规范)。
const (
	dbgEndSequence      = 0x00
	dbgAdvancePC        = 0x01
	dbgAdvanceLine      = 0x02
	dbgStartLocal       = 0x03
	dbgStartLocalExt    = 0x04
	dbgEndLocal         = 0x05
	dbgRestartLocal     = 0x06
	dbgSetPrologueEnd   = 0x07
	dbgSetEpilogueBegin = 0x08
	dbgSetFile          = 0x09
	dbgSpecialFirst     = 0x0a
)

// collectDebugInfoRefs 登记一条 debug_info_item 引用的全部字符串索引。
func (e *minidexBuilder) collectDebugInfoRefs(pos int) error {
	d := e.d
	readU := func() (uint32, error) {
		v, n := d.readUlebBounded(pos)
		if n == 0 {
			return 0, fmt.Errorf("minidex: malformed debug_info at %d", pos)
		}
		pos += n
		return v, nil
	}
	lineStart, err := readU()
	_ = lineStart
	if err != nil {
		return err
	}
	paramCount, err := readU()
	if err != nil {
		return err
	}
	if paramCount > 0xffff {
		return fmt.Errorf("minidex: debug_info param count %d", paramCount)
	}
	for i := uint32(0); i < paramCount; i++ {
		idx, err := readU()
		if err != nil {
			return err
		}
		e.addStr(idx) // NO_INDEX(0xffffffff)在 addStr 内安全忽略
	}
	for pos < len(d.buf) {
		op := d.buf[pos]
		pos++
		switch op {
		case dbgEndSequence:
			return nil
		case dbgAdvancePC:
			if _, err = readU(); err != nil {
				return err
			}
		case dbgAdvanceLine:
			v, n := d.readSlebBounded(pos)
			if n == 0 {
				return fmt.Errorf("minidex: malformed debug_info at %d", pos)
			}
			pos += n
			_ = v
		case dbgStartLocal, dbgStartLocalExt:
			if _, err = readU(); err != nil { // 寄存器号
				return err
			}
			name, err := readU()
			if err != nil {
				return err
			}
			e.addStr(name)
			typ, err := readU()
			if err != nil {
				return err
			}
			e.addStr(typ)
			if op == dbgStartLocalExt {
				sig, err := readU()
				if err != nil {
					return err
				}
				e.addStr(sig)
			}
		case dbgEndLocal, dbgRestartLocal:
			if _, err = readU(); err != nil {
				return err
			}
		case dbgSetPrologueEnd, dbgSetEpilogueBegin:
			// 无操作数
		case dbgSetFile:
			name, err := readU()
			if err != nil {
				return err
			}
			e.addStr(name)
		default:
			if op < dbgSpecialFirst {
				return fmt.Errorf("minidex: unknown debug opcode 0x%02x", op)
			}
			// special opcode:无操作数
		}
	}
	return fmt.Errorf("minidex: debug_info missing end sequence")
}

// emitDebugInfo 重写一条 debug_info_item(字符串索引换新),追加到 w
// 并返回新偏移。memo 化:多个方法可共享同一 debug_info。
func (e *minidexBuilder) emitDebugInfo(w *minidexWriter, debugOff uint32) (uint32, error) {
	if newOff, ok := e.debugMap[int(debugOff)]; ok {
		return newOff, nil
	}
	d := e.d
	var hdr []byte
	// 头部:line_start 原样(数值不变,重编码等价) + 参数名重映射
	off := int(debugOff)
	lineStart, n := d.readUlebBounded(off)
	if n == 0 {
		return 0, fmt.Errorf("minidex: malformed debug_info at %d", off)
	}
	pos := off + n
	hdr = append(hdr, miniULEB(lineStart)...)
	paramCount, n := d.readUlebBounded(pos)
	if n == 0 {
		return 0, fmt.Errorf("minidex: malformed debug_info at %d", off)
	}
	pos += n
	hdr = append(hdr, miniULEB(paramCount)...)
	for i := uint32(0); i < paramCount; i++ {
		idx, n := d.readUlebBounded(pos)
		if n == 0 {
			return 0, fmt.Errorf("minidex: malformed debug_info at %d", off)
		}
		pos += n
		if idx != noIndex {
			idx = e.strMap[idx]
		}
		hdr = append(hdr, miniULEB(idx)...)
	}

	// 字节码流重写
	var stream []byte
	stream = append(stream, hdr...)
	for {
		if pos >= len(d.buf) {
			return 0, fmt.Errorf("minidex: debug_info missing end sequence")
		}
		op := d.buf[pos]
		pos++
		stream = append(stream, op)
		switch op {
		case dbgEndSequence:
			w.align4()
			newOff := uint32(w.offset())
			if e.firstDebugOff == 0 {
				e.firstDebugOff = newOff
			}
			e.debugCount++
			w.place(stream)
			e.debugMap[int(debugOff)] = newOff
			e.noteAnno(0x2003, newOff)
			return newOff, nil
		case dbgAdvancePC:
			v, n := d.readUlebBounded(pos)
			if n == 0 {
				return 0, fmt.Errorf("minidex: malformed debug_info")
			}
			pos += n
			stream = append(stream, miniULEB(v)...)
		case dbgAdvanceLine:
			v, n := d.readSlebBounded(pos)
			if n == 0 {
				return 0, fmt.Errorf("minidex: malformed debug_info")
			}
			pos += n
			stream = append(stream, miniSLEB(v)...)
		case dbgStartLocal, dbgStartLocalExt:
			reg, n := d.readUlebBounded(pos)
			if n == 0 {
				return 0, fmt.Errorf("minidex: malformed debug_info")
			}
			pos += n
			stream = append(stream, miniULEB(reg)...)
			for k := 0; k < 2; k++ { // name / type 字符串
				idx, n := d.readUlebBounded(pos)
				if n == 0 {
					return 0, fmt.Errorf("minidex: malformed debug_info")
				}
				pos += n
				if idx != noIndex {
					idx = e.strMap[idx]
				}
				stream = append(stream, miniULEB(idx)...)
			}
			if op == dbgStartLocalExt {
				sig, n := d.readUlebBounded(pos)
				if n == 0 {
					return 0, fmt.Errorf("minidex: malformed debug_info")
				}
				pos += n
				if sig != noIndex {
					sig = e.strMap[sig]
				}
				stream = append(stream, miniULEB(sig)...)
			}
		case dbgEndLocal, dbgRestartLocal:
			reg, n := d.readUlebBounded(pos)
			if n == 0 {
				return 0, fmt.Errorf("minidex: malformed debug_info")
			}
			pos += n
			stream = append(stream, miniULEB(reg)...)
		case dbgSetPrologueEnd, dbgSetEpilogueBegin:
			// 无操作数
		case dbgSetFile:
			name, n := d.readUlebBounded(pos)
			if n == 0 {
				return 0, fmt.Errorf("minidex: malformed debug_info")
			}
			pos += n
			if name != noIndex {
				name = e.strMap[name]
			}
			stream = append(stream, miniULEB(name)...)
		default:
			if op < dbgSpecialFirst {
				return 0, fmt.Errorf("minidex: unknown debug opcode 0x%02x", op)
			}
			// special opcode:无操作数
		}
	}
}
