package godad

// ir.go — Go DAD IR(中间表示)节点定义。
// 对齐 androguard/instruction.py 的 IRForm 体系。
// 设计:每种 Dalvik 操作码映射到一个或多个 IR 节点,
// Java Writer 遍历 IR 节点输出源码。

import (
	"fmt"
	"strings"
)

// ────────────────────────── IR 节点基类 ──────────────────────────

// Node IR 节点接口。
type Node interface {
	// Java 输出该节点的 Java 表达式。
	Java() string
	// UsedVars 返回该节点引用的变量寄存器。
	UsedVars() []int
	// HasSideEffect 该节点是否有副作用(方法调用/字段写入)。
	HasSideEffect() bool
}

// ────────────────────────── 常量 ──────────────────────────

// IntConst 整数常量。
type IntConst struct{ V int64 }

func (c *IntConst) Java() string        { return fmt.Sprintf("%d", c.V) }
func (c *IntConst) UsedVars() []int     { return nil }
func (c *IntConst) HasSideEffect() bool { return false }

// LongConst 长整数常量。
type LongConst struct{ V int64 }

func (c *LongConst) Java() string        { return fmt.Sprintf("%dL", c.V) }
func (c *LongConst) UsedVars() []int     { return nil }
func (c *LongConst) HasSideEffect() bool { return false }

// FloatConst 浮点常量。
type FloatConst struct{ V float32 }

func (c *FloatConst) Java() string        { return fmt.Sprintf("%gF", c.V) }
func (c *FloatConst) UsedVars() []int     { return nil }
func (c *FloatConst) HasSideEffect() bool { return false }

// DoubleConst 双精度常量。
type DoubleConst struct{ V float64 }

func (c *DoubleConst) Java() string        { return fmt.Sprintf("%g", c.V) }
func (c *DoubleConst) UsedVars() []int     { return nil }
func (c *DoubleConst) HasSideEffect() bool { return false }

// StringConst 字符串常量。
type StringConst struct{ V string }

func (c *StringConst) Java() string {
	escaped := ""
	for _, r := range c.V {
		switch r {
		case '\\':
			escaped += `\\`
		case '"':
			escaped += `\"`
		case '\n':
			escaped += `\n`
		case '\r':
			escaped += `\r`
		case '\t':
			escaped += `\t`
		default:
			escaped += string(r)
		}
	}
	return `"` + escaped + `"`
}
func (c *StringConst) UsedVars() []int     { return nil }
func (c *StringConst) HasSideEffect() bool { return false }

// ClassConst 类常量(用于 const-class)。
type ClassConst struct{ Desc string }

func (c *ClassConst) Java() string        { return JavaType(c.Desc) + ".class" }
func (c *ClassConst) UsedVars() []int     { return nil }
func (c *ClassConst) HasSideEffect() bool { return false }

// NullConst null 常量。
type NullConst struct{}

func (c *NullConst) Java() string        { return "null" }
func (c *NullConst) UsedVars() []int     { return nil }
func (c *NullConst) HasSideEffect() bool { return false }

// ────────────────────────── 变量 ──────────────────────────

// Var 寄存器变量。
type Var struct {
	Reg  int
	Name string // Java 变量名(如 "v0", "r1" 等;后续可由数据流分析替换为有意义的名字)
}

func (v *Var) Java() string {
	if v.Name != "" {
		return v.Name
	}
	return fmt.Sprintf("v%d", v.Reg)
}
func (v *Var) UsedVars() []int     { return []int{v.Reg} }
func (v *Var) HasSideEffect() bool { return false }

// ────────────────────────── 赋值 ──────────────────────────

// Assign 赋值表达式: lhs = rhs。
// DeclType 非空时渲染为局部变量声明 `Type lhs = rhs;`(首次使用处声明)。
type Assign struct {
	LHS      Node
	RHS      Node
	DeclType string
}

func (a *Assign) Java() string        { return a.LHS.Java() + " = " + a.RHS.Java() }
func (a *Assign) UsedVars() []int     { return append(a.LHS.UsedVars(), a.RHS.UsedVars()...) }
func (a *Assign) HasSideEffect() bool { return a.RHS.HasSideEffect() }

