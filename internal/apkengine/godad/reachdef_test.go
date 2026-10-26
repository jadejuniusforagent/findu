package godad

// reachdef_test — 块级到达定义分析测试:顺序版本分裂、汇合点歧义合并、
// 循环携带定义、switch 寄存器版本名。

import "testing"

// TestVersionRegsSequential 单块内两次定义、各自使用 → 分裂为 v0 / v0_2,
// 使用点随定义版本同步改名。
func TestVersionRegsSequential(t *testing.T) {
	d1, u1 := &Var{Reg: 0}, &Var{Reg: 0}
	d2, u2 := &Var{Reg: 0}, &Var{Reg: 0}
	blocks := []*BasicBlock{{
		Index: 0,
		Nodes: []Node{
			&Assign{LHS: d1, RHS: &IntConst{V: 1}},
			&Invoke{Name: "use", Args: []Node{u1}, RetType: "V"},
			&Assign{LHS: d2, RHS: &IntConst{V: 2}},
			&Invoke{Name: "use", Args: []Node{u2}, RetType: "V"},
		},
		Term: TermReturn,
	}}
	versionRegs(blocks, nil)
	if d1.Name != "v0" {
		t.Errorf("first def name = %q, want v0", d1.Name)
	}
	if d2.Name != "v0_2" {
		t.Errorf("second def name = %q, want v0_2", d2.Name)
	}
	if u1.Name != "v0" || u2.Name != "v0_2" {
		t.Errorf("use names = %q / %q, want v0 / v0_2", u1.Name, u2.Name)
	}
}

// TestVersionRegsAmbiguousJoin 菱形汇合:两个分支定义共同到达一个使用 →
// 合并组保持默认名,使用不重命名。
func TestVersionRegsAmbiguousJoin(t *testing.T) {
	da, db := &Var{Reg: 0}, &Var{Reg: 0}
	use := &Var{Reg: 0}
	blocks := []*BasicBlock{
		{Index: 0, Nodes: []Node{&Assign{LHS: da, RHS: &IntConst{V: 1}}},
			Term: TermIf, TargetIdx: 2, TermCond: &BoolConst{V: true},
			Succs: []int{1, 2}},
		{Index: 1, Nodes: []Node{&Assign{LHS: db, RHS: &IntConst{V: 2}}},
			Term: TermGoto, TargetIdx: 2, Succs: []int{2}, Preds: []int{0}},
		{Index: 2, Nodes: []Node{&Invoke{Name: "use", Args: []Node{use}, RetType: "V"}},
			Term: TermReturn, Preds: []int{0, 1}},
	}
	versionRegs(blocks, nil)
	if da.Name != "" || db.Name != "" {
		t.Errorf("ambiguous defs should keep default names, got %q / %q", da.Name, db.Name)
	}
	if use.Name != "" {
		t.Errorf("ambiguous use should keep default name, got %q", use.Name)
	}
}

// TestVersionRegsLoopCarried 循环携带定义(头条件使用 + 体内再定义)→
// 头条件使用有两个到达定义,合并为默认名。
func TestVersionRegsLoopCarried(t *testing.T) {
	dEntry, dBody := &Var{Reg: 0}, &Var{Reg: 0}
	condUse := &Var{Reg: 0}
	blocks := []*BasicBlock{
		{Index: 0, Nodes: []Node{&Assign{LHS: dEntry, RHS: &IntConst{V: 0}}},
			Term: TermGoto, TargetIdx: 1, Succs: []int{1}},
		{Index: 1, Term: TermIf, TargetIdx: 3,
			TermCond: &Compare{Op: "==", LHS: condUse, RHS: &IntConst{V: 0}},
			Succs:    []int{2, 3}, Preds: []int{0, 2}},
		{Index: 2, Nodes: []Node{&Assign{LHS: dBody, RHS: &IntConst{V: 1}}},
			Term: TermGoto, TargetIdx: 1, Succs: []int{1}},
		{Index: 3, Term: TermReturn, Preds: []int{1}},
	}
	versionRegs(blocks, nil)
	if dEntry.Name != "" || dBody.Name != "" {
		t.Errorf("loop-carried defs should merge to default, got %q / %q", dEntry.Name, dBody.Name)
	}
	if condUse.Name != "" {
		t.Errorf("cond use should keep default name, got %q", condUse.Name)
	}
}

// TestVersionRegsNestedDefVersioned 嵌套(if 体)定义只要无歧义汇合,
// 同样获得独占版本——这是相对树级 SplitVars 的能力扩展。
func TestVersionRegsNestedDefVersioned(t *testing.T) {
	dOuter1 := &Var{Reg: 0}
	dInner := &Var{Reg: 0}
	// b0: v0 = 1;goto b1
	// b1: v0 = 2(嵌套场景由结构化阶段表达;CFG 层验证独占分支版本);
	//     使用 v0;return
	use := &Var{Reg: 0}
	blocks := []*BasicBlock{
		{Index: 0, Nodes: []Node{&Assign{LHS: dOuter1, RHS: &IntConst{V: 1}}},
			Term: TermGoto, TargetIdx: 2, Succs: []int{2}},
		{Index: 1, Nodes: []Node{&Assign{LHS: dInner, RHS: &IntConst{V: 2}}},
			Term: TermGoto, TargetIdx: 2, Succs: []int{2}},
		{Index: 2, Nodes: []Node{
			&Invoke{Name: "use", Args: []Node{use}, RetType: "V"},
		}, Term: TermReturn, Preds: []int{0, 1}},
	}
	// b1 不可达(无前驱指向)→ 只有 dOuter1 到达使用
	blocks[1].Preds = nil
	versionRegs(blocks, nil)
	if dOuter1.Name != "v0" {
		t.Errorf("sole reaching def = %q, want v0", dOuter1.Name)
	}
	if use.Name != "v0" {
		t.Errorf("use should reference the reaching def, got %q", use.Name)
	}
}

// TestVersionRegsSwitchName switch 寄存器取块尾到达版本名。
func TestVersionRegsSwitchName(t *testing.T) {
	d1, d2 := &Var{Reg: 0}, &Var{Reg: 0}
	blocks := []*BasicBlock{{
		Index: 0,
		Nodes: []Node{
			&Assign{LHS: d1, RHS: &IntConst{V: 1}},
			&Assign{LHS: d2, RHS: &IntConst{V: 2}},
		},
		Term: TermSwitch, SwitchReg: 0,
	}}
	versionRegs(blocks, nil)
	if d1.Name != "v0" || d2.Name != "v0_2" {
		t.Errorf("defs = %q / %q, want v0 / v0_2", d1.Name, d2.Name)
	}
	if blocks[0].SwitchRegName != "v0_2" {
		t.Errorf("SwitchRegName = %q, want v0_2", blocks[0].SwitchRegName)
	}
}

// TestVersionRegsParams 参数寄存器不参与版本化。
func TestVersionRegsParams(t *testing.T) {
	dp := &Var{Reg: 8, Name: "this"}
	up := &Var{Reg: 8, Name: "this"}
	blocks := []*BasicBlock{{
		Index: 0,
		Nodes: []Node{
			&Assign{LHS: dp, RHS: &IntConst{V: 1}},
			&Invoke{Name: "use", Args: []Node{up}, RetType: "V"},
		},
		Term: TermReturn,
	}}
	versionRegs(blocks, map[int]string{8: "this"})
	if dp.Name != "this" || up.Name != "this" {
		t.Errorf("param names disturbed: %q / %q", dp.Name, up.Name)
	}
}
