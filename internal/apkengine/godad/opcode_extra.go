package godad

// opcode_extra.go — 补齐高频缺失操作码：move 变体、const-wide、数组读写、
// 类型转换、long/float/double 算术、invoke-range、filled-new-array、
// sget/sput byte/char/short、异常与低频语义节点。

import "finduai/internal/apkengine/dex"

func init() {
	// ── move 变体(0x04-0x09) ── 12x/22x/32x ──
	OpcodeHandlers[0x04] = func(ctx *DecompileCtx) []Node {
		A, B := moveArgs(ctx)
		return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: ctx.reg(int(B))}}
	}
	OpcodeHandlers[0x05] = func(ctx *DecompileCtx) []Node {
		A, B := moveFrom16Args(ctx)
		return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: ctx.reg(int(B))}}
	}
	OpcodeHandlers[0x06] = func(ctx *DecompileCtx) []Node {
		A, B := move16Args(ctx)
		return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: ctx.reg(int(B))}}
	}
	OpcodeHandlers[0x07] = func(ctx *DecompileCtx) []Node {
		A, B := moveArgs(ctx)
		return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: ctx.reg(int(B))}}
	}
	OpcodeHandlers[0x08] = func(ctx *DecompileCtx) []Node {
		A, B := moveFrom16Args(ctx)
		return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: ctx.reg(int(B))}}
	}
	OpcodeHandlers[0x09] = func(ctx *DecompileCtx) []Node {
		A, B := move16Args(ctx)
		return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: ctx.reg(int(B))}}
	}

	// ── const 系列(0x15,0x19,0x1b) ──
	// 0x15 const/high16 vAA, #+BBBB0000 (21h)
	OpcodeHandlers[0x15] = func(ctx *DecompileCtx) []Node {
		A := ctx.CurrentAA()
		B := ctx.nextUnit(1)
		return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: &IntConst{V: int64(int32(B)) << 16}}}
	}
	// 0x16 const-wide/16 vAA, #+BBBB (21s)
	OpcodeHandlers[0x16] = func(ctx *DecompileCtx) []Node {
		A := ctx.CurrentAA()
		B := ctx.nextUnit(1)
		return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: &LongConst{V: int64(int16(B))}}}
	}
	// 0x17 const-wide/32 vAA, #+BBBBBBBB (31i)
	OpcodeHandlers[0x17] = func(ctx *DecompileCtx) []Node {
		A := ctx.CurrentAA()
		u2 := uint32(ctx.nextUnit(1))
		u3 := uint32(ctx.nextUnit(2))
		return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: &LongConst{V: int64(int32(u2 | u3<<16))}}}
	}
	// 0x18 const-wide vAA, #+BBBBBBBBBBBBBBBB (51l)
	OpcodeHandlers[0x18] = func(ctx *DecompileCtx) []Node {
		A := ctx.CurrentAA()
		u2 := uint64(ctx.nextUnit(1))
		u3 := uint64(ctx.nextUnit(2))
		u4 := uint64(ctx.nextUnit(3))
		u5 := uint64(ctx.nextUnit(4))
		val := u2 | u3<<16 | u4<<32 | u5<<48
		return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: &LongConst{V: int64(val)}}}
	}
	// 0x19 const-wide/high16 vAA, #+BBBB000000000000 (21h)
	OpcodeHandlers[0x19] = func(ctx *DecompileCtx) []Node {
		A := ctx.CurrentAA()
		B := ctx.nextUnit(1)
		return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: &LongConst{V: int64(int16(B)) << 48}}}
	}
	// 0x1b const-string/jumbo vAA, string@BBBBBBBB (31c)
	OpcodeHandlers[0x1b] = func(ctx *DecompileCtx) []Node {
		A := ctx.CurrentAA()
		u2 := uint32(ctx.nextUnit(1))
		u3 := uint32(ctx.nextUnit(2))
		idx := u2 | u3<<16
		str := ctx.DEX.GetString(idx)
		return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: &StringConst{V: str}}}
	}

	// ── filled-new-array (0x24, 0x25) ──
	// 0x24 filled-new-array {vC..vG}, type@BBBB (35c)
	OpcodeHandlers[0x24] = func(ctx *DecompileCtx) []Node {
		u := ctx.currentUnit()
		count := int((u >> 12) & 0xf)
		typeIdx := uint32(ctx.nextUnit(1))
		regUnit := ctx.nextUnit(2)
		elems := make([]Node, 0, count)
		for i := 0; i < count && i < 5; i++ {
			elems = append(elems, ctx.reg(int(regUnitNibble(regUnit, i))))
		}
		typeDesc := ctx.DEX.GetType(typeIdx).Descriptor
		return []Node{&FilledArray{Type: typeDesc, Elems: elems}}
	}
	// 0x25 filled-new-array/range {vCCCC..vNNNN}, type@BBBB (3rc)
	OpcodeHandlers[0x25] = func(ctx *DecompileCtx) []Node {
		u := ctx.currentUnit()
		count := int((u >> 8) & 0xff)
		typeIdx := uint32(ctx.nextUnit(1))
		start := int(ctx.nextUnit(2))
		elems := make([]Node, 0, count)
		for i := 0; i < count; i++ {
			elems = append(elems, ctx.reg(start+i))
		}
		typeDesc := ctx.DEX.GetType(typeIdx).Descriptor
		return []Node{&FilledArray{Type: typeDesc, Elems: elems}}
	}

	// ── 数组读写 aget/aput (0x44-0x51, 23x) ──
	// aget: vAA = vBB[vCC];aput: vBB[vCC] = vAA
	arrayElemType := map[byte]string{
		0x44: "I", 0x45: "J", 0x46: "Ljava/lang/Object;", 0x47: "Z",
		0x48: "B", 0x49: "C", 0x4a: "S",
	}
	for op, elem := range arrayElemType {
		op, elem := op, elem
		OpcodeHandlers[op] = func(ctx *DecompileCtx) []Node {
			A, B, C := binop23xArgs(ctx)
			return []Node{&Assign{
				LHS: ctx.reg(int(A)),
				RHS: &ArrayGet{Array: ctx.reg(int(B)), Index: ctx.reg(int(C)), Type: elem},
			}}
		}
	}
	for op := byte(0x4b); op <= 0x51; op++ {
		op := op
		OpcodeHandlers[op] = func(ctx *DecompileCtx) []Node {
			A, B, C := binop23xArgs(ctx)
			return []Node{&ArrayPut{Array: ctx.reg(int(B)), Index: ctx.reg(int(C)), Value: ctx.reg(int(A))}}
		}
	}

	// ── sget/sput byte/char/short (0x64-0x66, 0x6b-0x6d) ──
	for _, op := range []byte{0x64, 0x65, 0x66} {
		op := op
		OpcodeHandlers[op] = func(ctx *DecompileCtx) []Node {
			A := ctx.CurrentAA()
			B := uint32(ctx.nextUnit(1))
			f := ctx.DEX.GetField(B)
			return []Node{&Assign{LHS: ctx.reg(int(A)), RHS: &SGet{Owner: f.Cls(), Name: f.Name(), Type: f.Type().Descriptor}}}
		}
	}
	for _, op := range []byte{0x6b, 0x6c, 0x6d} {
		op := op
		OpcodeHandlers[op] = func(ctx *DecompileCtx) []Node {
			A := ctx.CurrentAA()
			B := uint32(ctx.nextUnit(1))
			f := ctx.DEX.GetField(B)
			return []Node{&SPut{Owner: f.Cls(), Name: f.Name(), Type: f.Type().Descriptor, Value: ctx.reg(int(A))}}
		}
	}

	// ── invoke-range (0x74-0x78, 3rc) ──
	for _, op := range []byte{0x74, 0x75, 0x76, 0x77, 0x78} {
		op := op
		OpcodeHandlers[op] = func(ctx *DecompileCtx) []Node {
			return []Node{invokeRange(ctx, op)}
		}
	}

	// ── neg/not (0x7d-0x80, 12x) ──
	for _, pair := range []struct {
		op  byte
		sym string
	}{
		{0x7d, "-"}, {0x7e, "~"}, {0x7f, "-"}, {0x80, "-"},
	} {
		op, sym := pair.op, pair.sym
		OpcodeHandlers[op] = func(ctx *DecompileCtx) []Node {
			A, B := moveArgs(ctx) // 12x
			return []Node{&Assign{LHS: ctx.reg(int(A)), RHS: &BinaryOp{Op: sym, LHS: ctx.reg(int(B)), RHS: &IntConst{V: 0}}}}
		}
	}

	// ── int-to-X 转换 (0x81-0x8f, 12x) ──
	intToTypes := map[byte]string{
		0x81: "J", 0x82: "F", 0x83: "D",
		0x84: "I", 0x85: "F", 0x86: "D",
		0x87: "I", 0x88: "J", 0x89: "D",
		0x8a: "I", 0x8b: "J", 0x8c: "F",
		0x8d: "B", 0x8e: "C", 0x8f: "S",
	}
	for op, desc := range intToTypes {
		op, desc := op, desc
		OpcodeHandlers[op] = func(ctx *DecompileCtx) []Node {
			A, B := moveArgs(ctx)
			return []Node{&Assign{LHS: ctx.reg(int(A)), RHS: &Cast{Desc: desc, Expr: ctx.reg(int(B))}}}
		}
	}

	// ── long/float/double 算术 (0x9b-0xaf, 23x) ──
	type binop3 struct {
		op  byte
		sym string
	}
	arithmetic := []binop3{
		{0x9b, "+"}, {0x9c, "-"}, {0x9d, "*"}, {0x9e, "/"}, {0x9f, "%"},
		{0xa0, "&"}, {0xa1, "|"}, {0xa2, "^"},
		{0xa3, "<<"}, {0xa4, ">>"}, {0xa5, ">>>"},
		{0xa6, "+"}, {0xa7, "-"}, {0xa8, "*"}, {0xa9, "/"}, {0xaa, "%"},
		{0xab, "+"}, {0xac, "-"}, {0xad, "*"}, {0xae, "/"}, {0xaf, "%"},
	}
	for _, b := range arithmetic {
		op, sym := b.op, b.sym
		OpcodeHandlers[op] = func(ctx *DecompileCtx) []Node {
			A, B, C := binop23xArgs(ctx)
			return []Node{&Assign{LHS: ctx.reg(int(A)), RHS: &BinaryOp{Op: sym, LHS: ctx.reg(int(B)), RHS: ctx.reg(int(C))}}}
		}
	}

	// ── 低频操作码语义化(0xfa-0xff) ──
	// invoke-polymorphic(45cc/4rcc):B = MethodHandle/VarHandle 方法索引,
	// 名字即语义(invoke/invokeExact/get/set...),H = 实际调用原型;
	// 渲染为 receiver.name(args),返回类型取 H,move-result 可正常消费。
	OpcodeHandlers[0xfa] = func(ctx *DecompileCtx) []Node {
		return []Node{invokePolymorphic(ctx, false)}
	}
	OpcodeHandlers[0xfb] = func(ctx *DecompileCtx) []Node {
		return []Node{invokePolymorphic(ctx, true)}
	}
	// invoke-custom(0xfc/0xfd):BBBB = call_site_idx;经 map_list →
	// call_site_item 解析出函数接口方法名与原型,渲染真实调用形态。
	OpcodeHandlers[0xfc] = func(ctx *DecompileCtx) []Node {
		return []Node{invokeCustom(ctx, false)}
	}
	OpcodeHandlers[0xfd] = func(ctx *DecompileCtx) []Node {
		return []Node{invokeCustom(ctx, true)}
	}
	// const-method-handle(21c):BBBB = method_handle_idx
	OpcodeHandlers[0xfe] = func(ctx *DecompileCtx) []Node {
		A := ctx.CurrentAA()
		B := uint32(ctx.nextUnit(1))
		return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: &MethodHandleConst{Idx: B}}}
	}
	// const-method-type(21c):BBBB = proto_idx(可完整解析)
	OpcodeHandlers[0xff] = func(ctx *DecompileCtx) []Node {
		A := ctx.CurrentAA()
		B := uint32(ctx.nextUnit(1))
		return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: &MethodTypeConst{Desc: protoText(ctx.DEX.GetPrototype(B))}}}
	}
}

