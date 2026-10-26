package godad

// structurer.go — 控制流结构化:基本块序列 → if/else/while 语句树。
// 采用"PC 序区间 + 回边识别"的务实算法(对齐 DAD 区间分析的 Basic 子集):
//   - 回边(goto/if 目标 ≤ 当前块)识别自然循环,循环范围 = [header, max(回边源)+1)
//   - 前向条件分支识别 if-then / if-else(尾块 goto 跳过 else 体 → 判定 else 存在)
//   - 循环内 goto 目标为循环尾 → break;目标为循环头 → continue
//   - 不规整控制流保留注释标记,保证代码不丢失

// Structurer 结构化上下文。
type Structurer struct {
	blocks       []*BasicBlock
	loops        map[int]int // header index → end(exclusive)
	loopStack    []loopFrame
	loopDone     map[int]bool     // 已结构化的循环头(防 while(true) 分支重入递归)
	reachable    []bool           // BFS 可达性(含异常边)
	tries        []tryInfo        // 块级 try 区域
	tryStart     map[int]*tryInfo // 区域起始块 → 区域
	handlerStart map[int]bool     // handler 入口块集合(extent 边界用)
	consumed     map[int]bool     // 已渲染块(try 体/handler 体),防重复输出
	tryDone      map[*tryInfo]bool
}

type loopFrame struct {
	header int
	end    int
}

// Structure 把基本块序列结构化为语句树(无异常表)。
func Structure(blocks []*BasicBlock) []Node {
	return StructureWithTries(blocks, nil)
}

// StructureWithTries 结构化 + try/catch 区域(异常表结构化入口)。
func StructureWithTries(blocks []*BasicBlock, tries []tryInfo) []Node {
	if len(blocks) == 0 {
		return nil
	}
	s := &Structurer{
		blocks:       blocks,
		loops:        computeLoops(blocks),
		loopDone:     map[int]bool{},
		tries:        tries,
		tryStart:     map[int]*tryInfo{},
		handlerStart: map[int]bool{},
		consumed:     map[int]bool{},
		tryDone:      map[*tryInfo]bool{},
	}
	for i := range tries {
		tr := &tries[i]
		if _, dup := s.tryStart[tr.Start]; !dup {
			s.tryStart[tr.Start] = tr
		}
		for _, c := range tr.Catches {
			if c.HandlerBlock >= 0 && c.HandlerBlock < len(blocks) {
				s.handlerStart[c.HandlerBlock] = true
			}
		}
	}
	s.reachable = computeReachable(blocks)
	return s.emitRange(0, len(blocks), -1)
}

// computeReachable 从块 0 BFS 标记可达块(控制流边 + 异常边)。
func computeReachable(blocks []*BasicBlock) []bool {
	reachable := make([]bool, len(blocks))
	if len(blocks) == 0 {
		return reachable
	}
	queue := []int{0}
	reachable[0] = true
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		edges := append(append([]int{}, blocks[cur].Succs...), blocks[cur].ExcSuccs...)
		for _, sc := range edges {
			if sc >= 0 && sc < len(blocks) && !reachable[sc] {
				reachable[sc] = true
				queue = append(queue, sc)
			}
		}
	}
	return reachable
}

// computeLoops 回边识别:目标块索引 ≤ 源块索引的分支构成自然循环,
// 循环尾取全部回边源的最大块索引 + 1(PC 序布局假定,适配编译器产物)。
func computeLoops(blocks []*BasicBlock) map[int]int {
	loops := map[int]int{}
	for i, b := range blocks {
		if b.Term != TermGoto && b.Term != TermIf {
			continue
		}
		tgt := b.TargetIdx
		if tgt < 0 || tgt > i {
			continue
		}
		if end, ok := loops[tgt]; !ok || i+1 > end {
			loops[tgt] = i + 1
		}
	}
	// 过滤过短/退化循环(end 必须大于 header)
	for h, e := range loops {
		if e <= h {
			delete(loops, h)
		}
	}
	return loops
}

func (s *Structurer) curLoop() (loopFrame, bool) {
	if len(s.loopStack) == 0 {
		return loopFrame{}, false
	}
	return s.loopStack[len(s.loopStack)-1], true
}

// isLoopBreak 目标是否为当前循环的出口(break)。
func (s *Structurer) isLoopBreak(idx int) bool {
	f, ok := s.curLoop()
	return ok && idx == f.end
}

