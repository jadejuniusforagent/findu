package godad

// decompiler.go — Go DAD 反编译编排:遍历方法指令,调用 opcode handlers,
// 输出 IR 节点序列,Writer 转 Java 源码。

import (
	"fmt"
	"strings"

	"finduai/internal/apkengine/dex"
)

// Decompiler Go DAD 反编译器。
type Decompiler struct {
	dex *dex.DEX
}

// New 创建反编译器。
func New(d *dex.DEX) *Decompiler {
	return &Decompiler{dex: d}
}

// DecompileClass 反编译整个类,输出 Java 源码。
func (dec *Decompiler) DecompileClass(class *dex.Class) (out string) {
	defer func() {
		if r := recover(); r != nil {
			out = fmt.Sprintf("/* class decompilation aborted (malformed dex?): %v */\n", r)
		}
	}()
	var sb strings.Builder
	// 类头:access + class/interface/enum 关键字 + 名 + 父类
	flags := class.ClassAccessFlags()
	access := classAccessNames(flags)
	keyword := classKeyword(flags)
	super := class.Super()
	sb.WriteString(strings.TrimSpace(fmt.Sprintf("%s %s %s extends %s {", access, keyword, shortName(class.Fullname()), shortName(super))) + "\n")

	// 字段
	for _, f := range class.Fields() {
		fAccess := fieldAccessNames(f.AccessFlags)
		sb.WriteString(fmt.Sprintf("    %s %s %s;\n", fAccess, shortName(f.Type().Descriptor), f.Name()))
	}

	// 方法
	for _, m := range class.Methods() {
		sb.WriteString("\n")
		sb.WriteString(dec.DecompileMethod(m))
	}

	sb.WriteString("}\n")
	return sb.String()
}

// DecompileMethod 反编译单个方法。
// 流水线:指令 → 基本块(CFG) → 结构化(if/else/while) → 构造器折叠 → Java 输出。
// 底层 DEX 读取为速度不做逐次边界检查(与引擎约定一致):
// 畸形输入在此 recover,降级为注释输出,不影响同类其他方法。
func (dec *Decompiler) DecompileMethod(m *dex.Method) (out string) {
	defer func() {
		if r := recover(); r != nil {
			out = fmt.Sprintf("    /* decompilation aborted (malformed dex?): %v */\n", r)
		}
	}()
	code := m.Code()
	if code == nil || len(code.Insns) == 0 {
		// abstract/native 方法
		mAccess := methodAccessNames(m.AccessFlags)
		return fmt.Sprintf("    %s %s %s { /* no body */ }\n", mAccess, shortName(m.Prototype().ReturnType().Descriptor), m.Name()+renderProto(m.Prototype()))
	}

	names := paramNames(m, code)
	paramTypes := paramTypeNames(m, code)
	blocks := dec.BuildCFG(m, code, names)
	tries := collectTries(m, code, blocks, dec.dex)
	// 块级到达定义分析:寄存器版本分裂(含嵌套定义,先于结构化改写块节点)
	versionRegs(blocks, names)
	nodes := StructureWithTries(blocks, tries)
	nodes = foldConstructors(nodes)
	applyParamNames(nodes, names)
	if declared := DeclareVars(nodes, names, paramTypes); declared != nil {
		nodes = declared
	}

	// Writer 输出 Java
	w := NewWriter()
	w.WriteMethod(m)
	for _, n := range nodes {
		w.WriteNode(n)
	}
	w.EndMethod()
	return w.String()
}

