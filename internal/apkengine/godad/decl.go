package godad

import "slices"

// decl.go — 变量声明推断(首次使用处声明 + 作用域安全回退)。
// 前置:versionRegs 已在块级完成到达定义分析与版本重命名——
//   - LHS Name 非空 = 独占版本(定义与全部使用已配对),可内联声明;
//   - LHS Name 为空 = 歧义合并组(多定义共同到达某使用),保持默认名
//     vN,由方法顶部统一声明。
//
// DeclareVars 对独占版本做作用域检查(定义所在容器必须是全部使用的
// 祖先,否则 Java 块作用域被破坏)后内联 `Type vN = rhs;`;检查不过或
// 合并组的变量回退为顶部声明(构造委托之后)。类型按版本键推断:
// 定义右值优先,move 链向参数类型传播。

// versionKey 版本类型键:空名(合并组)同寄存器共享一键。
func versionKey(reg int, name string) string {
	return "r" + itoa(reg) + ":" + name
}

// DeclareVars 首次使用处内联声明 + 残余顶部声明。
// 返回值非 nil 表示顶部声明节点需要插入(无残余声明时返回原列表)。
func DeclareVars(nodes []Node, params map[int]string, paramTypes map[int]string) []Node {
	// ── 1) 按版本键推断类型(定义右值 + move 链传播,参数做种子)──
	typeOf := map[string]string{}
	for r, t := range paramTypes {
		if nm, ok := params[r]; ok {
			typeOf[versionKey(r, nm)] = t
		}
	}
	var walkAssigns func(ns []Node, fn func(*Assign, *Var))
	walkAssigns = func(ns []Node, fn func(*Assign, *Var)) {
		for _, n := range ns {
			switch v := n.(type) {
			case *Assign:
				if lv, ok := v.LHS.(*Var); ok {
					fn(v, lv)
				}
			case *IfStmt:
				walkAssigns(v.Then, fn)
				walkAssigns(v.Else, fn)
			case *WhileStmt:
				walkAssigns(v.Body, fn)
			case *SwitchStmt:
				for ci := range v.Cases {
					walkAssigns(v.Cases[ci].Body, fn)
				}
			case *TryStmt:
				walkAssigns(v.Body, fn)
				for _, c := range v.Catches {
					walkAssigns(c.Body, fn)
				}
			}
		}
	}
	for pass := 0; pass < 3; pass++ {
		walkAssigns(nodes, func(a *Assign, lv *Var) {
			if _, isParam := params[lv.Reg]; isParam {
				return
			}
			k := versionKey(lv.Reg, lv.Name)
			if typeOf[k] != "" {
				return
			}
			t := inferType(a.RHS)
			if t == "" {
				if rv, ok := a.RHS.(*Var); ok {
					t = typeOf[versionKey(rv.Reg, rv.Name)]
				}
			}
			if t != "" {
				typeOf[k] = t
			}
		})
	}

	// ── 2) 作用域检查:定义容器须为全部使用的祖先(链前缀)──
	// chain = 从方法根到定义/使用所在列表的容器节点序列(根列表为空链)。
	type defSiteInfo struct {
		assign *Assign
		reg    int
		name   string
		chain  []Node
	}
	defSites := map[string]*defSiteInfo{}
	useChains := map[string][][]Node{}
	child := func(chain []Node, owner Node) []Node {
		return append(append([]Node{}, chain...), owner)
	}
	chainPrefix := func(def, use []Node) bool {
		if len(def) > len(use) {
			return false
		}
		for i := range def {
			if def[i] != use[i] {
				return false
			}
		}
		return true
	}
	var collectUses func(n Node, chain []Node)
	collectUses = func(n Node, chain []Node) {
		walkVars(n, func(v *Var) {
			if _, isParam := params[v.Reg]; isParam {
				return
			}
			if v.Name == "" {
				return // 合并组使用无版本键
			}
			useChains[versionKey(v.Reg, v.Name)] = append(useChains[versionKey(v.Reg, v.Name)], chain)
		})
	}
	var walk func(ns []Node, chain []Node)
	walk = func(ns []Node, chain []Node) {
		for _, n := range ns {
			switch v := n.(type) {
			case *Assign:
				collectUses(v.RHS, chain)
				if lv, ok := v.LHS.(*Var); ok && lv.Name != "" {
					if _, isParam := params[lv.Reg]; !isParam {
						defSites[versionKey(lv.Reg, lv.Name)] = &defSiteInfo{
							assign: v, reg: lv.Reg, name: lv.Name, chain: chain,
						}
					}
				}
			case *IPut:
				collectUses(v, chain)
			case *SPut:
				collectUses(v, chain)
			case *Invoke:
				collectUses(v, chain)
			case *ReturnVal:
				collectUses(v, chain)
			case *ThrowStmt:
				collectUses(v, chain)
			case *CheckCast:
				collectUses(v, chain)
			case *Cast:
				collectUses(v, chain)
			case *IfStmt:
				collectUses(v.Cond, chain)
				walk(v.Then, child(chain, v))
				walk(v.Else, child(chain, v))
			case *WhileStmt:
				collectUses(v.Cond, chain)
				walk(v.Body, child(chain, v))
			case *SwitchStmt:
				collectUses(v.Expr, chain)
				c := child(chain, v)
				for ci := range v.Cases {
					walk(v.Cases[ci].Body, c)
				}
			case *TryStmt:
				c := child(chain, v)
				walk(v.Body, c)
				for _, cc := range v.Catches {
					walk(cc.Body, child(c, cc))
				}
			case *DeclStmt, *Unresolved, *BreakStmt, *ContinueStmt:
				// 无寄存器引用
			default:
				collectUses(n, chain) // 兜底:独立表达式语句
			}
		}
	}
	walk(nodes, nil)

	// ── 3) 内联声明(独占版本 + 作用域通过 + 类型可推断)──
	residual := map[string]struct {
		reg  int
		name string
	}{}
	for k, si := range defSites {
		t := typeOf[k]
		if t == "" {
			continue
		}
		ok := true
		for _, uc := range useChains[k] {
			if !chainPrefix(si.chain, uc) {
				ok = false
				break
			}
		}
		if ok {
			si.assign.DeclType = t
		} else {
			residual[k] = struct {
				reg  int
				name string
			}{si.reg, si.name}
		}
	}

	// ── 4) 残余顶部声明:合并组 + 作用域检查失败的版本 ──
	var entries []struct {
		reg  int
		name string
		typ  string
	}
	seen := map[string]bool{}
	addResidual := func(reg int, name string) {
		k := versionKey(reg, name)
		if seen[k] {
			return
		}
		t := typeOf[k]
		if t == "" {
			return
		}
		seen[k] = true
		if name == "" {
			name = "v" + itoa(reg) // 合并组默认名
		}
		entries = append(entries, struct {
			reg  int
			name string
			typ  string
		}{reg, name, t})
	}
	walkAssigns(nodes, func(a *Assign, lv *Var) {
		if _, isParam := params[lv.Reg]; isParam {
			return
		}
		if lv.Name == "" {
			addResidual(lv.Reg, "") // 合并组:定义处即默认名
		}
	})
	for k, si := range residual {
		_ = k
		addResidual(si.reg, si.name)
	}
	if len(entries) == 0 {
		return nodes
	}
	// 按寄存器序排序,同类型相邻合并一行
	sortBy := func() {
		for i := 0; i < len(entries); i++ {
			for j := i + 1; j < len(entries); j++ {
				if entries[j].reg < entries[i].reg {
					entries[i], entries[j] = entries[j], entries[i]
				}
			}
		}
	}
	sortBy()
	var decls []Decl
	for _, e := range entries {
		if n := len(decls); n > 0 && decls[n-1].Type == e.typ {
			decls[n-1].Names = append(decls[n-1].Names, e.name)
			continue
		}
		decls = append(decls, Decl{Type: e.typ, Names: []string{e.name}})
	}
	declNode := &DeclStmt{Decls: decls}

	// 插入位置:跳过方法开头的构造委托(super/this 必须是首语句)
	insertAt := 0
	for insertAt < len(nodes) {
		inv, ok := nodes[insertAt].(*Invoke)
		if !ok || inv.CtorCall == "" {
			break
		}
		insertAt++
	}
	out := make([]Node, 0, len(nodes)+1)
	out = append(out, nodes[:insertAt]...)
	out = append(out, declNode)
	out = append(out, nodes[insertAt:]...)
	return out
}

