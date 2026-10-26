package godad

// cfg.go — 控制流图构建:指令流 → 基本块,终结类型与后继链接。
// leader 规则:方法入口、分支目标、分支/终结指令的下一条指令。
// 分支目标用 PC 映射到块索引;payload 作为数据跳过。

import (
	"finduai/internal/apkengine/dex"
)

// BasicBlock 基本块。
type BasicBlock struct {
	Index   int
	StartPC int // unit 偏移
	EndPC   int // 排他
	Nodes   []Node

	Term       TermKind
	TermTarget int  // 目标 unit 偏移(TermGoto/TermIf)
	TermCond   Node // TermIf 条件(跳转成立时)
	TargetIdx  int  // 目标块索引(-1 = 未解析)

	// switch 信息(TermSwitch)
	SwitchReg        int          // switch 寄存器
	SwitchCases      []SwitchCase // 键 → 目标 unit 偏移
	SwitchCaseIdx    []int        // 键 → 目标块索引(与 Cases 平行)
	SwitchDefault    int          // 默认分支 unit 偏移(fallthrough)
	SwitchDefaultIdx int          // 默认分支块索引(-1 = 未解析)

	Succs    []int // 后继块索引
	Preds    []int // 前驱块索引
	ExcSuccs []int // 异常边后继(try 覆盖块 → handler 入口块;不参与循环/switch 收敛判定)

	// SwitchRegName switch 表达式寄存器的版本名(到达定义分析产物;
	// 空串 = 渲染默认 vN)。由 versionRegs 回填,switchExpr 消费。
	SwitchRegName string
}

// tryInfo 块级 try 区域(异常表 → 块区间映射)。
type tryInfo struct {
	Start, End int // 块区间 [Start, End),End 排他
	Catches    []catchClause
}

// catchClause catch 子句(块级):多 catch 同址合并为多类型。
type catchClause struct {
	Types        []string // Java 类型名
	CatchAll     bool     // catch-all(finally 风格)
	HandlerBlock int      // handler 入口块索引
}

// collectTries 把方法异常表映射为块级 try 区域,并回填各块异常边。
// 无异常表/畸形时返回 nil(调用方降级为旧行为)。
func collectTries(m *dex.Method, code *dex.CodeHeader, blocks []*BasicBlock, d *dex.DEX) []tryInfo {
	et := m.ExceptionTable()
	if et == nil || len(blocks) == 0 {
		return nil
	}
	total := len(code.Insns) / 2
	blockAt := func(pc int) int {
		if pc >= total {
			return len(blocks)
		}
		for _, b := range blocks {
			if b.StartPC == pc {
				return b.Index
			}
		}
		// 边界未对齐(罕见):取包含块
		for _, b := range blocks {
			if pc >= b.StartPC && pc < b.EndPC {
				return b.Index
			}
		}
		return len(blocks)
	}
	var tries []tryInfo
	seen := map[[2]int]bool{}
	for _, ti := range et.Tries {
		s := blockAt(int(ti.StartAddr))
		e := blockAt(int(ti.StartAddr) + int(ti.InsnCount))
		if e <= s || s >= len(blocks) {
			continue
		}
		if seen[[2]int{s, e}] {
			continue
		}
		seen[[2]int{s, e}] = true
		tr := tryInfo{Start: s, End: e}
		if h := et.Handler(ti.HandlerOff); h != nil {
			byAddr := map[int]*catchClause{}
			var order []int
			for _, ta := range h.Typed {
				addr := int(ta.Addr)
				c, ok := byAddr[addr]
				if !ok {
					c = &catchClause{HandlerBlock: blockAt(addr)}
					byAddr[addr] = c
					order = append(order, addr)
				}
				c.Types = append(c.Types, JavaType(d.GetType(ta.TypeIdx).Descriptor))
			}
			for _, addr := range order {
				c := *byAddr[addr]
				if c.HandlerBlock < tr.End && c.HandlerBlock >= tr.Start {
					continue // handler 指向 try 体内:畸形,跳过
				}
				tr.Catches = append(tr.Catches, c)
			}
			if h.CatchAllAddr >= 0 {
				ca := blockAt(h.CatchAllAddr)
				if ca < tr.Start || ca >= tr.End {
					merged := false
					for i := range tr.Catches {
						if tr.Catches[i].HandlerBlock == ca {
							tr.Catches[i].CatchAll = true
							merged = true
						}
					}
					if !merged {
						tr.Catches = append(tr.Catches, catchClause{CatchAll: true, HandlerBlock: ca})
					}
				}
			}
		}
		if len(tr.Catches) == 0 {
			continue // 无 handler 的 try:结构化无意义,保持线性输出
		}
		tries = append(tries, tr)
	}
	// 异常边:覆盖块 → handler 入口块(单独字段,不进 Succs)
	for _, tr := range tries {
		for bi := tr.Start; bi < tr.End && bi < len(blocks); bi++ {
			b := blocks[bi]
			for _, c := range tr.Catches {
				if c.HandlerBlock < 0 || c.HandlerBlock >= len(blocks) {
					continue
				}
				dup := false
				for _, e := range b.ExcSuccs {
					if e == c.HandlerBlock {
						dup = true
						break
					}
				}
				if !dup {
					b.ExcSuccs = append(b.ExcSuccs, c.HandlerBlock)
				}
			}
		}
	}
	return tries
}

