package godad

// opcodes.go — 指令长度与 payload 解析。
// 长度表复用 findrefs.OpcodeOplens(已通过 ASC 差分测试验证)。

import "finduai/internal/apkengine/findrefs"

// Oplen 指令长度(16-bit code unit 数)。
func Oplen(op byte) int {
	n := int(findrefs.OpcodeOplens[op])
	if n <= 0 {
		return 1
	}
	return n
}

// payloadUnits payload 伪指令总长(16-bit unit 数)。
func payloadUnits(units []byte, pc int) int {
	if (pc+1)*2+2 > len(units) {
		return 1
	}
	unit1 := uint16(units[(pc+1)*2]) | uint16(units[(pc+1)*2+1])<<8
	switch uint16(units[pc*2+1]) {
	case 0x01: // packed-switch-payload: size*2 + 4
		return int(unit1)*2 + 4
	case 0x02: // sparse-switch-payload: size*4 + 2
		return int(unit1)*4 + 2
	default: // fill-array-data-payload: (size*width+1)/2 + 4
		width := int(unit1)
		if (pc+4)*2 > len(units) {
			return 1
		}
		lo := uint32(units[(pc+2)*2]) | uint32(units[(pc+2)*2+1])<<8
		hi := uint32(units[(pc+3)*2]) | uint32(units[(pc+3)*2+1])<<8
		size := int(hi<<16 | lo)
		return (size*width+1)/2 + 4
	}
}
