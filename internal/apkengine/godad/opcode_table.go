package godad

// opcode_table.go — Dalvik 操作码 → IR 节点映射表。
// 对齐 androguard/opcode_ins.py 的 Op 类。
// 仅实现 Basic 级别:常见操作码(构造器/字段/返回/简单调用/算术/比较)。
// 未知/未实现的操作码产生 Unresolved 节点,Writer 输出注释标记。

import "finduai/internal/apkengine/dex"

// OpcodeHandler 操作码处理函数:给定指令上下文,返回 IR 节点序列。
type OpcodeHandler func(ctx *DecompileCtx) []Node

// Unresolved 未实现操作码的占位节点。
type Unresolved struct{ Op string }

func (u *Unresolved) Java() string        { return "/* " + u.Op + " */" }
func (u *Unresolved) UsedVars() []int     { return nil }
func (u *Unresolved) HasSideEffect() bool { return false }

// ────────────────────────── 操作码表 ──────────────────────────

// TermKind 基本块终结类型。
type TermKind uint8

const (
	TermFall   TermKind = iota // 顺序落入下一块
	TermGoto                   // 无条件跳转
	TermIf                     // 条件分支
	TermSwitch                 // switch(近似:按顺序继续)
	TermReturn                 // 返回
	TermThrow                  // 抛出
)

// DecompileCtx 反编译上下文:持有当前方法的 DEX 和指令位置。
type DecompileCtx struct {
	DEX        *dex.DEX
	Method     *dex.Method
	Code       *dex.CodeHeader
	PC         int            // 当前指令偏移(unit 数)
	LastInvoke *Invoke        // 最近一条 invoke(move-result 消费其结果)
	Register   map[int]Node   // 寄存器 → IR 节点映射(简化:不建全量映射,按需读)
	ParamNames map[int]string // 参数寄存器 → Java 名("this"/"arg0"...)

	// 终结指令信息(CFG 构建消费)
	Term       TermKind
	TermTarget int  // 跳转目标(unit 数),仅 TermGoto/TermIf
	TermCond   Node // 条件表达式,仅 TermIf
}

// setTerm 记录终结指令。
func (ctx *DecompileCtx) setTerm(kind TermKind, target int, cond Node) {
	ctx.Term = kind
	ctx.TermTarget = target
	ctx.TermCond = cond
}

// resetTerm 每条指令处理前清空终结信息。
func (ctx *DecompileCtx) resetTerm() {
	ctx.Term = TermFall
	ctx.TermTarget = -1
	ctx.TermCond = nil
}

// reg 构造寄存器引用;参数寄存器映射为 this/argN。
func (ctx *DecompileCtx) reg(r int) Node {
	if name, ok := ctx.ParamNames[r]; ok {
		return &Var{Reg: r, Name: name}
	}
	return &Var{Reg: r}
}

// OpcodeHandlers 操作码 → 处理函数。索引为操作码字节值。
var OpcodeHandlers [256]OpcodeHandler