// isLoopContinue 目标是否为当前循环头(continue)。
func (s *Structurer) isLoopContinue(idx int) bool {
	f, ok := s.curLoop()
	return ok && idx == f.header
}

// emitRange 输出块区间 [from, to) 的结构化语句。
// swallow:区间末块的 goto 目标(被上层 if/else 吸收的 join 跳转),-1 表示不吸收。
func (s *Structurer) emitRange(from, to int, swallow int) []Node {
	var out []Node
	for i := from; i < to; {
		// 已被 try 体/handler 体渲染过的块:跳过
		if s.consumed[i] {
			i++
			continue
		}

		// 0) try 区域入口(先于其他结构,嵌套 try 在体内递归处理)
		if tr, ok := s.tryStart[i]; ok && !s.tryDone[tr] {
			s.tryDone[tr] = true
			out = append(out, s.emitTry(tr))
			i = tr.End
			continue
		}

		b := s.blocks[i]

		// 1) 循环头(loopDone 防 emitLoop while(true) 分支重入递归)
		if end, ok := s.loops[i]; ok && end <= to && end > i && !s.loopDone[i] {
			s.loopDone[i] = true
			out = append(out, s.emitLoop(i, end))
			i = end
			continue
		}

		switch b.Term {
		case TermIf:
			stmts, next := s.emitIf(i, to)
			out = append(out, stmts...)
			i = next

		case TermSwitch:
			stmts, next := s.emitSwitch(i, to)
			out = append(out, stmts...)
			i = next

		case TermGoto:
			tgt := b.TargetIdx
			// 下一个未消费块(try/handler 渲染可能跳过中间块)
			nextAlive := -1
			for j := i + 1; j < to; j++ {
				if !s.consumed[j] {
					nextAlive = j
					break
				}
			}
			switch {
			case i == to-1 && swallow >= 0 && tgt == swallow:
				// 被 if/else 吸收的 join 跳转:只输出块内语句
				out = append(out, b.Nodes...)
			case tgt == i+1 || (nextAlive >= 0 && tgt == nextAlive):
				// 落到下一渲染块:等价顺序执行
				out = append(out, b.Nodes...)
			case s.isLoopBreak(tgt):
				out = append(out, b.Nodes...)
				out = append(out, &BreakStmt{})
			case s.isLoopContinue(tgt):
				out = append(out, b.Nodes...)
				out = append(out, &ContinueStmt{})
			default:
				// 不规整跳转:保留注释,代码不丢失
				out = append(out, b.Nodes...)
				out = append(out, &Unresolved{Op: "goto L" + itoa(tgt)})
			}
			i++

		case TermReturn, TermThrow:
			if !s.reachable[i] {
				out = append(out, &Unresolved{Op: "unreachable return"})
			}
			out = append(out, b.Nodes...)
			i++

		default:
			// TermFall / TermSwitch
			if !s.reachable[i] {
				// 无任何入边(控制流+异常):不可达代码,标注保留
				out = append(out, &Unresolved{Op: "unreachable block"})
			}
			out = append(out, b.Nodes...)
			i++
		}
	}
	return out
}

// emitTry 结构化单个 try 区域:try { 覆盖块 } catch (Type e) { handler 块 }。
// handler 体被标记 consumed,线性流不再重复输出;嵌套 try 在体内递归识别。
func (s *Structurer) emitTry(tr *tryInfo) Node {
	end := tr.End
	if end > len(s.blocks) {
		end = len(s.blocks)
	}
	body := s.emitRange(tr.Start, end, -1)
	for j := tr.Start; j < end; j++ {
		s.consumed[j] = true
	}
	stmt := &TryStmt{Body: body}
	for _, c := range tr.Catches {
		if c.HandlerBlock < 0 || c.HandlerBlock >= len(s.blocks) || c.HandlerBlock < tr.Start {
			stmt.Catches = append(stmt.Catches, &CatchClause{
				Types: c.Types, CatchAll: c.CatchAll,
				Body: []Node{&Unresolved{Op: "handler (unresolved)"}},
			})
			continue
		}
		clause := &CatchClause{Types: c.Types, CatchAll: c.CatchAll}
		if s.consumed[c.HandlerBlock] {
			// 多 try 共享 handler_off:首个 try 已输出,后续以注释指引
			clause.Body = []Node{&Unresolved{Op: "shared handler (rendered above)"}}
		} else {
			ext := s.handlerExtent(c.HandlerBlock)
			clause.Body = s.emitRange(c.HandlerBlock, ext, -1)
			for j := c.HandlerBlock; j < ext; j++ {
				s.consumed[j] = true
			}
			clause.Param, clause.Body = extractCatchParam(clause.Body)
		}
		stmt.Catches = append(stmt.Catches, clause)
	}
	return stmt
}

