package godad

// stmts.go — 结构化语句节点:if/else、while、break/continue、逻辑取反。
// Writer 递归渲染;非结构化回退时 Java() 提供单行形态。

import "strings"

// IfStmt 条件语句。
type IfStmt struct {
	Cond Node
	Then []Node
	Else []Node
}

func (s *IfStmt) Java() string {
	var sb strings.Builder
	sb.WriteString("if (" + s.Cond.Java() + ") { ... }")
	if len(s.Else) > 0 {
		sb.WriteString(" else { ... }")
	}
	return sb.String()
}
func (s *IfStmt) UsedVars() []int     { return s.Cond.UsedVars() }
func (s *IfStmt) HasSideEffect() bool { return true }

// WhileStmt 循环语句(while(true) 覆盖 do-while 形态)。
type WhileStmt struct {
	Cond Node
	Body []Node
}

func (s *WhileStmt) Java() string        { return "while (" + s.Cond.Java() + ") { ... }" }
func (s *WhileStmt) UsedVars() []int     { return s.Cond.UsedVars() }
func (s *WhileStmt) HasSideEffect() bool { return true }

// SwitchCaseBody 单个分支的语句体(Keys 为空且 IsDefault 时渲染 default:)。
type SwitchCaseBody struct {
	Keys      []int32
	IsDefault bool
	Body      []Node
}

// SwitchStmt switch 语句。
type SwitchStmt struct {
	Expr  Node
	Cases []SwitchCaseBody
}

func (s *SwitchStmt) Java() string        { return "switch (" + s.Expr.Java() + ") { ... }" }
func (s *SwitchStmt) UsedVars() []int     { return s.Expr.UsedVars() }
func (s *SwitchStmt) HasSideEffect() bool { return true }

// BreakStmt break 语句。
type BreakStmt struct{}

func (s *BreakStmt) Java() string        { return "break;" }
func (s *BreakStmt) UsedVars() []int     { return nil }
func (s *BreakStmt) HasSideEffect() bool { return true }

// ContinueStmt continue 语句。
type ContinueStmt struct{}

func (s *ContinueStmt) Java() string        { return "continue;" }
func (s *ContinueStmt) UsedVars() []int     { return nil }
func (s *ContinueStmt) HasSideEffect() bool { return true }

// ThrowStmt throw 语句。
type ThrowStmt struct{ Value Node }

func (s *ThrowStmt) Java() string        { return "throw " + s.Value.Java() }
func (s *ThrowStmt) UsedVars() []int     { return s.Value.UsedVars() }
func (s *ThrowStmt) HasSideEffect() bool { return true }

// CatchClause catch 子句。Types 为 Java 类型名(多 catch 合并 `A | B`);
// CatchAll 渲染为 Throwable(catch-all,finally 风格);Param 为空时无参数。
// 实现 Node 接口以便声明作用域分析将其作为容器标识。
type CatchClause struct {
	Types    []string
	CatchAll bool
	Param    string
	Body     []Node
}

func (c *CatchClause) Java() string {
	label := strings.Join(c.Types, " | ")
	if c.CatchAll {
		if label != "" {
			label += " | "
		}
		label += "Throwable"
	}
	if c.Param != "" {
		label += " " + c.Param
	}
	return "catch (" + label + ") { ... }"
}
func (c *CatchClause) UsedVars() []int {
	var vars []int
	for _, n := range c.Body {
		vars = append(vars, n.UsedVars()...)
	}
	return vars
}
func (c *CatchClause) HasSideEffect() bool { return true }

// TryStmt try/catch 语句(异常表结构化产物)。
type TryStmt struct {
	Body    []Node
	Catches []*CatchClause
}

func (s *TryStmt) Java() string { return "try { ... }" }
func (s *TryStmt) UsedVars() []int {
	var vars []int
	var walk = func(ns []Node) {
		for _, n := range ns {
			vars = append(vars, n.UsedVars()...)
		}
	}
	walk(s.Body)
	for _, c := range s.Catches {
		walk(c.Body)
	}
	return vars
}
func (s *TryStmt) HasSideEffect() bool { return true }

// NotExpr 逻辑取反(非比较条件时使用)。
type NotExpr struct{ Expr Node }

func (n *NotExpr) Java() string        { return "!(" + n.Expr.Java() + ")" }
func (n *NotExpr) UsedVars() []int     { return n.Expr.UsedVars() }
func (n *NotExpr) HasSideEffect() bool { return false }

// invertCond 条件取反:比较节点翻转运算符,其余包一层 !(...)。
func invertCond(n Node) Node {
	if c, ok := n.(*Compare); ok {
		flip := map[string]string{
			"==": "!=", "!=": "==",
			"<": ">=", ">=": "<",
			">": "<=", "<=": ">",
		}
		if op, ok := flip[c.Op]; ok {
			return &Compare{Op: op, LHS: c.LHS, RHS: c.RHS}
		}
	}
	return &NotExpr{Expr: n}
}

// BoolConst true/false 常量。
type BoolConst struct{ V bool }

func (c *BoolConst) Java() string {
	if c.V {
		return "true"
	}
	return "false"
}
func (c *BoolConst) UsedVars() []int     { return nil }
func (c *BoolConst) HasSideEffect() bool { return false }

// Decl 单条变量声明(类型 + 变量名列表;版本名或合并组默认名 vN)。
type Decl struct {
	Type  string
	Names []string
}

// DeclStmt 方法顶部变量声明语句(残余声明:合并组/作用域检查失败版本)。
type DeclStmt struct{ Decls []Decl }

func (d *DeclStmt) Java() string {
	parts := make([]string, 0, len(d.Decls))
	for _, decl := range d.Decls {
		parts = append(parts, decl.Type+" "+strings.Join(decl.Names, ", "))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "; ") + ";"
}
func (d *DeclStmt) UsedVars() []int     { return nil }
func (d *DeclStmt) HasSideEffect() bool { return false }
