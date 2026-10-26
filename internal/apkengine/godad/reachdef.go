package godad

// reachdef.go — 块级到达定义分析 + 寄存器版本分裂。
// 替代早期保守的树级 SplitVars:嵌套(if/while/try 体)定义的寄存器
// 同样参与分裂,消除寄存器复用噪声的能力覆盖全方法。
//
// 每个非参数寄存器的每个定义点是一个"版本"。到达定义数据流(may 分析,
// 前驱含控制边与异常边)求出每个使用点的到达定义集合:
//   - 唯一到达 → 该使用归属该版本,与定义一起重命名为 v0 / v0_2 / …
//   - 多个到达 → 歧义,相关定义经 union-find 合并为一组,全部保持默认
//     名 vN(同一寄存器语义,由 DeclareVars 顶部统一声明),保证重命名
//     后引用一致。
//
// 命名规则:同寄存器各组按程序序编号;存在合并组时,独占版本号从 _2
// 起编(避免与合并组默认名 vN 的声明冲突);否则首组名 vN、后续 _2 起。
// 块内扫描按语句序先 RHS 后 LHS;switch 寄存器取块尾到达状态。

// versionRegs 对块序列做到达定义分析与版本重命名(原位改写 Var 名)。
// 必须在 Structure 之前调用(直接改写块节点与 TermCond)。
func versionRegs(blocks []*BasicBlock, params map[int]string) {
	if len(blocks) == 0 {
		return
	}
	// ── 1) 枚举定义点(程序序:块序即 PC 序,块内语句序)──
	var defs []*Var // def id → LHS Var
	regOf := []int{}
	blockDefIDs := make([][]int, len(blocks))
	for bi, b := range blocks {
		for _, n := range b.Nodes {
			if a, ok := n.(*Assign); ok {
				if lv, ok := a.LHS.(*Var); ok {
					if _, isParam := params[lv.Reg]; isParam {
						continue
					}
					blockDefIDs[bi] = append(blockDefIDs[bi], len(defs))
					defs = append(defs, lv)
					regOf = append(regOf, lv.Reg)
				}
			}
		}
	}
	if len(defs) == 0 {
		return
	}
	D := len(defs)
	words := (D + 63) / 64
	regDefs := map[int][]int{}
	for id, r := range regOf {
		regDefs[r] = append(regDefs[r], id)
	}

	newSet := func() []uint64 { return make([]uint64, words) }
	setBit := func(s []uint64, id int) { s[id/64] |= 1 << uint(id%64) }
	getBit := func(s []uint64, id int) bool { return s[id/64]&(1<<uint(id%64)) != 0 }
	orInto := func(dst, src []uint64) {
		for i := range dst {
			dst[i] |= src[i]
		}
	}
	andNot := func(dst, src []uint64) {
		for i := range dst {
			dst[i] &^= src[i]
		}
	}
	bitsEq := func(a, b []uint64) bool {
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}
	bitsToIDs := func(s []uint64) []int {
		var out []int
		for id := 0; id < D; id++ {
			if getBit(s, id) {
				out = append(out, id)
			}
		}
		return out
	}

	// ── 2) GEN/KILL ──
	gen := make([][]uint64, len(blocks))
	kill := make([][]uint64, len(blocks))
	for bi := range blocks {
		g, k := newSet(), newSet()
		last := map[int]int{}
		for _, id := range blockDefIDs[bi] {
			for _, other := range regDefs[regOf[id]] {
				if other != id {
					setBit(k, other)
				}
			}
			last[regOf[id]] = id
		}
		for _, id := range last {
			setBit(g, id)
		}
		gen[bi], kill[bi] = g, k
	}

	// ── 3) 到达定义不动点(前驱 = 控制前驱 + 异常边反向;不可达前驱剪枝:
	//     标准数据流下不可达块的 GEN 仍会外溢,先做可达性标记)──
	reachable := make([]bool, len(blocks))
	reachable[0] = true
	queue := []int{0}
	for len(queue) > 0 {
		bi := queue[0]
		queue = queue[1:]
		edges := append(append([]int{}, blocks[bi].Succs...), blocks[bi].ExcSuccs...)
		for _, s := range edges {
			if s >= 0 && s < len(blocks) && !reachable[s] {
				reachable[s] = true
				queue = append(queue, s)
			}
		}
	}
	preds := make([][]int, len(blocks))
	for bi, b := range blocks {
		for _, p := range b.Preds {
			if reachable[p] {
				preds[bi] = append(preds[bi], p)
			}
		}
		if !reachable[bi] {
			continue // 不可达块的异常边同样不外溢
		}
		for _, e := range b.ExcSuccs {
			preds[e] = append(preds[e], bi)
		}
	}
	inSets := make([][]uint64, len(blocks))
	outSets := make([][]uint64, len(blocks))
	for i := range inSets {
		inSets[i], outSets[i] = newSet(), newSet()
	}
	for changed := true; changed; {
		changed = false
		for bi := range blocks {
			next := newSet()
			for _, p := range preds[bi] {
				orInto(next, outSets[p])
			}
			if !bitsEq(next, inSets[bi]) {
				inSets[bi] = next
				changed = true
			}
			// OUT = GEN ∪ (IN − KILL);KILL 只作用于 IN——
			// 同块内同寄存器的多个定义互在 KILL 中,不能减掉 GEN
			o := newSet()
			orInto(o, next)
			andNot(o, kill[bi])
			orInto(o, gen[bi])
			outSets[bi] = o
		}
	}

	// ── 4) 使用点收集(块内顺序,RHS 先于 LHS)──
	type useInfo struct {
		reg  int
		v    *Var
		defs []int
	}
	var uses []useInfo
	switchReach := make([][]int, len(blocks))
	for bi, b := range blocks {
		cur := map[int][]uint64{} // reg → 块内已定义时的精确到达集
		reachIn := func(reg int) []uint64 {
			s := newSet()
			for _, id := range regDefs[reg] {
				if getBit(inSets[bi], id) {
					setBit(s, id)
				}
			}
			return s
		}
		defCursor := 0
		var use func(*Var)
		use = func(v *Var) {
			if v == nil {
				return
			}
			if _, isParam := params[v.Reg]; isParam {
				return
			}
			reach, ok := cur[v.Reg]
			if !ok {
				reach = reachIn(v.Reg)
				cur[v.Reg] = reach
			}
			ids := bitsToIDs(reach)
			if len(ids) == 0 {
				return
			} // 未初始化读:保持默认名
			uses = append(uses, useInfo{reg: v.Reg, v: v, defs: ids})
		}
		var expr func(Node)
		expr = func(n Node) {
			switch e := n.(type) {
			case *Var:
				use(e)
			case *BinaryOp:
				expr(e.LHS)
				expr(e.RHS)
			case *Compare:
				expr(e.LHS)
				expr(e.RHS)
			case *NotExpr:
				expr(e.Expr)
			case *IGet:
				expr(e.Obj)
			case *IPut:
				expr(e.Obj)
				expr(e.Value)
			case *SPut:
				expr(e.Value)
			case *Invoke:
				if e.This != nil {
					expr(e.This)
				}
				for _, a := range e.Args {
					expr(a)
				}
			case *ReturnVal:
				if e.Value != nil {
					expr(e.Value)
				}
			case *CheckCast:
				expr(e.Expr)
			case *Cast:
				expr(e.Expr)
			case *ArrayGet:
				expr(e.Array)
				expr(e.Index)
			case *ArrayPut:
				expr(e.Array)
				expr(e.Index)
				expr(e.Value)
			case *FilledArray:
				for _, x := range e.Elems {
					expr(x)
				}
			case *NewArray:
				expr(e.Size)
			case *NewObj:
				for _, a := range e.Args {
					expr(a)
				}
			case *ThrowStmt:
				expr(e.Value)
			}
		}
		var node func(Node)
		node = func(n Node) {
			switch a := n.(type) {
			case *Assign:
				expr(a.RHS)
				if lv, ok := a.LHS.(*Var); ok {
					if _, isParam := params[lv.Reg]; !isParam {
						cur[lv.Reg] = newSet()
						setBit(cur[lv.Reg], blockDefIDs[bi][defCursor])
						defCursor++
					}
				}
			case *IPut:
				expr(a)
			case *SPut:
				expr(a)
			case *Invoke:
				expr(a)
			case *ReturnVal:
				expr(a)
			case *ThrowStmt:
				expr(a)
			case *CheckCast:
				expr(a)
			case *Cast:
				expr(a)
			default:
				expr(n) // 常量/Unresolved 等无引用节点安全落空
			}
		}
		for _, n := range b.Nodes {
			node(n)
		}
		if b.TermCond != nil {
			expr(b.TermCond)
		}
		if b.Term == TermSwitch && b.SwitchReg >= 0 {
			reach, ok := cur[b.SwitchReg]
			if !ok {
				reach = reachIn(b.SwitchReg)
			}
			switchReach[bi] = bitsToIDs(reach)
		}
	}

	// ── 5) union-find 合并歧义定义组 ──
	parent := make([]int, D)
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(x int) int {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	for _, u := range uses {
		for _, id := range u.defs[1:] {
			ra, rb := find(u.defs[0]), find(id)
			if ra != rb {
				parent[ra] = rb
			}
		}
	}

	// ── 6) 按组命名 ──
	groups := map[int][]int{} // root → def ids
	for id := 0; id < D; id++ {
		r := find(id)
		groups[r] = append(groups[r], id)
	}
	regGroups := map[int][][]int{}
	for _, ids := range groups {
		sortInts(ids)
		r := regOf[ids[0]]
		regGroups[r] = append(regGroups[r], ids)
	}
	defName := make([]string, D)
	for reg, gs := range regGroups {
		// 组按最小 def id 排序(程序序)
		for i := 0; i < len(gs); i++ {
			for j := i + 1; j < len(gs); j++ {
				if gs[j][0] < gs[i][0] {
					gs[i], gs[j] = gs[j], gs[i]
				}
			}
		}
		mergedExists := false
		for _, g := range gs {
			if len(g) > 1 {
				mergedExists = true
				break
			}
		}
		seq := 0
		for _, g := range gs {
			if len(g) > 1 {
				continue // 合并组:默认名(空),顶部统一声明
			}
			seq++
			idx := seq
			if mergedExists {
				idx++
			}
			suffix := ""
			if idx > 1 {
				suffix = "_" + itoa(idx)
			}
			defName[g[0]] = "v" + itoa(reg) + suffix
		}
	}

	// ── 7) 应用重命名 ──
	for id, v := range defs {
		v.Name = defName[id]
	}
	for _, u := range uses {
		if len(u.defs) >= 1 {
			u.v.Name = defName[u.defs[0]]
		}
	}
	for bi, ids := range switchReach {
		if len(ids) == 1 {
			blocks[bi].SwitchRegName = defName[ids[0]]
		}
	}
}
