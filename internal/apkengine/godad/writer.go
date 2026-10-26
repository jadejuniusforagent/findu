package godad

// writer.go — Java 源码输出器:遍历 IR 节点,输出缩进的 Java 代码。
// 基础级:支持构造器/字段访问/返回/简单调用/赋值/算术。
// 未知节点输出 /* op */ 注释标记,不阻塞输出。

import (
	"strconv"
	"strings"

	"finduai/internal/apkengine/dex"
)

// Writer Java 源码输出器。
type Writer struct {
	sb     strings.Builder
	ind    int // 缩进级别
	buffer strings.Builder
}

// NewWriter 创建 Writer。
func NewWriter() *Writer {
	return &Writer{}
}

func (w *Writer) space() string {
	return strings.Repeat("    ", w.ind)
}

func (w *Writer) write(s string) {
	w.buffer.WriteString(s)
}

func (w *Writer) writeLine(s string) {
	w.write(w.space() + s + "\n")
}

func (w *Writer) incInd() { w.ind++ }
func (w *Writer) decInd() {
	if w.ind > 0 {
		w.ind--
	}
}

// WriteMethod 输出方法签名。
func (w *Writer) WriteMethod(m *dex.Method) {
	mAccess := methodAccessNames(m.AccessFlags)
	retType := shortName(m.Prototype().ReturnType().Descriptor)
	params := m.Prototype().Parameters()
	paramStrs := make([]string, 0, len(params))
	for i, p := range params {
		paramStrs = append(paramStrs, shortName(p.Descriptor)+" arg"+itoa(i))
	}
	w.writeLine(mAccess + " " + retType + " " + m.Name() + "(" + strings.Join(paramStrs, ", ") + ") {")
	w.incInd()
}

// WriteNode 输出一个 IR 节点。
func (w *Writer) WriteNode(n Node) {
	switch v := n.(type) {
	case *Unresolved:
		w.writeLine("/* " + v.Op + " */")
	case *DeclStmt:
		// 类型分组残余声明:每行 `Type v0, v1;`
		for _, decl := range v.Decls {
			w.writeLine(decl.Type + " " + strings.Join(decl.Names, ", ") + ";")
		}
	case *ReturnVal:
		if v.Value == nil {
			w.writeLine("return;")
		} else {
			w.writeLine("return " + v.Value.Java() + ";")
		}
	case *Assign:
		if v.DeclType != "" {
			// 首次使用处声明:Type vN = rhs;
			w.writeLine(v.DeclType + " " + v.LHS.Java() + " = " + v.RHS.Java() + ";")
		} else {
			w.writeLine(v.Java() + ";")
		}
	case *IPut:
		w.writeLine(v.Java() + ";")
	case *SPut:
		w.writeLine(v.Java() + ";")
	case *Invoke:
		if v.ResultUsed {
			return
		}
		w.writeLine(v.Java() + ";")
	case *ThrowStmt:
		w.writeLine(v.Java() + ";")
	case *TryStmt:
		w.writeLine("try {")
		w.incInd()
		for _, s := range v.Body {
			w.WriteNode(s)
		}
		w.decInd()
		for _, c := range v.Catches {
			label := strings.Join(c.Types, " | ")
			if c.CatchAll {
				if label != "" {
					label += " | "
				}
				label += "Throwable"
			}
			param := ""
			if c.Param != "" {
				param = " " + c.Param
			}
			note := ""
			if c.CatchAll {
				note = " // catch-all (finally?)"
			}
			w.writeLine("} catch (" + label + param + ") {" + note)
			w.incInd()
			for _, s := range c.Body {
				w.WriteNode(s)
			}
			w.decInd()
		}
		w.writeLine("}")
	case *CheckCast:
		w.writeLine("/* check-cast " + v.Desc + " */")
	case *Cast:
		w.writeLine(v.Java() + ";")
	case *IfStmt:
		w.writeLine("if (" + v.Cond.Java() + ") {")
		w.incInd()
		for _, s := range v.Then {
			w.WriteNode(s)
		}
		w.decInd()
		if len(v.Else) > 0 {
			w.writeLine("} else {")
			w.incInd()
			for _, s := range v.Else {
				w.WriteNode(s)
			}
			w.decInd()
		}
		w.writeLine("}")
	case *SwitchStmt:
		w.writeLine("switch (" + v.Expr.Java() + ") {")
		w.incInd()
		for _, c := range v.Cases {
			if c.IsDefault {
				w.writeLine("default:")
			}
			for _, k := range c.Keys {
				w.writeLine("case " + strconv.FormatInt(int64(k), 10) + ":")
			}
			w.incInd()
			for _, s := range c.Body {
				w.WriteNode(s)
			}
			w.decInd()
		}
		w.decInd()
		w.writeLine("}")
	case *WhileStmt:
		w.writeLine("while (" + v.Cond.Java() + ") {")
		w.incInd()
		for _, s := range v.Body {
			w.WriteNode(s)
		}
		w.decInd()
		w.writeLine("}")
	case *BreakStmt:
		w.writeLine("break;")
	case *ContinueStmt:
		w.writeLine("continue;")
	default:
		// 其他节点(赋值等)用 Java() 输出
		java := n.Java()
		if java != "" {
			if n.HasSideEffect() {
				w.writeLine(java + ";")
			}
		}
	}
}

// EndMethod 输出方法结束括号。
func (w *Writer) EndMethod() {
	w.decInd()
	w.writeLine("}")
}

// String 返回完整 Java 源码。
func (w *Writer) String() string {
	return w.buffer.String()
}