// ────────────────────────── 二元运算 ──────────────────────────

// BinaryOp 二元运算。
type BinaryOp struct {
	Op   string // "+", "-", "*", "/", "&", "|", "^", "<<", ">>", ">>>"
	LHS  Node
	RHS  Node
	Type string // Java 类型(用于强制转换)
}

func (b *BinaryOp) Java() string {
	left := b.LHS.Java()
	right := b.RHS.Java()
	return fmt.Sprintf("(%s) %s (%s)", left, b.Op, right)
}
func (b *BinaryOp) UsedVars() []int     { return append(b.LHS.UsedVars(), b.RHS.UsedVars()...) }
func (b *BinaryOp) HasSideEffect() bool { return false }

// ────────────────────────── 比较 ──────────────────────────

// Compare 比较表达式。
type Compare struct {
	Op  string // "==", "!=", "<", ">", "<=", ">="
	LHS Node
	RHS Node
}

func (c *Compare) Java() string        { return c.LHS.Java() + " " + c.Op + " " + c.RHS.Java() }
func (c *Compare) UsedVars() []int     { return append(c.LHS.UsedVars(), c.RHS.UsedVars()...) }
func (c *Compare) HasSideEffect() bool { return false }

// ────────────────────────── 字段访问 ──────────────────────────

// IGet 读取实例字段: obj.field。
type IGet struct {
	Owner string // 类描述符
	Name  string
	Type  string // 字段类型描述符
	Obj   Node   // this 或其他变量
}

func (g *IGet) Java() string {
	return g.Obj.Java() + "." + g.Name
}
func (g *IGet) UsedVars() []int     { return g.Obj.UsedVars() }
func (g *IGet) HasSideEffect() bool { return false }

// SGet 读取静态字段: Class.field。
type SGet struct {
	Owner string
	Name  string
	Type  string
}

func (g *SGet) Java() string {
	return JavaType(g.Owner) + "." + g.Name
}
func (g *SGet) UsedVars() []int     { return nil }
func (g *SGet) HasSideEffect() bool { return false }

// IPut 写入实例字段: obj.field = value。
type IPut struct {
	Owner string
	Name  string
	Type  string
	Obj   Node
	Value Node
}

func (p *IPut) Java() string {
	return p.Obj.Java() + "." + p.Name + " = " + p.Value.Java()
}
func (p *IPut) UsedVars() []int     { return append(p.Obj.UsedVars(), p.Value.UsedVars()...) }
func (p *IPut) HasSideEffect() bool { return true }

// SPut 写入静态字段: Class.field = value。
type SPut struct {
	Owner string
	Name  string
	Type  string
	Value Node
}

func (p *SPut) Java() string {
	return JavaType(p.Owner) + "." + p.Name + " = " + p.Value.Java()
}
func (p *SPut) UsedVars() []int     { return p.Value.UsedVars() }
func (p *SPut) HasSideEffect() bool { return true }

// ────────────────────────── 方法调用 ──────────────────────────

// InvokeType 调用类型。
type InvokeType int

const (
	InvokeVirtual InvokeType = iota
	InvokeSuper
	InvokeDirect
	InvokeStatic
	InvokeInterface
)

// Invoke 方法调用。
type Invoke struct {
	Type       InvokeType
	Owner      string // 类描述符
	Name       string
	Proto      string // 原型描述符(参数+返回值)
	RetType    string // 返回值类型描述符(如 "V"/"I"/"Ljava/lang/String;")
	Args       []Node // 参数列表(不含 this)
	This       Node   // this 参数(virtual/interface/direct 时)
	CtorCall   string // 非空时渲染为 super(...)/this(...)(构造器委托)
	ResultUsed bool   // 结果被 move-result 消费(Writer 跳过独立语句)
}