// invokePolymorphic 构造 invoke-polymorphic 节点(45cc/4rcc)。
func invokePolymorphic(ctx *DecompileCtx, ranged bool) *Invoke {
	u := ctx.currentUnit()
	mi := uint32(ctx.nextUnit(1))
	protoIdx := uint32(ctx.nextUnit(3))
	var args []Node
	if ranged {
		count := int((u >> 8) & 0xff)
		start := int(ctx.nextUnit(2))
		for i := 0; i < count; i++ {
			args = append(args, ctx.reg(start+i))
		}
	} else {
		count := int((u >> 12) & 0xf)
		ru := ctx.nextUnit(2)
		for i := 0; i < count && i < 5; i++ {
			args = append(args, ctx.reg(regUnitNibble(ru, i)))
		}
	}
	m := ctx.DEX.GetMethod(mi)
	ret := "Ljava/lang/Object;"
	if proto := ctx.DEX.GetPrototype(protoIdx); proto != nil {
		ret = proto.ReturnType().Descriptor
	}
	this := Node(nil)
	rest := args
	if len(rest) > 0 {
		this = rest[0]
		rest = rest[1:]
	}
	inv := &Invoke{
		Type:    InvokeVirtual,
		Owner:   m.Cls(),
		Name:    m.Name(),
		Proto:   "(?)",
		RetType: ret,
		Args:    rest,
		This:    this,
	}
	ctx.LastInvoke = inv
	return inv
}