// foldConstructors 窥孔折叠:new-instance v0 + invoke-direct <init>(v0, args)
// 合并为 v0 = new Owner(args)。对齐 DAD 的构造器还原语义。
// 折叠产物放在 invoke 原位(位于参数定义之后),保证 `new Owner(v1)` 的
// 参数求值顺序与字节码一致(v1 = const 必须先于分配表达式)。
// 递归处理 if/while/switch/try 内嵌语句列表。
func foldConstructors(nodes []Node) []Node {
	for _, n := range nodes {
		switch v := n.(type) {
		case *IfStmt:
			v.Then = foldConstructors(v.Then)
			v.Else = foldConstructors(v.Else)
		case *WhileStmt:
			v.Body = foldConstructors(v.Body)
		case *SwitchStmt:
			for ci := range v.Cases {
				v.Cases[ci].Body = foldConstructors(v.Cases[ci].Body)
			}
		case *TryStmt:
			v.Body = foldConstructors(v.Body)
			for _, c := range v.Catches {
				c.Body = foldConstructors(c.Body)
			}
		}
	}
	foldedAt := map[int]*Assign{} // invoke 位置 → 折叠产物
	out := make([]Node, 0, len(nodes))
	for i := 0; i < len(nodes); i++ {
		if assign, ok := foldedAt[i]; ok {
			out = append(out, assign)
			continue
		}
		n := nodes[i]
		assign, ok := n.(*Assign)
		if !ok {
			out = append(out, n)
			continue
		}
		newObj, ok := assign.RHS.(*NewObj)
		if !ok || len(newObj.Args) > 0 {
			out = append(out, n)
			continue
		}
		lhs, ok := assign.LHS.(*Var)
		if !ok {
			out = append(out, n)
			continue
		}
		// 向后找 4 条内匹配的 <init> 调用
		folded := false
		for j := i + 1; j < len(nodes) && j <= i+4; j++ {
			inv, ok := nodes[j].(*Invoke)
			if !ok || inv.Name != "<init>" {
				continue
			}
			this, ok := inv.This.(*Var)
			if !ok || this.Reg != lhs.Reg {
				continue
			}
			newObj.Args = inv.Args
			foldedAt[j] = assign
			folded = true
			break
		}
		if !folded {
			out = append(out, n)
		}
	}
	return out
}

// ────────────────────────── 辅助 ──────────────────────────

// paramNames 计算参数寄存器 → Java 名映射。
// Dalvik:最后 ins 个寄存器是参数;非 static 方法第一个是 this。
func paramNames(m *dex.Method, code *dex.CodeHeader) map[int]string {
	out := map[int]string{}
	ins := int(code.InsSize)
	if ins == 0 {
		return out
	}
	first := int(code.RegistersSize) - ins
	isStatic := m.AccessFlags&0x8 != 0
	idx := 0
	if !isStatic {
		out[first] = "this"
		idx = 1
	}
	for i := idx; i < ins; i++ {
		out[first+i] = "arg" + itoa(i-idx)
	}
	return out
}

// applyParamNames 递归改写 IR 树中的 Var 名(参数寄存器 → this/argN)。
func applyParamNames(nodes []Node, names map[int]string) {
	if len(names) == 0 {
		return
	}
	for _, n := range nodes {
		rewriteNode(n, names)
	}
}

func rewriteNode(n Node, names map[int]string) {
	switch v := n.(type) {
	case *Var:
		// 读位置:this 与 argN 都可直接命名
		if nm, ok := names[v.Reg]; ok {
			v.Name = nm
		}
	case *Assign:
		// LHS 是写目标:this 不可赋值,跳过;其余参数寄存器可重命名
		if lv, ok := v.LHS.(*Var); ok {
			if nm, ok := names[lv.Reg]; ok && nm != "this" {
				lv.Name = nm
			}
		}
		rewriteNode(v.RHS, names)
	case *BinaryOp:
		rewriteNode(v.LHS, names)
		rewriteNode(v.RHS, names)
	case *Compare:
		rewriteNode(v.LHS, names)
		rewriteNode(v.RHS, names)
	case *NotExpr:
		rewriteNode(v.Expr, names)
	case *IGet:
		rewriteNode(v.Obj, names)
	case *IPut:
		rewriteNode(v.Obj, names)
		rewriteNode(v.Value, names)
	case *SPut:
		rewriteNode(v.Value, names)
	case *Invoke:
		if v.This != nil {
			rewriteNode(v.This, names)
		}
		for _, a := range v.Args {
			rewriteNode(a, names)
		}
	case *ReturnVal:
		if v.Value != nil {
			rewriteNode(v.Value, names)
		}
	case *CheckCast:
		rewriteNode(v.Expr, names)
	case *Cast:
		rewriteNode(v.Expr, names)
	case *ArrayGet:
		rewriteNode(v.Array, names)
		rewriteNode(v.Index, names)
	case *ArrayPut:
		rewriteNode(v.Array, names)
		rewriteNode(v.Index, names)
		rewriteNode(v.Value, names)
	case *FilledArray:
		for _, e := range v.Elems {
			rewriteNode(e, names)
		}
	case *NewArray:
		rewriteNode(v.Size, names)
	case *NewObj:
		for _, a := range v.Args {
			rewriteNode(a, names)
		}
	case *IfStmt:
		rewriteNode(v.Cond, names)
		for _, s := range v.Then {
			rewriteNode(s, names)
		}
		for _, s := range v.Else {
			rewriteNode(s, names)
		}
	case *WhileStmt:
		rewriteNode(v.Cond, names)
		for _, s := range v.Body {
			rewriteNode(s, names)
		}
	case *SwitchStmt:
		rewriteNode(v.Expr, names)
		for ci := range v.Cases {
			for _, s := range v.Cases[ci].Body {
				rewriteNode(s, names)
			}
		}
	case *ThrowStmt:
		rewriteNode(v.Value, names)
	case *TryStmt:
		for _, s := range v.Body {
			rewriteNode(s, names)
		}
		for _, c := range v.Catches {
			for _, s := range c.Body {
				rewriteNode(s, names)
			}
		}
	}
}

