package godad

// bitorder_test — 寄存器位序回归测试。
// 基线与 androguard 真值反汇编逐条核对(ByteStreamReader.apk):
//   unit=0x0112 → const/4 v1, 0      (11n: A=(u>>8)&0xf, B=(u>>12)&0xf)
//   unit=0xf312 → const/4 v3, -1
//   unit=0x3201 → move v2, v3        (12x: A=(u>>8)&0xf, B=(u>>12)&0xf)
//   unit=0x3237 → if-le v2, v3       (22t: A=(u>>8)&0xf, B=(u>>12)&0xf)
// 若有人再改错位序,这些断言立即失败。

import (
	"testing"

	"finduai/internal/apkengine/dex"
)

func ctxWith(units ...byte) *DecompileCtx {
	return &DecompileCtx{
		Code:       &dex.CodeHeader{Insns: units},
		ParamNames: map[int]string{},
	}
}

func TestRegBitOrderConst4(t *testing.T) {
	// 0x0112 → const/4 v1, 0
	ctx := ctxWith(0x12, 0x01)
	A, B := const4Args(ctx)
	if A != 1 || B != 0 {
		t.Errorf("const4(0x0112) = v%d, %d; want v1, 0", A, B)
	}
	// 0xf312 → const/4 v3, -1
	ctx = ctxWith(0x12, 0xf3)
	A, B = const4Args(ctx)
	if A != 3 || B != -1 {
		t.Errorf("const4(0xf312) = v%d, %d; want v3, -1", A, B)
	}
}

func TestRegBitOrderMove12x(t *testing.T) {
	// 0x3201 → move v2, v3
	ctx := ctxWith(0x01, 0x32)
	A, B := moveArgs(ctx)
	if A != 2 || B != 3 {
		t.Errorf("move(0x3201) = v%d, v%d; want v2, v3", A, B)
	}
}

func TestRegBitOrderIf22t(t *testing.T) {
	// 0x3237 if-le v2, v3, +0x11
	ctx := ctxWith(0x37, 0x32, 0x11, 0x00)
	A, B, off := ifArgs(ctx)
	if A != 2 || B != 3 || off != 0x11 {
		t.Errorf("if-le(0x3237) = v%d, v%d, +%d; want v2, v3, +17", A, B, off)
	}
}

func TestRegBitOrderField22c(t *testing.T) {
	// iget vA, vB, field@CCCC:A=(u>>8)&0xf, B=(u>>12)&0xf
	// A=1, B=2 → unit0 = (2<<12)|(1<<8)|0x52 = 0x2152
	ctx := ctxWith(0x52, 0x21, 0x03, 0x00)
	A, B, C := fieldArgs(ctx)
	if A != 1 || B != 2 || C != 3 {
		t.Errorf("iget(0x2152) = v%d, v%d, @%d; want v1, v2, @3", A, B, C)
	}
}

func TestRegBitOrder23x(t *testing.T) {
	// add-int v4, v7, v3:unit0 = 0x0490, unit1 = (3<<8)|7 = 0x0307
	ctx := ctxWith(0x90, 0x04, 0x07, 0x03)
	A, B, C := binop23xArgs(ctx)
	if A != 4 || B != 7 || C != 3 {
		t.Errorf("add-int(0x0490,0x0307) = v%d, v%d, v%d; want v4, v7, v3", A, B, C)
	}
}

func TestRegBitOrderConst16(t *testing.T) {
	// const/16 v2, #+4096:unit0 = 0x0213, unit1 = 0x1000
	ctx := ctxWith(0x13, 0x02, 0x00, 0x10)
	A, B := const16Args(ctx)
	if A != 2 || B != 4096 {
		t.Errorf("const/16 = v%d, %d; want v2, 4096", A, B)
	}
}

func TestRegBitOrderNewArray22c(t *testing.T) {
	// new-array vA, vB, type@CCCC:A=2, B=8
	// unit0 = (8<<12)|(2<<8)|0x23 = 0x8223, type idx=8 → unit1=0x0008
	ctx := ctxWith(0x23, 0x82, 0x08, 0x00)
	A, B, C := newArrayArgs(ctx)
	if A != 2 || B != 8 || C != 8 {
		t.Errorf("new-array(0x8223) = v%d, v%d, @%d; want v2, v8, @8", A, B, C)
	}
}