func (i *Invoke) Java() string {
	args := make([]string, len(i.Args))
	for j, a := range i.Args {
		args[j] = a.Java()
	}
	if i.CtorCall != "" {
		return i.CtorCall + "(" + joinArgs(args) + ")"
	}
	recv := ""
	if i.This != nil {
		recv = i.This.Java()
	} else if i.Type == InvokeStatic {
		recv = JavaType(i.Owner)
	} else {
		recv = JavaType(i.Owner) + ".this"
	}
	return fmt.Sprintf("%s.%s(%s)", recv, i.Name, joinArgs(args))
}
func (i *Invoke) UsedVars() []int {
	var vars []int
	if i.This != nil {
		vars = append(vars, i.This.UsedVars()...)
	}
	for _, a := range i.Args {
		vars = append(vars, a.UsedVars()...)
	}
	return vars
}
func (i *Invoke) HasSideEffect() bool { return true }

// ────────────────────────── 返回 ──────────────────────────

// ReturnVal 返回值。
type ReturnVal struct{ Value Node }

func (r *ReturnVal) Java() string {
	if r.Value == nil {
		return "return"
	}
	return "return " + r.Value.Java()
}
func (r *ReturnVal) UsedVars() []int {
	if r.Value == nil {
		return nil
	}
	return r.Value.UsedVars()
}
func (r *ReturnVal) HasSideEffect() bool { return false }

// ────────────────────────── 新建对象 ──────────────────────────

// NewObj new-instance: 创建新对象。Args 非空时渲染为构造调用。
type NewObj struct {
	Desc string // 类描述符
	Args []Node // 构造参数(空 = 无参构造)
}

func (n *NewObj) Java() string {
	args := make([]string, len(n.Args))
	for i, a := range n.Args {
		args[i] = a.Java()
	}
	return "new " + JavaType(n.Desc) + "(" + joinArgs(args) + ")"
}
func (n *NewObj) UsedVars() []int {
	var vars []int
	for _, a := range n.Args {
		vars = append(vars, a.UsedVars()...)
	}
	return vars
}
func (n *NewObj) HasSideEffect() bool { return false }

// ────────────────────────── 类型转换 ──────────────────────────

// CheckCast 类型转换。
type CheckCast struct {
	Desc string
	Expr Node
}

func (c *CheckCast) Java() string {
	return "(" + JavaType(c.Desc) + ") " + c.Expr.Java()
}
func (c *CheckCast) UsedVars() []int     { return c.Expr.UsedVars() }
func (c *CheckCast) HasSideEffect() bool { return false }

// ────────────────────────── 数组 ──────────────────────────

// NewArray new-array: 创建数组。Type 为数组类型描述符(如 "[B")。
type NewArray struct {
	Size Node
	Type string
}

func (n *NewArray) Java() string {
	elem := n.Type
	if len(elem) > 0 && elem[0] == '[' {
		elem = elem[1:]
	}
	return "new " + JavaType(elem) + "[" + n.Size.Java() + "]"
}
func (n *NewArray) UsedVars() []int     { return n.Size.UsedVars() }
func (n *NewArray) HasSideEffect() bool { return false }

// ArrayGet aget: vAA = vBB[vCC]。
type ArrayGet struct {
	Array Node
	Index Node
	Type  string // 元素类型描述符(可选)
}

func (a *ArrayGet) Java() string        { return a.Array.Java() + "[" + a.Index.Java() + "]" }
func (a *ArrayGet) UsedVars() []int     { return append(a.Array.UsedVars(), a.Index.UsedVars()...) }
func (a *ArrayGet) HasSideEffect() bool { return false }

// ArrayPut aput: vBB[vCC] = vAA。
type ArrayPut struct {
	Array Node
	Index Node
	Value Node
}

func (a *ArrayPut) Java() string {
	return a.Array.Java() + "[" + a.Index.Java() + "] = " + a.Value.Java()
}
func (a *ArrayPut) UsedVars() []int {
	vars := append(a.Array.UsedVars(), a.Index.UsedVars()...)
	return append(vars, a.Value.UsedVars()...)
}
func (a *ArrayPut) HasSideEffect() bool { return true }

