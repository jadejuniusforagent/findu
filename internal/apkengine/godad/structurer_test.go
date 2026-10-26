package godad

// structurer_test — CFG 结构化测试:if/else、while、break/continue 识别,
// 以及全 fixture 结构化输出冒烟(任何 panic / 死循环视为失败)。

import (
	"path/filepath"
	"strings"
	"testing"

	"finduai/internal/apkengine/dex"
	"finduai/internal/apkengine/zipprobe"
)

// TestStructureFixtureWhileIf fixture(读取流 + 缓冲循环)应产出真实
// while/if 语句,而非注释标记。
func TestStructureFixtureWhileIf(t *testing.T) {
	d, cls := fixtureClass(t, "LByteStreamReader;")
	src := New(d).DecompileClass(cls)

	if !strings.Contains(src, "while (") {
		t.Errorf("expected a structured while loop:\n%s", src)
	}
	if !strings.Contains(src, "if (") {
		t.Errorf("expected a structured if statement:\n%s", src)
	}
	// 不应再出现旧的 if 注释形态(结构化成功)
	if strings.Contains(src, "/* if ") {
		t.Errorf("if still rendered as comment:\n%s", src)
	}
	// super() 调用仍应正确
	if !strings.Contains(src, "super();") {
		t.Errorf("super() lost:\n%s", src)
	}
}

// TestStructureBreakContinue 第二个 read 方法含 break。
func TestStructureBreakContinue(t *testing.T) {
	d, cls := fixtureClass(t, "LByteStreamReader;")
	src := New(d).DecompileClass(cls)
	if !strings.Contains(src, "break;") {
		t.Errorf("expected a break statement:\n%s", src)
	}
}

// TestComputeLoops 回边识别的单元验证。
func TestComputeLoops(t *testing.T) {
	// 块 0: fall → 1;块 1: if → 3 (前向);块 2: goto → 1 (回边);块 3: return
	blocks := []*BasicBlock{
		{Index: 0, Term: TermFall, TargetIdx: -1},
		{Index: 1, Term: TermIf, TargetIdx: 3, TermCond: &Compare{Op: "==", LHS: &Var{Reg: 0}, RHS: &IntConst{V: 0}}},
		{Index: 2, Term: TermGoto, TargetIdx: 1},
		{Index: 3, Term: TermReturn, TargetIdx: -1},
	}
	loops := computeLoops(blocks)
	if end, ok := loops[1]; !ok || end != 3 {
		t.Errorf("loops = %v, want header 1 → end 3", loops)
	}

	// 无回边 → 无循环
	lin := []*BasicBlock{
		{Index: 0, Term: TermGoto, TargetIdx: 1},
		{Index: 1, Term: TermReturn, TargetIdx: -1},
	}
	if loops := computeLoops(lin); len(loops) != 0 {
		t.Errorf("linear blocks should have no loops: %v", loops)
	}
}

// TestStructureAllFixtures 全 fixture 结构化冒烟:反编译不得 panic,
// 输出必须含类头。
func TestStructureAllFixtures(t *testing.T) {
	matches, err := filepath.Glob(filepath.Join("..", "findrefs", "testdata", "*.apk"))
	if err != nil || len(matches) == 0 {
		t.Skip("no fixtures")
	}
	total, withLoop, withIf := 0, 0, 0
	for _, apkPath := range matches {
		r, err := zipprobe.Open(apkPath)
		if err != nil {
			t.Fatal(err)
		}
		entries, _ := zipprobe.ParseCDDexEntries(r)
		for _, e := range entries {
			data, err := zipprobe.InflateDex(r, e)
			if err != nil {
				continue
			}
			for _, ld := range zipprobe.LogicalDexBuffers(e.Name, data) {
				d, err := dex.Parse(ld.Buf, ld.Name)
				if err != nil {
					continue
				}
				dec := New(d)
				for i := 0; i < int(d.NumClasses()); i++ {
					cls := d.GetClassByIdx(uint32(i))
					src := dec.DecompileClass(cls)
					if strings.Contains(src, "aborted") {
						t.Errorf("%s %s: decompilation aborted:\n%s", apkPath, cls.Fullname(), src)
					}
					if strings.Contains(src, "while (") {
						withLoop++
					}
					if strings.Contains(src, "if (") {
						withIf++
					}
					total++
				}
			}
		}
		r.Close()
	}
	t.Logf("structured %d classes (%d with while, %d with if) across %d fixtures", total, withLoop, withIf, len(matches))
	if total == 0 {
		t.Fatal("no classes decompiled")
	}
	if withIf == 0 || withLoop == 0 {
		t.Errorf("expected at least one if and one while across fixtures (if=%d loop=%d)", withIf, withLoop)
	}
}

// TestInvertCond 条件取反。
func TestInvertCond(t *testing.T) {
	c := &Compare{Op: "<", LHS: &Var{Reg: 0}, RHS: &Var{Reg: 1}}
	inv := invertCond(c)
	if inv.Java() != "v0 >= v1" {
		t.Errorf("invert(<) = %q", inv.Java())
	}
	// 非比较节点包 !()
	u := &Unresolved{Op: "x"}
	inv2 := invertCond(u)
	if inv2.Java() != "!(/* x */)" {
		t.Errorf("invert(unresolved) = %q", inv2.Java())
	}
}
