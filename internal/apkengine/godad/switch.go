package godad

// switch.go — switch payload 解析(packed/sparse)。
// 布局参考 DEX 规范:
//   packed-switch-payload: ident(0x0100) size first_key targets[size]
//   sparse-switch-payload: ident(0x0200) size keys[size] targets[size]
// 偏移均为相对 switch 指令 PC 的 int32(单位:code unit)。

// SwitchCase 一个 switch 分支(键 → 目标 unit 偏移)。
type SwitchCase struct {
	Key    int32
	Target int
}

// switchPayload 解析结果。
type switchPayload struct {
	Cases []SwitchCase
	// Default switch 指令后继(fallthrough)的 unit 偏移。
	Default int
}

// parseSwitchPayload 解析 switch 载荷;非法返回 nil。
// switchPC: switch 指令偏移;switchSize: 指令长度(unit)。
func parseSwitchPayload(units []byte, total, switchPC int, op byte) *switchPayload {
	// 31t: AA|op +BBBBBBBB,偏移在 units 1-2
	if switchPC*2+6 > len(units) {
		return nil
	}
	off := int32(u16(units, switchPC*2+2)) | int32(u16(units, switchPC*2+4))<<16
	payloadPC := switchPC + int(off)
	if payloadPC < 0 || payloadPC+4 > total {
		return nil
	}
	ident := u16(units, payloadPC*2)
	size := int(u16(units, payloadPC*2+2))
	if size <= 0 || size > 4096 {
		return nil
	}
	rel := func(i int) int { // 第 i 个 int32(以 payload 起始计,单位:unit)
		base := (payloadPC + i) * 2
		if base+4 > len(units) {
			return 0
		}
		return int(int32(u16(units, base)) | int32(u16(units, base+2))<<16)
	}
	pl := &switchPayload{Default: switchPC + Oplen(op)}
	switch {
	case ident == 0x0100 && op == 0x2b: // packed
		firstKey := int32(rel(2))
		for i := 0; i < size; i++ {
			target := switchPC + rel(4+2*i)
			pl.Cases = append(pl.Cases, SwitchCase{Key: firstKey + int32(i), Target: target})
		}
	case ident == 0x0200 && op == 0x2c: // sparse
		for i := 0; i < size; i++ {
			key := int32(rel(2 + 2*i))
			target := switchPC + rel(2+2*size+2*i)
			pl.Cases = append(pl.Cases, SwitchCase{Key: key, Target: target})
		}
	default:
		return nil
	}
	return pl
}