// walkVars 前序遍历子树内全部 Var 引用。
func walkVars(n Node, fn func(*Var)) {
	switch v := n.(type) {
	case *Var:
		fn(v)
	case *Assign:
		walkVars(v.LHS, fn)
		walkVars(v.RHS, fn)
	case *BinaryOp:
		walkVars(v.LHS, fn)
		walkVars(v.RHS, fn)
	case *Compare:
		walkVars(v.LHS, fn)
		walkVars(v.RHS, fn)
	case *NotExpr:
		walkVars(v.Expr, fn)
	case *IGet:
		walkVars(v.Obj, fn)
	case *IPut:
		walkVars(v.Obj, fn)
		walkVars(v.Value, fn)
	case *SPut:
		walkVars(v.Value, fn)
	case *Invoke:
		if v.This != nil {
			walkVars(v.This, fn)
		}
		for _, a := range v.Args {
			walkVars(a, fn)
		}
	case *ReturnVal:
		if v.Value != nil {
			walkVars(v.Value, fn)
		}
	case *CheckCast:
		walkVars(v.Expr, fn)
	case *Cast:
		walkVars(v.Expr, fn)
	case *ArrayGet:
		walkVars(v.Array, fn)
		walkVars(v.Index, fn)
	case *ArrayPut:
		walkVars(v.Array, fn)
		walkVars(v.Index, fn)
		walkVars(v.Value, fn)
	case *FilledArray:
		for _, e := range v.Elems {
			walkVars(e, fn)
		}
	case *NewArray:
		walkVars(v.Size, fn)
	case *NewObj:
		for _, a := range v.Args {
			walkVars(a, fn)
		}
	case *ThrowStmt:
		walkVars(v.Value, fn)
	case *IfStmt:
		walkVars(v.Cond, fn)
		for _, s := range v.Then {
			walkVars(s, fn)
		}
		for _, s := range v.Else {
			walkVars(s, fn)
		}
	case *WhileStmt:
		walkVars(v.Cond, fn)
		for _, s := range v.Body {
			walkVars(s, fn)
		}
	case *SwitchStmt:
		walkVars(v.Expr, fn)
		for ci := range v.Cases {
			for _, s := range v.Cases[ci].Body {
				walkVars(s, fn)
			}
		}
	case *TryStmt:
		for _, s := range v.Body {
			walkVars(s, fn)
		}
		for _, c := range v.Catches {
			for _, s := range c.Body {
				walkVars(s, fn)
			}
		}
	}
}

// inferType 右值 → Java 类型(不可推断返回 "")。
func inferType(n Node) string {
	switch v := n.(type) {
	case *NewObj:
		return JavaType(v.Desc)
	case *StringConst:
		return "String"
	case *NewArray:
		return JavaType(v.Type)
	case *ClassConst:
		return "Class"
	case *IGet:
		return JavaType(v.Type)
	case *SGet:
		return JavaType(v.Type)
	case *Invoke:
		if v.RetType != "" && v.RetType != "V" {
			return JavaType(v.RetType)
		}
	case *IntConst:
		return "int"
	case *LongConst:
		return "long"
	case *FloatConst:
		return "float"
	case *DoubleConst:
		return "double"
	case *BoolConst:
		return "boolean"
	case *CheckCast:
		return JavaType(v.Desc)
	case *Cast:
		return JavaType(v.Desc)
	case *ArrayGet:
		if v.Type != "" {
			return JavaType(v.Type)
		}
	case *FilledArray:
		return JavaType(v.Type)
	case *Compare:
		return "boolean"
	case *BinaryOp:
		return "int"
	}
	return ""
}

func sortInts(a []int) { slices.Sort(a) }