// BuildCFG 解码方法指令为基本块序列(按 PC 排序,索引即顺序)。
// 异常表边界(try 起/止、handler 入口)预先标注为 leader,
// 保证 try 区域与 handler 在块边界精确对齐。
func (dec *Decompiler) BuildCFG(m *dex.Method, code *dex.CodeHeader, paramNames map[int]string) []*BasicBlock {
	units := code.Insns
	total := len(units) / 2

	leaders := map[int]bool{0: true}
	// 异常表边界 → leader(先于指令扫描,分支目标对齐块边界)
	if et := m.ExceptionTable(); et != nil {
		addLeader := func(pc int) {
			if pc > 0 && pc < total {
				leaders[pc] = true
			}
		}
		for _, ti := range et.Tries {
			addLeader(int(ti.StartAddr))
			addLeader(int(ti.StartAddr) + int(ti.InsnCount))
			if h := et.Handler(ti.HandlerOff); h != nil {
				for _, ta := range h.Typed {
					addLeader(int(ta.Addr))
				}
				if h.CatchAllAddr >= 0 {
					addLeader(h.CatchAllAddr)
				}
			}
		}
	}

	// 第一遍:扫描指令边界、leader 集合、分支目标
	type insnInfo struct {
		pc     int
		size   int
		op     byte
		unit   uint16
		leader bool
	}
	var insns []insnInfo
	payloads := map[int]*switchPayload{} // switch PC → payload
	pc := 0
	for pc < total {
		unit := uint16(units[pc*2]) | uint16(units[pc*2+1])<<8
		op := byte(unit & 0xff)
		if op == 0x00 && unit>>8 != 0 {
			size := payloadUnits(units, pc)
			insns = append(insns, insnInfo{pc: pc, size: size, op: op, unit: unit})
			pc += size
			continue
		}
		size := Oplen(op)
		insns = append(insns, insnInfo{pc: pc, size: size, op: op, unit: unit})
		// 分支目标 → leader
		target := -1
		switch op {
		case 0x28: // goto 10t
			target = pc + int(int8(byte(unit>>8)))
		case 0x29: // goto/16
			target = pc + int(int16(u16(units, pc*2+2)))
		case 0x2a: // goto/32: ØØ|op +AAAAAAAA, offset spans units 1-2
			off := int32(u16(units, pc*2+2)) | int32(u16(units, pc*2+4))<<16
			target = pc + int(off)
		case 0x32, 0x33, 0x34, 0x35, 0x36, 0x37: // if-* 22t
			target = pc + int(int16(u16(units, pc*2+2)))
		case 0x38, 0x39, 0x3a, 0x3b, 0x3c, 0x3d: // if-*z 21t
			target = pc + int(int16(u16(units, pc*2+2)))
		case 0x2b, 0x2c: // switch:payload 解析出分支目标
			if pl := parseSwitchPayload(units, total, pc, op); pl != nil {
				payloads[pc] = pl
				for _, c := range pl.Cases {
					if c.Target >= 0 && c.Target < total {
						leaders[c.Target] = true
					}
				}
				if pl.Default >= 0 && pl.Default < total {
					leaders[pl.Default] = true
				}
			}
		}
		if target >= 0 && target < total {
			leaders[target] = true
			leaders[pc+size] = true
		}
		switch op {
		case 0x0e, 0x0f, 0x10, 0x11, 0x27: // return/throw:下一条也可能是新块边界
			leaders[pc+size] = true
		}
		pc += size
	}

	// 第二遍:按 leader 切块,调用 handlers 产出 IR
	ctx := &DecompileCtx{
		DEX:        dec.dex,
		Method:     m,
		Code:       code,
		ParamNames: paramNames,
	}
	var blocks []*BasicBlock
	pcToBlock := map[int]int{}
	var cur *BasicBlock
	for _, ins := range insns {
		if ins.op == 0x00 && ins.unit>>8 != 0 {
			// payload 数据:不产生代码,不切块(隶属前块尾部)
			if cur != nil {
				cur.EndPC = ins.pc + ins.size
			}
			continue
		}
		if leaders[ins.pc] || cur == nil {
			cur = &BasicBlock{Index: len(blocks), StartPC: ins.pc, EndPC: ins.pc + ins.size, Term: TermFall, TermTarget: -1, TargetIdx: -1}
			blocks = append(blocks, cur)
			pcToBlock[ins.pc] = cur.Index
		} else {
			cur.EndPC = ins.pc + ins.size
		}
		ctx.PC = ins.pc
		ctx.resetTerm()
		// 注意:不清空 LastInvoke —— move-result 必须消费前一条 invoke 的结果,
		// invoke handler 自身负责更新,消费后置 nil。
		handler := OpcodeHandlers[ins.op]
		if handler != nil {
			cur.Nodes = append(cur.Nodes, handler(ctx)...)
		} else {
			cur.Nodes = append(cur.Nodes, &Unresolved{Op: opName(ins.op)})
		}
		if ctx.Term != TermFall {
			cur.Term = ctx.Term
			cur.TermTarget = ctx.TermTarget
			cur.TermCond = ctx.TermCond
		}
		// switch 载荷信息(第一遍已解析)
		if pl, ok := payloads[ins.pc]; ok {
			cur.Term = TermSwitch
			cur.SwitchReg = int(ins.unit >> 8)
			cur.SwitchCases = pl.Cases
			cur.SwitchDefault = pl.Default
		}
	}

	// 第三遍:链接后继
	for i, b := range blocks {
		next := i + 1
		hasNext := next < len(blocks)
		switch b.Term {
		case TermGoto:
			if t, ok := pcToBlock[b.TermTarget]; ok {
				b.TargetIdx = t
				b.Succs = append(b.Succs, t)
			}
		case TermIf:
			if t, ok := pcToBlock[b.TermTarget]; ok {
				b.TargetIdx = t
				b.Succs = append(b.Succs, t)
			}
			if hasNext {
				b.Succs = append(b.Succs, next)
			}
		case TermReturn, TermThrow:
			// 无后继
		case TermSwitch:
			// 案例与默认分支的块索引
			b.SwitchCaseIdx = make([]int, len(b.SwitchCases))
			for ci, c := range b.SwitchCases {
				b.SwitchCaseIdx[ci] = -1
				if t, ok := pcToBlock[c.Target]; ok {
					b.SwitchCaseIdx[ci] = t
				}
			}
			b.SwitchDefaultIdx = -1
			if t, ok := pcToBlock[b.SwitchDefault]; ok {
				b.SwitchDefaultIdx = t
			}
			// 全部 case 目标 + 默认分支(fallthrough)
			seen := map[int]bool{}
			for _, t := range b.SwitchCaseIdx {
				if t >= 0 && !seen[t] {
					seen[t] = true
					b.Succs = append(b.Succs, t)
				}
			}
			if t := b.SwitchDefaultIdx; t >= 0 && !seen[t] {
				b.Succs = append(b.Succs, t)
			}
			if hasNext && len(b.Succs) == 0 {
				b.Succs = append(b.Succs, next)
			}
		default: // TermFall
			if hasNext {
				b.Succs = append(b.Succs, next)
			}
		}
		for _, s := range b.Succs {
			blocks[s].Preds = append(blocks[s].Preds, i)
		}
	}
	return blocks
}

func u16(b []byte, off int) uint16 {
	if off+2 > len(b) {
		return 0
	}
	return uint16(b[off]) | uint16(b[off+1])<<8
}

func opName(op byte) string {
	return "op-0x" + hexByte(op)
}

func hexByte(b byte) string {
	const hexdigits = "0123456789abcdef"
	return string([]byte{hexdigits[b>>4], hexdigits[b&0xf]})
}