// FilledArray filled-new-array: new T[]{...}(结果经 move-result 消费)。
type FilledArray struct {
	Type  string // 数组类型描述符(如 "[I")
	Elems []Node
}

func (f *FilledArray) Java() string {
	args := make([]string, len(f.Elems))
	for i, e := range f.Elems {
		args[i] = e.Java()
	}
	return "new " + JavaType(f.Type) + "{" + joinArgs(args) + "}"
}
func (f *FilledArray) UsedVars() []int {
	var vars []int
	for _, e := range f.Elems {
		vars = append(vars, e.UsedVars()...)
	}
	return vars
}
func (f *FilledArray) HasSideEffect() bool { return false }

// Cast 显式类型转换:(Type) expr。
type Cast struct {
	Desc string // 目标类型描述符
	Expr Node
}

func (c *Cast) Java() string        { return "(" + JavaTypeShort(c.Desc) + ") " + c.Expr.Java() }
func (c *Cast) UsedVars() []int     { return c.Expr.UsedVars() }
func (c *Cast) HasSideEffect() bool { return false }

// ────────────────────────── 异常与低频语义节点 ──────────────────────────

// MoveException move-exception 标记:catch 入口取异常对象。
// try/catch 结构化时被剥离为 catch 参数;独立出现时渲染为注释。
type MoveException struct{}

func (m *MoveException) Java() string        { return "/* move-exception */" }
func (m *MoveException) UsedVars() []int     { return nil }
func (m *MoveException) HasSideEffect() bool { return false }

// MethodHandleConst const-method-handle:方法句柄引用(索引级语义)。
// 完整解析需 method_handles 表,当前以 @索引 形式保留数据流。
type MethodHandleConst struct{ Idx uint32 }

func (c *MethodHandleConst) Java() string        { return "MethodHandle@" + itoa(int(c.Idx)) }
func (c *MethodHandleConst) UsedVars() []int     { return nil }
func (c *MethodHandleConst) HasSideEffect() bool { return false }

// MethodTypeConst const-method-type:方法类型(原型已解析)。
type MethodTypeConst struct{ Desc string }

func (c *MethodTypeConst) Java() string        { return "MethodType<" + c.Desc + ">" }
func (c *MethodTypeConst) UsedVars() []int     { return nil }
func (c *MethodTypeConst) HasSideEffect() bool { return false }

// ────────────────────────── 工具函数 ──────────────────────────

// JavaType Dalvik 描述符 → Java 类型名。
// "Ljava/lang/String;" → "java.lang.String"
// "[I" → "int[]"
// "Lcom/foo/Bar;" → "com.foo.Bar"
func JavaType(desc string) string {
	if desc == "" {
		return "Object"
	}
	// 数组类型
	if len(desc) > 0 && desc[0] == '[' {
		return JavaType(desc[1:]) + "[]"
	}
	// 基本类型
	switch desc {
	case "V":
		return "void"
	case "Z":
		return "boolean"
	case "B":
		return "byte"
	case "S":
		return "short"
	case "C":
		return "char"
	case "I":
		return "int"
	case "J":
		return "long"
	case "F":
		return "float"
	case "D":
		return "double"
	}
	// 对象类型:去掉 L...; 并把 / 转 .
	if len(desc) > 2 && desc[0] == 'L' && desc[len(desc)-1] == ';' {
		return strings.ReplaceAll(desc[1:len(desc)-1], "/", ".")
	}
	return desc
}

// JavaTypeShort 返回短类型名(用于类型转换)。
func JavaTypeShort(desc string) string {
	switch desc {
	case "Z":
		return "boolean"
	case "B":
		return "byte"
	case "S":
		return "short"
	case "C":
		return "char"
	case "I":
		return "int"
	case "J":
		return "long"
	case "F":
		return "float"
	case "D":
		return "double"
	default:
		return JavaType(desc)
	}
}

func joinArgs(args []string) string {
	result := ""
	for i, a := range args {
		if i > 0 {
			result += ", "
		}
		result += a
	}
	return result
}
