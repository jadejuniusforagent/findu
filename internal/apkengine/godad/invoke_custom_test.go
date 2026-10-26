package godad

// invoke_custom_test — invoke-custom(0xfc) 与构造器折叠顺序测试:
//   - callsite 表缺失时的 siteN 伪调用回退(数据流保留)
//   - 折叠产物置于 invoke 原位,保证参数求值顺序与字节码一致

import (
	"strings"
	"testing"

	"finduai/internal/apkengine/dex"
)

// TestInvokeCustomFallback fixture DEX 无 call_site_ids(map_list 缺失),
// invoke-custom 回退为 CallSite.siteN 伪调用,move-result 正常消费结果。
func TestInvokeCustomFallback(t *testing.T) {
	d, _ := fixtureClass(t, "LByteStreamReader;")
	code := &dex.CodeHeader{
		Insns: []byte{
			0xfc, 0x10, // AG|op:A=1(count)
			0x00, 0x00, // call_site_idx = 0
			0x01, 0x00, // regs {v0}
		},
	}
	ctx := &DecompileCtx{DEX: d, Code: code, PC: 0}
	nodes := OpcodeHandlers[0xfc](ctx)
	if len(nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(nodes))
	}
	inv, ok := nodes[0].(*Invoke)
	if !ok {
		t.Fatalf("node = %T, want Invoke", nodes[0])
	}
	if inv.Owner != "CallSite" || inv.Name != "site0" {
		t.Errorf("invoke = %s.%s, want CallSite.site0", inv.Owner, inv.Name)
	}
	if len(inv.Args) != 1 {
		t.Errorf("args = %d, want 1", len(inv.Args))
	}
	if ctx.LastInvoke != inv {
		t.Error("move-result should consume the invoke result")
	}
}

// TestFoldConstructorsArgOrder 参数定义位于 new-instance 与 <init> 之间时,
// 折叠产物必须落在 invoke 原位(arg def 在前,new 表达式在后)。
func TestFoldConstructorsArgOrder(t *testing.T) {
	nodes := []Node{
		&Assign{LHS: &Var{Reg: 0}, RHS: &NewObj{Desc: "Lcom/Foo;"}},
		&Assign{LHS: &Var{Reg: 1}, RHS: &StringConst{V: "x"}},
		&Invoke{Type: InvokeDirect, Owner: "Lcom/Foo;", Name: "<init>", This: &Var{Reg: 0}, Args: []Node{&Var{Reg: 1}}},
	}
	out := foldConstructors(nodes)
	if len(out) != 2 {
		t.Fatalf("nodes = %d, want 2", len(out))
	}
	// 第一条:v1 = "x";(参数定义在前)
	first, ok := out[0].(*Assign)
	if !ok {
		t.Fatalf("out[0] = %T, want Assign", out[0])
	}
	if _, isStr := first.RHS.(*StringConst); !isStr {
		t.Errorf("out[0].RHS = %T, want StringConst", first.RHS)
	}
	// 第二条:v0 = new com.Foo(v1);(折叠产物在 invoke 原位)
	second, ok := out[1].(*Assign)
	if !ok {
		t.Fatalf("out[1] = %T, want Assign", out[1])
	}
	no, ok := second.RHS.(*NewObj)
	if !ok || len(no.Args) != 1 {
		t.Fatalf("out[1].RHS = %T, want folded NewObj", second.RHS)
	}
	w := NewWriter()
	w.WriteNode(first)
	w.WriteNode(second)
	if got := w.String(); !strings.Contains(got, `v1 = "x";`) || !strings.Contains(got, "v0 = new com.Foo(v1);") {
		t.Errorf("order render:\n%s", got)
	}
}