// handlerExtent 计算 handler 入口块 h 的输出区间 [h, ext):
// 边界为下一个 handler 入口 / try 区域起点 / 方法尾;再收窄:
//   - 外部跳转的共享 join 块(handler 落入与 try 体共用的汇合点)之前截断;
//   - 终止块(return/throw/外跳 goto)之后截断,防止吞并 join 后续代码。
func (s *Structurer) handlerExtent(h int) int {
	end := len(s.blocks)
	for j := h + 1; j < len(s.blocks); j++ {
		if s.handlerStart[j] {
			end = j
			break
		}
		if _, isTry := s.tryStart[j]; isTry {
			end = j
			break
		}
	}
	// 共享 join:存在来自 [h, end) 之外控制流入的块 → 在其前截断
	for j := h + 1; j < end; j++ {
		for _, p := range s.blocks[j].Preds {
			if p < h || p >= end {
				end = j
				break
			}
		}
	}
	for j := h; j < end; j++ {
		b := s.blocks[j]
		switch b.Term {
		case TermReturn, TermThrow:
			return j + 1
		case TermGoto:
			if b.TargetIdx < h || b.TargetIdx >= end {
				return j + 1
			}
		case TermIf:
			if b.TargetIdx >= 0 && b.TargetIdx < h {
				return j + 1
			}
		}
	}
	return end
}

// extractCatchParam 从 handler 体剥离首条 move-exception 赋值,
// 其目标寄存器改名为 catch 参数 e。
func extractCatchParam(body []Node) (string, []Node) {
	param := "e"
	for i, n := range body {
		assign, ok := n.(*Assign)
		if !ok {
			continue
		}
		if _, isMoveEx := assign.RHS.(*MoveException); !isMoveEx {
			continue
		}
		lv, ok := assign.LHS.(*Var)
		if !ok {
			return param, body
		}
		rest := make([]Node, 0, len(body)-1)
		rest = append(rest, body[:i]...)
		rest = append(rest, body[i+1:]...)
		renameUntilDef(rest, lv.Reg, param)
		return param, rest
	}
	return "", body // 无 move-exception:渲染无参 catch (Type)
}

// renameUntilDef 顺序敏感的异常参数改名:逐语句改写目标寄存器的读引用,
// 一旦该寄存器被再赋值(handler 复用存返回值等)即停止——之后的读引用
// 属于新值,不再是异常对象。
func renameUntilDef(nodes []Node, reg int, name string) {
	for _, n := range nodes {
		renameRegNode(n, reg, name) // 语句内读引用(含写前自读 vN = vN.getX())
		if writesTo(n, reg) {
			return
		}
	}
}

// writesTo 语句是否直接对该寄存器赋值。
func writesTo(n Node, reg int) bool {
	if a, ok := n.(*Assign); ok {
		if lv, ok := a.LHS.(*Var); ok {
			return lv.Reg == reg
		}
	}
	return false
}

// renameRegList 对语句列表逐节点改写读引用。
func renameRegList(nodes []Node, reg int, name string) {
	for _, n := range nodes {
		renameRegNode(n, reg, name)
	}
}