func init() {
	// nop (0x00) — 无操作
	OpcodeHandlers[0x00] = func(ctx *DecompileCtx) []Node { return nil }

	// move vA, vB (0x01)
	OpcodeHandlers[0x01] = func(ctx *DecompileCtx) []Node {
		A, B := moveArgs(ctx)
		return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: &Var{Reg: int(B)}}}
	}

	// move/from16 vAA, vBBBB (0x02)
	OpcodeHandlers[0x02] = func(ctx *DecompileCtx) []Node {
		A, B := moveFrom16Args(ctx)
		return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: &Var{Reg: int(B)}}}
	}

	// move/16 vAAAA, vBBBB (0x03)
	OpcodeHandlers[0x03] = func(ctx *DecompileCtx) []Node {
		A, B := move16Args(ctx)
		return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: &Var{Reg: int(B)}}}
	}

	// move-result vAA (0x0a) / move-result-wide (0x0b) / move-result-object (0x0c)
	for _, op := range []byte{0x0a, 0x0b, 0x0c} {
		OpcodeHandlers[op] = func(ctx *DecompileCtx) []Node {
			A := ctx.CurrentAA()
			if ctx.LastInvoke != nil {
				inv := ctx.LastInvoke
				inv.ResultUsed = true // 抑制 invoke 的独立语句输出
				ctx.LastInvoke = nil
				return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: inv}}
			}
			return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: &Unresolved{Op: "move-result"}}}
		}
	}

	// move-exception vAA (0x0d) — catch 入口取异常对象
	OpcodeHandlers[0x0d] = func(ctx *DecompileCtx) []Node {
		A := ctx.CurrentAA()
		return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: &MoveException{}}}
	}

	// return-void (0x0e)
	OpcodeHandlers[0x0e] = func(ctx *DecompileCtx) []Node {
		ctx.setTerm(TermReturn, -1, nil)
		return []Node{&ReturnVal{Value: nil}}
	}

	// return vAA (0x0f)
	OpcodeHandlers[0x0f] = func(ctx *DecompileCtx) []Node {
		A := ctx.CurrentAA()
		ctx.setTerm(TermReturn, -1, nil)
		return []Node{&ReturnVal{Value: ctx.reg(int(A))}}
	}

	// return-wide vAA (0x10)
	OpcodeHandlers[0x10] = func(ctx *DecompileCtx) []Node {
		A := ctx.CurrentAA()
		ctx.setTerm(TermReturn, -1, nil)
		return []Node{&ReturnVal{Value: ctx.reg(int(A))}}
	}

	// return-object vAA (0x11)
	OpcodeHandlers[0x11] = func(ctx *DecompileCtx) []Node {
		A := ctx.CurrentAA()
		ctx.setTerm(TermReturn, -1, nil)
		return []Node{&ReturnVal{Value: ctx.reg(int(A))}}
	}

	// const/4 vA, #+B (0x12)
	OpcodeHandlers[0x12] = func(ctx *DecompileCtx) []Node {
		A, B := const4Args(ctx)
		return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: &IntConst{V: int64(B)}}}
	}

	// const/16 vAA, #+BBBB (0x13)
	OpcodeHandlers[0x13] = func(ctx *DecompileCtx) []Node {
		A, B := const16Args(ctx)
		return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: &IntConst{V: int64(B)}}}
	}

	// const vAA, #+BBBBBBBB (0x14)
	OpcodeHandlers[0x14] = func(ctx *DecompileCtx) []Node {
		A, B := const32Args(ctx)
		return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: &IntConst{V: int64(B)}}}
	}

	// const-string vAA, string@BBBB (0x1a)
	OpcodeHandlers[0x1a] = func(ctx *DecompileCtx) []Node {
		A, B := constStringArgs(ctx)
		str := ctx.DEX.GetString(uint32(B))
		return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: &StringConst{V: str}}}
	}

	// const-class vAA, type@BBBB (0x1c)
	OpcodeHandlers[0x1c] = func(ctx *DecompileCtx) []Node {
		A, B := constClassArgs(ctx)
		desc := ctx.DEX.GetType(uint32(B)).Descriptor
		return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: &ClassConst{Desc: desc}}}
	}

	// monitor-enter vAA (0x1d)
	OpcodeHandlers[0x1d] = func(ctx *DecompileCtx) []Node {
		A := ctx.CurrentAA()
		return []Node{&Unresolved{Op: "monitor-enter v" + itoa(int(A))}}
	}

	// monitor-exit vAA (0x1e)
	OpcodeHandlers[0x1e] = func(ctx *DecompileCtx) []Node {
		A := ctx.CurrentAA()
		return []Node{&Unresolved{Op: "monitor-exit v" + itoa(int(A))}}
	}

	// check-cast vAA, type@BBBB (0x1f)
	OpcodeHandlers[0x1f] = func(ctx *DecompileCtx) []Node {
		A, B := checkCastArgs(ctx)
		desc := ctx.DEX.GetType(uint32(B)).Descriptor
		return []Node{&CheckCast{Desc: desc, Expr: &Var{Reg: int(A)}}}
	}

	// instance-of vA, vB, type@CCCC (0x20)
	OpcodeHandlers[0x20] = func(ctx *DecompileCtx) []Node {
		A, _, _ := instanceOfArgs(ctx)
		// 简化:返回赋值 vA = (vB instanceof Type)
		return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: &Unresolved{Op: "instance-of"}}}
	}

	// array-length vA, vB (0x21)
	OpcodeHandlers[0x21] = func(ctx *DecompileCtx) []Node {
		A, B := arrayLengthArgs(ctx)
		return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: &Unresolved{Op: "array-length " + (&Var{Reg: int(B)}).Java()}}}
	}

	// new-instance vAA, type@BBBB (0x22)
	OpcodeHandlers[0x22] = func(ctx *DecompileCtx) []Node {
		A, B := newInstanceArgs(ctx)
		desc := ctx.DEX.GetType(uint32(B)).Descriptor
		return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: &NewObj{Desc: desc}}}
	}

	// new-array vA, vB, type@CCCC (0x23)
	OpcodeHandlers[0x23] = func(ctx *DecompileCtx) []Node {
		A, B, C := newArrayArgs(ctx)
		desc := ctx.DEX.GetType(uint32(C)).Descriptor
		return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: &NewArray{Size: &Var{Reg: int(B)}, Type: desc}}}
	}

	// iget vA, vB, field@CCCC (0x52)
	OpcodeHandlers[0x52] = func(ctx *DecompileCtx) []Node {
		A, B, C := fieldArgs(ctx)
		f := ctx.DEX.GetField(uint32(C))
		return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: &IGet{Owner: f.Cls(), Name: f.Name(), Type: f.Type().Descriptor, Obj: &Var{Reg: int(B)}}}}
	}

	// iget-wide vA, vB, field@CCCC (0x53)
	OpcodeHandlers[0x53] = OpcodeHandlers[0x52]

	// iget-object vA, vB, field@CCCC (0x54)
	OpcodeHandlers[0x54] = OpcodeHandlers[0x52]

	// iget-boolean vA, vB, field@CCCC (0x55)
	OpcodeHandlers[0x55] = OpcodeHandlers[0x52]

	// iget-byte vA, vB, field@CCCC (0x56)
	OpcodeHandlers[0x56] = OpcodeHandlers[0x52]

	// iget-char vA, vB, field@CCCC (0x57)
	OpcodeHandlers[0x57] = OpcodeHandlers[0x52]

	// iget-short vA, vB, field@CCCC (0x58)
	OpcodeHandlers[0x58] = OpcodeHandlers[0x52]

	// iput vA, vB, field@CCCC (0x59)
	OpcodeHandlers[0x59] = func(ctx *DecompileCtx) []Node {
		A, B, C := fieldArgs(ctx)
		f := ctx.DEX.GetField(uint32(C))
		return []Node{&IPut{Owner: f.Cls(), Name: f.Name(), Type: f.Type().Descriptor, Obj: &Var{Reg: int(B)}, Value: &Var{Reg: int(A)}}}
	}

	// iput-wide vA, vB, field@CCCC (0x5a)
	OpcodeHandlers[0x5a] = OpcodeHandlers[0x59]

	// iput-object vA, vB, field@CCCC (0x5b)
	OpcodeHandlers[0x5b] = OpcodeHandlers[0x59]

	// iput-boolean vA, vB, field@CCCC (0x5c)
	OpcodeHandlers[0x5c] = OpcodeHandlers[0x59]

	// iput-byte vA, vB, field@CCCC (0x5d)
	OpcodeHandlers[0x5d] = OpcodeHandlers[0x59]

	// iput-char vA, vB, field@CCCC (0x5e)
	OpcodeHandlers[0x5e] = OpcodeHandlers[0x59]

	// iput-short vA, vB, field@CCCC (0x5f)
	OpcodeHandlers[0x5f] = OpcodeHandlers[0x59]

	// sget vAA, field@BBBB (0x60)
	OpcodeHandlers[0x60] = func(ctx *DecompileCtx) []Node {
		A, B := sfieldArgs(ctx)
		f := ctx.DEX.GetField(uint32(B))
		return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: &SGet{Owner: f.Cls(), Name: f.Name(), Type: f.Type().Descriptor}}}
	}

	// sget-wide vAA, field@BBBB (0x61)
	OpcodeHandlers[0x61] = OpcodeHandlers[0x60]

	// sget-object vAA, field@BBBB (0x62)
	OpcodeHandlers[0x62] = OpcodeHandlers[0x60]

	// sget-boolean vAA, field@BBBB (0x63)
	OpcodeHandlers[0x63] = OpcodeHandlers[0x60]

	// sput vAA, field@BBBB (0x67)
	OpcodeHandlers[0x67] = func(ctx *DecompileCtx) []Node {
		A, B := sfieldArgs(ctx)
		f := ctx.DEX.GetField(uint32(B))
		return []Node{&SPut{Owner: f.Cls(), Name: f.Name(), Type: f.Type().Descriptor, Value: &Var{Reg: int(A)}}}
	}

	// sput-wide vAA, field@BBBB (0x68)
	OpcodeHandlers[0x68] = OpcodeHandlers[0x67]

	// sput-object vAA, field@BBBB (0x69)
	OpcodeHandlers[0x69] = OpcodeHandlers[0x67]

	// sput-boolean vAA, field@BBBB (0x6a)
	OpcodeHandlers[0x6a] = OpcodeHandlers[0x67]

	// invoke-virtual {vC..vG}, method@BBBB (0x6e)
	OpcodeHandlers[0x6e] = func(ctx *DecompileCtx) []Node {
		return []Node{invokeVirtual(ctx)}
	}

	// invoke-super {vC..vG}, method@BBBB (0x6f)
	OpcodeHandlers[0x6f] = func(ctx *DecompileCtx) []Node {
		m := invokeMethod(ctx)
		m.Type = InvokeSuper
		ctx.LastInvoke = m
		return []Node{m}
	}

	// invoke-direct {vC..vG}, method@BBBB (0x70)
	OpcodeHandlers[0x70] = func(ctx *DecompileCtx) []Node {
		m := invokeMethod(ctx)
		m.Type = InvokeDirect
		ctx.LastInvoke = m
		return []Node{m}
	}

	// invoke-static {vC..vG}, method@BBBB (0x71)
	OpcodeHandlers[0x71] = func(ctx *DecompileCtx) []Node {
		m := invokeMethod(ctx)
		m.Type = InvokeStatic
		m.This = nil
		ctx.LastInvoke = m
		return []Node{m}
	}

	// invoke-interface {vC..vG}, method@BBBB (0x72)
	OpcodeHandlers[0x72] = func(ctx *DecompileCtx) []Node {
		m := invokeMethod(ctx)
		m.Type = InvokeInterface
		ctx.LastInvoke = m
		return []Node{m}
	}

	// neg-int vA, vB (0x7b)
	OpcodeHandlers[0x7b] = func(ctx *DecompileCtx) []Node {
		A, B := binop12xArgs(ctx)
		return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: &BinaryOp{Op: "-", LHS: &IntConst{V: 0}, RHS: &Var{Reg: int(B)}}}}
	}

	// not-int vA, vB (0x7c)
	OpcodeHandlers[0x7c] = func(ctx *DecompileCtx) []Node {
		A, B := binop12xArgs(ctx)
		return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: &BinaryOp{Op: "~", LHS: &Var{Reg: int(B)}, RHS: &IntConst{V: 0}}}}
	}

	// add-int vAA, vBB, vCC (0x90)
	OpcodeHandlers[0x90] = func(ctx *DecompileCtx) []Node {
		A, B, C := binop23xArgs(ctx)
		return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: &BinaryOp{Op: "+", LHS: ctx.reg(int(B)), RHS: ctx.reg(int(C))}}}
	}

	// sub-int vAA, vBB, vCC (0x91)
	OpcodeHandlers[0x91] = func(ctx *DecompileCtx) []Node {
		A, B, C := binop23xArgs(ctx)
		return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: &BinaryOp{Op: "-", LHS: ctx.reg(int(B)), RHS: ctx.reg(int(C))}}}
	}

	// mul-int vAA, vBB, vCC (0x92)
	OpcodeHandlers[0x92] = func(ctx *DecompileCtx) []Node {
		A, B, C := binop23xArgs(ctx)
		return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: &BinaryOp{Op: "*", LHS: ctx.reg(int(B)), RHS: ctx.reg(int(C))}}}
	}

	// div-int vAA, vBB, vCC (0x93)
	OpcodeHandlers[0x93] = func(ctx *DecompileCtx) []Node {
		A, B, C := binop23xArgs(ctx)
		return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: &BinaryOp{Op: "/", LHS: ctx.reg(int(B)), RHS: ctx.reg(int(C))}}}
	}

	// rem-int vAA, vBB, vCC (0x94)
	OpcodeHandlers[0x94] = func(ctx *DecompileCtx) []Node {
		A, B, C := binop23xArgs(ctx)
		return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: &BinaryOp{Op: "%", LHS: ctx.reg(int(B)), RHS: ctx.reg(int(C))}}}
	}

	// and-int vAA, vBB, vCC (0x95)
	OpcodeHandlers[0x95] = func(ctx *DecompileCtx) []Node {
		A, B, C := binop23xArgs(ctx)
		return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: &BinaryOp{Op: "&", LHS: ctx.reg(int(B)), RHS: ctx.reg(int(C))}}}
	}

	// or-int vAA, vBB, vCC (0x96)
	OpcodeHandlers[0x96] = func(ctx *DecompileCtx) []Node {
		A, B, C := binop23xArgs(ctx)
		return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: &BinaryOp{Op: "|", LHS: ctx.reg(int(B)), RHS: ctx.reg(int(C))}}}
	}

	// xor-int vAA, vBB, vCC (0x97)
	OpcodeHandlers[0x97] = func(ctx *DecompileCtx) []Node {
		A, B, C := binop23xArgs(ctx)
		return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: &BinaryOp{Op: "^", LHS: ctx.reg(int(B)), RHS: ctx.reg(int(C))}}}
	}

	// shl-int vAA, vBB, vCC (0x98)
	OpcodeHandlers[0x98] = func(ctx *DecompileCtx) []Node {
		A, B, C := binop23xArgs(ctx)
		return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: &BinaryOp{Op: "<<", LHS: ctx.reg(int(B)), RHS: ctx.reg(int(C))}}}
	}

	// shr-int vAA, vBB, vCC (0x99)
	OpcodeHandlers[0x99] = func(ctx *DecompileCtx) []Node {
		A, B, C := binop23xArgs(ctx)
		return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: &BinaryOp{Op: ">>", LHS: ctx.reg(int(B)), RHS: ctx.reg(int(C))}}}
	}

	// ushr-int vAA, vBB, vCC (0x9a)
	OpcodeHandlers[0x9a] = func(ctx *DecompileCtx) []Node {
		A, B, C := binop23xArgs(ctx)
		return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: &BinaryOp{Op: ">>>", LHS: ctx.reg(int(B)), RHS: ctx.reg(int(C))}}}
	}

	// /2addr 变体(0xb0-0xcf):vA op= vB。按 opcode 段映射运算符。
	binop2addr := map[byte]string{
		0xb0: "+", 0xb1: "-", 0xb2: "*", 0xb3: "/", 0xb4: "%", 0xb5: "&", 0xb6: "|", 0xb7: "^",
		0xb8: "<<", 0xb9: ">>", 0xba: ">>>",
		0xbb: "+", 0xbc: "-", 0xbd: "*", 0xbe: "/", 0xbf: "%", 0xc0: "&", 0xc1: "|", 0xc2: "^",
		0xc3: "<<", 0xc4: ">>", 0xc5: ">>>",
		0xc6: "+", 0xc7: "-", 0xc8: "*", 0xc9: "/", 0xca: "%",
		0xcb: "+", 0xcc: "-", 0xcd: "*", 0xce: "/", 0xcf: "%",
	}
	for op, sym := range binop2addr {
		sym := sym
		OpcodeHandlers[op] = func(ctx *DecompileCtx) []Node {
			A, B := binop12xArgs(ctx)
			return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: &BinaryOp{Op: sym, LHS: ctx.reg(int(A)), RHS: ctx.reg(int(B))}}}
		}
	}

	// /lit16 变体(0xd0-0xd7):vA op= #+BBBB
	binopLit16 := map[byte]string{
		0xd0: "+", 0xd1: "-", 0xd2: "*", 0xd3: "/", 0xd4: "%", 0xd5: "&", 0xd6: "|", 0xd7: "^",
	}
	for op, sym := range binopLit16 {
		sym := sym
		OpcodeHandlers[op] = func(ctx *DecompileCtx) []Node {
			u := ctx.currentUnit()
			A := byte((u >> 8) & 0xf)
			B := byte((u >> 12) & 0xf)
			lit := int16(ctx.nextUnit(1))
			return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: &BinaryOp{Op: sym, LHS: ctx.reg(int(B)), RHS: &IntConst{V: int64(lit)}}}}
		}
	}

	// /lit8 变体(0xd8-0xe2):vAA, vBB, #+CC(有符号 8 位)
	binopLit8 := map[byte]string{
		0xd8: "+", 0xd9: "-", 0xda: "*", 0xdb: "/", 0xdc: "%", 0xdd: "&", 0xde: "|", 0xdf: "^",
		0xe0: "<<", 0xe1: ">>", 0xe2: ">>>",
	}
	for op, sym := range binopLit8 {
		sym := sym
		OpcodeHandlers[op] = func(ctx *DecompileCtx) []Node {
			u := ctx.currentUnit()
			A := byte(u >> 8)
			B := byte(ctx.nextUnit(1) & 0xff)
			lit := int8(byte(ctx.nextUnit(1) >> 8))
			rhs := Node(&IntConst{V: int64(lit)})
			if sym == "<<" || sym == ">>" || sym == ">>>" {
				rhs = &IntConst{V: int64(byte(lit))}
			}
			return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: &BinaryOp{Op: sym, LHS: ctx.reg(int(B)), RHS: rhs}}}
		}
	}

	// cmpl-float (0x2d), cmpg-float (0x2e), cmpl-double (0x2f), cmpg-double (0x30), cmp-long (0x31)
	for _, op := range []byte{0x2d, 0x2e, 0x2f, 0x30, 0x31} {
		opcodeHandlersCmp(op)
	}

	// if-eq ... if-ne ... (0x32-0x3d) — 条件分支,简化为注释标记
	for op := byte(0x32); op <= 0x3d; op++ {
		opcodeHandlersIf(op)
	}

	// goto +AA (0x28) — 无条件跳转
	OpcodeHandlers[0x28] = func(ctx *DecompileCtx) []Node {
		off := int(int8(ctx.CurrentAA()))
		ctx.setTerm(TermGoto, ctx.PC+off, nil)
		return nil
	}

	// goto/16 +BBBB (0x29)
	OpcodeHandlers[0x29] = func(ctx *DecompileCtx) []Node {
		off := int(int16(ctx.nextUnit(1)))
		ctx.setTerm(TermGoto, ctx.PC+off, nil)
		return nil
	}

	// goto/32 +BBBBBBBB (0x2a)
	OpcodeHandlers[0x2a] = func(ctx *DecompileCtx) []Node {
		off := int(int32(ctx.nextUnit(1)) | int32(ctx.nextUnit(2))<<16)
		ctx.setTerm(TermGoto, ctx.PC+off, nil)
		return nil
	}

	// packed-switch (0x2b) / sparse-switch (0x2c) — 近似:按顺序继续,输出注释
	OpcodeHandlers[0x2b] = func(ctx *DecompileCtx) []Node {
		ctx.setTerm(TermSwitch, -1, nil)
		return []Node{&Unresolved{Op: "packed-switch (按顺序近似)"}}
	}
	OpcodeHandlers[0x2c] = func(ctx *DecompileCtx) []Node {
		ctx.setTerm(TermSwitch, -1, nil)
		return []Node{&Unresolved{Op: "sparse-switch (按顺序近似)"}}
	}

	// fill-array-data vAA, +BBBBBBBB (0x26) — 数据引用,非分支
	OpcodeHandlers[0x26] = func(ctx *DecompileCtx) []Node {
		return []Node{&Unresolved{Op: "fill-array-data"}}
	}

	// throw vAA (0x27)
	OpcodeHandlers[0x27] = func(ctx *DecompileCtx) []Node {
		A := ctx.CurrentAA()
		ctx.setTerm(TermThrow, -1, nil)
		return []Node{&ThrowStmt{Value: ctx.reg(int(A))}}
	}
}