// paramTypeNames 参数寄存器 → Java 类型名(this → 类名,argN → 原型参数类型),
// 供首次使用处声明的 move 链类型传播。
func paramTypeNames(m *dex.Method, code *dex.CodeHeader) map[int]string {
	out := map[int]string{}
	ins := int(code.InsSize)
	if ins == 0 {
		return out
	}
	base := int(code.RegistersSize) - ins
	isStatic := m.AccessFlags&0x8 != 0
	idx := 0
	if !isStatic {
		out[base] = JavaType(m.Cls())
		idx = 1
	}
	params := m.Prototype().Parameters()
	for i := idx; i < ins; i++ {
		if pi := i - idx; pi < len(params) {
			out[base+i] = JavaType(params[pi].Descriptor)
		}
	}
	return out
}

// ────────────────────────── 输出辅助 ──────────────────────────

func shortName(desc string) string {
	return JavaType(desc)
}

// classKeyword 类类型关键字(class/interface/enum/@interface)。
func classKeyword(flags uint32) string {
	switch {
	case flags&0x2000 != 0: // annotation
		return "@interface"
	case flags&0x0200 != 0: // interface
		return "interface"
	case flags&0x4000 != 0: // enum
		return "enum"
	default:
		return "class"
	}
}

func classAccessNames(flags uint32) string {
	var parts []string
	if flags&0x0001 != 0 {
		parts = append(parts, "public")
	}
	if flags&0x0008 != 0 {
		parts = append(parts, "static")
	}
	if flags&0x0400 != 0 && flags&0x0200 == 0 {
		parts = append(parts, "abstract")
	}
	if flags&0x0010 != 0 {
		parts = append(parts, "final")
	}
	return strings.Join(parts, " ")
}

func methodAccessNames(flags uint32) string {
	var parts []string
	if flags&0x0001 != 0 {
		parts = append(parts, "public")
	}
	if flags&0x0002 != 0 {
		parts = append(parts, "private")
	}
	if flags&0x0004 != 0 {
		parts = append(parts, "protected")
	}
	if flags&0x0008 != 0 {
		parts = append(parts, "static")
	}
	if flags&0x0010 != 0 {
		parts = append(parts, "final")
	}
	if flags&0x0020 != 0 {
		parts = append(parts, "synchronized")
	}
	if flags&0x0400 != 0 {
		parts = append(parts, "abstract")
	}
	if flags&0x0100 != 0 {
		parts = append(parts, "native")
	}
	return strings.Join(parts, " ")
}

func fieldAccessNames(flags uint32) string {
	var parts []string
	if flags&0x0001 != 0 {
		parts = append(parts, "public")
	}
	if flags&0x0002 != 0 {
		parts = append(parts, "private")
	}
	if flags&0x0004 != 0 {
		parts = append(parts, "protected")
	}
	if flags&0x0008 != 0 {
		parts = append(parts, "static")
	}
	if flags&0x0010 != 0 {
		parts = append(parts, "final")
	}
	return strings.Join(parts, " ")
}

func renderProto(p *dex.Prototype) string {
	if p == nil {
		return "(?)"
	}
	params := p.Parameters()
	names := make([]string, 0, len(params))
	for _, pt := range params {
		names = append(names, shortName(pt.Descriptor))
	}
	return "(" + strings.Join(names, ", ") + ")"
}

// renderProtoText 原型描述符文本(参数类型+返回值),用于 Invoke 构造。
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