// renameRegNode 改写子树中目标寄存器的 Var 读引用。
// 赋值目标(LHS)不改写:写目标是新值,不属于异常参数。
func renameRegNode(n Node, reg int, name string) {
	switch v := n.(type) {
	case *Var:
		if v.Reg == reg {
			v.Name = name
		}
	case *Assign:
		// 仅改写 RHS 读引用,LHS 是写目标
		renameRegNode(v.RHS, reg, name)
	case *BinaryOp:
		renameRegNode(v.LHS, reg, name)
		renameRegNode(v.RHS, reg, name)
	case *Compare:
		renameRegNode(v.LHS, reg, name)
		renameRegNode(v.RHS, reg, name)
	case *NotExpr:
		renameRegNode(v.Expr, reg, name)
	case *IGet:
		renameRegNode(v.Obj, reg, name)
	case *IPut:
		renameRegNode(v.Obj, reg, name)
		renameRegNode(v.Value, reg, name)
	case *SPut:
		renameRegNode(v.Value, reg, name)
	case *Invoke:
		if v.This != nil {
			renameRegNode(v.This, reg, name)
		}
		for _, a := range v.Args {
			renameRegNode(a, reg, name)
		}
	case *ReturnVal:
		if v.Value != nil {
			renameRegNode(v.Value, reg, name)
		}
	case *CheckCast:
		renameRegNode(v.Expr, reg, name)
	case *Cast:
		renameRegNode(v.Expr, reg, name)
	case *ArrayGet:
		renameRegNode(v.Array, reg, name)
		renameRegNode(v.Index, reg, name)
	case *ArrayPut:
		renameRegNode(v.Array, reg, name)
		renameRegNode(v.Index, reg, name)
		renameRegNode(v.Value, reg, name)
	case *FilledArray:
		for _, e := range v.Elems {
			renameRegNode(e, reg, name)
		}
	case *NewArray:
		renameRegNode(v.Size, reg, name)
	case *NewObj:
		for _, a := range v.Args {
			renameRegNode(a, reg, name)
		}
	case *ThrowStmt:
		renameRegNode(v.Value, reg, name)
	case *IfStmt:
		renameRegNode(v.Cond, reg, name)
		renameRegList(v.Then, reg, name)
		renameRegList(v.Else, reg, name)
	case *WhileStmt:
		renameRegNode(v.Cond, reg, name)
		renameRegList(v.Body, reg, name)
	case *SwitchStmt:
		renameRegNode(v.Expr, reg, name)
		for ci := range v.Cases {
			renameRegList(v.Cases[ci].Body, reg, name)
		}
	case *TryStmt:
		renameRegList(v.Body, reg, name)
		for _, c := range v.Catches {
			renameRegList(c.Body, reg, name)
		}
	}
}

// emitIf 处理块 i 的条件分支,返回语句与下一块索引。
func (s *Structurer) emitIf(i, to int) ([]Node, int) {
	b := s.blocks[i]
	cond := b.TermCond
	tgt := b.TargetIdx

	// 目标解析失败:注释回退
	if tgt < 0 || cond == nil {
		return []Node{&Unresolved{Op: "branch (unresolved)"}}, i + 1
	}

	if tgt <= i {
		// 回边条件分支:循环内 → if (cond) continue;否则注释
		if s.isLoopContinue(tgt) {
			return []Node{&IfStmt{Cond: cond, Then: []Node{&ContinueStmt{}}}}, i + 1
		}
		if s.isLoopBreak(tgt) {
			return []Node{&IfStmt{Cond: cond, Then: []Node{&BreakStmt{}}}}, i + 1
		}
		return []Node{&Unresolved{Op: "if-back-edge L" + itoa(tgt)}}, i + 1
	}

	if tgt >= to {
		// 目标在区间外
		if s.isLoopBreak(tgt) {
			return []Node{&IfStmt{Cond: cond, Then: []Node{&BreakStmt{}}}}, i + 1
		}
		if tgt == to {
			// join 恰为区间尾:渲染 if (inv) { 剩余块 },区间结束
			body := s.emitRange(i+1, to, -1)
			if len(body) == 0 {
				return nil, to
			}
			return []Node{&IfStmt{Cond: invertCond(cond), Then: body}}, to
		}
		// 跳到更外层:注释回退(保持线性输出)
		return []Node{&Unresolved{Op: "if L" + itoa(tgt) + " (out-of-range)"}}, i + 1
	}

	// 区间内前向分支:if-then / if-else
	// else 判定:紧邻目标块之前的块以 goto 跳到 join(> tgt 且 ≤ to)
	g := tgt
	if tgt-1 > i {
		if pb := s.blocks[tgt-1]; pb.Term == TermGoto && pb.TargetIdx > tgt && pb.TargetIdx <= to {
			g = pb.TargetIdx
		}
	}
	// 分支方向:字节码跳转成立时去 tgt。PC 序下 fallthrough 体在前,
	// 因此渲染 if (inv(cond)) { fallthrough } [else { target }]。
	thenBody := s.emitRange(i+1, tgt, g)
	var elseBody []Node
	if g > tgt {
		elseBody = s.emitRange(tgt, g, -1)
	}
	return []Node{&IfStmt{Cond: invertCond(cond), Then: thenBody, Else: elseBody}}, g
}