func opcodeHandlersCmp(op byte) {
	OpcodeHandlers[op] = func(ctx *DecompileCtx) []Node {
		A, B, C := cmpArgs(ctx)
		opStr := "=="
		switch op {
		case 0x2d, 0x2f:
			opStr = "<=" // cmpl: -1 if either NaN
		case 0x2e, 0x30:
			opStr = ">=" // cmpg: 1 if either NaN
		case 0x31:
			opStr = "<" // cmp-long
		}
		return []Node{&Assign{LHS: &Var{Reg: int(A)}, RHS: &Compare{Op: opStr, LHS: &Var{Reg: int(B)}, RHS: &Var{Reg: int(C)}}}}
	}
}

func opcodeHandlersIf(op byte) {
	OpcodeHandlers[op] = func(ctx *DecompileCtx) []Node {
		A, _, off := ifArgs(ctx)
		var cond Node
		reg := func(r int) Node { return ctx.reg(r) }
		switch op {
		case 0x32: // if-eq vA, vB
			_, B, _ := ifArgs(ctx)
			cond = &Compare{Op: "==", LHS: reg(int(A)), RHS: reg(int(B))}
		case 0x33: // if-ne
			_, B, _ := ifArgs(ctx)
			cond = &Compare{Op: "!=", LHS: reg(int(A)), RHS: reg(int(B))}
		case 0x34: // if-lt
			_, B, _ := ifArgs(ctx)
			cond = &Compare{Op: "<", LHS: reg(int(A)), RHS: reg(int(B))}
		case 0x35: // if-ge
			_, B, _ := ifArgs(ctx)
			cond = &Compare{Op: ">=", LHS: reg(int(A)), RHS: reg(int(B))}
		case 0x36: // if-gt
			_, B, _ := ifArgs(ctx)
			cond = &Compare{Op: ">", LHS: reg(int(A)), RHS: reg(int(B))}
		case 0x37: // if-le
			_, B, _ := ifArgs(ctx)
			cond = &Compare{Op: "<=", LHS: reg(int(A)), RHS: reg(int(B))}
		case 0x38: // if-eqz vAA
			cond = &Compare{Op: "==", LHS: ctx.reg(int(ctx.CurrentAA())), RHS: &IntConst{V: 0}}
		case 0x39: // if-nez
			cond = &Compare{Op: "!=", LHS: ctx.reg(int(ctx.CurrentAA())), RHS: &IntConst{V: 0}}
		case 0x3a: // if-ltz
			cond = &Compare{Op: "<", LHS: ctx.reg(int(ctx.CurrentAA())), RHS: &IntConst{V: 0}}
		case 0x3b: // if-gez
			cond = &Compare{Op: ">=", LHS: ctx.reg(int(ctx.CurrentAA())), RHS: &IntConst{V: 0}}
		case 0x3c: // if-gtz
			cond = &Compare{Op: ">", LHS: ctx.reg(int(ctx.CurrentAA())), RHS: &IntConst{V: 0}}
		case 0x3d: // if-lez
			cond = &Compare{Op: "<=", LHS: ctx.reg(int(ctx.CurrentAA())), RHS: &IntConst{V: 0}}
		}
		ctx.setTerm(TermIf, ctx.PC+int(off), cond)
		return nil
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	for n > 0 {
		s = string(byte('0'+n%10)) + s
		n /= 10
	}
	if neg {
		s = "-" + s
	}
	return s
}