// invokeCustom 构造 invoke-custom 节点。
// call_site_idx 可解析时渲染为 `函数接口类型.方法名(实参)`(call_site_item
// 的方法名/原型即被调用形态);表缺失(合成 fixture/畸形)回退 siteN 伪调用。
func invokeCustom(ctx *DecompileCtx, ranged bool) *Invoke {
	u := ctx.currentUnit()
	idx := uint32(ctx.nextUnit(1))
	var args []Node
	if ranged {
		count := int((u >> 8) & 0xff)
		start := int(ctx.nextUnit(2))
		for i := 0; i < count; i++ {
			args = append(args, ctx.reg(start+i))
		}
	} else {
		count := int((u >> 12) & 0xf)
		ru := ctx.nextUnit(2)
		for i := 0; i < count && i < 5; i++ {
			args = append(args, ctx.reg(regUnitNibble(ru, i)))
		}
	}
	inv := &Invoke{
		Type:    InvokeStatic,
		Owner:   "CallSite",
		Name:    "site" + itoa(int(idx)),
		Proto:   "(?)",
		RetType: "Ljava/lang/Object;",
		Args:    args,
	}
	if cs := ctx.DEX.GetCallSite(idx); cs != nil {
		inv.Name = cs.Name
		if cs.Proto != nil {
			inv.Owner = JavaType(cs.Proto.ReturnType().Descriptor)
			inv.RetType = cs.Proto.ReturnType().Descriptor
		}
	}
	ctx.LastInvoke = inv
	return inv
}