// emitSwitch 结构化 switch 语句。
// 布局假定(编译器产物常见):switch 块 i;各 case 目标块按地址升序排列,
// 默认分支 = switch 后继块(i+1);全部 case 体在 [minTarget, J) 内收敛,
// J 为最小收敛点(区间内所有块的后继均落在区间内或回跳区间之前)。
// 不满足时回退为逐块线性输出 + 注释。
func (s *Structurer) emitSwitch(i, to int) ([]Node, int) {
	b := s.blocks[i]
	if len(b.SwitchCases) == 0 || b.SwitchReg < 0 {
		return []Node{&Unresolved{Op: "switch (unresolved)"}}, i + 1
	}
	// 目标块收集(去重排序)
	targetSet := map[int]bool{}
	for _, t := range b.SwitchCaseIdx {
		if t <= i || t >= to {
			return []Node{&Unresolved{Op: "switch (target out of range)"}}, i + 1
		}
		targetSet[t] = true
	}
	defaultIdx := b.SwitchDefaultIdx
	if defaultIdx <= i || defaultIdx >= to {
		return []Node{&Unresolved{Op: "switch (default out of range)"}}, i + 1
	}
	targetSet[defaultIdx] = true
	targets := make([]int, 0, len(targetSet))
	for t := range targetSet {
		targets = append(targets, t)
	}
	sortInts(targets)
	// 收敛点 J:区间 [targets[0], J) 的所有后继都在区间内或区间之前
	J := -1
	for j := targets[len(targets)-1] + 1; j <= to; j++ {
		ok := true
		for k := targets[0]; k < j && ok; k++ {
			for _, sc := range s.blocks[k].Succs {
				if sc >= j {
					ok = false
					break
				}
			}
		}
		if ok {
			J = j
			break
		}
	}
	if J < 0 {
		return []Node{&Unresolved{Op: "switch (no convergence point)"}}, i + 1
	}

	// 分支体 [targets[n], targets[n+1]) 或 [last, J)
	var cases []SwitchCaseBody
	for ti, t := range targets {
		end := J
		if ti+1 < len(targets) {
			end = targets[ti+1]
		}
		body := s.emitRange(t, end, J)
		last := s.blocks[end-1]
		switch {
		case last.Term == TermFall && end < J:
			// 自然落入下一 case:Java switch 落穿语义相同,不补 break
		case !caseTerminated(last):
			body = append(body, &BreakStmt{})
		}
		cb := SwitchCaseBody{Body: body}
		if t == defaultIdx {
			cb.IsDefault = true
		}
		for ci, ct := range b.SwitchCaseIdx {
			if ct == t {
				cb.Keys = append(cb.Keys, b.SwitchCases[ci].Key)
			}
		}
		cases = append(cases, cb)
	}
	return []Node{&SwitchStmt{Expr: s.blocks[i].switchExpr(), Cases: cases}}, J
}

// switchExpr switch 表达式节点(版本名来自到达定义分析)。
func (b *BasicBlock) switchExpr() Node { return &Var{Reg: b.SwitchReg, Name: b.SwitchRegName} }

// caseTerminated 分支末块是否已终止(return/throw/continue/break 或回跳)。
func caseTerminated(b *BasicBlock) bool {
	switch b.Term {
	case TermReturn, TermThrow:
		return true
	case TermGoto:
		// 回跳到区间之前(continue/循环头)视为终止
		return b.TargetIdx >= 0 && b.TargetIdx < b.Index
	}
	return false
}

// emitLoop 输出循环 [header, end)。
// 头部条件分支退出循环 → while (inv(cond)) { body };
// 否则(do-while / while(true))→ while (true) { body },底部回边由
// break/continue 规则吸收。
func (s *Structurer) emitLoop(header, end int) Node {
	b := s.blocks[header]
	s.loopStack = append(s.loopStack, loopFrame{header: header, end: end})
	defer func() { s.loopStack = s.loopStack[:len(s.loopStack)-1] }()

	if b.Term == TermIf {
		tgt := b.TargetIdx
		if tgt >= end || tgt < header {
			// 头部分支退出:while (inv(cond)) { [header+1, end) }
			cond := invertCond(b.TermCond)
			body := s.emitRange(header+1, end, -1)
			return &WhileStmt{Cond: cond, Body: body}
		}
	}
	// do-while / while(true):从头部整段输出,底部回边渲染为 continue
	body := s.emitRange(header, end, -1)
	return &WhileStmt{Cond: &BoolConst{V: true}, Body: body}
}
