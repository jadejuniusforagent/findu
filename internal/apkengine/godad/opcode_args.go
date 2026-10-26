package godad

// ────────────────────────── 参数提取工具 ──────────────────────────
// 位序对齐 DEX 规范(参考 ASC dvm_regdec.py 差分基线):
//   12x/11n/22c/22s/22t: B|A|op → A=(u>>8)&0xf, B=(u>>12)&0xf
//   23x/22b:            AA|op CC|BB → A=u>>8, B=unit1&0xff, C=unit1>>8

func (ctx *DecompileCtx) currentUnit() uint16 {
	units := ctx.Code.Insns
	off := ctx.PC * 2
	if off+2 > len(units) {
		return 0
	}
	return uint16(units[off]) | uint16(units[off+1])<<8
}

func (ctx *DecompileCtx) nextUnit(offset int) uint16 {
	units := ctx.Code.Insns
	off := (ctx.PC + offset) * 2
	if off+2 > len(units) {
		return 0
	}
	return uint16(units[off]) | uint16(units[off+1])<<8
}

func (ctx *DecompileCtx) CurrentAA() byte {
	return byte(ctx.currentUnit() >> 8)
}

func moveArgs(ctx *DecompileCtx) (byte, byte) {
	u := ctx.currentUnit()
	return byte((u >> 8) & 0xf), byte((u >> 12) & 0xf)
}

func moveFrom16Args(ctx *DecompileCtx) (byte, uint16) {
	A := ctx.CurrentAA()
	B := ctx.nextUnit(1)
	return A, B
}

func move16Args(ctx *DecompileCtx) (uint16, uint16) {
	return ctx.nextUnit(1), ctx.nextUnit(2)
}

func const4Args(ctx *DecompileCtx) (byte, int8) {
	u := ctx.currentUnit()
	A := byte((u >> 8) & 0xf)
	B := int8(byte((u >> 12) & 0xf))
	if B > 7 {
		B -= 16
	}
	return A, B
}

func const16Args(ctx *DecompileCtx) (byte, int16) {
	A := ctx.CurrentAA()
	B := int16(ctx.nextUnit(1))
	return A, B
}

func const32Args(ctx *DecompileCtx) (byte, int32) {
	A := ctx.CurrentAA()
	u2 := ctx.nextUnit(1)
	u3 := ctx.nextUnit(2)
	v := int32(u2) | int32(u3)<<16
	return A, v
}

func constStringArgs(ctx *DecompileCtx) (byte, uint16) {
	A := ctx.CurrentAA()
	B := ctx.nextUnit(1)
	return A, B
}

func constClassArgs(ctx *DecompileCtx) (byte, uint16) {
	A := ctx.CurrentAA()
	B := ctx.nextUnit(1)
	return A, B
}

func checkCastArgs(ctx *DecompileCtx) (byte, uint16) {
	A := ctx.CurrentAA()
	B := ctx.nextUnit(1)
	return A, B
}

func instanceOfArgs(ctx *DecompileCtx) (byte, byte, uint16) {
	u := ctx.currentUnit()
	A := byte((u >> 8) & 0xf)
	B := byte((u >> 12) & 0xf)
	C := ctx.nextUnit(1)
	return A, B, C
}

func arrayLengthArgs(ctx *DecompileCtx) (byte, byte) {
	u := ctx.currentUnit()
	return byte((u >> 8) & 0xf), byte((u >> 12) & 0xf)
}

func newInstanceArgs(ctx *DecompileCtx) (byte, uint16) {
	A := ctx.CurrentAA()
	B := ctx.nextUnit(1)
	return A, B
}

func newArrayArgs(ctx *DecompileCtx) (byte, byte, uint16) {
	u := ctx.currentUnit()
	A := byte((u >> 8) & 0xf)
	B := byte((u >> 12) & 0xf)
	C := ctx.nextUnit(1)
	return A, B, C
}

func fieldArgs(ctx *DecompileCtx) (byte, byte, uint16) {
	u := ctx.currentUnit()
	A := byte((u >> 8) & 0xf)
	B := byte((u >> 12) & 0xf)
	C := ctx.nextUnit(1)
	return A, B, C
}

func sfieldArgs(ctx *DecompileCtx) (byte, uint16) {
	A := ctx.CurrentAA()
	B := ctx.nextUnit(1)
	return A, B
}

func binop12xArgs(ctx *DecompileCtx) (byte, byte) {
	u := ctx.currentUnit()
	return byte((u >> 8) & 0xf), byte((u >> 12) & 0xf)
}

func binop23xArgs(ctx *DecompileCtx) (byte, byte, byte) {
	u := ctx.currentUnit()
	A := byte(u >> 8)
	B := byte(ctx.nextUnit(1) & 0xff)
	C := byte(ctx.nextUnit(1) >> 8)
	return A, B, C
}

func cmpArgs(ctx *DecompileCtx) (byte, byte, byte) {
	u := ctx.currentUnit()
	A := byte(u >> 8)
	B := byte(ctx.nextUnit(1) & 0xff)
	C := byte(ctx.nextUnit(1) >> 8)
	return A, B, C
}

func ifArgs(ctx *DecompileCtx) (byte, byte, int16) {
	u := ctx.currentUnit()
	A := byte((u >> 8) & 0xf)
	B := byte((u >> 12) & 0xf)
	C := int16(ctx.nextUnit(1))
	return A, B, C
}

func invokeVirtual(ctx *DecompileCtx) *Invoke {
	m := invokeMethod(ctx)
	m.Type = InvokeVirtual
	ctx.LastInvoke = m
	return m
}

func invokeMethod(ctx *DecompileCtx) *Invoke {
	u := ctx.currentUnit()
	count := byte(u >> 12)
	g := byte((u >> 8) & 0xf)
	methodIdx := uint32(ctx.nextUnit(1)) // 第二单元:方法索引
	m := ctx.DEX.GetMethod(methodIdx)
	// 构建原型字符串
	proto := renderProtoText(m.Prototype())
	// 参数寄存器:第三单元 nibble C,D,E,F + 第一单元 G(按 count 截取)
	regUnit := ctx.nextUnit(2)
	regs := []byte{}
	if count > 0 {
		regs = append(regs, byte(regUnit&0xf))
	}
	if count > 1 {
		regs = append(regs, byte((regUnit>>4)&0xf))
	}
	if count > 2 {
		regs = append(regs, byte((regUnit>>8)&0xf))
	}
	if count > 3 {
		regs = append(regs, byte((regUnit>>12)&0xf))
	}
	if count > 4 {
		regs = append(regs, g)
	}
	args := make([]Node, 0, len(regs))
	for _, r := range regs {
		args = append(args, ctx.reg(int(r)))
	}
	this := Node(nil)
	if len(args) > 0 {
		this = args[0]
		args = args[1:]
	}
	inv := &Invoke{
		Owner:   m.Cls(),
		Name:    m.Name(),
		Proto:   proto,
		RetType: m.Prototype().ReturnType().Descriptor,
		Args:    args,
		This:    this,
	}
	// 构造器委托:invoke <init> 且接收者是 this 寄存器 → super()/this()
	if m.Name() == "<init>" && ctx.isThis(this) {
		if m.Cls() == ctx.Method.Cls() {
			inv.CtorCall = "this"
		} else {
			inv.CtorCall = "super"
		}
	}
	return inv
}

// isThis 判断节点是否为当前方法的 this 寄存器引用。
func (ctx *DecompileCtx) isThis(n Node) bool {
	v, ok := n.(*Var)
	if !ok {
		return false
	}
	name, ok := ctx.ParamNames[v.Reg]
	return ok && name == "this"
}