// protoText 原型 → "(int, String) -> void" 形态文本。
func protoText(p *dex.Prototype) string {
	if p == nil {
		return "?"
	}
	params := p.Parameters()
	names := make([]string, 0, len(params))
	for _, pt := range params {
		names = append(names, JavaType(pt.Descriptor))
	}
	return "(" + joinArgs(names) + ") -> " + JavaType(p.ReturnType().Descriptor)
}

// regUnitNibble 取 regUnit 的第 i 个 4-bit 寄存器(0-4)。
func regUnitNibble(regUnit uint16, i int) int {
	switch i {
	case 0:
		return int(regUnit & 0xf)
	case 1:
		return int((regUnit >> 4) & 0xf)
	case 2:
		return int((regUnit >> 8) & 0xf)
	case 3:
		return int((regUnit >> 12) & 0xf)
	default:
		return 0
	}
}

// invokeRange 构造 invoke/range 节点(3rc: AA|op BBBB CCCC)。
func invokeRange(ctx *DecompileCtx, op byte) *Invoke {
	u := ctx.currentUnit()
	count := int((u >> 8) & 0xff)
	methodIdx := uint32(ctx.nextUnit(1))
	startReg := int(ctx.nextUnit(2))
	m := ctx.DEX.GetMethod(methodIdx)
	invType := InvokeVirtual
	switch op {
	case 0x75:
		invType = InvokeSuper
	case 0x76:
		invType = InvokeDirect
	case 0x77:
		invType = InvokeStatic
	case 0x78:
		invType = InvokeInterface
	}
	proto := renderProtoText(m.Prototype())
	args := make([]Node, 0, count)
	for i := 0; i < count; i++ {
		args = append(args, ctx.reg(startReg+i))
	}
	this := Node(nil)
	if len(args) > 0 {
		this = args[0]
		args = args[1:]
	}
	inv := &Invoke{
		Type:    invType,
		Owner:   m.Cls(),
		Name:    m.Name(),
		Proto:   proto,
		RetType: m.Prototype().ReturnType().Descriptor,
		Args:    args,
		This:    this,
	}
	if m.Name() == "<init>" && ctx.isThis(this) {
		if m.Cls() == ctx.Method.Cls() {
			inv.CtorCall = "this"
		} else {
			inv.CtorCall = "super"
		}
	}
	ctx.LastInvoke = inv
	return inv
}
